package main

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	libhoney "github.com/honeycombio/libhoney-go"
	flag "github.com/jessevdk/go-flags"
	"github.com/matttproud/golang_protobuf_extensions/pbutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

const acceptHeader = `application/vnd.google.protobuf;proto=io.prometheus.client.MetricFamily;encoding=delimited;q=0.7,text/plain;version=0.0.4;q=0.3`

type Options struct {
	URL      string `long:"url"`
	Dataset  string `long:"dataset"`
	Writekey string `long:"writekey"`
	APIHost  string `long:"apihost" default:"https://api.honeycomb.io"`
	Interval int    `long:"interval" default:"60"`
}

type MetricGroup struct {
	DataPoints  []*DataPoint
	MetricGroup string
}

type DataPoint struct {
	Name   string
	Value  interface{}
	Labels map[string]string
}

func NewMetricGroups(mfs []*dto.MetricFamily) []*MetricGroup {

	metricGroupsMap := make(map[string]*MetricGroup)

	for _, mf := range mfs {
		if mf.GetType() != dto.MetricType_GAUGE {
			continue
		}

		metricGroupName, err := getMetricGroupName(mf)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"error": err,
			})
			continue
		}

		for _, m := range mf.Metric {
			groupedKey := getGroupedKey(metricGroupName, m)
			metricGroup, ok := metricGroupsMap[groupedKey]
			if !ok {
				metricGroup = &MetricGroup{
					MetricGroup: metricGroupName,
				}
			}

			dp := getDatapointFromMetric(mf, m)
			if dp == nil {
				continue
			}

			metricGroup.DataPoints = append(metricGroup.DataPoints, dp)

			metricGroupsMap[groupedKey] = metricGroup
		}

	}

	metricGroups := make([]*MetricGroup, 0, len(metricGroupsMap))
	for k := range metricGroupsMap {
		metricGroups = append(metricGroups, metricGroupsMap[k])
	}

	return metricGroups
}

// We can't use m.GetGauge().GetValue() because there exist some
// exceptional cases we need to handle. Abstract exception cases
// in this func
func getDatapointFromMetric(mf *dto.MetricFamily, m *dto.Metric) *DataPoint {
	metricLabels := makeLabels(m)
	metricName := mf.GetName()
	var metricValue interface{}

	switch metricName {
	case "kube_pod_status_phase":
		metricValue = metricLabels["phase"]
		delete(metricLabels, "phase")

	// Only contribute labels
	case "kube_pod_labels", "kube_pod_info", "kube_service_info", "kube_pod_container_info", "kube_node_labels", "kube_service_labels":
		metricValue = nil

	// Formatted as Condition Values
	case "kube_pod_status_ready", "kube_pod_status_scheduled", "kube_node_status_disk_pressure", "kube_node_status_memory_pressure", "kube_node_status_out_of_disk", "kube_node_status_ready":
		if m.GetGauge().GetValue() == 1 {
			metricValue = metricLabels["condition"]
			delete(metricLabels, "condition")
		} else {
			return nil
		}

	default:
		metricValue = m.GetGauge().GetValue()
	}

	return &DataPoint{
		Name:   metricName,
		Value:  metricValue,
		Labels: metricLabels,
	}
}

func validateMetricName(metricName string) bool {
	match, _ := regexp.MatchString("^kube_[^_]+_*", metricName)
	if !match {
		return false
	}
	return true
}

// Returns Metric Group based on metric name. kube-state-metrics metric names are formatted kube_<group-name>_*
func getMetricGroupName(mf *dto.MetricFamily) (string, error) {
	metricName := mf.GetName()

	if isValid := validateMetricName(metricName); !isValid {
		return "", errors.New("unable to extract group name from Metric Name")
	}

	metricNameSplit := strings.Split(metricName, "_")

	if metricNameSplit[1] == "pod" && metricNameSplit[2] == "container" {
		return "pod-container", nil
	}

	return metricNameSplit[1], nil
}

// Create Key for Grouping Events based on https://github.com/kubernetes/kube-state-metrics/tree/master/Documentation
func getGroupedKey(metricGroup string, m *dto.Metric) string {
	labels := makeLabels(m)
	const SEP = ":"
	var metricGroupKey string

	switch metricGroup {
	case "node":
		metricGroupKey = labels["node"]
	case "pod-container":
		metricGroupKey = labels["namespace"] + SEP + labels["pod"] + SEP + labels["container"]
	default:
		metricGroupKey = labels["namespace"] + SEP + labels[metricGroup]
	}

	return metricGroup + SEP + metricGroupKey
}

func makeLabels(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, lp := range m.Label {
		result[lp.GetName()] = lp.GetValue()
	}
	return result
}

func (mg *MetricGroup) ToEvent() *libhoney.Event {
	ev := libhoney.NewEvent()
	for _, dp := range mg.DataPoints {
		// Some datapoints only contribute labels
		if dp.Value != nil {
			ev.AddField(dp.Name, dp.Value)
		}
		ev.Add(dp.Labels)
	}
	ev.AddField("metric_group", mg.MetricGroup)
	return ev
}

type Sender interface {
	Send([]*MetricGroup)
}

// TODO: handle transmission errors
type LibhoneySender struct{}

func (ls *LibhoneySender) Send(metricGroups []*MetricGroup) {
	for _, mg := range metricGroups {
		ev := mg.ToEvent()
		ev.Send()
	}
}

func (ls *LibhoneySender) ReadResponses() {
	for resp := range libhoney.Responses() {
		if resp.Err != nil || resp.StatusCode != 202 {
			logrus.WithFields(logrus.Fields{
				"error":  resp.Err,
				"body":   resp.Body,
				"status": resp.StatusCode,
			}).Error("Error sending event")
		}
	}
}

func ScrapeMetrics(url string) ([]*dto.MetricFamily, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Accept", acceptHeader)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return ParseResponse(resp.Header.Get("Content-Type"), resp.Body)
}

func ParseResponse(contentType string, body io.Reader) ([]*dto.MetricFamily, error) {
	var ret []*dto.MetricFamily
	mediatype, params, err := mime.ParseMediaType(contentType)
	if err == nil && mediatype == "application/vnd.google.protobuf" &&
		params["encoding"] == "delimited" &&
		params["proto"] == "io.prometheus.client.MetricFamily" {
		for {
			mf := &dto.MetricFamily{}
			if _, err = pbutil.ReadDelimited(body, mf); err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("Error reading metric family protobuf: %v", err)
			}
			ret = append(ret, mf)
		}
	} else {
		// We could do further content-type checks here, but the
		// fallback for now will anyway be the text format
		// version 0.0.4, so just go for it and see if it works.
		var parser expfmt.TextParser
		metricFamilies, err := parser.TextToMetricFamilies(body)
		if err != nil {
			return nil, fmt.Errorf("Error reading metric family text response: %v", err)
		}
		for _, mf := range metricFamilies {
			ret = append(ret, mf)
		}
	}
	return ret, nil
}

func run(options *Options, sender Sender) {
	ticker := time.NewTicker(time.Duration(options.Interval) * time.Second)
	for range ticker.C {
		metricFamilies, err := ScrapeMetrics(options.URL)
		if err != nil {
			fmt.Println("Error scraping metrics:", err)
		}

		metricGroups := NewMetricGroups(metricFamilies)
		sender.Send(metricGroups)
	}
}

func main() {
	options := &Options{}
	flagParser := flag.NewParser(options, flag.PrintErrors)
	if extraArgs, err := flagParser.Parse(); err != nil || len(extraArgs) != 0 {
		fmt.Println("Error: failed to parse the command line.")
		if err != nil {
			fmt.Printf("\t%s\n", err)
		} else {
			fmt.Printf("\tUnexpected extra arguments: %s\n", strings.Join(extraArgs, " "))
		}

		os.Exit(1)
	}

	if options.Writekey == "" {
		options.Writekey = os.Getenv("HONEYCOMB_WRITEKEY")
	}

	libhoney.Init(libhoney.Config{
		WriteKey: options.Writekey,
		Dataset:  options.Dataset,
		APIHost:  options.APIHost,
	})

	sender := &LibhoneySender{}
	go sender.ReadResponses()

	run(options, sender)

}

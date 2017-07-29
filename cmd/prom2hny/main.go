package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
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
	Interval int    `long:"interval" default:"60"`
}

type DataPoint struct {
	Name   string
	Value  float64
	Help   string
	Labels map[string]string
}

func NewDataPoints(mf *dto.MetricFamily) []*DataPoint {
	var ret []*DataPoint
	for _, m := range mf.Metric {
		if mf.GetType() == dto.MetricType_GAUGE {
			dp := &DataPoint{
				Name:   mf.GetName(),
				Help:   mf.GetHelp(),
				Labels: makeLabels(m),
			}
			ret = append(ret, dp)
		}
	}
	return ret
}

func makeLabels(m *dto.Metric) map[string]string {
	result := map[string]string{}
	for _, lp := range m.Label {
		result[lp.GetName()] = lp.GetValue()
	}
	return result
}

func (dp *DataPoint) ToEvent() *libhoney.Event {
	ev := libhoney.NewEvent()
	ev.Add(dp.Labels)
	ev.AddField(dp.Name, dp.Value)
	ev.AddField("help", dp.Help)
	return ev
}

type Sender interface {
	Send([]*DataPoint)
}

// TODO: handle transmission errors
type LibhoneySender struct{}

func (ls *LibhoneySender) Send(dataPoints []*DataPoint) {
	for _, dp := range dataPoints {
		ev := dp.ToEvent()
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
		for _, mf := range metricFamilies {
			dataPoints := NewDataPoints(mf)
			logrus.WithField("datapoints", len(dataPoints)).Info("Sending data")
			sender.Send(dataPoints)
		}

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
	})

	sender := &LibhoneySender{}
	go sender.ReadResponses()

	run(options, sender)

}

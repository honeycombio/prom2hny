package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
	"testing"
)

func readMetrics() *bytes.Reader {
	dat, _ := ioutil.ReadFile("./fixtures/metrics.txt")
	return bytes.NewReader(dat)
}

func readResultsToJSON() []string {
	file, _ := os.Open("./fixtures/result.txt")
	defer file.Close()

	var resultJSON []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		resultJSON = append(resultJSON, scanner.Text())

	}

	return resultJSON
}

func generateResultFixture() {
	data := readMetrics()
	metricFamilies, _ := ParseResponse("text/plain", data)
	metricGroups := NewMetricGroups(metricFamilies)

	f, _ := os.Create("./fixtures/result.txt")
	defer f.Close()

	w := bufio.NewWriter(f)

	for _, mg := range metricGroups {
		ev := mg.ToEvent()
		evJSON, _ := json.Marshal(ev)
		fmt.Fprintln(w, string(evJSON))
	}

	w.Flush()

}

// Compares generated events from fixtures/metrics.txt with expected result in fixtures/result.txt
func TestEndToEnd(t *testing.T) {
	data := readMetrics()
	metricFamilies, _ := ParseResponse("text/plain", data)
	metricGroups := NewMetricGroups(metricFamilies)

	resultJSON := readResultsToJSON()

	// Create JSON from generated Honeycomb Events
	var rawJSON []string
	for _, mg := range metricGroups {
		ev := mg.ToEvent()
		evJSON, _ := json.Marshal(ev)
		rawJSON = append(rawJSON, string(evJSON))
	}

	// Compare result with raw by sorting and compare the "data" field
	sort.Strings(resultJSON)
	sort.Strings(rawJSON)

	var curRaw map[string]string
	var curResult map[string]string

	for i, raw := range rawJSON {
		json.Unmarshal([]byte(raw), &curRaw)
		json.Unmarshal([]byte(resultJSON[i]), &curResult)

		if curRaw["data"] != curResult["data"] {
			t.Error(raw)
			t.Error(resultJSON[i])
			t.Fatal("Did not receive expected result")
		}
	}
}

func TestMetricNameValidaton(t *testing.T) {
	data := readMetrics()
	metricFamilies, _ := ParseResponse("text/plain", data)
	for _, mf := range metricFamilies {
		name := mf.GetName()
		if isValid := validateMetricName(name); !isValid {
			t.Errorf("%s should be a valid metric name", name)
		}
	}

	badNames := [...]string{
		"bad_name",
		"",
		"blah_kube_blah_blah",
		"kube__wut",
		"have you heard the story about the cat who reached the stars?",
	}

	for _, bn := range badNames {
		if isValid := validateMetricName(bn); isValid {
			t.Errorf("%s should not be a valid metric name", bn)
		}
	}
}

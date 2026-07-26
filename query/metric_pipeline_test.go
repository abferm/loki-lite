package query

import (
	"testing"
	"time"

	"github.com/abferm/loki-lite/model"
	"github.com/prometheus/prometheus/model/labels"
)

func TestMetricQLCountOverTime(t *testing.T) {
	mp, err := MetricQL(`count_over_time({job="sshd"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	matchers := mp.Matchers()
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Name != "job" || matchers[0].Value != "sshd" {
		t.Errorf("matcher = %v, want job=sshd", matchers[0])
	}
}

func TestMetricQLProcessMatch(t *testing.T) {
	mp, err := MetricQL(`count_over_time({job="sshd"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "hello",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	values, ok := mp.Process(entry)
	if !ok {
		t.Fatal("expected match")
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0] != 1.0 {
		t.Errorf("count_over_time value = %v, want 1.0", values[0])
	}
}

func TestMetricQLProcessNoMatch(t *testing.T) {
	mp, err := MetricQL(`count_over_time({job="nginx"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "hello",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	_, ok := mp.Process(entry)
	if ok {
		t.Fatal("expected no match")
	}
}

func TestMetricQLInvalidQuery(t *testing.T) {
	_, err := MetricQL(`not_a_metric({job="sshd"}[5m])`)
	if err == nil {
		t.Fatal("expected error for invalid metric query")
	}
}

func TestMetricQLBytesOverTime(t *testing.T) {
	mp, err := MetricQL(`bytes_over_time({job="sshd"}[5m])`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "hello",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	values, ok := mp.Process(entry)
	if !ok {
		t.Fatal("expected match")
	}
	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}
	if values[0] != 5.0 {
		t.Errorf("bytes_over_time value = %v, want 5.0", values[0])
	}
}

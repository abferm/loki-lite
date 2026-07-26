package query

import (
	"testing"
	"time"

	"github.com/abferm/loki-lite/model"
	"github.com/prometheus/prometheus/model/labels"
)

func TestLogQLSimpleSelector(t *testing.T) {
	lp, err := LogQL(`{job="sshd"}`)
	if err != nil {
		t.Fatal(err)
	}

	matchers := lp.Matchers()
	if len(matchers) != 1 {
		t.Fatalf("expected 1 matcher, got %d", len(matchers))
	}
	if matchers[0].Name != "job" || matchers[0].Value != "sshd" {
		t.Errorf("matcher = %v, want job=sshd", matchers[0])
	}
}

func TestLogQLWithLineFilter(t *testing.T) {
	lp, err := LogQL(`{job="sshd"} |= "error"`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "error: connection refused",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	got, ok := lp.Process(entry)
	if !ok {
		t.Fatal("expected match, got no match")
	}
	if got.Line != "error: connection refused" {
		t.Errorf("Line = %q, want %q", got.Line, "error: connection refused")
	}
}

func TestLogQLNoMatch(t *testing.T) {
	lp, err := LogQL(`{job="nginx"}`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "hello",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	_, ok := lp.Process(entry)
	if ok {
		t.Fatal("expected no match, got match")
	}
}

func TestLogQLLineFilterNoMatch(t *testing.T) {
	lp, err := LogQL(`{job="sshd"} |= "error"`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:    time.Unix(0, 0),
		Line:         "connection established",
		StreamLabels: labels.FromStrings("job", "sshd"),
	}

	_, ok := lp.Process(entry)
	if ok {
		t.Fatal("expected no match for line filter, got match")
	}
}

func TestLogQLInvalidQuery(t *testing.T) {
	_, err := LogQL(`{job=`)
	if err == nil {
		t.Fatal("expected error for invalid query")
	}
}

func TestLogQLPreservesStructuredMetadata(t *testing.T) {
	lp, err := LogQL(`{job="sshd"}`)
	if err != nil {
		t.Fatal(err)
	}

	entry := model.Entry{
		Timestamp:          time.Unix(0, 0),
		Line:               "hello",
		StreamLabels:       labels.FromStrings("job", "sshd"),
		StructuredMetadata: labels.FromStrings("PID", "1234"),
	}

	got, ok := lp.Process(entry)
	if !ok {
		t.Fatal("expected match")
	}
	if got.StructuredMetadata.Get("PID") != "1234" {
		t.Errorf("StructuredMetadata PID = %q, want %q", got.StructuredMetadata.Get("PID"), "1234")
	}
}

package model

import (
	"testing"
	"time"

	"github.com/abferm/loki-lite/journal"
	"github.com/prometheus/prometheus/model/labels"
)

func TestSchemaStreamLabels(t *testing.T) {
	schema := Schema{Labels: []string{"job", "instance"}}
	fields := map[string]string{
		"job":      "sshd",
		"instance": "host1",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
	}

	got := schema.StreamLabels(fields)
	want := labels.FromStrings("instance", "host1", "job", "sshd")
	if !labels.Equal(got, want) {
		t.Errorf("StreamLabels = %v, want %v", got, want)
	}
}

func TestSchemaStreamLabelsMissing(t *testing.T) {
	schema := Schema{Labels: []string{"job", "missing"}}
	fields := map[string]string{
		"job":     "sshd",
		"MESSAGE": "hello",
	}

	got := schema.StreamLabels(fields)
	want := labels.FromStrings("job", "sshd")
	if !labels.Equal(got, want) {
		t.Errorf("StreamLabels = %v, want %v", got, want)
	}
}

func TestSchemaStructuredMetadata(t *testing.T) {
	schema := Schema{Labels: []string{"job"}}
	fields := map[string]string{
		"job":      "sshd",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
		"_PID":     "1234",
	}

	got := schema.StructuredMetadata(fields)
	want := labels.FromStrings("PRIORITY", "4", "_PID", "1234")
	if !labels.Equal(got, want) {
		t.Errorf("StructuredMetadata = %v, want %v", got, want)
	}
}

func TestSchemaStructuredMetadataExcludesMessage(t *testing.T) {
	schema := Schema{Labels: []string{}}
	fields := map[string]string{
		"MESSAGE": "hello",
	}

	got := schema.StructuredMetadata(fields)
	if got.Len() != 0 {
		t.Errorf("StructuredMetadata should be empty, got %v", got)
	}
}

func TestSchemaEntry(t *testing.T) {
	schema := Schema{Labels: []string{"job"}}
	entry := journal.Entry{
		Timestamp: time.Unix(1000, 0),
		Fields: map[string]string{
			"job":      "sshd",
			"MESSAGE":  "hello world",
			"PRIORITY": "4",
		},
	}

	got := schema.Entry(entry)

	if got.Timestamp != entry.Timestamp {
		t.Errorf("Timestamp = %v, want %v", got.Timestamp, entry.Timestamp)
	}
	if got.Line != "hello world" {
		t.Errorf("Line = %q, want %q", got.Line, "hello world")
	}

	wantStream := labels.FromStrings("job", "sshd")
	if !labels.Equal(got.StreamLabels, wantStream) {
		t.Errorf("StreamLabels = %v, want %v", got.StreamLabels, wantStream)
	}

	wantMeta := labels.FromStrings("PRIORITY", "4")
	if !labels.Equal(got.StructuredMetadata, wantMeta) {
		t.Errorf("StructuredMetadata = %v, want %v", got.StructuredMetadata, wantMeta)
	}
}

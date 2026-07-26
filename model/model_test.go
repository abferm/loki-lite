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
	want := labels.FromStrings("priority", "4", "_pid", "1234")
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

func TestSchemaLabelNames(t *testing.T) {
	schema := Schema{Labels: []string{"job", "PRIORITY"}}
	got := schema.LabelNames()
	if len(got) != 2 || got[0] != "job" || got[1] != "priority" {
		t.Errorf("LabelNames = %v, want [job priority]", got)
	}
}

func TestSchemaLabelNamesEmpty(t *testing.T) {
	schema := Schema{}
	got := schema.LabelNames()
	if len(got) != 0 {
		t.Errorf("LabelNames = %v, want []", got)
	}
}

func TestSchemaIsLabel(t *testing.T) {
	schema := Schema{Labels: []string{"job", "PRIORITY"}}
	if !schema.IsLabel("job") {
		t.Error("IsLabel(job) = false, want true")
	}
	if schema.IsLabel("MESSAGE") {
		t.Error("IsLabel(MESSAGE) = true, want false")
	}
}

func TestNewSchemaDeduplicates(t *testing.T) {
	schema := NewSchema([]string{"job", "job", "PRIORITY"})
	if len(schema.Labels) != 2 {
		t.Errorf("expected 2 labels, got %d: %v", len(schema.Labels), schema.Labels)
	}
}

func TestFieldToLabelKeys(t *testing.T) {
	schema := Schema{Labels: []string{"job", "PRIORITY", "instance"}}
	got := schema.FieldToLabelKeys([]string{"MESSAGE", "job", "instance"})
	want := []string{"job", "instance"}
	if len(got) != len(want) {
		t.Fatalf("FieldToLabelKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FieldToLabelKeys = %v, want %v", got, want)
		}
	}
}

func TestFieldToLabelKeysLowercases(t *testing.T) {
	schema := Schema{Labels: []string{"PRIORITY", "_SYSTEMD_UNIT"}}
	got := schema.FieldToLabelKeys([]string{"PRIORITY", "_SYSTEMD_UNIT", "MESSAGE"})
	want := []string{"priority", "_systemd_unit"}
	if len(got) != len(want) {
		t.Fatalf("FieldToLabelKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FieldToLabelKeys = %v, want %v", got, want)
		}
	}
}

func TestStreamLabelsMap(t *testing.T) {
	schema := Schema{Labels: []string{"job", "instance"}}
	fields := map[string]string{
		"job":      "sshd",
		"instance": "host1",
		"MESSAGE":  "hello",
	}
	got := schema.StreamLabelsMap(fields)
	want := map[string]string{"job": "sshd", "instance": "host1"}
	if len(got) != len(want) {
		t.Fatalf("StreamLabelsMap = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("StreamLabelsMap[%s] = %q, want %q", k, got[k], v)
		}
	}
}

func TestStructuredMetadataMap(t *testing.T) {
	schema := Schema{Labels: []string{"job"}}
	fields := map[string]string{
		"job":      "sshd",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
	}
	got := schema.StructuredMetadataMap(fields)
	want := map[string]string{"priority": "4"}
	if len(got) != len(want) {
		t.Fatalf("StructuredMetadataMap = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("StructuredMetadataMap[%s] = %q, want %q", k, got[k], v)
		}
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

	wantMeta := labels.FromStrings("priority", "4")
	if !labels.Equal(got.StructuredMetadata, wantMeta) {
		t.Errorf("StructuredMetadata = %v, want %v", got.StructuredMetadata, wantMeta)
	}
}

func TestSchemaStreamLabelsLowercases(t *testing.T) {
	schema := Schema{Labels: []string{"PRIORITY", "_SYSTEMD_UNIT"}}
	fields := map[string]string{
		"PRIORITY":        "4",
		"_SYSTEMD_UNIT":   "sshd.service",
	}

	got := schema.StreamLabels(fields)
	want := labels.FromStrings("priority", "4", "_systemd_unit", "sshd.service")
	if !labels.Equal(got, want) {
		t.Errorf("StreamLabels = %v, want %v", got, want)
	}
}

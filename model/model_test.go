package model

import (
	"testing"
	"time"

	"github.com/abferm/loki-lite/journal"
	"github.com/prometheus/prometheus/model/labels"
)

func TestSchemaStreamLabels(t *testing.T) {
	schema := Schema{Exclude: []string{"_PID"}}
	fields := map[string]string{
		"job":      "sshd",
		"instance": "host1",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
		"_PID":     "1234",
	}

	got := schema.StreamLabels(fields)
	want := labels.FromStrings("instance", "host1", "job", "sshd", "priority", "4")
	if !labels.Equal(got, want) {
		t.Errorf("StreamLabels = %v, want %v", got, want)
	}
}

func TestSchemaStreamLabelsEmptyExclude(t *testing.T) {
	schema := Schema{}
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

func TestSchemaStreamLabelsExcludesMessage(t *testing.T) {
	schema := Schema{}
	fields := map[string]string{
		"job":     "sshd",
		"MESSAGE": "hello",
	}

	got := schema.StreamLabels(fields)
	if got.Get("message") != "" {
		t.Error("MESSAGE should not appear in stream labels")
	}
}

func TestSchemaStructuredMetadata(t *testing.T) {
	schema := Schema{Exclude: []string{"_PID", "_COMM"}}
	fields := map[string]string{
		"job":      "sshd",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
		"_PID":     "1234",
		"_COMM":    "sshd",
	}

	got := schema.StructuredMetadata(fields)
	want := labels.FromStrings("_comm", "sshd", "_pid", "1234")
	if !labels.Equal(got, want) {
		t.Errorf("StructuredMetadata = %v, want %v", got, want)
	}
}

func TestSchemaStructuredMetadataExcludesMessage(t *testing.T) {
	schema := Schema{Exclude: []string{"MESSAGE"}}
	fields := map[string]string{
		"MESSAGE": "hello",
	}

	got := schema.StructuredMetadata(fields)
	if got.Len() != 0 {
		t.Errorf("StructuredMetadata should be empty, got %v", got)
	}
}

func TestSchemaLabelNames(t *testing.T) {
	schema := Schema{Exclude: []string{"_PID", "_COMM"}}
	got := schema.LabelNames()
	if len(got) != 2 || got[0] != "_pid" || got[1] != "_comm" {
		t.Errorf("LabelNames = %v, want [_pid _comm]", got)
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
	schema := Schema{Exclude: []string{"_PID", "_COMM"}}
	if !schema.IsLabel("job") {
		t.Error("IsLabel(job) = false, want true")
	}
	if schema.IsLabel("MESSAGE") {
		t.Error("IsLabel(MESSAGE) = true, want false")
	}
	if schema.IsLabel("_PID") {
		t.Error("IsLabel(_PID) = true, want false")
	}
}

func TestNewSchemaDeduplicates(t *testing.T) {
	schema := NewSchema([]string{"_PID", "_PID", "_COMM"})
	if len(schema.Exclude) != 2 {
		t.Errorf("expected 2 excluded, got %d: %v", len(schema.Exclude), schema.Exclude)
	}
}

func TestStreamLabelsMap(t *testing.T) {
	schema := Schema{Exclude: []string{"_PID"}}
	fields := map[string]string{
		"job":      "sshd",
		"instance": "host1",
		"MESSAGE":  "hello",
		"_PID":     "1234",
	}
	got := schema.StreamLabelsMap(fields)
	want := map[string]string{"instance": "host1", "job": "sshd"}
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
	schema := Schema{Exclude: []string{"_PID"}}
	fields := map[string]string{
		"job":      "sshd",
		"MESSAGE":  "hello",
		"PRIORITY": "4",
		"_PID":     "1234",
	}
	got := schema.StructuredMetadataMap(fields)
	want := map[string]string{"_pid": "1234"}
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
	schema := Schema{Exclude: []string{"PRIORITY"}}
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
	schema := Schema{Exclude: []string{"_PID"}}
	fields := map[string]string{
		"PRIORITY":        "4",
		"_SYSTEMD_UNIT":   "sshd.service",
		"_PID":            "1234",
	}

	got := schema.StreamLabels(fields)
	want := labels.FromStrings("priority", "4", "_systemd_unit", "sshd.service")
	if !labels.Equal(got, want) {
		t.Errorf("StreamLabels = %v, want %v", got, want)
	}
}

func TestSchemaFieldName(t *testing.T) {
	schema := Schema{Exclude: []string{"_PID", "_COMM"}}
	if got := schema.FieldName("_pid"); got != "_PID" {
		t.Errorf("FieldName(_pid) = %q, want %q", got, "_PID")
	}
	if got := schema.FieldName("_PID"); got != "_PID" {
		t.Errorf("FieldName(_PID) = %q, want %q", got, "_PID")
	}
	// Non-excluded fields return name unchanged.
	if got := schema.FieldName("job"); got != "job" {
		t.Errorf("FieldName(job) = %q, want %q", got, "job")
	}
}

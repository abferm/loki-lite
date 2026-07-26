// Package model defines the shared types that bridge journald entries to
// Loki-compatible log entries. Schema controls which journald fields become
// stream labels versus structured metadata.
package model

import (
	"slices"
	"strings"
	"time"

	"github.com/abferm/loki-lite/journal"
	"github.com/prometheus/prometheus/model/labels"
)

// Schema defines which journald fields become Loki stream labels.
// Fields not listed become structured metadata. The MESSAGE field is always
// mapped to the log line.
type Schema struct {
	Labels []string
}

// NewSchema creates a Schema with deduplicated label names.
func NewSchema(labelNames []string) Schema {
	return Schema{Labels: unique(labelNames)}
}

// LabelNames returns the configured label field names, lowercased to match
// Loki conventions.
func (s Schema) LabelNames() []string {
	out := make([]string, len(s.Labels))
	for i, l := range s.Labels {
		out[i] = strings.ToLower(l)
	}
	return out
}

// IsLabel reports whether name matches one of the configured label fields,
// using case-insensitive comparison.
func (s Schema) IsLabel(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(s.Labels, func(l string) bool {
		return strings.ToLower(l) == lower
	})
}

// FieldToLabelKeys returns the subset of fieldKeys that are configured label
// fields, lowercased and preserving the order of s.Labels.
func (s Schema) FieldToLabelKeys(fieldKeys []string) []string {
	set := make(map[string]struct{}, len(fieldKeys))
	for _, k := range fieldKeys {
		set[strings.ToLower(k)] = struct{}{}
	}
	var out []string
	for _, l := range s.Labels {
		if _, ok := set[strings.ToLower(l)]; ok {
			out = append(out, strings.ToLower(l))
		}
	}
	return out
}

// StreamLabelsMap extracts the configured label fields from fields and returns
// them as a plain map with lowercased keys. Fields not present in the input
// are omitted.
func (s Schema) StreamLabelsMap(fields map[string]string) map[string]string {
	m := make(map[string]string, len(s.Labels))
	for _, name := range s.Labels {
		if v, ok := fields[name]; ok {
			m[strings.ToLower(name)] = v
		}
	}
	return m
}

// StreamLabels builds a labels.Labels from the configured label fields.
func (s Schema) StreamLabels(fields map[string]string) labels.Labels {
	return labels.FromMap(s.StreamLabelsMap(fields))
}

// StructuredMetadataMap returns all fields that are NOT configured labels and
// NOT MESSAGE, as a plain map with lowercased keys.
func (s Schema) StructuredMetadataMap(fields map[string]string) map[string]string {
	labelSet := make(map[string]struct{}, len(s.Labels))
	for _, l := range s.Labels {
		labelSet[strings.ToLower(l)] = struct{}{}
	}

	m := make(map[string]string, len(fields))
	for k, v := range fields {
		lower := strings.ToLower(k)
		if _, isLabel := labelSet[lower]; !isLabel && k != "MESSAGE" {
			m[lower] = v
		}
	}
	return m
}

// StructuredMetadata builds a labels.Labels from all fields NOT in the
// configured label set and NOT MESSAGE.
func (s Schema) StructuredMetadata(fields map[string]string) labels.Labels {
	return labels.FromMap(s.StructuredMetadataMap(fields))
}

// Entry converts a journal.Entry into a Loki-compatible representation.
// MESSAGE becomes the Line, configured label fields become stream labels,
// and remaining fields become structured metadata.
func (s Schema) Entry(entry journal.Entry) Entry {
	return Entry{
		Timestamp:          entry.Timestamp,
		Line:               entry.Message(),
		StreamLabels:       s.StreamLabels(entry.Fields),
		StructuredMetadata: s.StructuredMetadata(entry.Fields),
	}
}

// Entry is a log entry with processed line, stream labels, and structured
// metadata — the inputs consumed by LogPipeline and MetricPipeline.
type Entry struct {
	Timestamp          time.Time
	Line               string
	StreamLabels       labels.Labels
	StructuredMetadata labels.Labels
}

func unique[T comparable](in []T) []T {
	seen := make(map[T]struct{}, len(in))
	var out []T
	for _, v := range in {
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

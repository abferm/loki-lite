// Package model defines the shared types that bridge journald entries to
// Loki-compatible log entries. Schema controls which journald fields become
// stream labels versus structured metadata.
package model

import (
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

// StreamLabels builds a labels.Labels from the configured label fields.
func (s Schema) StreamLabels(fields map[string]string) labels.Labels {
	b := labels.NewScratchBuilder(len(s.Labels))
	for _, name := range s.Labels {
		if v, ok := fields[name]; ok {
			b.Add(name, v)
		}
	}
	b.Sort()
	return b.Labels()
}

// StructuredMetadata builds a labels.Labels from all fields NOT in the
// configured label set and NOT MESSAGE.
func (s Schema) StructuredMetadata(fields map[string]string) labels.Labels {
	labelSet := make(map[string]struct{}, len(s.Labels))
	for _, name := range s.Labels {
		labelSet[name] = struct{}{}
	}

	count := 0
	for k := range fields {
		if _, ok := labelSet[k]; !ok && k != "MESSAGE" {
			count++
		}
	}

	b := labels.NewScratchBuilder(count)
	for k, v := range fields {
		if _, ok := labelSet[k]; !ok && k != "MESSAGE" {
			b.Add(k, v)
		}
	}
	b.Sort()
	return b.Labels()
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

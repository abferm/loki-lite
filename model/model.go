package model

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/util"
	"github.com/prometheus/prometheus/model/labels"
)

// Schema defines which journald fields become structured metadata (i.e., not
// stream labels). The MESSAGE field is always mapped to the log line. All
// other fields become stream labels by default.
type Schema struct {
	// Exclude lists high-cardinality fields that should NOT be stream labels.
	// These become structured metadata instead, still queryable via LogQL
	// label filter expressions but not included in the stream identity.
	Exclude []string
}

// NewSchema creates a Schema with deduplicated excluded field names.
func NewSchema(exclude []string) Schema {
	return Schema{Exclude: util.Unique(exclude)}
}

// IsLabel reports whether name is NOT excluded and NOT MESSAGE, using
// case-insensitive comparison.
func (s Schema) IsLabel(name string) bool {
	lower := strings.ToLower(name)
	if lower == "message" {
		return false
	}
	for _, ex := range s.Exclude {
		if strings.ToLower(ex) == lower {
			return false
		}
	}
	return true
}

// FieldName resolves a case-insensitive name to its original case. Checks
// Exclude first, then returns name unchanged (the caller should validate
// via IsLabel or verify against the journal).
func (s Schema) FieldName(name string) string {
	lower := strings.ToLower(name)
	for _, ex := range s.Exclude {
		if strings.ToLower(ex) == lower {
			return ex
		}
	}
	return name
}

// LabelNames returns all excluded field names lowercased.
func (s Schema) LabelNames() []string {
	out := make([]string, len(s.Exclude))
	for i, l := range s.Exclude {
		out[i] = strings.ToLower(l)
	}
	return out
}

// Excluded returns a case-insensitive set of excluded field names.
func (s Schema) Excluded() map[string]struct{} {
	set := make(map[string]struct{}, len(s.Exclude))
	for _, ex := range s.Exclude {
		set[strings.ToLower(ex)] = struct{}{}
	}
	return set
}

// StreamLabelsMap returns all fields that are NOT excluded and NOT MESSAGE,
// with lowercased keys.
func (s Schema) StreamLabelsMap(fields map[string]string) map[string]string {
	m := make(map[string]string, len(fields))
	excluded := s.Excluded()
	for k, v := range fields {
		lower := strings.ToLower(k)
		if _, ok := excluded[lower]; ok || k == "MESSAGE" {
			continue
		}
		m[lower] = v
	}
	return m
}

// StreamLabels builds a labels.Labels from all non-excluded, non-MESSAGE fields.
func (s Schema) StreamLabels(fields map[string]string) labels.Labels {
	return labels.FromMap(s.StreamLabelsMap(fields))
}

// StructuredMetadataMap returns only excluded fields as a plain map with
// lowercased keys. MESSAGE is always excluded from metadata.
func (s Schema) StructuredMetadataMap(fields map[string]string) map[string]string {
	excluded := s.Excluded()
	m := make(map[string]string, len(s.Exclude))
	for k, v := range fields {
		lower := strings.ToLower(k)
		if _, ok := excluded[lower]; ok && k != "MESSAGE" {
			m[lower] = v
		}
	}
	return m
}

// StructuredMetadata builds a labels.Labels from excluded fields only.
func (s Schema) StructuredMetadata(fields map[string]string) labels.Labels {
	return labels.FromMap(s.StructuredMetadataMap(fields))
}

// Entry converts a journal.Entry into a Loki-compatible representation.
// MESSAGE becomes the Line, excluded fields become structured metadata,
// and all other fields become stream labels.
// All field values are sanitized to valid UTF-8 because journald may
// contain binary data that cannot be transmitted in a WebSocket text frame.
func (s Schema) Entry(entry journal.Entry) Entry {
	fields := sanitizeFields(entry.Fields)
	return Entry{
		Timestamp:          entry.Timestamp,
		Line:               fields["MESSAGE"],
		StreamLabels:       s.StreamLabels(fields),
		StructuredMetadata: s.StructuredMetadata(fields),
	}
}

// sanitizeUTF8 replaces invalid UTF-8 sequences with the Unicode replacement
// character. This ensures journald binary data does not cause WebSocket text
// frame protocol errors.
func sanitizeUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	replacement := string(utf8.RuneError)
	return strings.ToValidUTF8(s, replacement)
}

// sanitizeFields returns a copy of m with all values sanitized to valid UTF-8.
func sanitizeFields(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = sanitizeUTF8(v)
	}
	return out
}

// Entry is a log entry with processed line, stream labels, and structured
// metadata — the inputs consumed by LogPipeline and MetricPipeline.
type Entry struct {
	Timestamp          time.Time
	Line               string
	StreamLabels       labels.Labels
	StructuredMetadata labels.Labels
}

// Package engine translates Loki API queries into journald journal operations.
// Methods correspond 1-to-1 with Loki query endpoints and accept only domain
// types — no HTTP request or response types leak into this package.
package engine

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
	"github.com/prometheus/prometheus/model/labels"
)

// DefaultLabelValuesLimit is the maximum number of distinct values returned
// by LabelValues. This prevents unbounded memory growth for high-cardinality
// fields like MESSAGE or timestamps.
const DefaultLabelValuesLimit = 10000

// ErrLabelExcluded is returned when a label is not in the schema's label set.
var ErrLabelExcluded = fmt.Errorf("label excluded by configuration")

// Stats holds approximate counts returned by IndexStats.
type Stats struct {
	Streams int64 `json:"streams"`
	Chunks  int64 `json:"chunks"`
	Entries int64 `json:"entries"`
	Bytes   int64 `json:"bytes"`
}

// Engine executes Loki-compatible queries against journald journals.
type Engine struct {
	journal *journal.Journal
	schema  *model.Schema
}

// New creates an Engine that reads from j and uses schema to determine which
// fields are stream labels. Fields not in schema.Labels are treated as
// structured metadata, not stream identity.
func New(j *journal.Journal, schema *model.Schema) *Engine {
	return &Engine{journal: j, schema: schema}
}

// QueryRange executes a range query over log streams.
func (e *Engine) QueryRange(query string, start, end time.Time, limit int, direction string, step, interval time.Duration) (any, error) {
	panic("unimplemented")
}

// Query executes an instant query that returns a single point-in-time result.
func (e *Engine) Query(query string, ts time.Time, limit int, direction string) (any, error) {
	panic("unimplemented")
}

// Labels returns the configured label names that are actually present in the
// journal files, lowercased to match Loki conventions.
func (e *Engine) Labels() ([]string, error) {
	fields, err := e.journal.Fields()
	if err != nil {
		return nil, err
	}
	return e.schema.FieldToLabelKeys(fields), nil
}

// LabelValues returns all distinct values for the named label across the
// available journal files, up to DefaultLabelValuesLimit values. Returns
// ErrLabelExcluded if the label is not in the schema.
func (e *Engine) LabelValues(name string) ([]string, error) {
	if !e.schema.IsLabel(name) {
		return nil, ErrLabelExcluded
	}

	vals, _, err := e.journal.FieldValues(name, DefaultLabelValuesLimit)
	if err != nil {
		return nil, err
	}
	return vals, nil
}

// forEachEntry calls fn for each entry from start to end (inclusive). Uses the
// correct SeekRealtime + Entry/Next iteration pattern.
func (e *Engine) forEachEntry(start, end time.Time, fn func(*journal.Entry)) {
	e.journal.SeekRealtime(start)

	if entry := e.journal.Entry(); entry != nil && !entry.Timestamp.After(end) {
		fn(entry)
	}
	for e.journal.Next() {
		entry := e.journal.Entry()
		if entry == nil || entry.Timestamp.After(end) {
			break
		}
		fn(entry)
	}
}

// Series returns the distinct label sets matching the given filters within the
// time range. Each filter represents a parsed stream selector.
func (e *Engine) Series(filters []any, start, end time.Time) ([]map[string]string, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{})
	e.forEachEntry(start, end, func(entry *journal.Entry) {
		for range filters {
			seen[streamKey(entry.Fields, e.schema)] = struct{}{}
			break
		}
	})

	result := make([]map[string]string, 0, len(seen))
	for key := range seen {
		result = append(result, parseStringNoSpace(key))
	}
	sort.Slice(result, func(i, j int) bool {
		return streamKey(result[i], e.schema) < streamKey(result[j], e.schema)
	})
	return result, nil
}

// IndexStats returns approximate counts of streams, chunks, entries, and bytes
// for the given filter and time range.
func (e *Engine) IndexStats(filter any, start, end time.Time) (*Stats, error) {
	stats := &Stats{}
	streams := make(map[string]struct{})

	e.forEachEntry(start, end, func(entry *journal.Entry) {
		stats.Entries++
		stats.Bytes += int64(len(entry.Message()))
		streams[streamKey(entry.Fields, e.schema)] = struct{}{}
	})

	stats.Streams = int64(len(streams))
	stats.Chunks = int64(e.journal.NFiles())
	return stats, nil
}

// streamKey produces a deterministic string key for the schema-defined labels
// in fields using labels.Labels.StringNoSpace.
func streamKey(fields map[string]string, schema *model.Schema) string {
	return labels.FromMap(schema.StreamLabelsMap(fields)).StringNoSpace()
}

// parseStringNoSpace parses a labels.Labels.StringNoSpace representation
// (e.g. `{PRIORITY="4",job="sshd"}`) back into a map.
func parseStringNoSpace(s string) map[string]string {
	s = strings.TrimPrefix(s, "{")
	s = strings.TrimSuffix(s, "}")
	if s == "" {
		return map[string]string{}
	}
	m := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if ok {
			m[k] = strings.Trim(v, `"`)
		}
	}
	return m
}

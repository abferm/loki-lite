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
)

// DefaultLabelValuesLimit is the maximum number of distinct values returned
// by LabelValues. This prevents unbounded memory growth for high-cardinality
// fields like MESSAGE or timestamps.
const DefaultLabelValuesLimit = 10000

// ErrLabelExcluded is returned when a label is in the engine's blacklist
// and cannot be queried.
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
	journal   *journal.Journal
	blacklist map[string]struct{}
}

// New creates an Engine that reads from j and excludes blacklisted labels
// from Labels and LabelValues results. The blacklist is a list of field
// names (e.g. "MESSAGE", "__REALTIME_TIMESTAMP") that should not be
// treated as Loki labels.
func New(j *journal.Journal, blacklist []string) *Engine {
	bl := make(map[string]struct{}, len(blacklist))
	for _, name := range blacklist {
		bl[name] = struct{}{}
	}
	return &Engine{journal: j, blacklist: bl}
}

// QueryRange executes a range query over log streams.
func (e *Engine) QueryRange(query string, start, end time.Time, limit int, direction string, step, interval time.Duration) (any, error) {
	panic("unimplemented")
}

// Query executes an instant query that returns a single point-in-time result.
func (e *Engine) Query(query string, ts time.Time, limit int, direction string) (any, error) {
	panic("unimplemented")
}

// Labels returns all distinct label names across the available journal files,
// excluding blacklisted fields.
func (e *Engine) Labels() ([]string, error) {
	fields, err := e.journal.Fields()
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(fields))
	for _, name := range fields {
		if _, excluded := e.blacklist[name]; !excluded {
			result = append(result, name)
		}
	}
	return result, nil
}

// LabelValues returns all distinct values for the named label across the
// available journal files, up to DefaultLabelValuesLimit values. Returns
// ErrLabelExcluded if the label is blacklisted.
func (e *Engine) LabelValues(name string) ([]string, error) {
	if _, excluded := e.blacklist[name]; excluded {
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
			seen[labelSetKey(entry.Fields, e.blacklist)] = struct{}{}
			break
		}
	})

	result := make([]map[string]string, 0, len(seen))
	for key := range seen {
		result = append(result, keyToLabelSet(key))
	}
	sort.Slice(result, func(i, j int) bool {
		return labelSetKey(result[i], e.blacklist) < labelSetKey(result[j], e.blacklist)
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
		streams[labelSetKey(entry.Fields, e.blacklist)] = struct{}{}
	})

	stats.Streams = int64(len(streams))
	stats.Chunks = int64(e.journal.NFiles())
	return stats, nil
}

// labelSetKey produces a deterministic string key for a label map by sorting
// the key-value pairs. Blacklisted fields are excluded since they represent
// log line content, not stream identity.
func labelSetKey(fields map[string]string, blacklist map[string]struct{}) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if _, excluded := blacklist[k]; !excluded {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var b strings.Builder
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(fields[k])
	}
	return b.String()
}

// keyToLabelSet converts a sorted "k=v,k=v" string back into a map.
func keyToLabelSet(key string) map[string]string {
	if key == "" {
		return map[string]string{}
	}
	m := make(map[string]string)
	for _, pair := range strings.Split(key, ",") {
		if idx := strings.IndexByte(pair, '='); idx >= 0 {
			m[pair[:idx]] = pair[idx+1:]
		}
	}
	return m
}

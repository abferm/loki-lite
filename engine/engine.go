// Package engine translates Loki API queries into journald journal operations.
// Methods correspond 1-to-1 with Loki query endpoints and accept only domain
// types — no HTTP request or response types leak into this package.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
	queryPkg "github.com/abferm/loki-lite/query"
	"github.com/abferm/loki-lite/util"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	prommodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
)

// DefaultLabelValuesLimit is the maximum number of distinct values returned
// by LabelValues. This prevents unbounded memory growth for high-cardinality
// fields like MESSAGE or timestamps.
const DefaultLabelValuesLimit = 10000

// ErrLabelExcluded is returned when a label is not in the schema's label set.
var ErrLabelExcluded = fmt.Errorf("label excluded by configuration")

// Engine executes Loki-compatible queries against journald journals. For
// each request it acquires a Journal from the pool, using it for the
// duration of that request before releasing it back.
type Engine struct {
	pool   *util.Pool[*journal.Journal]
	schema *model.Schema
}

// New creates an Engine that acquires Journals from p for each request.
// Non-excluded fields become stream labels; excluded fields become structured
// metadata.
func New(p *util.Pool[*journal.Journal], schema *model.Schema) *Engine {
	return &Engine{pool: p, schema: schema}
}

// LogQueryRange executes a log query over a time range, returning log stream
// entries that match the LogQL selector. This corresponds to the
// /loki/api/v1/query_range endpoint for log queries.
//
// query is a LogQL log selector expression (e.g. `{job="sshd"}`).
// start and end define the time window to search.
// limit caps the total number of log entries returned.
// direction controls whether entries are returned newest-first (BACKWARD)
// or oldest-first (FORWARD).
//
// Returns loghttp.Streams which can be marshaled directly with json-iterator
// to produce Loki-compatible JSON (entries serialized as ["ts","line"] arrays).
func (e *Engine) LogQueryRange(query string, start, end time.Time, limit int, direction logproto.Direction) (loghttp.Streams, error) {
	pipeline, err := queryPkg.LogQL(query)
	if err != nil {
		return nil, err
	}

	type timedEntry struct {
		streamKey string
		streamLbl labels.Labels
		entry     loghttp.Entry
	}

	var all []timedEntry

	if direction == logproto.BACKWARD {
		// BACKWARD must return the newest entries first. journald files aren't
		// indexable backward, so scan forward once, keep the newest `limit`
		// matches, then reverse — File.Previous would rescan each file from its
		// head on every step (O(n²) over the window).
		var kept []timedEntry
		keptStart := 0
		if err := e.forEachEntry(start, end, func(je *journal.Entry) bool {
			modelEntry := e.schema.Entry(*je)
			processed, ok := pipeline.Process(modelEntry)
			if !ok {
				return true
			}
			kept = append(kept, timedEntry{
				streamKey: processed.StreamLabels.StringNoSpace(),
				streamLbl: processed.StreamLabels,
				entry: loghttp.Entry{
					Timestamp: processed.Timestamp,
					Line:      processed.Line,
				},
			})
			if limit > 0 && len(kept)-keptStart > limit {
				keptStart++
				if keptStart >= limit {
					kept = append(kept[:0], kept[keptStart:]...)
					keptStart = 0
				}
			}
			return true
		}); err != nil {
			return nil, err
		}
		n := len(kept) - keptStart
		all = make([]timedEntry, 0, n)
		for i := n - 1; i >= 0; i-- {
			all = append(all, kept[keptStart+i])
		}
	} else if err := e.forEachEntry(start, end, func(je *journal.Entry) bool {
		modelEntry := e.schema.Entry(*je)
		processed, ok := pipeline.Process(modelEntry)
		if !ok {
			return true
		}
		all = append(all, timedEntry{
			streamKey: processed.StreamLabels.StringNoSpace(),
			streamLbl: processed.StreamLabels,
			entry: loghttp.Entry{
				Timestamp: processed.Timestamp,
				Line:      processed.Line,
			},
		})
		return limit <= 0 || len(all) < limit
	}); err != nil {
		return nil, err
	}
	grouped := make(map[string]*loghttp.Stream)
	for _, te := range all {
		s, exists := grouped[te.streamKey]
		if !exists {
			s = &loghttp.Stream{Labels: loghttp.LabelSet(te.streamLbl.Map())}
			grouped[te.streamKey] = s
		}
		s.Entries = append(s.Entries, te.entry)
	}

	result := make(loghttp.Streams, 0, len(grouped))
	for _, s := range grouped {
		result = append(result, *s)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Labels.String() < result[j].Labels.String()
	})

	return result, nil
}

// MetricQueryRange executes a metric query over a time range, returning
// sampled results at the given step interval. This corresponds to the
// /loki/api/v1/query_range endpoint for metric queries (e.g. rate,
// count_over_time, bytes_over_time).
//
// query is a LogQL sample expression (e.g. `count_over_time({job="sshd"}[5m])`).
// start and end define the time window to search.
// step is the sampling interval; a data point is produced for each step
// within [start, end].
// direction is accepted for API compatibility but does not affect the output
// order of sampled data points.
//
// Returns a loghttp.Matrix — one SampleStream per distinct stream, each
// containing a SamplePair for every step that had matching entries.
func (e *Engine) MetricQueryRange(query string, start, end time.Time, step time.Duration, direction logproto.Direction) (loghttp.Matrix, error) {
	pipeline, err := queryPkg.MetricQL(query)
	if err != nil {
		return nil, err
	}

	if !pipeline.HasRealSelector() {
		val, err := pipeline.EvaluateLiteral()
		if err != nil {
			return nil, err
		}
		numSteps := int(end.Sub(start)/step) + 1
		var pairs []prommodel.SamplePair
		for i := 0; i < numSteps; i++ {
			ts := start.Add(time.Duration(i) * step)
			pairs = append(pairs, prommodel.SamplePair{
				Timestamp: prommodel.TimeFromUnixNano(ts.UnixNano()),
				Value:     prommodel.SampleValue(val),
			})
		}
		return loghttp.Matrix{
			{
				Metric: prommodel.Metric{},
				Values: pairs,
			},
		}, nil
	}

	numSteps := int(end.Sub(start)/step) + 1

	rangeDur := pipeline.Range()
	adjustedStart := start.Add(-rangeDur)
	if adjustedStart.Before(time.Unix(0, 0)) {
		adjustedStart = time.Unix(0, 0)
	}

	type streamAcc struct {
		metric prommodel.Metric
		values []float64
	}

	acc := make(map[string]*streamAcc)

	if err := e.forEachEntry(adjustedStart, end, func(je *journal.Entry) bool {
		modelEntry := e.schema.Entry(*je)
		values, ok := pipeline.Process(modelEntry)
		if !ok {
			return true
		}

		stepIdx := int(je.Timestamp.Sub(start) / step)
		if stepIdx < 0 || stepIdx >= numSteps {
			return true
		}

		key := modelEntry.StreamLabels.StringNoSpace()
		sa, exists := acc[key]
		if !exists {
			m := make(prommodel.Metric)
			modelEntry.StreamLabels.Range(func(l labels.Label) {
				m[prommodel.LabelName(l.Name)] = prommodel.LabelValue(l.Value)
			})
			sa = &streamAcc{metric: m, values: make([]float64, numSteps)}
			acc[key] = sa
		}

		for _, v := range values {
			sa.values[stepIdx] += v
		}

		return true
	}); err != nil {
		return nil, err
	}

	result := make(loghttp.Matrix, 0, len(acc))
	for _, sa := range acc {
		var pairs []prommodel.SamplePair
		for i, v := range sa.values {
			if v != 0 {
				pairs = append(pairs, prommodel.SamplePair{
					Timestamp: prommodel.TimeFromUnixNano(start.Add(time.Duration(i) * step).UnixNano()),
					Value:     prommodel.SampleValue(v),
				})
			}
		}
		if len(pairs) > 0 {
			result = append(result, prommodel.SampleStream{
				Metric: sa.metric,
				Values: pairs,
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Metric.Before(result[j].Metric)
	})

	return result, nil
}

// MetricQuery executes an instant metric query at a single point in time.
// This corresponds to the /loki/api/v1/query endpoint for metric expressions.
//
// query is a LogQL sample expression (e.g. `count_over_time({job="sshd"}[5m])`).
// ts is the evaluation timestamp; the lookback window is determined by the
// range selector in the query expression (e.g., [5m]).
// direction is accepted for API compatibility and does not affect evaluation.
//
// Returns a loghttp.Vector — one Sample per distinct stream.
func (e *Engine) MetricQuery(query string, ts time.Time, direction logproto.Direction) (loghttp.Vector, error) {
	pipeline, err := queryPkg.MetricQL(query)
	if err != nil {
		return nil, err
	}

	if !pipeline.HasRealSelector() {
		val, err := pipeline.EvaluateLiteral()
		if err != nil {
			return nil, err
		}
		return loghttp.Vector{
			{
				Metric: prommodel.Metric{},
				Value:  prommodel.SampleValue(val),
				Timestamp: prommodel.TimeFromUnixNano(ts.UnixNano()),
			},
		}, nil
	}

	rangeDur := pipeline.Range()
	if rangeDur == 0 {
		rangeDur = 5 * time.Minute // default lookback
	}

	start := ts.Add(-rangeDur)
	end := ts

	type streamAcc struct {
		metric prommodel.Metric
		value  float64
	}

	acc := make(map[string]*streamAcc)

	if err := e.forEachEntry(start, end, func(je *journal.Entry) bool {
		modelEntry := e.schema.Entry(*je)
		values, ok := pipeline.Process(modelEntry)
		if !ok {
			return true
		}

		key := modelEntry.StreamLabels.StringNoSpace()
		sa, exists := acc[key]
		if !exists {
			m := make(prommodel.Metric)
			modelEntry.StreamLabels.Range(func(l labels.Label) {
				m[prommodel.LabelName(l.Name)] = prommodel.LabelValue(l.Value)
			})
			sa = &streamAcc{metric: m}
			acc[key] = sa
		}

		for _, v := range values {
			sa.value += v
		}

		return true
	}); err != nil {
		return nil, err
	}

	result := make(loghttp.Vector, 0, len(acc))
	for _, sa := range acc {
		result = append(result, prommodel.Sample{
			Metric:    sa.metric,
			Value:     prommodel.SampleValue(sa.value),
			Timestamp: prommodel.TimeFromUnixNano(ts.UnixNano()),
		})
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Metric.Before(result[j].Metric)
	})

	return result, nil
}

// Labels returns all label names present in the journal files that are NOT
// in the schema's exclude list. Names are lowercased to match Loki
// conventions. This corresponds to the /loki/api/v1/labels endpoint.
func (e *Engine) Labels() ([]string, error) {
	j, err := e.pool.Acquire()
	if err != nil {
		return nil, err
	}
	defer e.pool.Release(j)

	fields, err := j.Fields()
	if err != nil {
		return nil, err
	}
	var out []string
	excluded := e.schema.Excluded()
	for _, f := range fields {
		lower := strings.ToLower(f)
		if _, ok := excluded[lower]; !ok && lower != "message" {
			out = append(out, lower)
		}
	}
	sort.Strings(out)
	return out, nil
}

// LabelValues returns all distinct values for the given label across the
// journal files, up to DefaultLabelValuesLimit (10000) values. This
// corresponds to the /loki/api/v1/label/{name}/values endpoint.
//
// name is the label to inspect (case-insensitive). Returns ErrLabelExcluded
// if the label is in the schema's exclude list.
func (e *Engine) LabelValues(name string) ([]string, error) {
	if !e.schema.IsLabel(name) {
		return nil, ErrLabelExcluded
	}

	j, err := e.pool.Acquire()
	if err != nil {
		return nil, err
	}
	defer e.pool.Release(j)

	// Resolve case-insensitive name to the exact journal field name.
	fieldName := e.schema.FieldName(name)
	if fieldName == name {
		// Not found in Exclude — find the exact case from journal fields.
		fields, err := j.Fields()
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(name)
		for _, f := range fields {
			if strings.ToLower(f) == lower {
				fieldName = f
				break
			}
		}
	}

	vals, _, err := j.FieldValues(fieldName, DefaultLabelValuesLimit)
	if err != nil {
		return nil, err
	}
	return vals, nil
}

// forEachEntry acquires a Journal from the pool, calls fn for each entry from
// start to end (inclusive) in forward order, and releases it. fn returns true
// to continue iterating, false to stop early.
func (e *Engine) forEachEntry(start, end time.Time, fn func(*journal.Entry) bool) error {
	j, err := e.pool.Acquire()
	if err != nil {
		return err
	}
	defer e.pool.Release(j)

	j.SeekRealtime(start)
	if entry := j.Entry(); entry != nil && !entry.Timestamp.After(end) {
		if !fn(entry) {
			return nil
		}
	}
	for j.Next() {
		entry := j.Entry()
		if entry == nil || entry.Timestamp.After(end) {
			break
		}
		if !fn(entry) {
			return nil
		}
	}
	return nil
}

// Series returns the distinct stream label sets that exist within the given
// time range. This corresponds to the /loki/api/v1/series endpoint.
//
// filters are parsed stream selectors (e.g. from LogQL) that identify which
// label sets to include. If filters is empty, nil is returned — callers must
// pass at least one matcher.
//
// The returned LabelSets use Loki's JSON key "stream" when marshaled, matching
// the expected format for /loki/api/v1/series responses.
func (e *Engine) Series(filters []*labels.Matcher, start, end time.Time) ([]loghttp.LabelSet, error) {
	if len(filters) == 0 {
		return nil, nil
	}

	seen := make(map[string]struct{})
	if err := e.forEachEntry(start, end, func(entry *journal.Entry) bool {
		for range filters {
			seen[streamKey(entry.Fields, e.schema)] = struct{}{}
			break
		}
		return true
	}); err != nil {
		return nil, err
	}

	result := make([]loghttp.LabelSet, 0, len(seen))
	for key := range seen {
		result = append(result, parseStringNoSpace(key))
	}
	sort.Slice(result, func(i, j int) bool {
		return streamKey(result[i], e.schema) < streamKey(result[j], e.schema)
	})
	return result, nil
}

// IndexStats returns approximate counts of streams, chunks, entries, and bytes
// for entries matching the given time range. This corresponds to the
// /loki/api/v1/index/stats endpoint.
//
// matchers is a LogQL stream selector string (e.g. `{job="sshd"}`). Currently
// treated as a match-all for any non-empty string; per-stream filtering will
// be implemented later.
//
// The returned IndexStatsResponse can be marshaled directly to produce
// Loki-compatible JSON with uint64 fields for streams, chunks, entries, and
// bytes.
func (e *Engine) IndexStats(matchers string, start, end time.Time) (*logproto.IndexStatsResponse, error) {
	stats := &logproto.IndexStatsResponse{}
	streams := make(map[string]struct{})

	if err := e.forEachEntry(start, end, func(entry *journal.Entry) bool {
		stats.Entries++
		stats.Bytes += uint64(len(entry.Message()))
		streams[streamKey(entry.Fields, e.schema)] = struct{}{}
		return true
	}); err != nil {
		return nil, err
	}

	stats.Streams = uint64(len(streams))

	j, err := e.pool.Acquire()
	if err != nil {
		return nil, err
	}
	stats.Chunks = uint64(j.NFiles())
	e.pool.Release(j)
	return stats, nil
}

// Tail streams log entries matching query starting from start. Positions the
// journal at start, processes the first matching entry there, then delegates
// to Journal.Follow for all subsequent entries (existing and new). Blocks
// until ctx is cancelled. Each matching entry is passed to cb with its stream
// key, stream labels, and loghttp.Entry.
func (e *Engine) Tail(ctx context.Context, query string, start time.Time, _ int, cb func(string, labels.Labels, loghttp.Entry)) error {
	pipeline, err := queryPkg.LogQL(query)
	if err != nil {
		return err
	}

	j, err := e.pool.Acquire()
	if err != nil {
		return err
	}
	defer e.pool.Release(j)

	j.SeekRealtime(start)

	// Send current entry if it matches the query.
	if entry := j.Entry(); entry != nil {
		modelEntry := e.schema.Entry(*entry)
		processed, ok := pipeline.Process(modelEntry)
		if ok {
			cb(processed.StreamLabels.StringNoSpace(), processed.StreamLabels, loghttp.Entry{
				Timestamp: processed.Timestamp,
				Line:      processed.Line,
			})
		}
	}

	// Live follow — never stop based on limit.
	if err := j.Follow(ctx, 100*time.Millisecond, func(je *journal.Entry) bool {
		modelEntry := e.schema.Entry(*je)
		processed, ok := pipeline.Process(modelEntry)
		if !ok {
			return true
		}
		cb(processed.StreamLabels.StringNoSpace(), processed.StreamLabels, loghttp.Entry{
			Timestamp: processed.Timestamp,
			Line:      processed.Line,
		})
		return true
	}); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	return nil
}

// streamKey produces a deterministic string key for the stream labels in
// fields, matching the stream identity used in LogQueryRange.
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

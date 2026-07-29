package handler

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/abferm/loki-lite/engine"
	"github.com/grafana/loki/v3/pkg/loghttp"
	"github.com/grafana/loki/v3/pkg/logproto"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

type queryStats struct {
	resultType  string
	resultCount int
}

type statsCtxKeyType struct{}

var statsCtxKey statsCtxKeyType

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
	bytes      int64
}

func (w *loggingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *loggingResponseWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)
	return n, err
}

const (
	defaultQueryLimit = 100
	defaultSince      = 1 * time.Hour
	defaultDirection  = logproto.BACKWARD
)

// Handler serves Loki-compatible HTTP endpoints backed by an Engine.
type Handler struct {
	engine *engine.Engine
	mux    *http.ServeMux
}

// New creates a Handler that delegates query execution to eng.
func New(eng *engine.Engine) *Handler {
	h := &Handler{engine: eng, mux: http.NewServeMux()}
	h.registerRoutes()
	return h
}

// Handler returns the http.Handler for this Loki API with compression and
// logging middleware.
func (h *Handler) Handler() http.Handler {
	return h.loggingMiddleware(WithCompression(h.mux))
}

type gzipResponseWriter struct {
	http.ResponseWriter
	w *gzip.Writer
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	return w.w.Write(b)
}

// WithCompression wraps h with gzip compression when the client includes
// Accept-Encoding: gzip in the request. Browsers, Grafana, and most HTTP
// clients send this header by default.
func WithCompression(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := gzip.NewWriter(w)
		defer gw.Close()
		w.Header().Set("Content-Encoding", "gzip")
		next.ServeHTTP(&gzipResponseWriter{ResponseWriter: w, w: gw}, r)
	})
}

// loggingMiddleware wraps an http.Handler with request logging.
func (h *Handler) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		stats := &queryStats{}
		ctx := context.WithValue(r.Context(), statsCtxKey, stats)
		r = r.WithContext(ctx)
		lrw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lrw, r)

		query := r.FormValue("query")
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", lrw.statusCode),
			slog.Int64("bytes", lrw.bytes),
			slog.Duration("duration", time.Since(start)),
		}
		if query != "" {
			attrs = append(attrs, slog.String("query", query))
		}
		if start := r.FormValue("start"); start != "" {
			attrs = append(attrs, slog.String("start", start))
		}
		if end := r.FormValue("end"); end != "" {
			attrs = append(attrs, slog.String("end", end))
		}
		if step := r.FormValue("step"); step != "" {
			attrs = append(attrs, slog.String("step", step))
		}
		if stats.resultType != "" {
			attrs = append(attrs, slog.String("result_type", stats.resultType))
			attrs = append(attrs, slog.Int("results", stats.resultCount))
		}
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
	})
}

func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("GET /loki/api/v1/query_range", h.handleQueryRange)
	h.mux.HandleFunc("GET /loki/api/v1/query", h.handleQuery)
	h.mux.HandleFunc("GET /loki/api/v1/labels", h.handleLabels)
	h.mux.HandleFunc("GET /loki/api/v1/label/{name}/values", h.handleLabelValues)
	h.mux.HandleFunc("GET /loki/api/v1/series", h.handleSeries)
	h.mux.HandleFunc("GET /loki/api/v1/index/stats", h.handleIndexStats)
	h.mux.HandleFunc("GET /loki/api/v1/format_query", h.handleFormatQuery)
	h.mux.HandleFunc("GET /ready", h.handleReady)

	// Stubs — not yet implemented.
	h.mux.HandleFunc("GET /loki/api/v1/tail", h.handleNotImplemented)
	h.mux.HandleFunc("GET /loki/api/v1/patterns", h.handleNotImplemented)
	h.mux.HandleFunc("GET /loki/api/v1/index/volume", h.handleNotImplemented)
	h.mux.HandleFunc("GET /loki/api/v1/index/volume_range", h.handleNotImplemented)
	h.mux.HandleFunc("GET /loki/api/v1/detected_fields", h.handleNotImplemented)
	h.mux.HandleFunc("GET /loki/api/v1/rules", h.handleNotImplemented)
	h.mux.HandleFunc("POST /loki/api/v1/rules", h.handleNotImplemented)
	h.mux.HandleFunc("DELETE /loki/api/v1/rules/{namespace}/{groupName}", h.handleNotImplemented)

	// Stubs — read-only / ingest / flush / delete.
	h.mux.HandleFunc("POST /loki/api/v1/push", h.handleReadOnly)
	h.mux.HandleFunc("POST /otlp/v1/logs", h.handleReadOnly)
	h.mux.HandleFunc("POST /loki/api/v1/delete", h.handleReadOnly)
	h.mux.HandleFunc("GET /flush", h.handleReadOnly)
	h.mux.HandleFunc("POST /flush", h.handleReadOnly)
	h.mux.HandleFunc("GET /shutdown", h.handleReadOnly)
	h.mux.HandleFunc("POST /shutdown", h.handleReadOnly)
	h.mux.HandleFunc("GET /ring", h.handleReadOnly)
	h.mux.HandleFunc("POST /ingester/flush", h.handleReadOnly)
	h.mux.HandleFunc("POST /ingester/shutdown", h.handleReadOnly)
}

// handleQueryRange handles GET /loki/api/v1/query_range.
//
// Executes a log or metric query over a time range. The LogQL expression is
// parsed to determine the query type: SampleExpr routes to a metric query
// returning a matrix, all other expressions route to a log query returning
// streams. Query parameters: query (required), start, end, since, limit,
// direction, step.
func (h *Handler) handleQueryRange(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter required")
		return
	}

	start, end, err := parseBounds(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	limit := parseLimit(r.FormValue("limit"))
	direction := parseDirection(r.FormValue("direction"))
	step := parseStep(r.FormValue("step"), start, end)

	expr, err := syntax.ParseExpr(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch expr.(type) {
	case syntax.SampleExpr:
		result, err := h.engine.MetricQueryRange(query, start, end, step, direction)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		setQueryStats(r, "matrix", len(result))
		writeLokiResponse(w, http.StatusOK, "matrix", result)
	default:
		result, err := h.engine.LogQueryRange(query, start, end, limit, direction)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		totalEntries := 0
		for _, s := range result {
			totalEntries += len(s.Entries)
		}
		setQueryStats(r, "streams", totalEntries)
		writeLokiResponse(w, http.StatusOK, "streams", result)
	}
}

// handleQuery handles GET /loki/api/v1/query.
//
// Executes an instant query at a single point in time. SampleExpr routes to
// a metric query returning a vector; log selectors return streams at the
// given timestamp. Query parameters: query (required), time, direction, limit.
func (h *Handler) handleQuery(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("query")
	if query == "" {
		writeError(w, http.StatusBadRequest, "query parameter required")
		return
	}

	ts, err := parseTimestamp(r.FormValue("time"), time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	direction := parseDirection(r.FormValue("direction"))

	expr, err := syntax.ParseExpr(query)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch expr.(type) {
	case syntax.SampleExpr:
		result, err := h.engine.MetricQuery(query, ts, direction)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		setQueryStats(r, "vector", len(result))
		writeLokiResponse(w, http.StatusOK, "vector", result)
	default:
		limit := parseLimit(r.FormValue("limit"))
		result, err := h.engine.LogQueryRange(query, ts, ts, limit, direction)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		totalEntries := 0
		for _, s := range result {
			totalEntries += len(s.Entries)
		}
		setQueryStats(r, "streams", totalEntries)
		writeLokiResponse(w, http.StatusOK, "streams", result)
	}
}

// handleLabels handles GET /loki/api/v1/labels.
//
// Returns all label names configured in the schema that are present in the
// journal files. No query parameters required.
func (h *Handler) handleLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := h.engine.Labels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setQueryStats(r, "labels", len(labels))
	writeJSON(w, http.StatusOK, loghttp.LabelResponse{
		Status: "success",
		Data:   labels,
	})
}

// handleLabelValues handles GET /loki/api/v1/label/{name}/values.
//
// Returns all distinct values for the named label across the journal files.
// Returns 400 if the label is not in the schema's label set.
func (h *Handler) handleLabelValues(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vals, err := h.engine.LabelValues(name)
	if err != nil {
		if errors.Is(err, engine.ErrLabelExcluded) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("label %q not found", name))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setQueryStats(r, "values", len(vals))
	writeJSON(w, http.StatusOK, loghttp.LabelResponse{
		Status: "success",
		Data:   vals,
	})
}

// handleSeries handles GET /loki/api/v1/series.
//
// Returns the distinct stream label sets matching the given match[] selectors
// within the time range. Query parameters: match[] (required, one or more
// LogQL stream selectors), start, end.
func (h *Handler) handleSeries(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	matchers, err := parseMatchers(r.Form["match[]"])
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(matchers) == 0 {
		writeJSON(w, http.StatusOK, struct {
			Status string              `json:"status"`
			Data   []map[string]string `json:"data"`
		}{Status: "success", Data: nil})
		return
	}

	start, end, err := parseBounds(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := h.engine.Series(matchers, start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setQueryStats(r, "series", len(result))

	data := make([]map[string]string, len(result))
	for i, ls := range result {
		data[i] = map[string]string(ls)
	}
	writeJSON(w, http.StatusOK, struct {
		Status string              `json:"status"`
		Data   []map[string]string `json:"data"`
	}{Status: "success", Data: data})
}

// handleIndexStats handles GET /loki/api/v1/index/stats.
//
// Returns approximate counts of streams, chunks, entries, and bytes for
// entries in the given time range. Query parameters: match, start, end.
func (h *Handler) handleIndexStats(w http.ResponseWriter, r *http.Request) {
	match := r.FormValue("match")
	if match == "" {
		match = "{}"
	}

	start, end, err := parseBounds(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	stats, err := h.engine.IndexStats(match, start, end)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setQueryStats(r, "stats", int(stats.Entries))
	writeJSON(w, http.StatusOK, stats)
}

// handleFormatQuery handles GET /loki/api/v1/format_query.
//
// Parses and pretty-prints a LogQL expression. Returns the formatted query
// on success, or an "invalid-query" error with the parse error message.
// Query parameters: query.
func (h *Handler) handleFormatQuery(w http.ResponseWriter, r *http.Request) {
	query := r.FormValue("query")
	if query == "" {
		writeJSON(w, http.StatusOK, formatQueryResponse{
			Status: "success",
			Data:   "",
		})
		return
	}

	expr, err := syntax.ParseExpr(query)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, formatQueryResponse{
			Status: "invalid-query",
			Err:    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, formatQueryResponse{
		Status: "success",
		Data:   syntax.Prettify(expr),
	})
}

// handleReady handles GET /ready.
//
// Readiness probe. Always returns 200 with a plain text "ready" body.
func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ready")
}

// handleNotImplemented handles endpoints that may be added in the future.
//
// Returns 501 Not Implemented.
func (h *Handler) handleNotImplemented(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "endpoint not yet implemented in Loki Lite")
}

// handleReadOnly handles endpoints that are not applicable to a query-only
// frontend backed by journald.
//
// Returns 501 Not Implemented with a message explaining Loki Lite is a
// read-only frontend for journald.
func (h *Handler) handleReadOnly(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "Loki Lite is a read-only query frontend for journald and does not support ingestion, flushing, or deletion")
}

func setQueryStats(r *http.Request, resultType string, resultCount int) {
	if stats, ok := r.Context().Value(statsCtxKey).(*queryStats); ok {
		stats.resultType = resultType
		stats.resultCount = resultCount
	}
}

// formatQueryResponse is the JSON response for /loki/api/v1/format_query.
type formatQueryResponse struct {
	Status string `json:"status"`
	Data   string `json:"data,omitempty"`
	Err    string `json:"error,omitempty"`
}

// queryResponseData wraps a result with its type for the Loki response envelope.
type queryResponseData struct {
	ResultType string      `json:"resultType"`
	Result     interface{} `json:"result"`
}

func writeLokiResponse(w http.ResponseWriter, status int, resultType string, result interface{}) {
	writeJSON(w, status, struct {
		Status string            `json:"status"`
		Data   queryResponseData `json:"data"`
	}{
		Status: "success",
		Data: queryResponseData{
			ResultType: resultType,
			Result:     result,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	jsoniter.ConfigFastest.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	}{
		Status:  "error",
		Message: msg,
	})
}

// parseBounds extracts start and end timestamps from the request.
// Accepts Unix epoch seconds (integer or float) or RFC3339 strings.
// Defaults: start = now - 1h, end = now.
func parseBounds(r *http.Request) (time.Time, time.Time, error) {
	now := time.Now()
	end, err := parseTimestamp(r.FormValue("end"), now)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid end: %w", err)
	}

	startStr := r.FormValue("start")
	sinceStr := r.FormValue("since")
	var start time.Time
	if startStr != "" {
		start, err = parseTimestamp(startStr, now.Add(-defaultSince))
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start: %w", err)
		}
	} else if sinceStr != "" {
		since, err := parseDuration(sinceStr)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid since: %w", err)
		}
		start = end.Add(-since)
	} else {
		start = end.Add(-defaultSince)
	}

	return start, end, nil
}

// parseTimestamp parses a Unix epoch timestamp (seconds or nanoseconds,
// possibly fractional) or an RFC3339 string. If val is empty, def is returned.
//
// Grafana and the Loki API use nanosecond timestamps (> 1e12). Values ≤ 1e12
// are treated as seconds.
func parseTimestamp(val string, def time.Time) (time.Time, error) {
	if val == "" {
		return def, nil
	}

	// Try Unix epoch (integer or float).
	if v, err := strconv.ParseFloat(val, 64); err == nil {
		// Grafana sends nanosecond timestamps (> ~year 33658 in seconds).
		if v > 1e12 {
			v /= 1e9
		}
		sec := int64(v)
		nsec := int64((v - float64(sec)) * 1e9)
		return time.Unix(sec, nsec), nil
	}

	// Try RFC3339.
	if t, err := time.Parse(time.RFC3339Nano, val); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid timestamp %q", val)
}

// parseDuration parses a duration string, trying Go duration first
// then plain seconds (integer or float).
func parseDuration(val string) (time.Duration, error) {
	if d, err := time.ParseDuration(val); err == nil {
		return d, nil
	}
	if secs, err := strconv.ParseFloat(val, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}
	return 0, fmt.Errorf("invalid duration %q", val)
}

// parseLimit parses the limit parameter. Returns defaultQueryLimit on
// empty string or parse failure.
func parseLimit(val string) int {
	if val == "" {
		return defaultQueryLimit
	}
	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		return defaultQueryLimit
	}
	return n
}

// parseDirection parses the direction parameter. Accepts "FORWARD" or
// "BACKWARD" (case-insensitive). Returns defaultDirection for empty string.
func parseDirection(val string) logproto.Direction {
	if val == "" {
		return defaultDirection
	}
	switch strings.ToUpper(val) {
	case "FORWARD":
		return logproto.FORWARD
	case "BACKWARD":
		return logproto.BACKWARD
	default:
		return defaultDirection
	}
}

// parseStep parses the step parameter. Accepts seconds (integer or float)
// or Go duration strings. Default is max(floor((end-start)/250), 1) seconds.
func parseStep(val string, start, end time.Time) time.Duration {
	if val != "" {
		if d, err := parseDuration(val); err == nil {
			if d > 0 {
				return d
			}
		}
	}
	secs := math.Max(math.Floor(end.Sub(start).Seconds()/250), 1)
	return time.Duration(secs) * time.Second
}

// parseMatchers parses LogQL stream selector strings from match[] parameters
// into label matchers.
func parseMatchers(values []string) ([]*labels.Matcher, error) {
	if len(values) == 0 {
		return nil, nil
	}

	var all []*labels.Matcher
	for _, v := range values {
		expr, err := syntax.ParseLogSelector(v, true)
		if err != nil {
			return nil, fmt.Errorf("invalid matcher %q: %w", v, err)
		}
		all = append(all, expr.Matchers()...)
	}
	return all, nil
}

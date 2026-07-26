package query

import (
	"github.com/grafana/loki/v3/pkg/logql/log"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

// LogPipeline processes journal entries through a parsed LogQL log selector.
type LogPipeline struct {
	expr     syntax.LogSelectorExpr
	pipeline log.Pipeline
	schema   Schema
}

// LogQL parses a LogQL log selector query and returns a LogPipeline.
func (b *Builder) LogQL(query string) (*LogPipeline, error) {
	expr, err := syntax.ParseLogSelector(query, true)
	if err != nil {
		return nil, err
	}

	pipeline, err := expr.Pipeline()
	if err != nil {
		return nil, err
	}

	return &LogPipeline{
		expr:     expr,
		pipeline: pipeline,
		schema:   b.schema,
	}, err
}

// Process feeds a single journal entry through the pipeline and returns the
// processed entry and whether it matched the selector and pipeline filters.
func (lp *LogPipeline) Process(entry Entry) (Entry, bool) {
	matchers := lp.expr.Matchers()
	if !matchersMatch(entry.StreamLabels, matchers) {
		return Entry{}, false
	}

	streamLabels := entry.StreamLabels
	sp := lp.pipeline.ForStream(streamLabels)

	resultLine, labelsResult, ok := sp.Process(
		entry.Timestamp.UnixNano(),
		[]byte(entry.Line),
		entry.StructuredMetadata,
	)

	if !ok {
		return Entry{}, false
	}

	return Entry{
		Timestamp:          entry.Timestamp,
		Line:               string(resultLine),
		StreamLabels:       labelsResult.Stream(),
		StructuredMetadata: labelsResult.StructuredMetadata(),
	}, true
}

// Matchers returns the stream selectors from the parsed query.
func (lp *LogPipeline) Matchers() []*labels.Matcher {
	return lp.expr.Matchers()
}

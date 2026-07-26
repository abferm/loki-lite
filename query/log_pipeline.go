package query

import (
	"github.com/abferm/loki-lite/model"
	"github.com/grafana/loki/v3/pkg/logql/log"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

// LogPipeline processes log entries through a parsed LogQL log selector.
type LogPipeline struct {
	expr     syntax.LogSelectorExpr
	pipeline log.Pipeline
}

// LogQL parses a LogQL log selector query and returns a LogPipeline.
func LogQL(query string) (*LogPipeline, error) {
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
	}, err
}

// Process feeds a single log entry through the pipeline and returns the
// processed entry and whether it matched the selector and pipeline filters.
func (lp *LogPipeline) Process(entry model.Entry) (model.Entry, bool) {
	matchers := lp.expr.Matchers()
	if !matchersMatch(entry.StreamLabels, matchers) {
		return model.Entry{}, false
	}

	sp := lp.pipeline.ForStream(entry.StreamLabels)

	resultLine, labelsResult, ok := sp.Process(
		entry.Timestamp.UnixNano(),
		[]byte(entry.Line),
		entry.StructuredMetadata,
	)

	if !ok {
		return model.Entry{}, false
	}

	return model.Entry{
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

package query

import (
	"github.com/abferm/loki-lite/model"
	"github.com/grafana/loki/v3/pkg/logql/log"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
)

// MetricPipeline processes log entries through a parsed LogQL metric query,
// extracting numeric samples from matching entries.
type MetricPipeline struct {
	expr       syntax.SampleExpr
	selector   syntax.LogSelectorExpr
	extractors []log.SampleExtractor
}

// MetricQL parses a LogQL metric query and returns a MetricPipeline.
func MetricQL(query string) (*MetricPipeline, error) {
	expr, err := syntax.ParseSampleExpr(query)
	if err != nil {
		return nil, err
	}

	selector, err := expr.Selector()
	if err != nil {
		return nil, err
	}

	extractors, err := expr.Extractors()
	if err != nil {
		return nil, err
	}

	return &MetricPipeline{
		expr:       expr,
		selector:   selector,
		extractors: extractors,
	}, nil
}

// Process feeds a single log entry through the metric pipeline and returns
// the extracted sample values and whether the entry matched the selector.
// Each extractor produces its own set of samples; results are concatenated.
func (mp *MetricPipeline) Process(entry model.Entry) ([]float64, bool) {
	matchers := mp.selector.Matchers()
	if !matchersMatch(entry.StreamLabels, matchers) {
		return nil, false
	}

	var allValues []float64
	for _, extractor := range mp.extractors {
		sp := extractor.ForStream(entry.StreamLabels)

		samples, ok := sp.Process(
			entry.Timestamp.UnixNano(),
			[]byte(entry.Line),
			entry.StructuredMetadata,
		)
		if !ok {
			continue
		}

		for _, s := range samples {
			allValues = append(allValues, s.Value)
		}
	}

	if len(allValues) == 0 {
		return nil, false
	}
	return allValues, true
}

// Matchers returns the stream selectors from the parsed metric query.
func (mp *MetricPipeline) Matchers() []*labels.Matcher {
	return mp.selector.Matchers()
}

// matchersMatch checks if the given labels match all matchers.
func matchersMatch(lbs labels.Labels, matchers []*labels.Matcher) bool {
	for _, m := range matchers {
		val := lbs.Get(m.Name)
		if !m.Matches(val) {
			return false
		}
	}
	return true
}

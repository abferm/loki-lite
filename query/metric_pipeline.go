package query

import (
	"fmt"
	"math"
	"time"

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

// HasRealSelector reports whether the metric query contains at least one
// non-empty stream selector. Expressions like vector(1)+vector(1) have no
// real selectors and should be evaluated as literals rather than matched
// against journal entries.
func (mp *MetricPipeline) HasRealSelector() bool {
	for _, m := range mp.selector.Matchers() {
		if m.Name != "" {
			return true
		}
	}
	return false
}

// EvaluateLiteral evaluates a metric expression that has no real stream
// selector (e.g. vector(1)+vector(1)) and returns the resulting scalar value.
func (mp *MetricPipeline) EvaluateLiteral() (float64, error) {
	return evalSampleExpr(mp.expr)
}

func evalSampleExpr(expr syntax.SampleExpr) (float64, error) {
	switch e := expr.(type) {
	case *syntax.VectorExpr:
		return e.Val, e.Err()
	case *syntax.LiteralExpr:
		return e.Val, nil
	case *syntax.BinOpExpr:
		left, err := evalSampleExpr(e.SampleExpr)
		if err != nil {
			return 0, err
		}
		right, err := evalSampleExpr(e.RHS)
		if err != nil {
			return 0, err
		}
		switch e.Op {
		case "+":
			return left + right, nil
		case "-":
			return left - right, nil
		case "*":
			return left * right, nil
		case "/":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		case "%":
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return float64(int64(left) % int64(right)), nil
		case "^":
			return math.Pow(left, right), nil
		default:
			return 0, fmt.Errorf("unsupported binary operator %q", e.Op)
		}
	default:
		return 0, fmt.Errorf("unsupported literal expression type %T", expr)
	}
}

// Range returns the range duration from the metric query's range selector
// (e.g., the `5m` in `count_over_time({job="sshd"}[5m])`).
// Returns 0 if the expression does not contain a range selector.
func (mp *MetricPipeline) Range() time.Duration {
	if ra, ok := mp.expr.(*syntax.RangeAggregationExpr); ok {
		if ra.Left != nil {
			return ra.Left.Interval
		}
	}
	return 0
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

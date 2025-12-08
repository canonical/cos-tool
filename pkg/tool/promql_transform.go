package tool

import (
	"fmt"
	"io"

	"log/slog"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
)

func (p *PromQL) ValidateRules(filename string, data []byte) (*rulefmt.RuleGroups, error) {
	// Expose the backend parser for alert rule validation
	// setting ignoreUnknownFields to false to keep the old behavior
	rg, errs := rulefmt.Parse(data, false, model.UTF8Validation)

	if len(errs) > 0 {
		return rg, fmt.Errorf("error validating %s: %+v", filename, errs)
	}
	return rg, nil
}

// This function only checks syntax. If more in depth checking is needed, it must be expanded.
func (p *PromQL) ValidateConfig(filename string) error {
	// Assuming here that agent mode is false. If we support agent mode in the future, this needs to be revisited.
	// Define the slog logger that discards output. "log.NewNopLogger()" equivalent.
	_, err := config.LoadFile(filename, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		return err
	}
	return nil
}

func (p *PromQL) Transform(arg string, matchers *map[string]string) (string, error) {
	exp, err := parser.ParseExpr(arg)

	if err != nil {
		return arg, err
	}

	p.expr = exp
	p.matchers = matchers

	if e, ok := p.expr.(*parser.VectorSelector); ok {
		p.injectLabelMatcher(e)
	}

	p.traverseNode(p.expr)
	return p.expr.String(), nil
}

func (p *PromQL) traverseNode(exp parser.Node) {
	for _, c := range parser.Children(exp) {

		if e, ok := c.(*parser.VectorSelector); ok {
			p.injectLabelMatcher(e)
		}
		p.traverseNode(c)
	}
}

func (p *PromQL) injectLabelMatcher(e *parser.VectorSelector) {
	for key, val := range *(p.matchers) {
		var found = false
		for _, existing := range e.LabelMatchers {
			if existing.Name == key {
				found = true
				break
			}
		}
		if found {
			continue
		}
		e.LabelMatchers = append(
			e.LabelMatchers,
			&labels.Matcher{
				Type:  labels.MatchEqual,
				Name:  key,
				Value: val,
			},
		)
	}
}

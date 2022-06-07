package tool

import (
	"fmt"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/rulefmt"
	"github.com/prometheus/prometheus/promql/parser"
)

func (p *PromQL) Validate(data []byte) (*rulefmt.RuleGroups, error) {
	// Expose the backend parser for alert rule validation
	rg, errs := rulefmt.Parse(data)

	if len(errs) > 0 {
		return rg, fmt.Errorf("error validating: %+v", errs[0])
	}
	return rg, nil
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

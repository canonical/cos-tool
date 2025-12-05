package tool

import (
	"fmt"
	parser "github.com/canonical/cos-tool/pkg/logql/syntax"
	"github.com/canonical/cos-tool/pkg/lokiruler"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/rulefmt"
	"sort"
)

func (p *LogQL) ValidateRules(filename string, data []byte) (*rulefmt.RuleGroups, error) {
	// Expose the backend parser
	rg, errs := lokiruler.Load(data)

	if len(errs) > 0 {

		return rg, fmt.Errorf("error validating %s: %+v", filename, errs)
	}

	return rg, nil
}

func (p *LogQL) ValidateConfig(filename string) error {
	return fmt.Errorf("Loki not supported for validate-config")
}

func (p *LogQL) Transform(arg string, matchers *map[string]string) (string, error) {
	exp, err := parser.ParseExpr(arg)

	if err != nil {
		return arg, err
	}

	p.expr = exp
	p.matchers = matchers

	sm := []string{}
	for m := range *matchers {
		sm = append(sm, m)
	}

	sort.Strings(sm)
	p.sortedMatchers = &sm

	p.expr.Walk(p.traverse)
	return p.expr.String(), nil
}

func (p *LogQL) traverse(e interface{}) {
	// Even though we cast back, the signature has to be interface{}
	// or it cannot be satisfied
	switch e := e.(type) {
	case *parser.MatchersExpr:
		p.injectLabelMatcher(e)
	default:
		// Do nothing
	}
}

func (p *LogQL) injectLabelMatcher(e *parser.MatchersExpr) {
	appendMatchers := make([]*labels.Matcher, 0, len(*p.matchers))
	for _, key := range *(p.sortedMatchers) {
		existingMatchers := e.Matchers()
		var found = false
		for _, existing := range existingMatchers {
			if existing.Name == key {
				found = true
				break
			}
		}
		if found {
			continue
		}
		appendMatchers = append(appendMatchers, &labels.Matcher{
			Type:  labels.MatchEqual,
			Name:  key,
			Value: (*p.matchers)[key],
		})
	}
	e.AppendMatchers(appendMatchers)
}

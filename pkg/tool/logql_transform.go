package tool

import (
	"fmt"
	parser "github.com/canonical/cos-tool/pkg/logql/syntax"
	"github.com/canonical/cos-tool/pkg/lokiruler"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/model/rulefmt"
	"regexp"
	"slices"
	"sort"
	"strings"
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
	// Replace Grafana template variables with valid placeholders
	processed, occurrences := replaceGrafanaVariables(arg)
	exp, err := parser.ParseExpr(processed)

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
	result := p.expr.String()

	// Restore original Grafana variables
	result = restoreGrafanaVariables(result, occurrences)

	return result, nil
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

// replaceGrafanaVariables replaces Grafana template variables with valid numeric placeholders
// and returns a map for later restoration.
func replaceGrafanaVariables(query string) (string, map[int]string) {
	replacements := make(map[int]string)
	counter := 99990000 // Use a distinctive number to identify our placeholders

	// Match Grafana variables: ${var}, ${var:option}, $var (including $__var)
	varPattern := regexp.MustCompile(`\$\{[^}]+\}|\$\w+`)

	result := varPattern.ReplaceAllStringFunc(query, func(match string) string {
		placeholder := counter
		replacements[placeholder] = match
		counter++
		return fmt.Sprintf("%d", placeholder)
	})

	return result, replacements
}

// restoreGrafanaVariables restores the original Grafana variables from placeholders.
// It processes placeholders in descending order to avoid partial replacements.
func restoreGrafanaVariables(query string, replacements map[int]string) string {
	// Get placeholder keys and sort in descending order
	placeholders := make([]int, 0, len(replacements))
	for placeholder := range replacements {
		placeholders = append(placeholders, placeholder)
	}
	slices.Sort(placeholders)
	slices.Reverse(placeholders)

	result := query
	for _, placeholder := range placeholders {
		original := replacements[placeholder]
		result = strings.ReplaceAll(result, fmt.Sprintf("%d", placeholder), original)
	}

	return result
}

// ReplaceGrafanaVariables is exposed for testing purposes
func ReplaceGrafanaVariables(query string) (string, map[int]string) {
	return replaceGrafanaVariables(query)
}

// RestoreGrafanaVariables is exposed for testing purposes
func RestoreGrafanaVariables(query string, replacements map[int]string) string {
	return restoreGrafanaVariables(query, replacements)
}

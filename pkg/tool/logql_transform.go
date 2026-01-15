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
	"time"
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

// Precompiled regex patterns for LogQL variable detection and replacement
var (
	// Pattern matching Grafana template variables: $var, ${var}, ${var:option}
	logQLVarPattern = `\$(?:\{[^}]+\}|\w+)`

	// Matches variables in range duration brackets: [$var], [$__interval]
	logQLRangeDurationPattern = regexp.MustCompile(`\[(` + logQLVarPattern + `)\]`)

	// Label value pattern: captures variables in label matcher values
	// Matches variables in label matcher values (quoted and unquoted):
	// label="$var", label=~$var
	logQLLabelValuePattern = regexp.MustCompile(`(\w+)\s*(=~?|!=?~?)\s*(?:"(` + logQLVarPattern + `)"|` + `(` + logQLVarPattern + `)(?:\s|,|}|\]))`)

	// Matches any remaining Grafana variable not caught by specific patterns
	logQLGeneralVariablePattern = regexp.MustCompile(logQLVarPattern)
)

// replaceGrafanaVariables replaces Grafana variables with valid placeholders for LogQL parsing
// Returns modified query and map of placeholder→original variable for restoration
func replaceGrafanaVariables(query string) (string, map[string]string) {
	replacements := make(map[string]string)
	variableToPlaceholder := make(map[string]string) // Ensures same variable gets same placeholder
	quotedPlaceholders := make(map[string]bool)      // Tracks original quote state for restoration
	// counter generates unique numeric placeholders starting at 99990000
	// Placeholders are needed because Grafana variables ($var, ${var}) are not valid LogQL syntax
	// and would cause parsing errors. We replace them with valid placeholders, parse/transform the query,
	// then restore the original variables. This large counter value avoids collisions with real label values.
	// Examples: $var → 99990000, ${job} → 99990001, $__interval → 99990002s
	counter := 99990000

	// Returns existing placeholder or creates new one for variable (without quotes)
	getPlaceholder := func(variable string, format string) string {
		if placeholder, exists := variableToPlaceholder[variable]; exists {
			return placeholder
		}

		placeholder := fmt.Sprintf(format, counter)
		variableToPlaceholder[variable] = placeholder
		replacements[placeholder] = variable
		counter++
		return placeholder
	}

	// Returns existing placeholder or creates new one for variable (with quotes metadata)
	getPlaceholderQuoted := func(variable string, format string) string {
		if placeholder, exists := variableToPlaceholder[variable]; exists {
			return placeholder
		}

		placeholder := fmt.Sprintf(format, counter)
		variableToPlaceholder[variable] = placeholder
		replacements[placeholder] = variable
		quotedPlaceholders[placeholder] = true
		counter++
		return placeholder
	}

	result := query

	// Replace in order of specificity: durations, label values, then general variables
	result = replaceLogQLVariablesInDurations(result, getPlaceholder)

	result = replaceLogQLVariablesInLabelValues(result, getPlaceholder, getPlaceholderQuoted)

	result = replaceLogQLVariablesInOtherContexts(result, getPlaceholder)

	// Store quote metadata using special key prefix for restoration phase
	for placeholder, needsQuotes := range quotedPlaceholders {
		if needsQuotes {
			replacements["__quoted__"+placeholder] = "true"
		}
	}

	return result, replacements
}

// replaceLogQLDurationVariables replaces variables in range brackets with duration placeholders
// Adds "s" suffix for LogQL parser compatibility: [$__interval] → [99990000s]
func replaceLogQLVariablesInDurations(query string, getPlaceholder func(string, string) string) string {
	return logQLRangeDurationPattern.ReplaceAllStringFunc(query, func(match string) string {
		variable := match[1 : len(match)-1]            // Extract variable without brackets
		placeholder := getPlaceholder(variable, "%ds") // Add "s" suffix for LogQL
		return "[" + placeholder + "]"
	})
}

// replaceLogQLVariablesInLabelValues replaces variables in label values with quoted placeholders
// Handles both quoted and unquoted forms: {job="$job"} → {job="99990002"}
func replaceLogQLVariablesInLabelValues(query string, getPlaceholder func(string, string) string, getPlaceholderQuoted func(string, string) string) string {
	return logQLLabelValuePattern.ReplaceAllStringFunc(query, func(match string) string {
		parts := logQLLabelValuePattern.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		labelName := parts[1]
		operator := parts[2]
		wasQuoted := parts[3] != ""
		variable := parts[3]
		if variable == "" && len(parts) > 4 {
			variable = parts[4]
		}

		placeholder := ""
		if wasQuoted {
			placeholder = getPlaceholderQuoted(variable, "%d")
		} else {
			placeholder = getPlaceholder(variable, "%d")
		}
		suffix := ""
		if len(match) > 0 && (match[len(match)-1] == ' ' || match[len(match)-1] == ',' || match[len(match)-1] == '}' || match[len(match)-1] == ')') {
			suffix = string(match[len(match)-1])
		}

		return fmt.Sprintf(`%s%s"%s"%s`, labelName, operator, placeholder, suffix)
	})
}

// replaceLogQLVariablesInOtherContexts replaces remaining variables in filters and function arguments
// Example: |= "$pattern" → |= "99990005"
func replaceLogQLVariablesInOtherContexts(query string, getPlaceholder func(string, string) string) string {
	return logQLGeneralVariablePattern.ReplaceAllStringFunc(query, func(variable string) string {
		return getPlaceholder(variable, "%d")
	})
}

// restoreGrafanaVariables restores original Grafana variables from placeholders
// Handles LogQL's duration normalization (99990000s → 1157407h46m40s)
func restoreGrafanaVariables(query string, replacements map[string]string) string {
	durationMap := buildLogQLDurationMap(replacements)
	placeholders := sortLogQLPlaceholdersByLength(replacements)

	result := query
	result = restoreLogQLDurationVariables(result, durationMap)
	result = restoreLogQLOtherPlaceholders(result, placeholders, replacements)

	return result
}

// buildLogQLDurationMap maps LogQL-normalized durations back to original variables
// LogQL normalizes durations: 99990000s → 1157407h46m40s
func buildLogQLDurationMap(replacements map[string]string) map[string]string {
	durationToVariable := make(map[string]string)

	for placeholder, original := range replacements {
		if strings.HasSuffix(placeholder, "s") {
			numStr := placeholder[:len(placeholder)-1]
			var seconds int
			if _, err := fmt.Sscanf(numStr, "%d", &seconds); err == nil {
				duration := time.Duration(seconds) * time.Second
				normalized := formatLogQLDuration(duration)
				durationToVariable[normalized] = original
			}
		}
	}

	return durationToVariable
}

// formatLogQLDuration formats duration in LogQL's normalized format (e.g., 1157407h46m40s)
func formatLogQLDuration(d time.Duration) string {
	const day = 24 * time.Hour

	days := int(d / day)
	d -= time.Duration(days) * day

	hours := int(d.Hours())
	d -= time.Duration(hours) * time.Hour

	minutes := int(d.Minutes())
	d -= time.Duration(minutes) * time.Minute

	seconds := int(d.Seconds())

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	if seconds > 0 || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", seconds))
	}

	return strings.Join(parts, "")
}

// sortLogQLPlaceholdersByLength sorts placeholders by length descending
// Prevents partial replacements (e.g., "999" within "99990000")
func sortLogQLPlaceholdersByLength(replacements map[string]string) []string {
	placeholders := make([]string, 0, len(replacements))
	for placeholder := range replacements {
		if !strings.HasPrefix(placeholder, "__quoted__") {
			placeholders = append(placeholders, placeholder)
		}
	}

	slices.SortFunc(placeholders, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		if a > b {
			return -1
		}
		return 1
	})

	return placeholders
}

// restoreLogQLDurationVariables replaces normalized durations with original variables
func restoreLogQLDurationVariables(query string, durationMap map[string]string) string {
	result := query
	for normalized, original := range durationMap {
		result = strings.ReplaceAll(result, normalized, original)
	}
	return result
}

// restoreLogQLOtherPlaceholders replaces remaining placeholders with original variables
// Restores original quote state (adds quotes if originally quoted, removes if not)
func restoreLogQLOtherPlaceholders(query string, placeholders []string, replacements map[string]string) string {
	result := query
	for _, placeholder := range placeholders {
		original := replacements[placeholder]
		_, wasQuoted := replacements["__quoted__"+placeholder]

		if wasQuoted {
			result = strings.ReplaceAll(result, `"`+placeholder+`"`, `"`+original+`"`)
		} else {
			quotedPlaceholder := `"` + placeholder + `"`
			if strings.Contains(result, quotedPlaceholder) {
				result = strings.ReplaceAll(result, quotedPlaceholder, original)
			} else {
				result = strings.ReplaceAll(result, placeholder, original)
			}
		}
	}
	return result
}

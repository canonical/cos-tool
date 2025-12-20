package tool

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

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
	// Check for unsupported structural variables
	if err := checkUnsupportedVariables(arg); err != nil {
		return arg, err
	}

	// Replace Grafana template variables with valid placeholders
	processed, occurrences := replaceGrafanaVariablesPromQL(arg)

	exp, err := parser.ParseExpr(processed)

	if err != nil {
		return arg, err
	}

	p.expr = exp
	p.matchers = matchers

	if e, ok := p.expr.(*parser.VectorSelector); ok {
		p.injectLabelMatcher(e)
	}

	p.traverseNode(p.expr)
	result := p.expr.String()

	// Restore original Grafana variables
	result = restoreGrafanaVariablesPromQL(result, occurrences)

	return result, nil
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

// Precompiled regex patterns for unsupported variable detection
// These are compiled once at package initialization for better performance
var (
	// Pattern matching Grafana template variables: $var or ${var}
	varPattern = `\$(?:\w+|\{[^}]+\})`

	// Function name pattern: variable followed by opening parenthesis
	// Matches: $func(...) or ${func}(...)
	// Must be preceded by start, comma, or opening paren to avoid matching metric$var
	functionNamePattern = regexp.MustCompile(`(?:^|[,\(])\s*` + varPattern + `\s*\(`)

	// Grouping label pattern: by($var) or without($var)
	// Matches variables inside by() or without() clauses
	groupingLabelPattern = regexp.MustCompile(`\b(?:by|without)\s*\([^)]*` + varPattern)

	// Full metric name pattern: detects when entire metric name is a variable
	// Matches: $var{...} or ${var}{...} where variable is the complete metric name
	fullMetricNamePattern = regexp.MustCompile(`(?:^|[,\(])\s*(` + varPattern + `)\s*\{`)

	// Replacement patterns for variable substitution
	// These are used during the replace phase to swap variables with placeholders

	// Metric name replacement pattern: captures prefix, variable, and suffix
	// Matches: prefix + $var + suffix + {
	// Examples: otelcol_receiver + ${suffix_total} + (empty) + {
	metricNameReplacePattern = regexp.MustCompile(`(\w+)(` + varPattern + `)(\w*)\{`)

	// Range duration replacement pattern: captures variables in duration brackets
	// Matches: [$var]
	// Examples: [$__rate_interval], [$bucket_size]
	rangeDurationReplacePattern = regexp.MustCompile(`\[(` + varPattern + `)\]`)

	// General variable replacement pattern: matches any Grafana variable
	// Matches: $var or ${var} in any position
	// Examples: {job="$job"}, topk($limit, ...)
	generalVariablePattern = regexp.MustCompile(varPattern)
)

// checkUnsupportedVariables detects variables in unsupported structural positions
func checkUnsupportedVariables(expr string) error {
	// Check for function name variables: $func(...)
	if functionNamePattern.MatchString(expr) {
		return fmt.Errorf("variables in function name positions are not supported: cannot safely replace for validation")
	}

	// Check for grouping label variables: by($label)
	if groupingLabelPattern.MatchString(expr) {
		return fmt.Errorf("variables in grouping (by/without) positions are not supported: cannot safely replace for validation")
	}
	return nil
}

// replaceGrafanaVariablesPromQL replaces Grafana variables with parseable placeholders
// Handles four types: full metric names, metric name components, durations, and label values
func replaceGrafanaVariablesPromQL(query string) (string, map[string]string) {
	replacements := make(map[string]string)
	variableToPlaceholder := make(map[string]string) // Track same variable → same placeholder
	counter := 99990000

	// Helper closure to get or create placeholder for a variable
	// Ensures same variable always gets same placeholder across all positions
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

	result := query
	result = replaceFullMetricNameVariables(result, getPlaceholder)
	result = replaceVariablesInMetricNameComponents(result, getPlaceholder)
	result = replaceVariablesInDurations(result, getPlaceholder)
	result = replaceVariablesInValues(result, getPlaceholder)

	return result, replacements
}

// replaceFullMetricNameVariables replaces entire metric names that are variables
// Examples: $metric{...}, ${metric_name}{...}
// This must run before replaceMetricNameVariables to avoid conflicts
func replaceFullMetricNameVariables(query string, getPlaceholder func(string, string) string) string {
	result := query

	for {
		matches := fullMetricNamePattern.FindStringSubmatchIndex(result)
		if matches == nil {
			break
		}

		// Capture groups: [0,1] = full match, [2,3] = variable
		if len(matches) < 4 {
			break
		}

		matchStart, matchEnd := matches[0], matches[1]
		varStart, varEnd := matches[2], matches[3]
		variable := result[varStart:varEnd]

		// Get placeholder (uses __v%d__ format for metric names)
		placeholder := getPlaceholder(variable, "__v%d__")

		// Replace: keep any prefix (like comma/paren), replace variable, keep {
		prefix := result[matchStart:varStart]
		replacement := prefix + placeholder + "{"

		result = result[:matchStart] + replacement + result[matchEnd:]
	}

	return result
}

// replaceVariablesInMetricNameComponents replaces variables in metric name components
// Examples: metric${suffix}{...}, otelcol${v1}_process${v2}{...}
func replaceVariablesInMetricNameComponents(query string, getPlaceholder func(string, string) string) string {
	result := query

	for {
		matches := metricNameReplacePattern.FindStringIndex(result)
		if matches == nil {
			break
		}

		// Extract and parse the match
		matchStart, matchEnd := matches[0], matches[1]
		parts := metricNameReplacePattern.FindStringSubmatch(result[matchStart:matchEnd])
		if len(parts) < 4 {
			break
		}

		prefix := parts[1]   // "metric"
		variable := parts[2] // "${suffix}" or "$suffix"
		suffix := parts[3]   // optional text after variable (e.g., "_total")

		// Verify this is actually a metric name by checking what comes after
		// Must be followed by { (captured in pattern) to be valid metric syntax
		if matchEnd >= len(result) || result[matchEnd-1] != '{' {
			break // Invalid metric syntax, stop processing
		}

		// Get placeholder (uses __vN__ format for metric names)
		placeholder := getPlaceholder(variable, "__v%d__")

		// Replace this occurrence
		replacement := prefix + placeholder + suffix + "{"
		result = result[:matchStart] + replacement + result[matchEnd:]
	}

	return result
}

// replaceVariablesInDurations replaces variables in range duration brackets
// Examples: [$__rate_interval], [$bucket_size]
func replaceVariablesInDurations(query string, getPlaceholder func(string, string) string) string {
	return rangeDurationReplacePattern.ReplaceAllStringFunc(query, func(match string) string {
		variable := match[1 : len(match)-1]           // Extract variable without brackets
		placeholder := getPlaceholder(variable, "%d") // Numeric placeholder
		return "[" + placeholder + "]"
	})
}

// replaceVariablesInValues replaces variables in label values and function arguments
// Examples: {job="$job"}, topk($limit, metric)
func replaceVariablesInValues(query string, getPlaceholder func(string, string) string) string {
	return generalVariablePattern.ReplaceAllStringFunc(query, func(variable string) string {
		return getPlaceholder(variable, "%d") // Numeric placeholder
	})
}

// restoreGrafanaVariablesPromQL restores original Grafana variables from placeholders
// Handles duration normalization (99990000→1157d7h→$var) and placeholder order
func restoreGrafanaVariablesPromQL(query string, replacements map[string]string) string {
	durationMap := buildDurationMap(replacements)
	placeholders := sortPlaceholdersByLength(replacements)

	result := query
	result = restoreDurationVariables(result, durationMap)
	result = restoreOtherPlaceholders(result, placeholders, replacements)

	return result
}

// buildDurationMap creates inverse mapping from normalized durations to original variables
// For numeric placeholders (99990000, 99990001), it calculates their normalized form (1157d7h)
func buildDurationMap(replacements map[string]string) map[string]string {
	durationToPlaceholder := make(map[string]string)

	for placeholder, original := range replacements {
		// Check if this is a numeric placeholder (duration)
		var counter int
		if _, err := fmt.Sscanf(placeholder, "%d", &counter); err == nil {
			// Calculate normalized duration (e.g., 99990000 → 1157d7h)
			duration := time.Duration(counter) * time.Second
			normalized := model.Duration(duration).String()
			durationToPlaceholder[normalized] = original
		}
	}

	return durationToPlaceholder
}

// sortPlaceholdersByLength extracts placeholder keys and sorts them by length (longest first)
// This prevents partial replacements (e.g., replacing "999" before "99990000")
func sortPlaceholdersByLength(replacements map[string]string) []string {
	placeholders := make([]string, 0, len(replacements))
	for placeholder := range replacements {
		placeholders = append(placeholders, placeholder)
	}

	slices.SortFunc(placeholders, func(a, b string) int {
		// Sort by length descending, then alphabetically descending
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

// restoreDurationVariables replaces normalized durations with original variables
// Example: 1157d7h → $__rate_interval
func restoreDurationVariables(query string, durationMap map[string]string) string {
	result := query
	for normalized, original := range durationMap {
		result = strings.ReplaceAll(result, normalized, original)
	}
	return result
}

// restoreOtherPlaceholders replaces remaining placeholders with original variables
// Uses sorted placeholders to avoid partial replacements
func restoreOtherPlaceholders(query string, placeholders []string, replacements map[string]string) string {
	result := query
	for _, placeholder := range placeholders {
		original := replacements[placeholder]
		result = strings.ReplaceAll(result, placeholder, original)
	}
	return result
}

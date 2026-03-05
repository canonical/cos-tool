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
	// Replace function name variables first (before other variable processing)
	processed, funcReplacements, err := replaceVariablesInFunctionNames(arg)
	if err != nil {
		return arg, err
	}

	// Replace Grafana template variables with valid placeholders
	processed, occurrences := replaceGrafanaVariablesPromQL(processed)

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

	// Restore function name variables
	result = restoreFunctionNameVariables(result, funcReplacements)

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

	// Function name replacement pattern: captures function-name variables and following paren
	// Matches: $func( or ${func:modifier}( where the variable is in a function-call position
	// Guard: must be preceded by start of string or non-quote/non-word character (avoids matching inside strings)
	functionNameReplacePattern = regexp.MustCompile(`((?:^|[^"\w])\s*)(` + varPattern + `)(\s*\()`)

	// Pool of valid PromQL range-vector functions used as placeholders for function-name variables
	// When a variable like ${metric:value} appears as a function name, it's replaced with one of these
	functionPlaceholderPool = []string{"rate", "irate", "increase", "delta", "changes", "resets", "deriv", "idelta"}

	// Pattern to detect real (non-variable) function calls from the placeholder pool
	// Used to avoid picking a placeholder that conflicts with an existing function in the expression
	realFuncCallPattern = regexp.MustCompile(`(?:^|[^\w$])(` + strings.Join(functionPlaceholderPool, "|") + `)\s*\(`)

	// Grouping content pattern: captures the full by(...) or without(...) clause
	// Used to replace variables inside grouping clauses with valid placeholders
	groupingContentPattern = regexp.MustCompile(`\b((?:by|without)\s*\()([^)]*)(\))`)

	// Double-quoted string literal pattern: used to mask string contents before grouping replacement
	// Prevents by/without patterns inside filter strings (e.g. |= "by ($var)") from being rewritten
	doubleQuotedStringPattern = regexp.MustCompile(`"[^"\\]*(?:\\.[^"\\]*)*"`)

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

// replaceVariablesInFunctionNames replaces Grafana variables in function-call positions
// with valid PromQL function names from a pool, enabling the expression to be parsed.
// Each distinct variable gets a unique placeholder function that doesn't conflict with
// existing functions in the expression. Returns the modified query, a map of placeholder
// function names to original variables, and any error.
func replaceVariablesInFunctionNames(query string) (string, map[string]string, error) {
	// Find which functions from the pool are already used in the expression
	usedFunctions := make(map[string]struct{})
	for _, m := range realFuncCallPattern.FindAllStringSubmatch(query, -1) {
		usedFunctions[m[1]] = struct{}{}
	}

	// Build list of available placeholder functions
	var available []string
	for _, fn := range functionPlaceholderPool {
		if _, used := usedFunctions[fn]; !used {
			available = append(available, fn)
		}
	}

	placeholderToVar := make(map[string]string) // "rate" → "${metric:value}"
	varToPlaceholder := make(map[string]string) // "${metric:value}" → "rate"
	availIdx := 0
	var replaceErr error

	result := functionNameReplacePattern.ReplaceAllStringFunc(query, func(match string) string {
		if replaceErr != nil {
			return match
		}

		parts := functionNameReplacePattern.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		prefix := parts[1]
		variable := parts[2]
		paren := parts[3]

		funcName, exists := varToPlaceholder[variable]
		if !exists {
			if availIdx >= len(available) {
				replaceErr = fmt.Errorf("cannot safely replace function name variable %s: all placeholder functions are already in use", variable)
				return match
			}
			funcName = available[availIdx]
			availIdx++
			varToPlaceholder[variable] = funcName
			placeholderToVar[funcName] = variable
		}

		return prefix + funcName + paren
	})

	if replaceErr != nil {
		return query, nil, replaceErr
	}
	return result, placeholderToVar, nil
}

// restoreFunctionNameVariables restores original Grafana variables in function-call positions
// by replacing placeholder function names back to the original variable strings.
// Sorts by function name length descending to avoid substring collisions (e.g., "irate" before "rate").
func restoreFunctionNameVariables(query string, placeholderToVar map[string]string) string {
	if len(placeholderToVar) == 0 {
		return query
	}
	// Sort by length descending to prevent "rate(" matching inside "irate("
	funcNames := make([]string, 0, len(placeholderToVar))
	for fn := range placeholderToVar {
		funcNames = append(funcNames, fn)
	}
	slices.SortFunc(funcNames, func(a, b string) int {
		return len(b) - len(a)
	})

	result := query
	for _, funcName := range funcNames {
		result = strings.ReplaceAll(result, funcName+"(", placeholderToVar[funcName]+"(")
	}
	return result
}

// replaceGrafanaVariablesPromQL replaces Grafana variables with parseable placeholders
// Processes variables in order: full metric names, metric name components, durations, and label values
func replaceGrafanaVariablesPromQL(query string) (string, map[string]string) {
	replacements := make(map[string]string)
	variableToPlaceholder := make(map[string]string) // Track same variable → same placeholder
	// counter generates unique numeric placeholders starting at 99990000
	// Placeholders are needed because Grafana variables ($var, ${var}) are not valid PromQL syntax
	// and would cause parsing errors. We replace them with valid placeholders, parse/transform the query,
	// then restore the original variables. This large counter value avoids collisions with real metric values.
	// Examples: $var → 99990000, ${job} → 99990001, $__rate_interval → 99990002
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
	result = replaceVariablesInGrouping(result, generalVariablePattern, getPlaceholder)
	result = replaceFullMetricNameVariables(result, getPlaceholder)
	result = replaceVariablesInMetricNameComponents(result, getPlaceholder)
	result = replaceVariablesInDurations(result, getPlaceholder)
	result = replaceVariablesInValues(result, getPlaceholder)

	return result, replacements
}

// replaceVariablesInGrouping replaces variables inside by() and without() clauses.
// Uses __g%d__ format to avoid collisions with numeric placeholders used elsewhere.
// varPat is the variable pattern for the specific query language (PromQL or LogQL).
// Examples: by($var) → by(__g99990000__), without(${label}) → without(__g99990001__)
func replaceVariablesInGrouping(query string, varPat *regexp.Regexp, getPlaceholder func(string, string) string) string {
	// Mask string literals so that by/without patterns inside quoted strings
	// (e.g. |= "queued by ($queue $priority)" in LogQL) are not rewritten.
	var literals []string
	masked := doubleQuotedStringPattern.ReplaceAllStringFunc(query, func(m string) string {
		idx := len(literals)
		literals = append(literals, m)
		return fmt.Sprintf(`"__LIT%d__"`, idx)
	})

	result := groupingContentPattern.ReplaceAllStringFunc(masked, func(match string) string {
		parts := groupingContentPattern.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		prefix := parts[1]  // "by(" or "without("
		content := parts[2] // label list inside parentheses
		suffix := parts[3]  // ")"

		if !varPat.MatchString(content) {
			return match
		}

		newContent := varPat.ReplaceAllStringFunc(content, func(v string) string {
			return getPlaceholder(v, "__g%d__")
		})
		newContent = normalizeGroupingContent(newContent)
		return prefix + newContent + suffix
	})

	// Restore original string literals
	for i, lit := range literals {
		result = strings.ReplaceAll(result, fmt.Sprintf(`"__LIT%d__"`, i), lit)
	}
	return result
}

// normalizeGroupingContent ensures proper comma separation between labels in grouping clauses.
// In Grafana, users may write "by (label $var)" without commas since $var is interpolated as a string.
// The PromQL/LogQL parser requires commas, so we normalize "label __g0__" to "label, __g0__".
func normalizeGroupingContent(content string) string {
	parts := strings.Split(content, ",")
	var tokens []string
	for _, part := range parts {
		tokens = append(tokens, strings.Fields(part)...)
	}
	return strings.Join(tokens, ", ")
}

// replaceFullMetricNameVariables replaces entire metric names that are variables
// Examples: $metric{...}, ${metric_name}{...}
// This must run before replaceVariablesInMetricNameComponents to avoid conflicts
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

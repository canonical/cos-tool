#!/usr/bin/env bash
# Integration tests for cos-tool against real Grafana dashboard JSON files.
#
# Builds the binary, extracts every PromQL/LogQL expression from each dashboard
# in tests/testdata/dashboards/, runs `cos-tool transform` on each one and
# asserts that:
#   - the command exits without error
#   - the injected label matcher appears in the output
#
# Usage:
#   ./tests/integration/run_integration_tests.sh [--label-matcher KEY=VALUE]
#
# Defaults:
#   --label-matcher juju_model=test-integration

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DASHBOARD_DIR="${REPO_ROOT}/tests/testdata/dashboards"
LABEL_MATCHER="juju_model=test-integration"

# Parse optional --label-matcher argument
while [[ $# -gt 0 ]]; do
    case "$1" in
        --label-matcher)
            LABEL_MATCHER="$2"
            shift 2
            ;;
        --label-matcher=*)
            LABEL_MATCHER="${1#*=}"
            shift
            ;;
        *)
            echo "Unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

# Derive the expected label in output form: key=value → key="value"
MATCHER_KEY="${LABEL_MATCHER%%=*}"
MATCHER_VALUE="${LABEL_MATCHER#*=}"
EXPECTED_LABEL="${MATCHER_KEY}=\"${MATCHER_VALUE}\""

# ── Build ────────────────────────────────────────────────────────────────────

BIN="$(mktemp -d)/cos-tool"
echo "Building cos-tool..."
(cd "${REPO_ROOT}" && go build -o "${BIN}" .) || { echo "Build failed"; exit 1; }
echo "Binary: ${BIN}"
echo ""

# ── Test runner ──────────────────────────────────────────────────────────────

PASS=0
FAIL=0
ERRORS=()

run_expr() {
    local dashboard="$1"
    local format="$2"
    local expr="$3"
    local label="$4"  # display label (panel title + truncated expr)

    local output exit_code
    set +e
    output=$("${BIN}" --format "${format}" transform --label-matcher="${LABEL_MATCHER}" -- "${expr}" 2>&1)
    exit_code=$?
    set -e

    if [[ $exit_code -ne 0 ]]; then
        FAIL=$((FAIL + 1))
        ERRORS+=("FAIL [${dashboard}] ${label}")
        ERRORS+=("     expr:   ${expr:0:120}")
        ERRORS+=("     output: ${output}")
        return
    fi

    # The label is considered present if either:
    #   a) cos-tool injected it as an exact matcher: key="value"
    #   b) the expression already had the label (any operator), so cos-tool correctly
    #      skipped injection — we check the output contains the label key at all.
    if [[ "${output}" != *"${EXPECTED_LABEL}"* && "${output}" != *"${MATCHER_KEY}="* && "${output}" != *"${MATCHER_KEY}=~"* ]]; then
        FAIL=$((FAIL + 1))
        ERRORS+=("FAIL [${dashboard}] ${label}")
        ERRORS+=("     expr:   ${expr:0:120}")
        ERRORS+=("     output: ${output}")
        ERRORS+=("     expected label '${EXPECTED_LABEL}' not found")
        return
    fi

    PASS=$((PASS + 1))
}

# ── Process dashboards ───────────────────────────────────────────────────────

shopt -s nullglob
json_files=("${DASHBOARD_DIR}"/*.json)

if [[ ${#json_files[@]} -eq 0 ]]; then
    echo "No dashboard JSON files found in ${DASHBOARD_DIR}" >&2
    exit 1
fi

for dashboard_file in "${json_files[@]}"; do
    dashboard=$(basename "${dashboard_file}" .json)
    echo "Dashboard: ${dashboard}"

    exprs_before=$((PASS + FAIL))
    pass_before=${PASS}
    fail_before=${FAIL}

    # Extract PromQL expressions (any object with a non-empty "expr" string field)
    # Use NUL-separated output to handle multi-line expressions correctly.
    while IFS= read -r -d '' expr; do
        [[ -z "${expr}" ]] && continue
        run_expr "${dashboard}" "promql" "${expr}" "${expr:0:40}"
    done < <(jq --raw-output0 '[.. | objects | select(has("expr") and (.expr | type) == "string" and .expr != "") | .expr] | .[]' "${dashboard_file}" 2>/dev/null)

    # Extract LogQL expressions (objects with "query" + Loki datasource)
    while IFS= read -r -d '' expr; do
        [[ -z "${expr}" ]] && continue
        run_expr "${dashboard}" "logql" "${expr}" "${expr:0:40}"
    done < <(jq --raw-output0 '[.. | objects | select(
        has("query") and (.query | type) == "string" and .query != ""
        and (.datasource | (type == "object" and (.type | ascii_downcase | contains("loki")))
             or (type == "string" and (ascii_downcase | contains("loki"))))
    ) | .query] | .[]' "${dashboard_file}" 2>/dev/null)

    pass_this=$((PASS - pass_before))
    fail_this=$((FAIL - fail_before))
    exprs_this=$((pass_this + fail_this))
    if [[ $exprs_this -eq 0 ]]; then
        echo "  WARNING: no expressions extracted (JSON may be invalid or unsupported format)"
    else
        echo "  expressions: ${exprs_this} | passed: ${pass_this}, failed: ${fail_this}"
    fi
done

# ── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "══════════════════════════════════════"
echo "Results: ${PASS} passed, ${FAIL} failed"
echo "══════════════════════════════════════"

if [[ ${FAIL} -gt 0 ]]; then
    echo ""
    echo "Failures:"
    for line in "${ERRORS[@]}"; do
        echo "  ${line}"
    done
    exit 1
fi

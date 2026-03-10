#!/usr/bin/env bash
# Integration tests for cos-tool against real Grafana dashboard JSON files.
#
# Extracts every PromQL/LogQL expression from each dashboard in
# tests/testdata/dashboards/ and runs `cos-tool transform` on each one,
# asserting the command exits without error.
#
# Any extra arguments are forwarded directly to cos-tool transform.
#
# Usage:
#   ./tests/integration/run_integration_tests.sh [cos-tool-transform-args...]
#
# Example:
#   ./tests/integration/run_integration_tests.sh --label-matcher juju_model=test

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="${REPO_ROOT}/bin/cos-tool"
DASHBOARD_DIR="${REPO_ROOT}/tests/testdata/dashboards"
JQ_FILTER="${REPO_ROOT}/tests/integration/extract_expressions.jq"

PASS=0
FAIL=0
TOTAL_PROMQL=0
TOTAL_LOGQL=0
ERRORS=()
EXTRA_ARGS=("$@")

run_expr() {
    local format="$1" expr="$2" output exit_code
    set +e
    output=$("${BIN}" --format "${format}" transform "${EXTRA_ARGS[@]}" -- "${expr}" 2>&1)
    exit_code=$?
    set -e
    if [[ $exit_code -ne 0 ]]; then
        d_fail=$((d_fail + 1))
        ERRORS+=("FAIL [${dashboard}/${format}] ${expr:0:80}")
        ERRORS+=("     output: ${output}")
        return
    fi
    # If EXPECTED_LABEL is set, assert the injected matcher appears in the output.
    # cos-tool skips injection when the label already exists in the expression, so we
    # accept output that already contains the label key (with any operator).
    if [[ -n "${EXPECTED_LABEL:-}" ]]; then
        local label_key="${EXPECTED_LABEL%%=*}"
        if [[ "${output}" != *"${EXPECTED_LABEL}"* && "${output}" != *"${label_key}="* && "${output}" != *"${label_key}=~"* ]]; then
            d_fail=$((d_fail + 1))
            ERRORS+=("FAIL [${dashboard}/${format}] label '${EXPECTED_LABEL}' not found in output")
            ERRORS+=("     expr:   ${expr:0:80}")
            ERRORS+=("     output: ${output}")
            return
        fi
    fi
    d_pass=$((d_pass + 1))
}

for dashboard_file in "${DASHBOARD_DIR}"/*.json; do
    dashboard=$(basename "${dashboard_file}" .json)
    d_pass=0
    d_fail=0
    d_promql=0
    d_logql=0

    while IFS= read -r -d '' expr; do
        d_promql=$((d_promql + 1))
        run_expr "promql" "${expr}"
    done < <(jq --raw-output0 --arg mode promql -f "${JQ_FILTER}" "${dashboard_file}" 2>/dev/null)

    while IFS= read -r -d '' expr; do
        d_logql=$((d_logql + 1))
        run_expr "logql" "${expr}"
    done < <(jq --raw-output0 --arg mode logql -f "${JQ_FILTER}" "${dashboard_file}" 2>/dev/null)

    PASS=$((PASS + d_pass))
    FAIL=$((FAIL + d_fail))
    TOTAL_PROMQL=$((TOTAL_PROMQL + d_promql))
    TOTAL_LOGQL=$((TOTAL_LOGQL + d_logql))
    echo "Dashboard: ${dashboard}"
    echo "  promql: ${d_promql} | logql: ${d_logql} | passed: ${d_pass}, failed: ${d_fail}"
done

echo ""
echo "══════════════════════════════════════"
echo "Results: ${PASS} passed, ${FAIL} failed (promql: ${TOTAL_PROMQL}, logql: ${TOTAL_LOGQL})"
echo "══════════════════════════════════════"

if [[ ${FAIL} -gt 0 ]]; then
    echo ""
    echo "Failures:"
    for line in "${ERRORS[@]}"; do
        echo "  ${line}"
    done
    exit 1
fi

#!/usr/bin/env bash
# Integration tests for `cos-tool transform --batch` against real Grafana
# dashboard JSON files.
#
# Extracts every PromQL/LogQL expression from tests/testdata/dashboards/ into a
# single JSONL batch file, transforms the whole corpus in one invocation, and
# asserts the properties the batch interface guarantees:
#
#   - exit zero means the batch completed
#   - exactly one result line per request line, in input order
#   - every result carries the interface version and a known status
#   - batch output matches what the single-expression path produces
#
# Any extra arguments are forwarded to cos-tool transform.
#
# Usage:
#   ./tests/integration/run_batch_integration_tests.sh [cos-tool-transform-args...]
#
# Example:
#   ./tests/integration/run_batch_integration_tests.sh --label-matcher juju_model=test

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN="${REPO_ROOT}/bin/cos-tool"
DASHBOARD_DIR="${REPO_ROOT}/tests/testdata/dashboards"
JQ_FILTER="${REPO_ROOT}/tests/integration/extract_expressions.jq"

EXTRA_ARGS=("$@")

# Scratch space for the batch and results files. Honours TMPDIR so the suite
# can run where the default temp directory is not writable.
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/cos-tool-batch-XXXXXX")"
trap 'rm -rf "${WORK_DIR}"' EXIT

BATCH_FILE="${WORK_DIR}/requests.jsonl"
RESULTS_FILE="${WORK_DIR}/results.jsonl"

FAILURES=()

fail() {
    FAILURES+=("$1")
}

# ──────────────────────────────────────
# Capability probe
# ──────────────────────────────────────
echo "Checking batch support..."
if ! BATCH_VERSION=$("${BIN}" transform --batch-version 2>/dev/null); then
    echo "FATAL: ${BIN} does not support batch mode (transform --batch-version failed)"
    exit 1
fi
echo "  batch interface version: ${BATCH_VERSION}"

# ──────────────────────────────────────
# Build one batch file from every dashboard expression
# ──────────────────────────────────────
echo "Building batch file from dashboards in ${DASHBOARD_DIR}..."
: > "${BATCH_FILE}"

for dashboard_file in "${DASHBOARD_DIR}"/*.json; do
    dashboard=$(basename "${dashboard_file}" .json)

    for format in promql logql; do
        # Expressions are emitted NUL-separated, then rebuilt into request
        # objects by jq so that embedded quotes and newlines are escaped
        # correctly. Splitting on NUL keeps multi-line expressions intact.
        jq --raw-output0 --arg mode "${format}" -f "${JQ_FILTER}" "${dashboard_file}" 2>/dev/null \
            | jq --slurp --raw-input --compact-output \
                --arg dashboard "${dashboard}" \
                --arg format "${format}" \
                'split("\u0000")
                 | map(select(length > 0))
                 | to_entries[]
                 | {
                     id: ($dashboard + "/" + $format + "/" + (.key | tostring)),
                     expr: .value,
                     format: $format
                   }' \
            >> "${BATCH_FILE}" || true
    done
done

REQUEST_COUNT=$(wc -l < "${BATCH_FILE}" | tr -d ' ')
echo "  ${REQUEST_COUNT} expressions collected"

if [[ "${REQUEST_COUNT}" -eq 0 ]]; then
    echo "FATAL: no expressions extracted; the corpus or jq filter is broken"
    exit 1
fi

# ──────────────────────────────────────
# Transform the whole corpus in one invocation
# ──────────────────────────────────────
echo "Transforming ${REQUEST_COUNT} expressions in one invocation..."
set +e
BATCH_STDOUT=$("${BIN}" transform \
    --batch "${BATCH_FILE}" \
    --out "${RESULTS_FILE}" \
    "${EXTRA_ARGS[@]}" 2>"${WORK_DIR}/stderr")
BATCH_EXIT=$?
set -e

if [[ ${BATCH_EXIT} -ne 0 ]]; then
    echo "FATAL: batch invocation exited ${BATCH_EXIT}; results file is unusable"
    cat "${WORK_DIR}/stderr"
    exit 1
fi

if [[ -n "${BATCH_STDOUT}" ]]; then
    fail "stdout must carry no result data, got: ${BATCH_STDOUT:0:200}"
fi

# ──────────────────────────────────────
# Line parity
# ──────────────────────────────────────
RESULT_COUNT=$(wc -l < "${RESULTS_FILE}" | tr -d ' ')
echo "  ${RESULT_COUNT} results written"

if [[ "${RESULT_COUNT}" -ne "${REQUEST_COUNT}" ]]; then
    fail "line parity violated: ${REQUEST_COUNT} requests produced ${RESULT_COUNT} results"
fi

# ──────────────────────────────────────
# Per-result structure, ordering and status tally
# ──────────────────────────────────────
echo "Validating results..."

BAD_VERSION=$(jq --argjson want "${BATCH_VERSION}" 'select(.version != $want)' "${RESULTS_FILE}" | wc -l | tr -d ' ')
if [[ "${BAD_VERSION}" -ne 0 ]]; then
    fail "some results do not report version ${BATCH_VERSION}"
fi

UNKNOWN_STATUS=$(jq --raw-output \
    'select((.status | IN("ok","error","malformed","unsupported_version")) | not) | .status' \
    "${RESULTS_FILE}" | sort -u)
if [[ -n "${UNKNOWN_STATUS}" ]]; then
    fail "unknown status values in results: ${UNKNOWN_STATUS}"
fi

# Results must be in input order, which means the ids line up one-for-one.
jq --raw-output '.id' "${BATCH_FILE}" > "${WORK_DIR}/request-ids"
jq --raw-output '.id' "${RESULTS_FILE}" > "${WORK_DIR}/result-ids"
if ! diff "${WORK_DIR}/request-ids" "${WORK_DIR}/result-ids" > "${WORK_DIR}/order.diff"; then
    fail "results are not in input order (see diff below)"
    head -20 "${WORK_DIR}/order.diff"
fi

# An ok result must carry expr; any failure must carry error.
MISSING_EXPR=$(jq 'select(.status == "ok" and (.expr | not))' "${RESULTS_FILE}" | wc -l | tr -d ' ')
if [[ "${MISSING_EXPR}" -ne 0 ]]; then
    fail "some ok results carry no expr"
fi

MISSING_ERROR=$(jq 'select(.status != "ok" and (.error | not))' "${RESULTS_FILE}" | wc -l | tr -d ' ')
if [[ "${MISSING_ERROR}" -ne 0 ]]; then
    fail "some failed results carry no error message"
fi

OK_COUNT=$(jq --raw-output 'select(.status == "ok") | .id' "${RESULTS_FILE}" | wc -l | tr -d ' ')
ERROR_COUNT=$(jq --raw-output 'select(.status == "error") | .id' "${RESULTS_FILE}" | wc -l | tr -d ' ')
MALFORMED_COUNT=$(jq --raw-output 'select(.status == "malformed") | .id' "${RESULTS_FILE}" | wc -l | tr -d ' ')
UNSUPPORTED_COUNT=$(jq --raw-output 'select(.status == "unsupported_version") | .id' "${RESULTS_FILE}" | wc -l | tr -d ' ')

# Nothing in a generated batch should be malformed or version-mismatched:
# those would indicate the harness or the interface is broken, not the data.
if [[ "${MALFORMED_COUNT}" -ne 0 ]]; then
    fail "${MALFORMED_COUNT} generated requests were reported malformed"
    jq --raw-output 'select(.status == "malformed") | "    line \(.line): \(.error)"' "${RESULTS_FILE}" | head -5
fi

if [[ "${UNSUPPORTED_COUNT}" -ne 0 ]]; then
    fail "${UNSUPPORTED_COUNT} requests reported an unsupported version"
fi

# ──────────────────────────────────────
# Expected injected label
# ──────────────────────────────────────
if [[ -n "${EXPECTED_LABEL:-}" ]]; then
    LABEL_KEY="${EXPECTED_LABEL%%=*}"
    MISSING_LABEL=$(jq --raw-output \
        --arg key "${LABEL_KEY}" \
        'select(.status == "ok" and ((.expr | contains($key)) | not)) | .id' \
        "${RESULTS_FILE}" | wc -l | tr -d ' ')
    if [[ "${MISSING_LABEL}" -ne 0 ]]; then
        fail "${MISSING_LABEL} ok results are missing the injected label '${LABEL_KEY}'"
        jq --raw-output --arg key "${LABEL_KEY}" \
            'select(.status == "ok" and ((.expr | contains($key)) | not)) | "    \(.id): \(.expr[0:100])"' \
            "${RESULTS_FILE}" | head -5
    fi
fi

# ──────────────────────────────────────
# Batch output must agree with the single-expression path
# ──────────────────────────────────────
echo "Cross-checking a sample against the single-expression path..."
SAMPLE_SIZE=100
MISMATCHES=0
CHECKED=0
head -"${SAMPLE_SIZE}" "${BATCH_FILE}" > "${WORK_DIR}/sample.jsonl"

while IFS= read -r line; do
    id=$(jq --raw-output '.id' <<< "${line}")
    format=$(jq --raw-output '.format' <<< "${line}")
    expr=$(jq --raw-output '.expr' <<< "${line}")

    batch_status=$(jq --raw-output --arg id "${id}" \
        'select(.id == $id) | .status' "${RESULTS_FILE}" | head -1)
    batch_expr=$(jq --raw-output --arg id "${id}" \
        'select(.id == $id) | .expr // ""' "${RESULTS_FILE}" | head -1)

    set +e
    single_expr=$("${BIN}" --format "${format}" transform "${EXTRA_ARGS[@]}" -- "${expr}" 2>/dev/null)
    single_exit=$?
    set -e

    CHECKED=$((CHECKED + 1))

    if [[ ${single_exit} -eq 0 && "${batch_status}" != "ok" ]]; then
        MISMATCHES=$((MISMATCHES + 1))
        fail "single-expression path succeeded but batch reported '${batch_status}' for ${id}"
    elif [[ ${single_exit} -ne 0 && "${batch_status}" == "ok" ]]; then
        MISMATCHES=$((MISMATCHES + 1))
        fail "batch succeeded but the single-expression path failed for ${id}"
    elif [[ ${single_exit} -eq 0 && "${batch_expr}" != "${single_expr}" ]]; then
        MISMATCHES=$((MISMATCHES + 1))
        fail "batch and single-expression output differ for ${id}"
    fi
done < "${WORK_DIR}/sample.jsonl"

echo "  cross-checked ${CHECKED} expressions, ${MISMATCHES} mismatches"

# ──────────────────────────────────────
# Empty batch
# ──────────────────────────────────────
echo "Checking empty batch..."
: > "${WORK_DIR}/empty.jsonl"
if "${BIN}" transform --batch "${WORK_DIR}/empty.jsonl" --out "${WORK_DIR}/empty-results.jsonl" "${EXTRA_ARGS[@]}" 2>/dev/null; then
    if [[ -s "${WORK_DIR}/empty-results.jsonl" ]]; then
        fail "an empty batch must produce an empty results file"
    fi
else
    fail "an empty batch must exit zero"
fi

# ──────────────────────────────────────
# Usage errors must create no results file
# ──────────────────────────────────────
echo "Checking usage errors..."
if "${BIN}" transform --batch "${BATCH_FILE}" 2>/dev/null; then
    fail "--batch without --out must exit non-zero"
fi
if "${BIN}" transform --out "${WORK_DIR}/never.jsonl" 2>/dev/null; then
    fail "--out without --batch must exit non-zero"
fi
if [[ -e "${WORK_DIR}/never.jsonl" ]]; then
    fail "a usage error must not create the results file"
fi
if "${BIN}" transform --batch "${BATCH_FILE}" --out "${WORK_DIR}/never2.jsonl" -- 'up == 0' 2>/dev/null; then
    fail "--batch alongside an expression must exit non-zero"
fi

# ──────────────────────────────────────
# Summary
# ──────────────────────────────────────
echo ""
echo "══════════════════════════════════════"
echo "Batch results: ${REQUEST_COUNT} expressions in one invocation"
echo "  ok: ${OK_COUNT} | error: ${ERROR_COUNT} | malformed: ${MALFORMED_COUNT} | unsupported_version: ${UNSUPPORTED_COUNT}"
echo "══════════════════════════════════════"

if [[ ${#FAILURES[@]} -gt 0 ]]; then
    echo ""
    echo "Failures:"
    for failure in "${FAILURES[@]}"; do
        echo "  ${failure}"
    done
    exit 1
fi

echo ""
echo "All batch integration checks passed."

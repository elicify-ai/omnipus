#!/usr/bin/env bash
# check-e2e-skip-allowlist.sh
#
# Validate every skipped Playwright spec in a JSON reporter output against the
# skip manifest written by tests/e2e/global-teardown.ts. A skip is authorized
# only when its exact spec title has a non-expired entry in .allowlisted[].
# Missing or malformed inputs fail closed whenever the report contains skips.
#
# Usage:
#   bash scripts/check-e2e-skip-allowlist.sh <playwright-json> <skip-manifest-json> <expected-skipped-count>

set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "Usage: $0 <playwright-json> <skip-manifest-json> <expected-skipped-count>" >&2
  exit 2
fi

REPORT_PATH="$1"
MANIFEST_PATH="$2"
EXPECTED_SKIPPED="$3"
TODAY_UTC="$(date -u +%Y-%m-%d)"
TITLES_FILE="$(mktemp /tmp/check-e2e-skip-allowlist.XXXXXX)"
trap 'rm -f "$TITLES_FILE"' EXIT

case "$EXPECTED_SKIPPED" in
  ''|*[!0-9]*)
    echo "check-e2e-skip-allowlist: ERROR — expected skipped count must be a plain non-negative integer (got: '$EXPECTED_SKIPPED')" >&2
    exit 2
    ;;
esac

if [ ! -f "$REPORT_PATH" ] || ! jq -e 'type == "object"' "$REPORT_PATH" >/dev/null 2>&1; then
  echo "check-e2e-skip-allowlist: ERROR — Playwright JSON report is missing or unparseable: $REPORT_PATH" >&2
  exit 1
fi

# Playwright suites can nest arbitrarily. recurse(.suites[]?) visits the
# current suite and every descendant; the title of a spec is emitted ONCE PER
# skipped test-run entry under that spec. This matches Playwright's own
# `.stats.skipped` granularity (spec times project — e.g. the same skip under
# isolation-chromium/firefox/webkit is three test-runs), so the count cross-
# check below stays correct in the multi-project case. Do NOT deduplicate
# before the allow-list loop: a title appearing multiple times is checked
# (and, when authorized, printed as AUTHORIZED) per occurrence, which keeps
# `checked` in lockstep with `extracted_count` for free.
if ! jq -c '
  .suites[]?
  | recurse(.suites[]?)
  | .specs[]?
  | select(type == "object")
  | .title as $title
  | .tests[]?
  | select(.status == "skipped")
  | $title
  | select(type == "string")
' "$REPORT_PATH" > "$TITLES_FILE"; then
  echo "check-e2e-skip-allowlist: ERROR — could not extract skipped spec titles from: $REPORT_PATH" >&2
  exit 1
fi

extracted_count="$(jq -s 'length' "$TITLES_FILE")"
if [ "$extracted_count" -ne "$EXPECTED_SKIPPED" ]; then
  echo "check-e2e-skip-allowlist: ERROR — Playwright stats report $EXPECTED_SKIPPED skipped test(s), but extracted $extracted_count skipped spec title(s); refusing incomplete skip validation" >&2
  exit 1
fi

if [ "$extracted_count" -eq 0 ]; then
  echo "check-e2e-skip-allowlist: OK — no skipped Playwright spec titles found"
  exit 0
fi

manifest_valid=1
if [ ! -f "$MANIFEST_PATH" ] || ! jq -e '.allowlisted | type == "array"' "$MANIFEST_PATH" >/dev/null 2>&1; then
  manifest_valid=0
  echo "check-e2e-skip-allowlist: ERROR — skip manifest is missing, unparseable, or has no .allowlisted array: $MANIFEST_PATH" >&2
fi

checked=0
unauthorized=0
while IFS= read -r json_title; do
  title="$(jq -r 'if type == "string" then . else error("skipped spec title is not a string") end' <<<"$json_title")"
  checked=$((checked + 1))

  if [ "$manifest_valid" -ne 1 ]; then
    echo "check-e2e-skip-allowlist: UNAUTHORIZED — \"$title\" (skip manifest unavailable or invalid)" >&2
    unauthorized=$((unauthorized + 1))
    continue
  fi

  if ! entry="$(jq -e -c --arg title "$title" '[.allowlisted[] | select(.test == $title)][0] // empty' "$MANIFEST_PATH" 2>/dev/null)"; then
    echo "check-e2e-skip-allowlist: UNAUTHORIZED — \"$title\" (no exact SKIP_ALLOWLIST match)" >&2
    unauthorized=$((unauthorized + 1))
    continue
  fi

  issue="$(jq -r '.issue // empty' <<<"$entry")"
  until="$(jq -r '.until // empty' <<<"$entry")"
  if [ -z "$issue" ]; then
    echo "check-e2e-skip-allowlist: UNAUTHORIZED — \"$title\" (matching entry has no issue)" >&2
    unauthorized=$((unauthorized + 1))
    continue
  fi
  if [[ ! "$until" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
    echo "check-e2e-skip-allowlist: UNAUTHORIZED — \"$title\" (matching entry has invalid until date: '$until')" >&2
    unauthorized=$((unauthorized + 1))
    continue
  fi
  if [[ "$until" < "$TODAY_UTC" ]]; then
    echo "check-e2e-skip-allowlist: EXPIRED — \"$title\" (issue: $issue, until: $until, today UTC: $TODAY_UTC)" >&2
    unauthorized=$((unauthorized + 1))
    continue
  fi

  echo "check-e2e-skip-allowlist: AUTHORIZED — \"$title\" (issue: $issue, until: $until)"
done < "$TITLES_FILE"

if [ "$unauthorized" -gt 0 ]; then
  echo "check-e2e-skip-allowlist: FAILED — $unauthorized of $checked skipped spec title(s) unauthorized or expired" >&2
  exit 1
fi

echo "check-e2e-skip-allowlist: OK — all $checked skipped spec title(s) are allowlisted and non-expired"
exit 0

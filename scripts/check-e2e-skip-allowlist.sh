#!/usr/bin/env bash
# check-e2e-skip-allowlist.sh
#
# Validate every skipped Playwright spec in a JSON reporter output against the
# skip manifest written by tests/e2e/global-teardown.ts. A skip is authorized
# only when its exact spec title has a non-expired entry in .allowlisted[].
# Missing or malformed inputs fail closed whenever the report contains skips.
#
# Usage:
#   bash scripts/check-e2e-skip-allowlist.sh <playwright-json> <skip-manifest-json>

set -u

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <playwright-json> <skip-manifest-json>" >&2
  exit 2
fi

REPORT_PATH="$1"
MANIFEST_PATH="$2"
TODAY_UTC="$(date -u +%Y-%m-%d)"
TITLES_FILE="$(mktemp /tmp/check-e2e-skip-allowlist.XXXXXX)"
trap 'rm -f "$TITLES_FILE"' EXIT

if [ ! -f "$REPORT_PATH" ] || ! jq -e 'type == "object"' "$REPORT_PATH" >/dev/null 2>&1; then
  echo "check-e2e-skip-allowlist: ERROR — Playwright JSON report is missing or unparseable: $REPORT_PATH" >&2
  exit 1
fi

# Playwright suites can nest arbitrarily. recurse(.suites[]?) visits the
# current suite and every descendant; a spec is skipped when any project/test
# rollup under that spec has status "skipped".
if ! jq -r '
  .suites[]?
  | recurse(.suites[]?)
  | .specs[]?
  | select(any(.tests[]?; .status == "skipped"))
  | .title
  | select(type == "string")
  | @base64
' "$REPORT_PATH" > "$TITLES_FILE"; then
  echo "check-e2e-skip-allowlist: ERROR — could not extract skipped spec titles from: $REPORT_PATH" >&2
  exit 1
fi

if [ ! -s "$TITLES_FILE" ]; then
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
while IFS= read -r encoded_title; do
  [ -z "$encoded_title" ] && continue
  title="$(jq -nr --arg encoded "$encoded_title" '$encoded | @base64d')"
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

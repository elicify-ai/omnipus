#!/usr/bin/env bash
# Decides whether one test really ran and passed.
# usage: verify-log.sh <test name> <log file> <go test exit code>
# Exit 0 only for: go test exited 0, the log holds exactly one top-level PASS
# line for this test, and no failure, skip, BLOCKED or "no tests" line anywhere.
set -uo pipefail

name="${1:?test name}"
log="${2:?log file}"
code="${3:?exit code}"

refuse() {
  echo "office-real: $name: $1" >&2
  exit 1
}

[[ "$name" =~ ^Test[A-Za-z0-9_]+$ ]] || refuse "not a test name"
[ -f "$log" ] || refuse "log file missing"
[ "$code" = "0" ] || refuse "go test exited $code"
if grep -aq -- 'no tests to run' "$log"; then refuse "no test matched: it did not run"; fi
if grep -aqE -- '--- FAIL:' "$log"; then refuse "a test failed"; fi
if grep -aqE -- '--- SKIP:' "$log"; then refuse "a test or subtest was skipped"; fi
if grep -aq -- 'BLOCKED:' "$log"; then refuse "the log reports BLOCKED"; fi
passes="$(grep -acE -- "^--- PASS: $name \\(" "$log")"
[ "$passes" = "1" ] || refuse "expected exactly one PASS line, found $passes"

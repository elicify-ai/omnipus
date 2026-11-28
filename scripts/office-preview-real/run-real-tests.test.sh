#!/usr/bin/env bash
# Proves the dedicated job's instruments can fail: verify-log.sh on recorded
# logs, and run-real-tests.sh end to end with a stand-in `go`. No real test
# runs here. Exit 0 only if every case behaves as stated.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
failures=0
cases=0

expect() { # label wanted-exit actual-exit
  cases=$((cases + 1))
  if [ "$2" = "$3" ]; then
    echo "  PASS $1"
  else
    echo "  FAIL $1: wanted exit $2, got $3"
    failures=$((failures + 1))
  fi
}

T=TestExample
log="$tmp/case.log"
verify() { # label wanted-exit go-exit log-content
  printf '%b' "$4" > "$log"
  bash "$here/verify-log.sh" "$T" "$log" "$3" 2>/dev/null
  expect "verify: $1" "$2" "$?"
}

echo "verify-log.sh"
ok="=== RUN   $T\n--- PASS: $T (0.01s)\nPASS\n"
verify "a real pass" 0 0 "$ok"
verify "go test failed" 1 2 "$ok"
verify "nothing matched" 1 0 "testing: warning: no tests to run\nPASS\n"
verify "empty log" 1 0 ""
verify "only another test passed" 1 0 "--- PASS: TestOther (0.01s)\nPASS\n"
verify "a longer name passed" 1 0 "--- PASS: ${T}Longer (0.01s)\nPASS\n"
verify "ran twice" 1 0 "--- PASS: $T (0.01s)\n--- PASS: $T (0.01s)\nPASS\n"
verify "skipped" 1 0 "--- SKIP: $T (0.00s)\nPASS\n"
verify "subtest skipped" 1 0 "    --- SKIP: $T/sub (0.00s)\n--- PASS: $T (0.01s)\nPASS\n"
verify "subtest failed" 1 0 "    --- FAIL: $T/sub (0.00s)\n--- PASS: $T (0.01s)\nPASS\n"
verify "BLOCKED reported" 1 0 "BLOCKED: no fixture\n--- PASS: $T (0.01s)\nPASS\n"
bash "$here/verify-log.sh" "$T" "$tmp/absent.log" 0 2>/dev/null
expect "verify: missing log" 1 "$?"
bash "$here/verify-log.sh" "not a test" "$log" 0 2>/dev/null
expect "verify: bad name" 1 "$?"

echo "run-real-tests.sh with a stand-in go"
mkdir -p "$tmp/bin" "$tmp/office" "$tmp/renderer" "$tmp/repo"
cat > "$tmp/bin/go" <<'FAKE'
#!/usr/bin/env bash
# Prints whatever behaviour the case asked for, for the test named in -run.
name=""
while [ $# -gt 0 ]; do
  if [ "$1" = "-run" ]; then name="${2#^}"; name="${name%\$}"; fi
  shift
done
case "$FAKE_GO" in
  pass) printf -- '--- PASS: %s (0.01s)\nPASS\n' "$name" ;;
  none) printf 'testing: warning: no tests to run\nPASS\n' ;;
  skip) printf -- '--- SKIP: %s (0.00s)\nPASS\n' "$name" ;;
  fail) printf -- '--- FAIL: %s (0.00s)\nFAIL\n' "$name"; exit 1 ;;
  second-fails)
    if [ "$name" = "TestTwo" ]; then printf -- '--- FAIL: %s (0.00s)\nFAIL\n' "$name"; exit 1; fi
    printf -- '--- PASS: %s (0.01s)\nPASS\n' "$name" ;;
esac
FAKE
chmod +x "$tmp/bin/go"
printf '# comment\npkg/one TestOne\n\npkg/two TestTwo\n' > "$tmp/list"
: > "$tmp/empty"
printf '# only a comment\n' > "$tmp/comments"
printf 'pkg/one\n' > "$tmp/badline"

run() { # label wanted-exit fake-go list [env assignments...]
  local label="$1" wanted="$2" fake="$3" chosen="$4"
  shift 4
  env PATH="$tmp/bin:$PATH" FAKE_GO="$fake" OFFICE_REAL_LIST="$chosen" OFFICE_REAL_REPO="$tmp/repo" \
    OMNIPUS_TEST_OFFICE_ROOT="$tmp/office" OMNIPUS_TEST_RENDERER_ROOT="$tmp/renderer" \
    OMNIPUS_OFFICE_REAL_TESTS_JOB="" GITHUB_STEP_SUMMARY="$tmp/summary" "$@" \
    bash "$here/run-real-tests.sh" "$tmp/logs" > /dev/null 2>&1
  expect "run: $label" "$wanted" "$?"
}

run "all tests pass" 0 pass "$tmp/list"
run "go matches nothing" 1 none "$tmp/list"
run "every test skips" 1 skip "$tmp/list"
run "every test fails" 1 fail "$tmp/list"
run "one of two fails" 1 second-fails "$tmp/list"
run "empty list" 1 pass "$tmp/empty"
run "list of comments only" 1 pass "$tmp/comments"
run "malformed list line" 1 pass "$tmp/badline"
run "list file absent" 1 pass "$tmp/no-such-list"
run "handover variable set" 1 pass "$tmp/list" OMNIPUS_OFFICE_REAL_TESTS_JOB=somejob
run "office folder unset" 1 pass "$tmp/list" OMNIPUS_TEST_OFFICE_ROOT=
run "renderer folder absent" 1 pass "$tmp/list" OMNIPUS_TEST_RENDERER_ROOT="$tmp/no-such-folder"

echo "real-tests.txt"
listed="$(awk '!/^[[:space:]]*(#|$)/ {print NF}' "$here/real-tests.txt" | sort -u)"
if [ "$listed" = "2" ]; then got=0; else got=1; fi
expect "every line has a package and a test" 0 "$got"
dups="$(awk '!/^[[:space:]]*(#|$)/ {print $2}' "$here/real-tests.txt" | sort | uniq -d | wc -l | tr -d ' ')"
if [ "$dups" = "0" ]; then got=0; else got=1; fi
expect "no test is listed twice" 0 "$got"

echo "$cases cases, $failures failed"
[ "$failures" -eq 0 ]

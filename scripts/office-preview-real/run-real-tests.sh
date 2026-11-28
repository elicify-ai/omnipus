#!/usr/bin/env bash
# Runs every test in real-tests.txt, each in its own process, and fails unless
# every one of them really ran and passed.
# usage: run-real-tests.sh <log dir>
# Needs OMNIPUS_TEST_OFFICE_ROOT and OMNIPUS_TEST_RENDERER_ROOT (provision.sh).
#
# OFFICE_REAL_LIST and OFFICE_REAL_REPO exist only for run-real-tests.test.sh.
set -uo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
list="${OFFICE_REAL_LIST:-$here/real-tests.txt}"
repo="${OFFICE_REAL_REPO:-$(cd "$here/../.." && pwd)}"
logs="${1:?log dir}"

die() {
  echo "office-real: $1" >&2
  exit 1
}

# With this set, the tests would hand themselves over instead of running.
[ -z "${OMNIPUS_OFFICE_REAL_TESTS_JOB:-}" ] || die "OMNIPUS_OFFICE_REAL_TESTS_JOB is set; the tests would not run"
[ -d "${OMNIPUS_TEST_OFFICE_ROOT:-}" ] || die "OMNIPUS_TEST_OFFICE_ROOT is not a folder"
[ -d "${OMNIPUS_TEST_RENDERER_ROOT:-}" ] || die "OMNIPUS_TEST_RENDERER_ROOT is not a folder"
[ -f "$list" ] || die "test list missing: $list"
mkdir -p "$logs" || die "cannot create $logs"
logs="$(cd "$logs" && pwd)"
cd "$repo" || die "cannot enter $repo"

expected=0
passed=0
failed=""
rows=""
while read -r pkg name extra; do
  case "$pkg" in ''|\#*) continue ;; esac
  [ -n "$name" ] && [ -z "$extra" ] || die "bad list line: $pkg $name $extra"
  expected=$((expected + 1))
  log="$logs/$name.log"
  CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -v -timeout 40m -run "^${name}\$" "./$pkg/" > "$log" 2>&1 < /dev/null
  code=$?
  if bash "$here/verify-log.sh" "$name" "$log" "$code"; then
    passed=$((passed + 1))
    rows="$rows| \`$name\` | passed |"$'\n'
    echo "office-real: PASS $name"
  else
    failed="$failed $name"
    rows="$rows| \`$name\` | **not passed** (exit $code) |"$'\n'
    echo "--- last lines of $name ---"
    tail -n 40 "$log"
  fi
done < "$list"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf '## Office preview real programs\n\n| Test | Result |\n| --- | --- |\n%s\n%s of %s passed\n' \
    "$rows" "$passed" "$expected" >> "$GITHUB_STEP_SUMMARY"
fi

[ "$expected" -gt 0 ] || die "the test list names no tests; nothing ran"
[ -z "$failed" ] || die "not passed:$failed"
[ "$passed" -eq "$expected" ] || die "$passed of $expected passed"
echo "office-real: all $passed tests ran and passed"

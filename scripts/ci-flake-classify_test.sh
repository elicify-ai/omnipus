#!/usr/bin/env bash
# Proves a guard-only go-test log is not flake-excused, and a hang-shaped
# log still is. The expected token is the one the brief named (GUARD FAILURE:),
# not a string copied from a test run.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
# shellcheck source=scripts/ci-flake-classify.sh
source "$root/scripts/ci-flake-classify.sh"

token='GUARD FAILURE:'
rc=0

guard_log=$(cat <<EOF
ok  	github.com/elicify-ai/omnipus/pkg/gateway	1.200s
${token} gateway TestMain: a test wrote into the package directory instead of its temp home: tasks/.milestones_migrated
FAIL	github.com/elicify-ai/omnipus/pkg/gateway	1.200s
FAIL
EOF
)

hang_log=$(cat <<'EOF'
panic: test timed out after 75m0s
FAIL	github.com/elicify-ai/omnipus/pkg/gateway	4500.000s
FAIL
EOF
)

if [[ "$guard_log" == *"--- FAIL"* || "$guard_log" == *"DATA RACE"* ]]; then
	echo "FAIL fixture: the guard log must not contain --- FAIL or DATA RACE"
	rc=1
fi
if [[ "$hang_log" == *"$token"* || "$hang_log" == *"--- FAIL"* || "$hang_log" == *"DATA RACE"* ]]; then
	echo "FAIL fixture: the hang log must be a bare package failure"
	rc=1
fi
if [[ "$hang_log" != *"FAIL	github.com/elicify-ai/omnipus/pkg/gateway"* ]]; then
	echo "FAIL fixture: the hang log must name the package"
	rc=1
fi

if ci_log_has_guard_failure "$guard_log"; then
	echo "PASS guard-only log is not excused"
else
	echo "FAIL guard-only log was excused"
	rc=1
fi

if ci_log_has_guard_failure "$hang_log"; then
	echo "FAIL hang-shaped log was not excused"
	rc=1
else
	echo "PASS hang-shaped log may still be excused"
fi

# The three hygiene messages TestMain prints must carry the token.
main="$root/pkg/gateway/testmain_test.go"
token_hits=$(grep -c -F "$token" "$main" || true)
if [[ "$token_hits" -eq 3 ]]; then
	echo "PASS TestMain carries the token on all three hygiene messages"
else
	echo "FAIL TestMain token count is $token_hits, want 3"
	rc=1
fi

# Each excuse path that runs pkg/gateway must call the classifier before it
# retries. The shared race step skips the gateway shard, and the plain
# non-gateway step does not run this package.
want_before() {
	local file="$1" start="$2" end="$3" label="$4"
	local body
	body=$(awk -v start="$start" -v end="$end" '
		index($0, start) {capture=1}
		capture {print}
		capture && index($0, end) && !index($0, start) {exit}
	' "$file")
	if [[ -z "$body" ]]; then
		echo "FAIL $label: section not found"
		rc=1
		return
	fi
	local call flake
	call=$(printf '%s\n' "$body" | awk '/ci_log_has_guard_failure/{print NR; exit}')
	flake=$(printf '%s\n' "$body" | awk '/echo "=== FLAKE FILTER/{print NR; exit}')
	if [[ -z "$call" || -z "$flake" || "$call" -ge "$flake" ]]; then
		echo "FAIL $label: classifier is not called before the flake filter"
		rc=1
		return
	fi
	if ! printf '%s\n' "$body" | grep -q 'source scripts/ci-flake-classify.sh'; then
		echo "FAIL $label: does not source scripts/ci-flake-classify.sh"
		rc=1
		return
	fi
	echo "PASS $label calls the classifier before the flake filter"
}

want_before "$root/.github/workflows/pr.yml" \
	'Run pkg/gateway tests (dedicated shard' \
	'Run test-tagged approver escape-hatch test' \
	'pr.yml gateway step'
want_before "$root/.github/workflows/pr.yml" \
	'Run go test -race ./pkg/gateway/...' \
	'test-race:' \
	'pr.yml gateway race step'
want_before "$root/deploy/ci-worker/runci.sh" \
	'run_gotest()' \
	'run_records_no_sqlite()' \
	'run_gotest'
want_before "$root/deploy/ci-worker/runci.sh" \
	'run_gorace()' \
	'run_gotest()' \
	'run_gorace'

exit "$rc"

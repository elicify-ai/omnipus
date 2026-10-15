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

# Execute the checked-in post-processing body. A stub go records each
# invocation and exits 0, so a retry is visible and a clean retry would
# be excused. This is the real fragment, not a copy of its logic.
extract_guard_body() {
	local file="$1" start="$2" end="$3"
	awk -v start="$start" -v end="$end" '
		index($0, start) {capture=1}
		capture {print}
		capture && index($0, end) && !index($0, start) {exit}
	' "$file" | awk '
		index($0, "GUARD FAILURE is TestMain") {keep=1}
		keep {print}
		keep && ($0 ~ /(exit|return) \$rc[[:space:]]*$/) {exit}
	'
}

run_extracted() {
	local body="$1" input="$2" stublog="$3" outlog="$4"
	local stubdir work runsh status retry_path race_retry_path
	stubdir=$(mktemp -d)
	work=$(mktemp -d)
	: >"$stublog"
	cat >"$stubdir/go" <<-'EOF'
#!/bin/bash
printf '%s\n' "$*" >>"${GO_STUB_LOG:?}"
exit 0
EOF
	chmod +x "$stubdir/go"
	# Keep the workflow's retry evidence private to this extraction, too.
	printf -v retry_path '%q' "$work/rr_gateway.log"
	printf -v race_retry_path '%q' "$work/rr_gw_race.log"
	body=${body//\/tmp\/rr_gateway.log/$retry_path}
	body=${body//\/tmp\/rr_gw_race.log/$race_retry_path}
	printf '%s\n' "$body" >"$work/body.sh"
	printf '%s\n' "$input" >"$work/input.log"
	runsh="$work/run.sh"
	cat >"$runsh" <<-EOF
set +e
cd $(printf '%q' "$root") || exit 99
export PATH=$(printf '%q' "$stubdir"):\$PATH
export GO_STUB_LOG=$(printf '%q' "$stublog")
export TMPDIR=$(printf '%q' "$work")
export TAGS=goolm,stdjson
out=\$(cat $(printf '%q' "$work/input.log"))
code=1
post() {
  source $(printf '%q' "$work/body.sh")
}
post
exit \$?
EOF
	# The workflow steps run under set +e. A guard failure is a nonzero
	# status the caller records; it must not abort this proof script.
	set +e
	bash "$runsh" >"$outlog" 2>&1
	status=$?
	rm -rf "$stubdir" "$work"
	return "$status"
}

check_real_body() {
	local label="$1" file="$2" start="$3" end="$4"
	local body stublog outlog status goes text
	body=$(extract_guard_body "$file" "$start" "$end")
	if [[ -z "$body" || "$body" != *"ci_log_has_guard_failure"* ]]; then
		echo "FAIL $label: could not extract the checked-in guard body"
		rc=1
		return
	fi
	if [[ "$body" != *"exit 1"* && "$body" != *"return 1"* ]]; then
		echo "FAIL $label: extracted body has no failing exit or return"
		rc=1
		return
	fi
	stublog=$(mktemp)
	outlog=$(mktemp)
	set +e
	run_extracted "$body" "$guard_log" "$stublog" "$outlog"
	status=$?
	set -e
	goes=$(wc -l <"$stublog" | tr -d ' ')
	text=$(cat "$outlog")
	if [[ "$status" -ne 0 && "$goes" -eq 0 \
		&& "$text" == *"REAL FAILURE (guard failure detected — never excused)"* \
		&& "$text" != *"FLAKE (passed isolated)"* ]]; then
		echo "PASS $label: guard log fails without a retry"
	else
		echo "FAIL $label: guard log status=$status go_calls=$goes"
		printf '%s\n' "$text" | tail -n 20
		rc=1
	fi
	set +e
	run_extracted "$body" "$hang_log" "$stublog" "$outlog"
	status=$?
	set -e
	goes=$(wc -l <"$stublog" | tr -d ' ')
	text=$(cat "$outlog")
	if [[ "$status" -eq 0 && "$goes" -eq 1 && "$text" == *"FLAKE (passed isolated)"* ]]; then
		echo "PASS $label: hang log is retried once and excused"
	else
		echo "FAIL $label: hang log status=$status go_calls=$goes"
		printf '%s\n' "$text" | tail -n 20
		rc=1
	fi
	rm -f "$stublog" "$outlog"
}

check_real_body "pr.yml gateway step" \
	"$root/.github/workflows/pr.yml" \
	'Run pkg/gateway tests (dedicated shard' \
	'Run test-tagged approver escape-hatch test'
check_real_body "pr.yml gateway race step" \
	"$root/.github/workflows/pr.yml" \
	'Run go test -race ./pkg/gateway/...' \
	'test-race:'
check_real_body "run_gorace" \
	"$root/deploy/ci-worker/runci.sh" \
	'run_gorace()' \
	'run_gotest()'
check_real_body "run_gotest" \
	"$root/deploy/ci-worker/runci.sh" \
	'run_gotest()' \
	'run_records_no_sqlite()'


exit "$rc"

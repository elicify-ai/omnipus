#!/usr/bin/env bash
# Proof for scripts/ci-retry-once.sh (#1786). Stub commands only; no network.
# Run: bash scripts/ci-retry-once.test.sh   (exit 0 = all cases passed)

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RETRY="$SCRIPT_DIR/ci-retry-once.sh"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/ci-retry-once-test.XXXXXX")" || exit 1
trap 'rm -rf "$WORK"' EXIT
export CI_RETRY_PAUSE=0

fail=0
check() { # name want got
  if [ "$2" != "$3" ]; then
    echo "FAIL $1: want '$2', got '$3'" >&2
    fail=1
  else
    echo "ok   $1"
  fi
}

# stub <name> <fail-count> <exit-code>: fails <fail-count> times with <exit-code>,
# then succeeds. Appends one line per call to "$WORK/<name>.calls".
make_stub() {
  cat > "$WORK/$1" <<EOF
#!/usr/bin/env bash
echo call >> "$WORK/$1.calls"
n=\$(wc -l < "$WORK/$1.calls")
if [ "\$n" -le $2 ]; then exit $3; fi
exit 0
EOF
  chmod +x "$WORK/$1"
}
calls() { if [ -f "$WORK/$1.calls" ]; then wc -l < "$WORK/$1.calls" | tr -d ' '; else echo 0; fi; }

# 1. succeeds first time: one call, exit 0, no retry message.
make_stub ok 0 0
out=$("$RETRY" "$WORK/ok" 2>&1); code=$?
check "first-try success exit" 0 "$code"
check "first-try success calls" 1 "$(calls ok)"
case "$out" in *"::warning::"*|*"::error::"*) check "first-try success prints nothing" quiet noisy ;; *) check "first-try success prints nothing" quiet quiet ;; esac

# 2. fails once, then succeeds: two calls, exit 0, one warning, no error.
make_stub flaky 1 7
out=$("$RETRY" "$WORK/flaky" 2>&1); code=$?
check "fail-then-pass exit" 0 "$code"
check "fail-then-pass calls" 2 "$(calls flaky)"
case "$out" in *"::warning::"*"attempt 1 of 2"*) check "fail-then-pass warns" yes yes ;; *) check "fail-then-pass warns" yes no ;; esac
case "$out" in *"::error::"*) check "fail-then-pass no error" none error ;; *) check "fail-then-pass no error" none none ;; esac

# 3. always fails: exactly two calls (never a third), exit non-zero equal to the
#    command's own code, ::error:: printed.
make_stub bad 99 42
out=$("$RETRY" "$WORK/bad" 2>&1); code=$?
check "always-fail exit is the command's code" 42 "$code"
check "always-fail calls (exactly 2)" 2 "$(calls bad)"
case "$out" in *"::error::"*"attempt 2 of 2"*"re-run the job"*) check "always-fail prints error" yes yes ;; *) check "always-fail prints error" yes no ;; esac

# 4. arguments are passed through unchanged (including spaces).
cat > "$WORK/args" <<EOF
#!/usr/bin/env bash
printf '%s|' "\$@" > "$WORK/args.out"
EOF
chmod +x "$WORK/args"
"$RETRY" "$WORK/args" "a b" "--with-deps" "c" >/dev/null 2>&1
check "args passed through" "a b|--with-deps|c|" "$(cat "$WORK/args.out")"

# 5. no command: exit 2.
"$RETRY" >/dev/null 2>&1; code=$?
check "no command exits 2" 2 "$code"

if [ "$fail" -ne 0 ]; then
  echo "ci-retry-once.test: FAILED" >&2
  exit 1
fi
echo "ci-retry-once.test: OK"

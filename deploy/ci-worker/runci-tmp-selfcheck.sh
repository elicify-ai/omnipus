#!/usr/bin/env bash
# Exercise the real TMPDIR probe and dispatcher parsing without Fly or a gate body.
# Run as a non-root user so the 0555/0666 controls measure real permission denial.
# Run this script with bash; no Fly credentials are needed.
# RUNCI_UNDER_TEST may name a baseline/mutated runner to prove this check detects it.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNCI="${RUNCI_UNDER_TEST:-$HERE/runci.sh}"
ME="$(id -un)"; UID_NOW="$(id -u)"
[ "$UID_NOW" != 0 ] || { printf 'selfcheck: run as a non-root user for permission controls\n' >&2; exit 2; }
REAL_MKTEMP="$(command -v mktemp)"; REAL_RM="$(command -v rm)"
TMP="$("$REAL_MKTEMP" -d "${TMPDIR:-/tmp}/runci-tmp-selfcheck.XXXXXX")" || exit 2
trap 'chmod -R u+rwx "$TMP"; "$REAL_RM" -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" || exit 2

# Extract only the probe and its optional helper, never either entrypoint's prologue.
# Keeping the helper optional also lets the same assertions reproduce the old bug.
awk '
  /^fail_tmpdir\(\) \{/ { helper=1 }
  helper { print }
  helper && /^}/ { helper=0 }
  /^# Probe AFTER the re-exec/ { probe=1 }
  probe && /^# Go/ { probe=0 }
  probe { print }
' "$RUNCI" > "$TMP/probe.sh" || exit 2
printf ': > "%s"\n' "\$GATE_SENTINEL" >> "$TMP/probe.sh"
eval "$(awk '/^(dispatch_tier|verdict_of_log)\(\) / { copy=1 } copy { print } copy && /^}/ { copy=0 }' "$HERE/ci-cluster.sh")"

# Fault injection is at the process edge only. Successful calls still touch real files.
cat > "$TMP/bin/mktemp" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$@" >> "$CALLS.mktemp"
[ "$#" -eq 1 ] && [ "$1" = "$EXPECTED_TMPDIR/runci-write-check.XXXXXX" ] || exit 1
[ "$CASE" != create-fails ] || { printf 'forced mktemp failure\n' >&2; exit 1; }
created="$("$REAL_MKTEMP" "$@")" || exit 1
printf '%s\n' "$created" > "$CALLS.created"
printf '%s\n' "$created"
STUB
cat > "$TMP/bin/rm" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$@" >> "$CALLS.rm"
[ "$#" -eq 2 ] && [ "$1" = -f ] || exit 1
case "$2" in "$EXPECTED_TMPDIR"/runci-write-check.*) ;; *) exit 1 ;; esac
[ "$CASE" != remove-fails ] || { printf 'forced rm failure\n' >&2; exit 1; }
exec "$REAL_RM" "$@"
STUB
chmod +x "$TMP/bin/mktemp" "$TMP/bin/rm" || exit 2

FAILS=0
fail() { printf 'selfcheck FAIL: %s\n' "$*" >&2; FAILS=$((FAILS + 1)); }
for name in healthy write-denied search-denied create-fails remove-fails; do
  before="$FAILS"
  dir="$TMP/$name"; calls="$dir/calls"; out="$dir/output"
  mkdir -p "$dir/tmp" || exit 2
  chmod 1777 "$dir/tmp" || exit 2
  case "$name" in write-denied) chmod 0555 "$dir/tmp" ;; search-denied) chmod 0666 "$dir/tmp" ;; esac
  PATH="$TMP/bin:$PATH" TMPDIR="$dir/tmp" EXPECTED_TMPDIR="$dir/tmp" CALLS="$calls" CASE="$name" \
    REAL_MKTEMP="$REAL_MKTEMP" REAL_RM="$REAL_RM" GATE=gofmt GATE_SENTINEL="$dir/gate-ran" \
    bash -u "$TMP/probe.sh" > "$out" 2>&1
  code=$?
  printf 'selfcheck case %s: native exit %s\n%s\n' "$name" "$code" "$(< "$out")"

  if [ "$name" = healthy ]; then
    [ "$code" -eq 0 ] || fail "$name: expected exit 0, got $code"
    [ -f "$dir/gate-ran" ] || fail "$name: continuation not reached"
    grep -Fxq "temp check: TMPDIR=$dir/tmp writable/searchable for gate gofmt as $ME (uid $UID_NOW)" "$out" \
      || fail "$name: missing success diagnostic"
    grep -q 'GATE FAILURE' "$out" && fail "$name: unexpected failure marker"
  else
    [ "$code" -eq 2 ] || fail "$name: expected exit 2, got $code"
    [ ! -e "$dir/gate-ran" ] || fail "$name: gate body ran"
    grep -Fxq 'GATE FAILURE(S) — TMPDIR preflight failed' "$out" || fail "$name: missing dispatcher marker"
    case "$name" in
      write-denied|search-denied) detail="TMPDIR=$dir/tmp is not writable/searchable for gate gofmt as $ME (uid $UID_NOW); no /tmp fallback" ;;
      create-fails) detail="cannot create TMPDIR probe in $dir/tmp for gate gofmt as $ME (uid $UID_NOW); no /tmp fallback" ;;
      remove-fails) detail="cannot remove TMPDIR probe $(< "$calls.created") as $ME" ;;
    esac
    grep -Fxq "GATE FAILURE: $detail" "$out" || fail "$name: wrong detailed failure message"
    verdict="$(verdict_of_log "$out")"
    [ "$verdict" = FAIL:gate ] || fail "$name: dispatcher verdict $verdict, expected FAIL:gate"

    # The real dispatch_tier must continue after this terminal gate failure, not
    # misdiagnose an SSH drop. Its only substituted boundary is the Fly command.
    (
      NEGATIVE_LOG="$out"; FLY_CALLS="$dir/fly-calls"
      fly() {
        printf '%s\n' "$*" >> "$FLY_CALLS"
        if [[ "${!#}" == *' gofmt' ]]; then printf '%s\n' "$(< "$NEGATIVE_LOG")"; return 2; fi
        printf 'ALL GATES GREEN\n'
      }
      fly selfcheck-instrument > "$dir/fake-fly-control"
      : > "$FLY_CALLS"
      export APP=selfcheck REF=selfcheck
      LOG_DIR="$dir/dispatch"
      mkdir -p "$LOG_DIR" || exit 2
      dispatch_tier selfcheck not-a-real-machine 'gofmt cli-verb-guard'
    ) > "$dir/dispatch-output" 2>&1
    tier_code=$?
    [ "$tier_code" -eq 0 ] || fail "$name: dispatch_tier aborted (exit $tier_code)"
    [ "$(wc -l < "$dir/fly-calls")" -eq 2 ] || fail "$name: second gate not dispatched"
    grep -q ' cli-verb-guard$' "$dir/fly-calls" || fail "$name: remaining gate missing"
    grep -q 'session dropped' "$dir/dispatch-output" && fail "$name: misreported an SSH drop"
    printf 'selfcheck parser %s: verdict=%s tier_exit=%s\n' "$name" "$verdict" "$tier_code"
  fi

  case "$name" in
    write-denied|search-denied)
      [ ! -e "$calls.mktemp" ] && [ ! -e "$calls.rm" ] || fail "$name: filesystem probe ran despite denied access"
      ;;
    *)
      [ "$(< "$calls.mktemp")" = "$dir/tmp/runci-write-check.XXXXXX" ] || fail "$name: retried or attempted a /tmp fallback"
      if [ "$name" = create-fails ]; then
        [ ! -e "$calls.rm" ] && [ ! -e "$calls.created" ] || fail "$name: cleanup ran after failed creation"
      else
        created="$(< "$calls.created")"
        [ "$(< "$calls.rm")" = "$(printf '%s\n' -f "$created")" ] || fail "$name: removal did not target the created probe"
        if [ "$name" = remove-fails ]; then
          [ -f "$created" ] || fail "$name: failed removal did not leave the real probe"
        else
          [ ! -e "$created" ] || fail "$name: successful probe left a file"
        fi
      fi
      ;;
  esac
  [ "$FAILS" -ne "$before" ] || printf 'selfcheck PASS: %s\n' "$name"
done
[ "$FAILS" -eq 0 ] || { printf 'selfcheck: FAIL (%s assertions)\n' "$FAILS" >&2; exit 1; }
printf 'selfcheck: OK (5 cases; four failures exit 2, classify FAIL:gate, and permit the next gate; no fallback)\n'

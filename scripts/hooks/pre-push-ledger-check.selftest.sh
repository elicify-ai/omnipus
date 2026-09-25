#!/usr/bin/env bash
# scripts/hooks/pre-push-ledger-check.selftest.sh
#
# Self-check for pre-push-ledger-check (L10 task: "the hook blocks (a) a
# push under a hold and (b) a landing without the lock, and allows (c)").
# Runs entirely inside a temporary scratch directory: a bare "remote" repo
# and a working clone, both under $(mktemp -d) -- never this worktree, never
# the real coordination ledger. Prints PASS/FAIL per case and exits 1 if any
# case did not behave as specified.
#
# USAGE: scripts/hooks/pre-push-ledger-check.selftest.sh

set -u

HERE="$(cd "$(dirname "$0")" && pwd)"
HOOK_SRC="$HERE/pre-push-ledger-check"

TMP="$(mktemp -d /tmp/omnipus-hook-selftest.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

fail=0
pass_count=0
fail_count=0

note() { echo "[selftest] $*"; }
record() {
  # record <case> <expected_exit_class: block|allow> <actual_exit>
  case_name="$1"; expect="$2"; actual="$3"
  if [ "$expect" = "block" ] && [ "$actual" -ne 0 ]; then
    echo "PASS: $case_name (blocked, exit=$actual)"; pass_count=$((pass_count+1))
  elif [ "$expect" = "allow" ] && [ "$actual" -eq 0 ]; then
    echo "PASS: $case_name (allowed, exit=$actual)"; pass_count=$((pass_count+1))
  else
    echo "FAIL: $case_name (expected $expect, got exit=$actual)"; fail_count=$((fail_count+1)); fail=1
  fi
}

# ---- build the scratch remote + clone --------------------------------------
REMOTE="$TMP/remote.git"
WORK="$TMP/work"
COORD="$TMP/coordination"
mkdir -p "$COORD"

git init --bare -q "$REMOTE"
git init -q "$WORK"
git -C "$WORK" config user.email "selftest@example.invalid"
git -C "$WORK" config user.name "selftest"
git -C "$WORK" remote add origin "$REMOTE"
mkdir -p "$WORK/.git/hooks"
cp "$HOOK_SRC" "$WORK/.git/hooks/pre-push"
chmod +x "$WORK/.git/hooks/pre-push"

echo one > "$WORK/file.txt"
git -C "$WORK" add file.txt
git -C "$WORK" commit -q -m "initial"
git -C "$WORK" branch -M release/integration-scratch

export OMNIPUS_INTEGRATION_BRANCH="release/integration-scratch"
export OMNIPUS_COORDINATION_DIR="$COORD"

# ---- case (a): active hold + valid lock -> BLOCKED (hold checked in isolation) ----
note "case (a): push under an active hold"
cat > "$COORD/HOLDS.md" <<'EOF'
what=release/integration-scratch | held-by=squad-x | why=investigating a red check | since=2026-09-25T00:00:00Z | released-at=
EOF
cat > "$COORD/LANDING-LOCK" <<'EOF'
squad=squad-a branch=release/integration-scratch taken-at=2026-09-25T00:00:00Z
EOF
git -C "$WORK" push origin release/integration-scratch >/tmp/omnipus-hook-selftest-a.log 2>&1
record "hold blocks the push" block "$?"
cat /tmp/omnipus-hook-selftest-a.log | sed 's/^/    /'

# ---- case (b): no hold, no lock -> BLOCKED (lock checked in isolation) ----
note "case (b): push with no landing lock held"
: > "$COORD/HOLDS.md"
rm -f "$COORD/LANDING-LOCK"
git -C "$WORK" push origin release/integration-scratch >/tmp/omnipus-hook-selftest-b.log 2>&1
record "missing lock blocks the push" block "$?"
cat /tmp/omnipus-hook-selftest-b.log | sed 's/^/    /'

# ---- case (c): no hold, valid lock -> ALLOWED ----
note "case (c): push with no hold and a matching landing lock"
: > "$COORD/HOLDS.md"
cat > "$COORD/LANDING-LOCK" <<'EOF'
squad=squad-a branch=release/integration-scratch taken-at=2026-09-25T00:05:00Z
EOF
git -C "$WORK" push origin release/integration-scratch >/tmp/omnipus-hook-selftest-c.log 2>&1
record "hold-free + locked push is allowed" allow "$?"
cat /tmp/omnipus-hook-selftest-c.log | sed 's/^/    /'

# ---- case (d): unset OMNIPUS_INTEGRATION_BRANCH -> WARN, never blocks ----
note "case (d): unset OMNIPUS_INTEGRATION_BRANCH passes through with a warning"
echo two > "$WORK/file.txt"
git -C "$WORK" add file.txt
git -C "$WORK" commit -q -m "second"
( unset OMNIPUS_INTEGRATION_BRANCH; git -C "$WORK" push origin release/integration-scratch ) >/tmp/omnipus-hook-selftest-d.log 2>&1
record "unset integration branch never blocks" allow "$?"
grep -q "WARNING" /tmp/omnipus-hook-selftest-d.log && echo "    (warning line present, as required)" || echo "    MISSING WARNING LINE"

echo
echo "[selftest] $pass_count passed, $fail_count failed"
exit "$fail"

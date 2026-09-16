#!/usr/bin/env bash
# check-agents-md-sync.sh
#
# Guard: every CLAUDE.md in the tree must have a byte-identical AGENTS.md
# sibling in the same directory, and every AGENTS.md must have a CLAUDE.md.
#
# WHY PAIRS AT ALL
#
#   Different coding harnesses read different instruction files: Claude
#   Code loads CLAUDE.md, Codex/OpenClaw-style tools load AGENTS.md. A
#   folder that carries only one gives those harnesses different guidance
#   for the same code; a folder whose two copies have drifted does the
#   same thing more quietly. Either way the repo lies to somebody. The
#   invariant is therefore: same directory, same bytes, always both.
#
# WHY A CI GUARD AND NOT THE OBVIOUS ALTERNATIVES
#
#   - A symlink (AGENTS.md -> CLAUDE.md) is the obvious answer and is
#     wrong here: this repo supports Windows, and a Windows checkout
#     without core.symlinks materialises the link as a plain text file
#     containing the target path. Silent, per-platform, and exactly the
#     class of "documented protection that quietly is not there" this
#     project keeps finding (see pkg/fileutil/flock_windows.go for the
#     precedent). Rejected.
#   - A git hook is not shared (".git/hooks" is untracked, so no clone
#     inherits it) and any harness can commit without passing through
#     it. Rejected.
#   - A CI guard depends on no harness cooperating: it is enforced at
#     the merge boundary whatever wrote the file, whichever machine the
#     checkout lives on. That is the only property that satisfies "no
#     matter which harness writes which". Chosen.
#
# WHAT IS CHECKED
#
#   Every CLAUDE.md and AGENTS.md under the repo root, minus the same
#   generated/vendored trees the sibling guards exempt (scripts/
#   check-file-budget.sh's list, kept in lockstep): node_modules/,
#   vendor/, .git/, dist/, .gitnexus/, pkg/api/generated/,
#   src/lib/api/generated/, pkg/gateway/spa/. A stray CLAUDE.md shipped
#   inside an npm dependency must not fail our build.
#
#   A repo with zero CLAUDE.md/AGENTS.md files passes (vacuously) —
#   sibling lanes land those files progressively, and a guard that went
#   red before they landed would block their merge.
#
# OUTPUT CONTRACT
#
#   Exactly one line per mismatch, naming the directory and the shape:
#     <dir>: missing twin — CLAUDE.md present, AGENTS.md absent
#     <dir>: extra twin   — AGENTS.md present, CLAUDE.md absent
#     <dir>: differing    — CLAUDE.md and AGENTS.md are not byte-identical
#   Then a summary line. Exit 0 when consistent (including zero pairs),
#   1 when any mismatch, 2 on internal error.
#
#   Byte-identity is literal (cmp): any newline/mode/content divergence
#   counts. Fix drift with: make sync-agents-md  (scripts/sync-agents-md.sh
#   decides the source side from git; see its header).
#
# DISCOVERY (scripts/guards.sh conventions, C-19/C-44/C-45/C-92/C-93)
#
#   This file is discovered by scripts/guards.sh's scripts/check-*.sh glob.
#   Its proof-of-failure companion is scripts/check-agents-md-sync.test.sh
#   (a <name>.test.sh companion, subtracted from the guard list and run
#   before this guard). No wiring in .github/workflows/pr.yml,
#   deploy/ci-worker/runci.sh, or the Makefile references this script by
#   name — discovery is how it runs, and C-92 keeps those files at
#   exactly one guards.sh invocation each.
#
# Usage:
#   bash scripts/check-agents-md-sync.sh
#
# Overrides (for the companion test only — never set these in CI):
#   REPO_ROOT   — repo root to scan. Defaults to this script's own
#                 parent directory's parent.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

if [ ! -d "$REPO_ROOT" ]; then
  echo "check-agents-md-sync: ERROR — REPO_ROOT is not a directory: $REPO_ROOT" >&2
  exit 2
fi

WORK="$(mktemp -d /tmp/agents-md-guard.XXXXXX)" || exit 2
trap 'rm -rf "$WORK"' EXIT

# ─── Discovery ────────────────────────────────────────────────────────────────
# Prune the big vendored/generated trees by name during the walk (descending
# into node_modules would cost seconds and can only ever find exempt files),
# then post-filter the path-specific prefixes, mirroring check-file-budget.sh.

find "$REPO_ROOT" \
  \( -name .git -o -name node_modules -o -name vendor -o -name dist -o -name .gitnexus \) -prune \
  -o -type f \( -name CLAUDE.md -o -name AGENTS.md \) -print \
  > "$WORK/raw.txt" 2>/dev/null || {
    echo "check-agents-md-sync: ERROR — find failed under $REPO_ROOT" >&2
    exit 2
  }

is_exempt() {
  # $1: path relative to REPO_ROOT (no leading slash).
  case "$1" in
    pkg/api/generated/*|src/lib/api/generated/*|pkg/gateway/spa/*|node_modules/*|vendor/*|dist/*|.gitnexus/*|.git/*)
      return 0 ;;
    *)
      return 1 ;;
  esac
}

: > "$WORK/dirs.txt"
while IFS= read -r f || [ -n "$f" ]; do
  [ -n "$f" ] || continue
  rel="${f#"$REPO_ROOT"/}"
  is_exempt "$rel" && continue
  printf '%s\n' "$(dirname "$rel")" >> "$WORK/dirs.txt"
done < "$WORK/raw.txt"

sort -u "$WORK/dirs.txt" -o "$WORK/dirs.txt"

# ─── Pair check ───────────────────────────────────────────────────────────────

MISMATCHES=0
PAIRS=0
: > "$WORK/findings.txt"

while IFS= read -r dir || [ -n "$dir" ]; do
  [ -n "$dir" ] || continue
  has_c=0; has_a=0
  [ -f "$REPO_ROOT/$dir/CLAUDE.md" ] && has_c=1
  [ -f "$REPO_ROOT/$dir/AGENTS.md" ] && has_a=1

  if [ "$has_c" -eq 1 ] && [ "$has_a" -eq 1 ]; then
    PAIRS=$((PAIRS + 1))
    if ! cmp -s "$REPO_ROOT/$dir/CLAUDE.md" "$REPO_ROOT/$dir/AGENTS.md"; then
      printf '%s\n' "agents-md-sync: $dir: differing — CLAUDE.md and AGENTS.md are not byte-identical" >> "$WORK/findings.txt"
      MISMATCHES=$((MISMATCHES + 1))
    fi
  elif [ "$has_c" -eq 1 ]; then
    printf '%s\n' "agents-md-sync: $dir: missing twin — CLAUDE.md present, AGENTS.md absent" >> "$WORK/findings.txt"
    MISMATCHES=$((MISMATCHES + 1))
  elif [ "$has_a" -eq 1 ]; then
    printf '%s\n' "agents-md-sync: $dir: extra twin — AGENTS.md present, CLAUDE.md absent" >> "$WORK/findings.txt"
    MISMATCHES=$((MISMATCHES + 1))
  fi
done < "$WORK/dirs.txt"

# ─── Output ───────────────────────────────────────────────────────────────────

if [ "$MISMATCHES" -gt 0 ]; then
  cat "$WORK/findings.txt"
  echo "check-agents-md-sync: $MISMATCHES mismatch(es) across $PAIRS checked pair(s) — run: make sync-agents-md"
  exit 1
fi

echo "check-agents-md-sync: OK — $PAIRS pair(s) in sync, 0 mismatches"
exit 0

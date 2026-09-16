#!/usr/bin/env bash
# sync-agents-md.sh
#
# Fixer for the CLAUDE.md / AGENTS.md twin invariant guarded by
# scripts/check-agents-md-sync.sh: every folder carrying one of the two
# must carry both, byte-identical, so a Claude harness reading CLAUDE.md
# and a Codex/OpenClaw-style harness reading AGENTS.md see exactly the
# same instructions. See that guard's header for why this is a CI guard
# and not a symlink (silently a text file on Windows checkouts without
# core.symlinks) or a git hook (untracked, bypassable).
#
# WHICH SIDE IS THE SOURCE — decided per directory, from git
#
#   Do not assume CLAUDE.md always wins: a Codex harness legitimately
#   edits AGENTS.md, and either harness can be the one that wrote last.
#   Git is the only witness of which file a harness actually touched,
#   so for each directory where the twins differ:
#
#     only CLAUDE.md differs from HEAD  -> copy CLAUDE.md over AGENTS.md
#     only AGENTS.md differs from HEAD  -> copy AGENTS.md over CLAUDE.md
#     both changed and they differ      -> CONFLICT: fail loudly naming
#                                          the directory; never silently
#                                          pick a side
#     neither changed but they differ   -> the committed state itself is
#                                          inconsistent; report it, pick
#                                          nothing
#     one twin absent entirely          -> create it from the other
#
#   "Differs from HEAD" includes untracked (newly created) files: a
#   harness that just created the file changed it.
#
#   Resolving the two unresolvable shapes by hand:
#     CONFLICT      — open both, decide the true content, write it to
#                     ONE side only, re-run: that side is now the
#                     git-changed side and wins.
#     PRE-EXISTING  — delete the side you reject (the survivor recreates
#                     its twin), or edit the side you keep (any change
#                     makes it the source), then re-run.
#
# EXIT CODES
#
#   0 — every scanned directory is now consistent (or already was).
#   1 — something was left inconsistent: a CONFLICT, a PRE-EXISTING
#       drift, or (in --dry-run) pending actions not actually applied.
#   2 — usage or internal error (not a git repo, copy failed, guard
#       missing). The final verdict is always re-derived by running
#       scripts/check-agents-md-sync.sh over the result — sync never
#       trusts its own bookkeeping for the exit code.
#
# Usage:
#   bash scripts/sync-agents-md.sh            # fix what is fixable
#   bash scripts/sync-agents-md.sh --dry-run  # print actions, change nothing;
#                                             # exits 1 if actions are pending
#
# Overrides (for the companion test only — never set these in CI):
#   REPO_ROOT   — repo root to scan. Defaults to this script's own
#                 parent directory's parent.

set -u

DRY_RUN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1 ;;
    *)
      echo "sync-agents-md: ERROR — unknown argument: $1 (only --dry-run is supported)" >&2
      exit 2
      ;;
  esac
  shift
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
GUARD="$SCRIPT_DIR/check-agents-md-sync.sh"

if [ ! -d "$REPO_ROOT" ]; then
  echo "sync-agents-md: ERROR — REPO_ROOT is not a directory: $REPO_ROOT" >&2
  exit 2
fi
if [ ! -f "$GUARD" ]; then
  echo "sync-agents-md: ERROR — guard not found next to this script: $GUARD" >&2
  exit 2
fi
if ! git -C "$REPO_ROOT" rev-parse --git-dir >/dev/null 2>&1; then
  echo "sync-agents-md: ERROR — $REPO_ROOT is not a git repository; source-side detection needs HEAD" >&2
  exit 2
fi

# ─── Git helpers ──────────────────────────────────────────────────────────────

has_head() {
  git -C "$REPO_ROOT" rev-parse --verify HEAD >/dev/null 2>&1
}

# changed_vs_head <path-relative-to-REPO_ROOT>
# Exit 0 when the file differs from HEAD — modified, staged, or untracked
# (a file HEAD does not know yet was just created by some harness). With
# no commits at all, everything counts as changed.
changed_vs_head() {
  has_head || return 0
  git -C "$REPO_ROOT" cat-file -e "HEAD:$1" 2>/dev/null || return 0
  ! git -C "$REPO_ROOT" diff --quiet HEAD -- "$1"
}

# ─── Discovery (lockstep with check-agents-md-sync.sh — same exemptions) ──────

WORK="$(mktemp -d /tmp/agents-md-sync.XXXXXX)" || exit 2
trap 'rm -rf "$WORK"' EXIT

find "$REPO_ROOT" \
  \( -name .git -o -name node_modules -o -name vendor -o -name dist -o -name .gitnexus \) -prune \
  -o -type f \( -name CLAUDE.md -o -name AGENTS.md \) -print \
  > "$WORK/raw.txt" 2>/dev/null || {
    echo "sync-agents-md: ERROR — find failed under $REPO_ROOT" >&2
    exit 2
  }

is_exempt() {
  # Keep in lockstep with check-agents-md-sync.sh's is_exempt.
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

# ─── Per-directory resolution ─────────────────────────────────────────────────

FAIL=0
ACTIONS=0

do_copy() {
  # do_copy <imperative-label> <src> <dst> <dir>
  local label="$1" src="$2" dst="$3" dir="$4"
  ACTIONS=$((ACTIONS + 1))
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "agents-md-sync: $dir: would $label (dry-run)"
    return 0
  fi
  if ! cp "$src" "$dst"; then
    echo "sync-agents-md: ERROR — copy failed: $src -> $dst" >&2
    FAIL=1
    return 0
  fi
  if ! cmp -s "$src" "$dst"; then
    echo "sync-agents-md: ERROR — copy verified unequal: $src -> $dst" >&2
    FAIL=1
    return 0
  fi
  echo "agents-md-sync: $dir: $label"
}

while IFS= read -r dir || [ -n "$dir" ]; do
  [ -n "$dir" ] || continue
  c="$REPO_ROOT/$dir/CLAUDE.md"
  a="$REPO_ROOT/$dir/AGENTS.md"
  has_c=0; has_a=0
  [ -f "$c" ] && has_c=1
  [ -f "$a" ] && has_a=1

  if [ "$has_c" -eq 1 ] && [ "$has_a" -eq 1 ]; then
    cmp -s "$c" "$a" && continue
    c_changed=0; a_changed=0
    changed_vs_head "$dir/CLAUDE.md" && c_changed=1
    changed_vs_head "$dir/AGENTS.md" && a_changed=1
    if [ "$c_changed" -eq 1 ] && [ "$a_changed" -eq 0 ]; then
      do_copy "copy CLAUDE.md over AGENTS.md (CLAUDE.md is the git-changed side)" "$c" "$a" "$dir"
    elif [ "$c_changed" -eq 0 ] && [ "$a_changed" -eq 1 ]; then
      do_copy "copy AGENTS.md over CLAUDE.md (AGENTS.md is the git-changed side)" "$a" "$c" "$dir"
    elif [ "$c_changed" -eq 1 ] && [ "$a_changed" -eq 1 ]; then
      echo "sync-agents-md: CONFLICT $dir: both CLAUDE.md and AGENTS.md changed vs HEAD and they differ — no side copied; write the intended content to ONE side and re-run" >&2
      FAIL=1
    else
      echo "sync-agents-md: PRE-EXISTING $dir: CLAUDE.md and AGENTS.md differ but neither changed vs HEAD — committed state is inconsistent, no side picked; delete the side you reject or edit the side you keep, then re-run" >&2
      FAIL=1
    fi
  elif [ "$has_c" -eq 1 ]; then
    do_copy "create AGENTS.md from CLAUDE.md (twin was absent)" "$c" "$a" "$dir"
  elif [ "$has_a" -eq 1 ]; then
    do_copy "create CLAUDE.md from AGENTS.md (twin was absent)" "$a" "$c" "$dir"
  fi
done < "$WORK/dirs.txt"

# ─── Verdict ──────────────────────────────────────────────────────────────────

if [ "$DRY_RUN" -eq 1 ]; then
  if [ "$FAIL" -ne 0 ]; then
    echo "sync-agents-md: dry-run — unresolved conflict/pre-existing drift (see above), nothing changed"
    exit 1
  fi
  if [ "$ACTIONS" -gt 0 ]; then
    echo "sync-agents-md: dry-run — $ACTIONS action(s) pending, nothing changed"
    exit 1
  fi
  echo "sync-agents-md: dry-run — nothing to do"
  exit 0
fi

# Authoritative final verdict: re-derive consistency with the guard rather
# than trusting this script's own bookkeeping.
REPO_ROOT="$REPO_ROOT" bash "$GUARD"
gexit=$?
if [ "$gexit" -eq 0 ]; then
  echo "sync-agents-md: tree consistent ($ACTIONS action(s) applied)"
  exit 0
fi
echo "sync-agents-md: tree still inconsistent after sync (see guard output above)" >&2
exit 1

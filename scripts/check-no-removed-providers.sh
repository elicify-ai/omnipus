#!/usr/bin/env bash
# check-no-removed-providers.sh
#
# Regression guard for ADR-068 §2.4: the `antigravity` OAuth provider and the
# `claude-cli` provider are DELETED — greenfield, no alias, no shim, no
# migration, no error string naming them. This script fails the build if
# either leaves (or regains) a trace in the source tree.
#
# ADR-068 §8b (2026-08-23 amendment): the OpenAI device-code/OAuth login flow
# this header used to list as deleted is RESTORED (T068-32) — the shared
# Codex login is the form OpenAI itself endorses and every peer agent ships.
# Its symbols are no longer forbidden here. The Anthropic/Claude store-OAuth
# ladder (createClaudeAuthProvider, createClaudeTokenSource) remains deleted
# and is still checked below.
#
# WHY A SCRIPT AND NOT A GO TEST
#
# A Go test under pkg/ would itself have to spell the names it forbids, which
# is a trace (ADR-068 §2.4 "Exit proof"). A script under scripts/ with its
# allow-list as a DATA file sits outside the scanned roots, so the tree it
# guards can be genuinely clean. The same reasoning as
# scripts/check-no-jpeg-screencast.sh applies for merges: long-lived branches
# cut before the deletion still contain every file; git re-adds them as an
# ordinary, conflict-free addition. RESOLVE BY KEEPING THE DELETION.
#
# WHAT IS CHECKED (ADR-068 spec FR-001, FR-002, FR-002a; SC-001)
#
#   * the provider id `antigravity`   — case-insensitive, content AND file name
#   * the provider id `claude-cli`    — the id, NOT the word "claude"
#                                       (spec resolution #11)
#   * the deleted Anthropic/Claude store-OAuth ladder symbols
#     (createClaudeAuthProvider, createClaudeTokenSource) — D13 §2.3 item 2
#
# across `pkg cmd src contracts config docs`, in every file type (generated
# code, YAML contracts, JSON examples and prose included — "no trace" is a
# property of the SOURCE, so comments count). The ONLY exemptions are the
# historical decision records enumerated in scripts/no-removed-providers.allow.
#
# "No trace" is a SOURCE property, not a runtime one (spec CRIT-003): the
# generic unknown-provider path echoing a user-supplied id back is not a trace
# and is not this script's concern.
#
# The forbidden strings below are assembled from fragments so that THIS file
# never spells them either — if the scanned roots are ever widened to include
# scripts/, the guard must not trip on itself.
#
# Usage:  bash scripts/check-no-removed-providers.sh
#         REPO_ROOT=<dir> bash scripts/check-no-removed-providers.sh   (self-check)
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
ALLOW_FILE="${ALLOW_FILE:-$REPO_ROOT/scripts/no-removed-providers.allow}"
NAME="check-no-removed-providers"

cd "$REPO_ROOT" || { echo "$NAME: cannot cd to $REPO_ROOT" >&2; exit 2; }

ROOTS=(pkg cmd src contracts config docs)
for d in "${ROOTS[@]}"; do
  if [ ! -d "$d" ]; then
    echo "$NAME: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

if [ ! -f "$ALLOW_FILE" ]; then
  echo "$NAME: allow-list not found at $ALLOW_FILE" >&2
  exit 2
fi

# --- allow-list: one anchored ERE per line, joined into a single alternation --
ALLOW_RE="$(grep -vE '^[[:space:]]*(#|$)' "$ALLOW_FILE" | sed 's/[[:space:]]*$//' | paste -sd '|' -)"
if [ -z "$ALLOW_RE" ]; then
  echo "$NAME: allow-list $ALLOW_FILE has no entries — the file must at least list itself" >&2
  exit 2
fi
ALLOW_RE="^($ALLOW_RE)\$"

# --- forbidden strings (fragments, see header) --------------------------------
ID_ANTI="anti""gravity"                         # matched case-insensitively
ID_CCLI="claude""-cli"                          # the id, not the word
# ADR-068 §8b (2026-08-23 amendment): the OpenAI device-code/OAuth symbols
# (RequestDeviceCode, PollDeviceCodeOnce, OpenAIOAuthConfig,
# createCodexTokenSource, createCodexAuthProvider) are RESTORED (T068-32) —
# the shared Codex login is the form OpenAI itself endorses. They are
# deliberately dropped from this list. The Anthropic/Claude store-OAuth
# ladder stays deleted (D13 §2.3 item 2 unchanged) — those two names remain
# forbidden.
SYMS="createClaude""AuthProvider|createClaude""TokenSource"

is_allowed() { printf '%s\n' "$1" | grep -qE "$ALLOW_RE"; }

OFFENDERS=""
add_offender() { OFFENDERS="${OFFENDERS}$1"$'\n'; }

# Content hits. -I skips binaries; -l gives one path per file; we then print the
# first few matching lines for each non-allow-listed file so the report is
# actionable. Grep returning 1 (no match) is fine; anything else is a real error.
scan_content() { # scan_content <label> <grep-flags> <ere>
  local label="$1" flags="$2" ere="$3" files f rc
  # flags is an intentional, possibly empty, space-separated grep flag list.
  # shellcheck disable=SC2086
  files="$(grep -rIl $flags -E -e "$ere" -- "${ROOTS[@]}" 2>/dev/null)"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    echo "$NAME: grep failed (exit $rc) while scanning for $label" >&2
    exit 2
  fi
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    is_allowed "$f" && continue
    add_offender "[$label] $f"
    # shellcheck disable=SC2086
    add_offender "$(grep -nI $flags -E -e "$ere" -- "$f" 2>/dev/null | head -n 3 | sed 's/^/        /')"
  done <<< "$files"
}

scan_content "$ID_ANTI" "-i" "$ID_ANTI"
scan_content "$ID_CCLI" ""   "$ID_CCLI"
scan_content "deleted OAuth symbol" "" "$SYMS"

# File-name hits: a file CALLED after the provider is a trace even if empty
# (docs/<ID>_USAGE.md, pkg/providers/<id>_provider.go and friends).
while IFS= read -r f; do
  [ -z "$f" ] && continue
  f="${f#./}"
  is_allowed "$f" && continue
  add_offender "[file name] $f"
done < <(find "${ROOTS[@]}" -type f \( -iname "*${ID_ANTI}*" -o -iname "*${ID_CCLI}*" -o -iname "*claude_cli*" \) 2>/dev/null)

OFFENDERS="$(printf '%s' "$OFFENDERS" | grep -v '^[[:space:]]*$' || true)"

if [ -n "$OFFENDERS" ]; then
  echo "$NAME: FOUND traces of providers deleted under ADR-068 §2.4:" >&2
  echo "" >&2
  printf '%s\n' "$OFFENDERS" >&2
  echo "" >&2
  echo "These providers and login flows were deleted deliberately (ADR-068 §2.4," >&2
  echo "spec FR-001/FR-002/FR-002a): no alias, no shim, no migration, no error" >&2
  echo "string, no doc mention outside the historical decision records listed in" >&2
  echo "scripts/no-removed-providers.allow." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "predates the removal and git re-added the code as an ordinary addition." >&2
  echo "RESOLVE BY KEEPING THE DELETION — do not 'fix the conflict' by restoring it." >&2
  echo "" >&2
  echo "If a NEW historical record legitimately has to name them, add its path to" >&2
  echo "the allow-list in the same commit and say why. Never add an allow-list" >&2
  echo "under pkg/." >&2
  exit 1
fi

echo "$NAME: OK (no trace of the providers deleted under ADR-068 §2.4)"
exit 0

#!/usr/bin/env bash
# new-adr-id.sh
#
# Mints the ID stem for a NEW ADR under the founder-decreed naming scheme
# (2026-09-27): every new ADR lives at
#
#     docs/internal/architecture/ADR-<YYYYMMDD>-<slug>.md
#
# with a UTC 8-digit date, NO numeric ID and NO random suffix. The slug
# alone carries uniqueness. Existing ADR-001..ADR-093 keep their numbers
# forever; the in-flight ADR-094/095/096 trio is grandfathered (enforced
# by scripts/check-adr-id-scheme.sh). This script renames nothing — it
# only mints the stem for the file you are about to create.
#
# Usage:
#   scripts/new-adr-id.sh "<title text>"
#
# On success prints ONLY the ID stem to stdout — e.g.
#
#   ADR-20260927-my-feature
#
# (no path, no .md). Everything else — usage help, warnings, errors —
# goes to stderr, so `ADR_ID="$(scripts/new-adr-id.sh "…")"` is always
# clean. Exit codes: 0 success · 1 collision or degenerate slug · 2
# usage error.
#
# ─── Slug derivation from the title (in order) ──────────────────────────────
#   1. lowercase (LC_ALL=C tr — byte-safe, bash-3.2-compatible: no ${var,,})
#   2. every character outside [a-z0-9] becomes a hyphen. LC_ALL=C makes
#      sed byte-oriented, so a multi-byte UTF-8 letter (é, ü, …) is
#      dropped to ASCII — the "strip to ASCII" reading of the founder
#      rule — and a whole accented word still reads through its ASCII
#      letters. Deterministic on macOS (BSD sed) and Linux (GNU sed):
#      byte classes, no locale-dependent transliteration tables.
#   3. runs of hyphens collapse to one; leading/trailing hyphens trimmed.
#   4. hard-truncate to 80 characters (the longest slug in the tree today
#      is 59 — ADR-090's …opus-dispositions; 80 leaves room for a future
#      hand-appended -review / -review-roundN sibling).
#   5. re-trim a trailing hyphen the truncation exposed. This step is NOT
#      in the founder's list order (trim, then truncate) but is forced by
#      it: step 3 ran before truncation, and a slug left ending in "-" by
#      step 4 would fail the guard's shape regex
#      (ADR-[0-9]{8}-[a-z0-9]+(-[a-z0-9]+)*\.md) — the generator must
#      never mint an ID its own guard rejects.
#
# ─── Collision rule (the scheme's entire uniqueness mechanism) ──────────────
# The derived slug is compared, as a full string, against the slug of
# EVERY existing ADR file this script can see:
#   (a) docs/internal/architecture/ADR-*.md in the working tree, and
#   (b) docs/internal/architecture/ in origin/release/v0.1.1 (git
#       ls-tree). That ref is resolved LOCALLY ONLY — this script never
#       touches the network, so it can never hang on one. If the ref does
#       not resolve (not fetched, no origin remote), or ls-tree itself
#       fails (e.g. a missing/corrupt tree object), a WARNING goes to
#       stderr and only the working tree is checked.
# Slug extraction is IDENTICAL to the guard's (strip the ADR- prefix and
# the leading ID token — a 3-digit legacy number or an 8-digit date — and
# keep the remainder), so the two scripts can never disagree about what a
# slug is; the function is textually the same in both files. On a
# collision the exact existing file is named on stderr and the exit is
# nonzero: pick a more specific title. No ID will ever disambiguate two
# same-slug ADRs, so none is handed out.
#
# ─── Scope note (honest boundary of the check) ─────────────────────────────
# (a)+(b) are what this script can see. An ADR that exists only on an
# in-flight branch (ADR-095 on feat/thinking-reasoning as of 2026-09-27,
# for example) is NOT visible from here until it lands on the release
# ref; scripts/check-adr-id-scheme.sh re-checks duplicates tree-wide at
# merge time, which is the backstop.
#
# Overrides (for tests — never set these in CI):
#   REPO_ROOT — repo root whose docs/internal/architecture/ is scanned.
#               Defaults to this script's parent directory's parent.

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
ADR_DIR="$REPO_ROOT/docs/internal/architecture"
ADR_REF="origin/release/v0.1.1"   # pinned by the founder decision as the collision baseline
MAX_SLUG_LEN=80

usage() {
  echo "usage: scripts/new-adr-id.sh \"<title text>\"" >&2
  echo "  prints ADR-<YYYYMMDD>-<slug> on stdout (UTC date; uniqueness is the slug alone)" >&2
}

if [ "$#" -ne 1 ]; then
  usage
  exit 2
fi

TITLE="$1"

# ─── Slug derivation ────────────────────────────────────────────────────────

DATE_STEM="$(date -u +%Y%m%d)"

SLUG="$(printf '%s' "$TITLE" \
  | LC_ALL=C tr '[:upper:]' '[:lower:]' \
  | LC_ALL=C sed -e 's/[^a-z0-9]/-/g' \
                 -e 's/-\{2,\}/-/g' \
                 -e 's/^-*//' \
                 -e 's/-*$//')"
SLUG="${SLUG:0:$MAX_SLUG_LEN}"
SLUG="${SLUG%-}"   # step 5: truncation may expose a trailing hyphen; see header

if [ -z "$SLUG" ]; then
  echo "new-adr-id: ERROR — title yields an empty slug; use a title containing ASCII letters or digits" >&2
  exit 1
fi

# ─── Slug extraction — IDENTICAL function text to check-adr-id-scheme.sh ───

extract_adr_slug() {
  # "$1" = an ADR basename (e.g. ADR-090-…-dispositions.md). Prints the
  # slug: strip the ADR- prefix, the .md suffix, then the leading ID token
  # (an 8-digit date, or a 3-digit legacy number), leaving the remainder.
  _slug="$1"
  _slug="${_slug#ADR-}"
  _slug="${_slug%.md}"
  case "$_slug" in
    [0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-*) _slug="${_slug#????????-}" ;;
    [0-9][0-9][0-9]-*)                          _slug="${_slug#???-}" ;;
  esac
  printf '%s' "$_slug"
}

# ─── Collision comparison ───────────────────────────────────────────────────

collision() {
  # "$1" = how to name the existing file in the error, "$2" = its slug.
  if [ "$2" = "$SLUG" ]; then
    echo "new-adr-id: COLLISION — slug '$SLUG' is already used by $1" >&2
    echo "new-adr-id: pick a more specific title (uniqueness is the slug alone; no ID will disambiguate it)" >&2
    return 1
  fi
  return 0
}

FOUND_COLLISION=0

# (a) working tree
for f in "$ADR_DIR"/ADR-*.md; do
  [ -f "$f" ] || continue
  base="$(basename "$f")"
  if ! collision "$base" "$(extract_adr_slug "$base")"; then
    FOUND_COLLISION=1
    break
  fi
done

# (b) origin/release/v0.1.1 — local ref resolution only, never the network.
# ls-tree's own failure (distinct from "ref does not resolve") is not
# swallowed either: stderr to a temp file, exit status kept, WARNING with
# the git error, then the same working-tree-only fallback as the
# unresolvable-ref branch.
if [ "$FOUND_COLLISION" -eq 0 ]; then
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$ADR_REF" >/dev/null 2>&1; then
    if ! LS_TREE_ERR_FILE="$(mktemp "${TMPDIR:-/tmp}/new-adr-id-lstree.XXXXXX")"; then
      echo "new-adr-id: WARNING — cannot create a temp file; collision check fell back to the working tree only" >&2
    else
      trap 'rm -f "$LS_TREE_ERR_FILE" 2>/dev/null' EXIT
      LS_TREE_RC=0
      LS_TREE_OUT="$(git -C "$REPO_ROOT" ls-tree -r --name-only "$ADR_REF" -- docs/internal/architecture/ 2>"$LS_TREE_ERR_FILE")" || LS_TREE_RC=$?
      if [ "$LS_TREE_RC" -ne 0 ]; then
        echo "new-adr-id: WARNING — ls-tree against $ADR_REF failed; collision check fell back to the working tree only ($(tr '\n' ' ' < "$LS_TREE_ERR_FILE"))" >&2
      else
        while IFS= read -r rel; do
          [ -z "$rel" ] && continue
          base="$(basename "$rel")"
          if ! collision "$base (in $ADR_REF)" "$(extract_adr_slug "$base")"; then
            FOUND_COLLISION=1
            break
          fi
        done <<EOF
$LS_TREE_OUT
EOF
      fi
      rm -f "$LS_TREE_ERR_FILE"
    fi
  else
    echo "new-adr-id: WARNING — $ADR_REF does not resolve locally; collision check fell back to the working tree only" >&2
  fi
fi

if [ "$FOUND_COLLISION" -ne 0 ]; then
  exit 1
fi

printf 'ADR-%s-%s\n' "$DATE_STEM" "$SLUG"
exit 0

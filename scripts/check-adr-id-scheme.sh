#!/usr/bin/env bash
# check-adr-id-scheme.sh
#
# Guard: every NEW ADR file under docs/internal/architecture/ must follow
# the founder-decreed naming scheme (2026-09-27):
#
#     ADR-<YYYYMMDD>-<slug>.md
#
# — a UTC 8-digit date, a lowercase-alnum slug with single hyphens, no
# numeric ID, no random suffix. The slug alone carries uniqueness, so the
# guard also fails a new file whose slug duplicates ANY other ADR file's
# slug, whatever that file's scheme (3-digit legacy or 8-digit date).
# Existing ADR-001..ADR-093 files are NEVER renamed and never checked;
# the in-flight ADR-094/095/096 trio is grandfathered wholesale.
#
# SCOPE — NEW (ADDED) FILES ONLY, NEVER THE WHOLE TREE
#
#   The tree carries ~165 ADR-*.md from the ADR-001..ADR-093 era that
#   predate this scheme; a guard scanning everything would go red on all
#   of them on day one, and the founder decision explicitly never renames
#   an existing ADR. Only files this branch ADDS are checked — the exact
#   analogue of check-spec-status.sh's "new/changed specs only" scoping
#   and its "do NOT fail on the existing specs" reasoning.
#
# DETERMINING THE BASE REF (mirrors scripts/check-spec-status.sh exactly)
#
#   Resolution order, first match wins:
#     1. CHECK_ADR_ID_SCHEME_BASE_REF — explicit override (this script's
#        own self-test, or a caller that already knows the ref)
#     2. GITHUB_BASE_REF              — auto-populated by GitHub Actions
#                                        for pull_request-triggered runs;
#                                        no workflow-file wiring needed
#     3. OMNIPUS_INTEGRATION_BRANCH   — the coordination-ledger convention
#     4. push event: github.event.before; otherwise WARNING + pass (no
#        main fallback — on a release branch that would count every ADR
#        of the release as "new")
#
#   The resolved ref is then turned into a commit git can diff against:
#   the ref itself if it resolves locally, else origin/<ref> if already
#   fetched, else a shallow `git fetch --depth=1 origin <ref>` — CI's
#   actions/checkout@vN default fetch-depth (1, the PR merge commit only)
#   does not carry the base branch's history.
#
#   FAIL-OPEN, NOT FAIL-CLOSED, WHEN THE BASE CANNOT BE RESOLVED: no
#   local ref, no origin/<ref>, and the shallow fetch fails (no network,
#   renamed ref, no origin remote at all) — and, identically, when the
#   base commit RESOLVES but the diff itself fails (no merge base under a
#   shallow CI checkout): a WARNING naming the git error is printed,
#   nothing is checked, the guard exits 0.
#   Widening to "scan everything" instead would fail on the ~165 legacy
#   ADRs this guard is explicitly never supposed to touch — a worse
#   failure mode than staying silent this one run.
#
# WHAT COUNTS AS "NEW"
#
#   `git diff --name-only --diff-filter=A <base-commit>... --
#   docs/internal/architecture/ADR-*.md` — ADDED files only. Deletions
#   and modifications of existing ADRs are out of scope by design: this
#   guard never demands a rename, and a renamed pre-existing file must
#   not suddenly become a "new" ADR. The three-dot form diffs from the
#   merge base to HEAD, so rebasing or merging the branch does not
#   resurrect the whole tree as "added".
#
# ─── The checks, per added file ───────────────────────────────────────────
#   1. Grandfather: a basename matching ADR-09[456]-*.md is exempt from
#      every check below. The ID-family regex (not exact filenames)
#      covers all three in-flight numbers AND any -review /
#      -review-roundN siblings they may still grow — the founder decision
#      fixes the numbers, not their current filenames, so a pending
#      renumber (ADR-094-web-search-provider-model.md → ADR-096-…) and
#      further review rounds stay exempt.
#   2. Shape: the basename MUST match
#      ADR-[0-9]{8}-[a-z0-9]+(-[a-z0-9]+)*\.md (8-digit date, then a
#      lowercase-alnum slug, single hyphens, no leading/trailing/double
#      hyphen), and the slug must be ≤ 80 characters. Any deviation gets
#      a SPECIFIC reason: old 3-digit numbered scheme, uppercase or
#      underscores, double hyphen, leading/trailing hyphen, missing slug,
#      non-date ID token, oversize slug.
#   3. Duplicate slug: the new file's slug — extracted by stripping the
#      ADR- prefix and the leading ID token (8-digit date or 3-digit
#      legacy number; identical function text to scripts/new-adr-id.sh)
#      — is compared against the slug of EVERY OTHER ADR-*.md file
#      present in the tree, legacy-numbered and date-stamped alike.
#      (-review siblings of an ADR naturally get distinct slugs, so a
#      review round is not a false duplicate.) A match fails with BOTH
#      files named. This is the merge-time backstop for slugs that the
#      generator could not see on in-flight branches.
#
# OUTPUT CONTRACT (mirrors scripts/check-spec-status.sh)
#
#   One line per finding: "check-adr-id-scheme: <path>: <problem>".
#   Exit 0 clean (including "scope undetermined" and "nothing added"),
#   1 on any finding, 2 on internal error (bad REPO_ROOT, not a git repo).
#
# DISCOVERY (scripts/guards.sh conventions)
#
#   Discovered by scripts/guards.sh's scripts/check-*.sh glob. Its
#   proof-of-failure companion is scripts/check-adr-id-scheme.test.sh (a
#   <name>.test.sh companion — subtracted from the guard list and run
#   before this guard). No wiring change: adding this guard is these two
#   files, nothing more.
#
# Usage:
#   bash scripts/check-adr-id-scheme.sh
#
# Overrides (for the companion test — never set these in CI):
#   REPO_ROOT                    — repo root to scan
#   CHECK_ADR_ID_SCHEME_BASE_REF — explicit base ref, skips GITHUB_BASE_REF/
#                                  OMNIPUS_INTEGRATION_BRANCH resolution

set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
ADR_PATHSPEC='docs/internal/architecture/ADR-*.md'
ADR_DIR='docs/internal/architecture'
GRANDFATHER_RE='^ADR-09[456]-.*\.md$'
SHAPE_RE='^ADR-[0-9]{8}-[a-z0-9]+(-[a-z0-9]+)*\.md$'
MAX_SLUG_LEN=80

if [ ! -d "$REPO_ROOT" ]; then
  echo "check-adr-id-scheme: ERROR — REPO_ROOT is not a directory: $REPO_ROOT" >&2
  exit 2
fi

if [ ! -e "$REPO_ROOT/.git" ]; then
  echo "check-adr-id-scheme: ERROR — REPO_ROOT is not a git repository (no .git): $REPO_ROOT" >&2
  exit 2
fi

cd "$REPO_ROOT" || exit 2

# ─── Resolve the base ref name (never the commit yet) ──────────────────────

BASE_REF="${CHECK_ADR_ID_SCHEME_BASE_REF:-}"
if [ -z "$BASE_REF" ] && [ -n "${GITHUB_BASE_REF:-}" ]; then
  BASE_REF="$GITHUB_BASE_REF"
fi
if [ -z "$BASE_REF" ] && [ -n "${OMNIPUS_INTEGRATION_BRANCH:-}" ]; then
  BASE_REF="$OMNIPUS_INTEGRATION_BRANCH"
fi
# Push events (e.g. a push to the integration branch) have no PR base: check
# only the ADRs that push added, by comparing against the commit the branch
# pointed at before the push (github.event.before). Never fall back to `main`:
# on a release branch that would count every ADR of the release as "new".
if [ -z "$BASE_REF" ] && [ "${GITHUB_EVENT_NAME:-}" = "push" ] && [ -n "${GITHUB_EVENT_PATH:-}" ] && [ -f "${GITHUB_EVENT_PATH}" ]; then
  PUSH_BEFORE="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1])).get("before",""))' "$GITHUB_EVENT_PATH" 2>/dev/null || true)"
  case "$PUSH_BEFORE" in
    ""|0000000000000000000000000000000000000000) ;;
    *) BASE_REF="$PUSH_BEFORE" ;;
  esac
fi
if [ -z "$BASE_REF" ]; then
  echo "check-adr-id-scheme: WARNING — no comparison base (not a pull request, not a push with a previous commit, and neither CHECK_ADR_ID_SCHEME_BASE_REF nor OMNIPUS_INTEGRATION_BRANCH is set) — nothing is checked this run. This never widens to scanning every existing ADR." >&2
  echo "check-adr-id-scheme: OK — 0 findings (no base, fail-open by design)"
  exit 0
fi

# ─── Resolve the base ref to a commit, fetching shallowly if needed ────────

resolve_base_commit() {
  local ref="$1"
  if git rev-parse --verify --quiet "$ref" >/dev/null 2>&1; then
    git rev-parse --verify --quiet "$ref"
    return 0
  fi
  if git rev-parse --verify --quiet "origin/$ref" >/dev/null 2>&1; then
    git rev-parse --verify --quiet "origin/$ref"
    return 0
  fi
  if git remote get-url origin >/dev/null 2>&1; then
    if git fetch --quiet --depth=1 origin "$ref" >/dev/null 2>&1; then
      if git rev-parse --verify --quiet FETCH_HEAD >/dev/null 2>&1; then
        git rev-parse --verify --quiet FETCH_HEAD
        return 0
      fi
    fi
  fi
  return 1
}

BASE_COMMIT="$(resolve_base_commit "$BASE_REF" || true)"

if [ -z "$BASE_COMMIT" ]; then
  echo "check-adr-id-scheme: WARNING — cannot resolve integration branch '$BASE_REF' to a commit (no local ref, no origin/$BASE_REF, and the shallow fetch failed or there is no 'origin' remote) — the set of NEW ADR files cannot be determined, so nothing is checked this run. This never widens to scanning every existing ADR." >&2
  echo "check-adr-id-scheme: OK — 0 findings (scope undetermined, fail-open by design)"
  exit 0
fi

# ─── Discover NEW (added) ADR files against the base commit ────────────────
#
# The diff's own failure is never swallowed into a false-clean pass:
# stderr is captured to a temp file (never /dev/null) and the exit status
# is kept, so a failing diff — e.g. a shallow CI checkout whose
# base...HEAD has no merge base — surfaces as a WARNING naming the git
# error, then falls open to exit 0 like the two base-resolution warnings
# above.

if ! DIFF_STDERR_FILE="$(mktemp "${TMPDIR:-/tmp}/check-adr-id-scheme-differr.XXXXXX")"; then
  echo "check-adr-id-scheme: WARNING — cannot create a temp file to capture git diff stderr — nothing is checked this run (fail-open by design)" >&2
  exit 0
fi
trap 'rm -f "$DIFF_STDERR_FILE"' EXIT

DIFF_RC=0
CHANGED_FILES="$(git diff --name-only --diff-filter=A "$BASE_COMMIT"... -- "$ADR_PATHSPEC" 2>"$DIFF_STDERR_FILE")" || DIFF_RC=$?

if [ "$DIFF_RC" -ne 0 ]; then
  DIFF_ERR_TEXT="$(tr '\n' ' ' < "$DIFF_STDERR_FILE")"
  echo "check-adr-id-scheme: WARNING — git diff against $BASE_COMMIT failed (exit $DIFF_RC): ${DIFF_ERR_TEXT:-no error text} — the set of NEW ADR files cannot be determined, so nothing is checked this run. This never widens to scanning every existing ADR." >&2
  echo "check-adr-id-scheme: OK — 0 findings (diff failed, fail-open by design)"
  exit 0
fi
rm -f "$DIFF_STDERR_FILE"

if [ -z "$CHANGED_FILES" ]; then
  echo "check-adr-id-scheme: OK — 0 new ADR files against $BASE_REF ($BASE_COMMIT)"
  exit 0
fi

# ─── Slug extraction — IDENTICAL function text to new-adr-id.sh ────────────

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

# Specific-reason diagnostic for a basename that failed the shape regex.
shape_problem() {
  # "$1" = the basename. Prints one reason; the caller prefixes context.
  local b="$1" sp
  case "$b" in
    ADR-[0-9][0-9][0-9]-*)
      echo "uses the retired 3-digit numbered scheme — new ADRs must be ADR-<YYYYMMDD>-<slug>.md (only ADR-094/095/096 are grandfathered)"
      ;;
    ADR-[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]-*)
      sp="${b#ADR-}"; sp="${sp#*-}"; sp="${sp%.md}"
      case "$sp" in
        *[_A-Z]*) echo "slug '$sp' must be lowercase alnum with single hyphens (uppercase or underscore found)" ;;
        *--*)     echo "slug '$sp' contains a double hyphen" ;;
        -*)       echo "slug '$sp' starts with a hyphen" ;;
        *-)       echo "slug '$sp' ends with a hyphen" ;;
        '')       echo "no slug after the 8-digit date" ;;
        *)        echo "slug '$sp' contains characters outside [a-z0-9-]" ;;
      esac
      ;;
    ADR-[0-9]*)
      echo "ID token is neither a grandfathered 3-digit number nor an 8-digit UTC date"
      ;;
    ADR-*)
      echo "does not match ADR-<YYYYMMDD>-<slug>.md (8-digit UTC date + lowercase-alnum slug)"
      ;;
    *)
      echo "does not start with ADR- and is not grandfathered"
      ;;
  esac
}

# ─── Every ADR file present in the tree, as "<basename><TAB><slug>" ────────
# (the duplicate check compares against every OTHER file present — legacy
# and date-stamped alike; the new files themselves are in this list too,
# which is how two same-slug additions on one branch catch each other)

tree_slug_list=""
for f in "$REPO_ROOT"/"$ADR_DIR"/ADR-*.md; do
  [ -f "$f" ] || continue
  b="$(basename "$f")"
  tree_slug_list+="${b}"$'\t'"$(extract_adr_slug "$b")"$'\n'
done

findings=0
checked=0
exempt=0

while IFS= read -r rel; do
  [ -z "$rel" ] && continue
  path="$REPO_ROOT/$rel"
  if [ ! -f "$path" ]; then
    continue
  fi

  base="$(basename "$rel")"

  # 1. Grandfathered trio + their -review/-review-roundN siblings: exempt.
  if [[ "$base" =~ $GRANDFATHER_RE ]]; then
    exempt=$((exempt + 1))
    continue
  fi

  checked=$((checked + 1))

  # 2. Shape: ADR-<8-digit-date>-<slug>.md, slug lowercase-alnum, single
  #    hyphens, no leading/trailing hyphen.
  if [[ "$base" =~ $SHAPE_RE ]]; then
    slug="$(extract_adr_slug "$base")"
    if [ "${#slug}" -gt "$MAX_SLUG_LEN" ]; then
      echo "check-adr-id-scheme: $rel: slug '$slug' is ${#slug} characters (max $MAX_SLUG_LEN) — shorten the title"
      findings=$((findings + 1))
    fi
  else
    echo "check-adr-id-scheme: $rel: $(shape_problem "$base")"
    findings=$((findings + 1))
    continue
  fi

  # 3. Duplicate slug against every OTHER ADR file present in the tree.
  while IFS=$'\t' read -r other_base other_slug; do
    [ -z "$other_base" ] && continue
    [ "$other_base" = "$base" ] && continue
    if [ "$other_slug" = "$slug" ]; then
      echo "check-adr-id-scheme: $rel: duplicate slug '$slug' — also used by $other_base"
      findings=$((findings + 1))
    fi
  done <<EOF
$tree_slug_list
EOF

done <<EOF
$CHANGED_FILES
EOF

if [ "$findings" -gt 0 ]; then
  echo "check-adr-id-scheme: $findings finding(s) across $checked new ADR file(s) checked, $exempt grandfathered-exempt (base $BASE_REF, $BASE_COMMIT)"
  exit 1
fi

echo "check-adr-id-scheme: OK — 0 findings ($checked new ADR file(s) checked, $exempt grandfathered-exempt, against $BASE_REF, $BASE_COMMIT)"
exit 0

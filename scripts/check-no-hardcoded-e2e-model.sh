#!/usr/bin/env bash
# check-no-hardcoded-e2e-model.sh — one committed source of truth for the
# real-LLM e2e/evals model id.
#
# The real-LLM CI surfaces (Playwright e2e in pr.yml, sandbox-uat.yml, the
# nightly evals, the Fly CI runner) all consume ONE committed file:
# tests/e2e/e2e-model.json ({"model": "<vendor>/<model>"}). PR #868 migrated
# most paths to DeepSeek but left the id as scattered literals and missed
# runci.sh entirely (founder request 2026-09-27). This guard fails any banned
# model-id literal in the surfaces it scans, so the value cannot scatter
# again: the old ids must never return, and the CURRENT id must never be
# re-hardcoded outside the source file either.
#
# Switching the model (the ONLY sanctioned procedure — runbook):
#   1. edit tests/e2e/e2e-model.json to the new {"model": "<vendor>/<model>"}, AND
#   2. add the new id to PATTERN below. The old id STAYS BANNED — old ids must
#      never return, and the current id must never be re-hardcoded outside the
#      source file either.
# Any other path (a literal in a workflow, a deploy script, an eval flag
# default, a TOML, an extensionless file, a comment) fails this guard.
#
# Banned literals (old ids that must never return, plus the current id):
#   z-ai/glm-5.2, z-ai/glm-5.3-flash, z-ai/glm-5-turbo,
#   deepseek/deepseek-v4.1-flash,
#   google/gemini-2.0-flash-001, google/gemini-2.5-flash,
#   openai/gpt-4o, anthropic/claude-sonnet-4.6, claude-sonnet-4-6
#
# Scanned — exactly the surfaces this centralization covers, where a model id
# would be CONSUMED as the e2e/evals model choice:
#   .github/workflows/  deploy/  evals/  tests/e2e/**  .github/*.md
#
#   .github/workflows/, deploy/ and evals/ are scanned as ALL text files
#   (-I; binaries skipped) — no extension filter, so a literal in a .toml,
#   a .txt or an extensionless file is an offender like any other (finding
#   G1, 2026-09-27). No vendored/generated dirs exist under these surfaces
#   (checked 2026-09-27); if one ever appears, exclude it here explicitly.
#
#   tests/e2e/** is scanned across every file type present there (.ts, .tsx,
#   .js, .json, .md, .yml, .go, ... — any text file; binaries are skipped),
#   EXCLUDING exactly tests/e2e/e2e-model.json — the ONE allowed literal —
#   and the transient Playwright/dependency dirs (node_modules, test-results,
#   playwright-report) that can appear under tests/e2e at runtime. A comment
#   is a literal on a scanned surface; it fails like code.
#
# Deliberately NOT scanned (and why):
#   docs/** (incl. ADR-054-* and docs/internal/_archive/**)
#                            — illustrative prose only, not live config
#                              (same rationale as the ADR/_archive exemptions
#                              in the original request).
#   contracts/**             — schema `example:` values (illustrative, like ADRs).
#   scripts/                 — the guard + companion live here; and scripts/uat
#                              provisioning defaults are a separate concern, not
#                              the e2e/evals model choice.
#   pkg/ cmd/ src/           — Go/TS test fixtures there use model ids as
#                              arbitrary test DATA for (provider, model) routing
#                              semantics; comments are prose. Neither is the
#                              e2e model choice.
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run — an
# existence-checked path is missing, OR a grep errored mid-scan (rc > 1,
# e.g. an unreadable dir/file): a PARTIAL scan refuses a green verdict and
# shows the grep stderr (finding G2, 2026-09-27).

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-hardcoded-e2e-model: cannot cd to $REPO_ROOT" >&2; exit 2; }

# Refuse to report a green verdict for a tree this script never scanned (the
# exact false-green trap docs/internal/false-green-patterns.md warns about;
# mirrors check-no-goal-confirm-gate.sh's existence-check pattern). Dirs AND
# files are checked — .github/SECRETS.md is a required file, not a dir.
for p in .github/workflows deploy evals tests/e2e .github/SECRETS.md; do
  if [ ! -e "$p" ]; then
    echo "check-no-hardcoded-e2e-model: expected path '$p' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "  verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

PATTERN='z-ai/glm-5\.2|z-ai/glm-5\.3-flash|z-ai/glm-5-turbo|google/gemini-2\.5-flash|google/gemini-2\.0-flash-001|openai/gpt-4o|anthropic/claude-sonnet-4\.6|claude-sonnet-4-6|deepseek/deepseek-v4\.1-flash'

violations=""

# scan <label> <post-filter-ERE-or-empty> <grep args ...>: run one grep over
# one surface, accumulate matching lines into $violations, and refuse a
# partial-scan green. grep's own exit codes decide (finding G2):
#   0   offenders — lines are appended to $violations
#   1   clean
#   >1  the scan itself failed (unreadable dir/file, bad regex) — exit 2 with
#       the grep stderr shown; never a silent partial green.
# The scan grep runs ALONE and its rc is read directly — never inside a
# pipeline. Under pipefail a pipeline's rc is the RIGHTMOST non-zero rc, so
# in the filtered branch the exclusion `grep -vE` exiting 1 on empty input
# (clean surface, or everything filtered out) would SUPERSEDE the scan
# grep's rc>1 and mask a partial scan as clean (finding G2b, 2026-09-27 —
# squad-lead review of c943cc1d9). Hence: raw matches to a temp file, rc
# checked, THEN the optional exclusion filter applied to the raw file — the
# filter's own rc is irrelevant (an empty selection is not an error here,
# only empty output, which just means no violations).
scan() {
  local label="$1"; shift
  local filter="$1"; shift
  local errf rawf out rc
  errf="$(mktemp "${TMPDIR:-/tmp}/e2e-model-guard.XXXXXX")" || {
    echo "check-no-hardcoded-e2e-model: cannot create a stderr capture file" >&2
    exit 2
  }
  rawf="$(mktemp "${TMPDIR:-/tmp}/e2e-model-guard.XXXXXX")" || {
    echo "check-no-hardcoded-e2e-model: cannot create a raw capture file" >&2
    rm -f "$errf"
    exit 2
  }
  grep "$@" >"$rawf" 2>"$errf"
  rc=$?
  if [ "$rc" -gt 1 ]; then
    echo "check-no-hardcoded-e2e-model: grep exited $rc while scanning $label — the scan was PARTIAL, refusing a green verdict. grep stderr:" >&2
    sed 's/^/  /' "$errf" >&2
    rm -f "$errf" "$rawf"
    exit 2
  fi
  if [ -n "$filter" ]; then
    out="$(grep -vE "$filter" <"$rawf")"
  else
    out="$(cat "$rawf")"
  fi
  if [ -n "$out" ]; then
    violations+="$out"$'\n'
  fi
  rm -f "$errf" "$rawf"
}

# .github/workflows/, deploy/ and evals/ — ALL text files, no --include
# filter (finding G1): a literal in deploy/foo.toml or an extensionless file
# is an offender like one in a .yml.
scan ".github/workflows/ deploy/ evals/" '' -rnIE "$PATTERN" .github/workflows/ deploy/ evals/

# tests/e2e/**: every text file type (binaries skipped via -I), excluding
# exactly tests/e2e/e2e-model.json (the single committed source of truth)
# and the transient node_modules/test-results/playwright-report dirs. The
# exclusion stays path-exact: the filter removes only lines whose file path
# is exactly tests/e2e/e2e-model.json.
scan "tests/e2e/" '^tests/e2e/e2e-model\.json:' -rnIE "$PATTERN" \
  --exclude-dir=node_modules --exclude-dir=test-results --exclude-dir=playwright-report \
  tests/e2e/

# .github/*.md (top level): SECRETS.md and any other doc that could
# describe the e2e/evals model. -H forces the filename prefix even when the
# glob resolves to a single file (a lone .github/SECRETS.md would otherwise
# print line content with no file name).
scan ".github/*.md" '' -HnE "$PATTERN" .github/*.md

if [ -n "$violations" ]; then
  {
    echo "check-no-hardcoded-e2e-model: FAILED — hardcoded e2e model id(s) found outside tests/e2e/e2e-model.json"
    echo "  (the single committed source of truth; see this guard's header for the consumer wiring"
    echo "   and the switch runbook: edit the JSON AND add the new id to PATTERN — the old id stays banned)"
    printf '%s\n' "$violations" | sed 's/^/  /'
  } >&2
  exit 1
fi

echo "check-no-hardcoded-e2e-model: OK — no banned model-id literal outside tests/e2e/e2e-model.json"
exit 0

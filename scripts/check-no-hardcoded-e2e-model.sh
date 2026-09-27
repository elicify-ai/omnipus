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
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

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

violations="$(
  grep -rnE "$PATTERN" \
    --include='*.yml' --include='*.yaml' --include='*.sh' --include='*.go' --include='*.ts' --include='*.tsx' --include='*.js' --include='*.json' --include='*.md' \
    .github/workflows/ deploy/ evals/ 2>/dev/null
  # tests/e2e/**: every text file type (binaries skipped via -I), excluding
  # exactly tests/e2e/e2e-model.json (the single committed source of truth)
  # and the transient node_modules/test-results/playwright-report dirs.
  grep -rnEI "$PATTERN" \
    --exclude-dir=node_modules --exclude-dir=test-results --exclude-dir=playwright-report \
    tests/e2e/ 2>/dev/null | grep -v '^tests/e2e/e2e-model\.json:'
  # .github/*.md (top level): SECRETS.md and any other doc that could
  # describe the e2e/evals model. -H forces the filename prefix even when the
  # glob resolves to a single file (a lone .github/SECRETS.md would otherwise
  # print line content with no file name).
  grep -HnE "$PATTERN" .github/*.md 2>/dev/null
)"

if [ -n "$violations" ]; then
  {
    echo "check-no-hardcoded-e2e-model: FAILED — hardcoded e2e model id(s) found outside tests/e2e/e2e-model.json"
    echo "  (the single committed source of truth; see this guard's header for the consumer wiring)"
    printf '%s\n' "$violations" | sed 's/^/  /'
  } >&2
  exit 1
fi

echo "check-no-hardcoded-e2e-model: OK — no banned model-id literal outside tests/e2e/e2e-model.json"
exit 0

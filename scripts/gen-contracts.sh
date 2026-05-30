#!/usr/bin/env bash
# gen-contracts.sh — Single source-of-truth codegen for all wire-format types.
#
# Drives both _gen-ts.sh (TypeScript types + Zod) and _gen-go.sh (Go types).
# Each helper script is idempotent and self-contained; this orchestrator runs
# them in sequence after linting the specs.
#
# Idempotent: running twice in a clean tree produces no git diff.
# Used by `make gen-contracts` and `make verify-contracts` (the latter adds a
# git-diff-exit-code gate on top).
#
# Required tools (verified by the child scripts, fail fast if missing):
#   - npx + node_modules (openapi-typescript, openapi-zod-client, js-yaml, @asyncapi/parser, @redocly/cli)
#   - go 1.22+ in PATH (or /usr/local/go/bin/go)
#   - oapi-codegen v2 (install: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest)
#   - gofmt in PATH (ships with Go)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

# Make sure Go toolchain is on PATH for child processes (oapi-codegen, gofmt).
# Priority: /usr/local/go/bin (system Go), then GOBIN or ~/go/bin (user installs).
if [ -d /usr/local/go/bin ] && ! echo "$PATH" | grep -q "/usr/local/go/bin"; then
  export PATH="/usr/local/go/bin:$PATH"
fi
if [ -n "${GOBIN:-}" ] && ! echo "$PATH" | grep -q "${GOBIN}"; then
  export PATH="${GOBIN}:${PATH}"
elif [ -d "${HOME}/go/bin" ] && ! echo "$PATH" | grep -q "${HOME}/go/bin"; then
  export PATH="${HOME}/go/bin:${PATH}"
fi

echo "[gen-contracts] Working directory: ${REPO_ROOT}"

# ---------------------------------------------------------------------------
# Step 1: Lint specs
# ---------------------------------------------------------------------------
echo "[gen-contracts] Step 1/5: Linting contracts/openapi.yaml..."
npx --no-install @redocly/cli lint contracts/openapi.yaml --skip-rule no-server-example.com

echo "[gen-contracts] Step 1/5: Validating contracts/asyncapi.yaml..."
node -e "
  const { Parser } = require('@asyncapi/parser');
  const fs = require('fs');
  const p = new Parser();
  p.parse(fs.readFileSync('contracts/asyncapi.yaml', 'utf8')).then(r => {
    const errors = r.diagnostics.filter(d => d.severity === 0);
    if (errors.length > 0) {
      console.error('asyncapi.yaml validation errors:');
      console.error(JSON.stringify(errors, null, 2));
      process.exit(1);
    }
    console.log('asyncapi.yaml valid');
  }).catch(err => { console.error(err); process.exit(1); });
"

# ---------------------------------------------------------------------------
# Step 2: TypeScript types + Zod (delegated to _gen-ts.sh)
# ---------------------------------------------------------------------------
echo "[gen-contracts] Step 2/5: Generating TypeScript types + Zod schemas..."
bash scripts/_gen-ts.sh

# ---------------------------------------------------------------------------
# Step 3: Go types (delegated to _gen-go.sh, mirrors _gen-ts.sh symmetry)
# ---------------------------------------------------------------------------
echo "[gen-contracts] Step 3/5: Generating Go types..."
mkdir -p pkg/api/generated
bash scripts/_gen-go.sh

# ---------------------------------------------------------------------------
# Step 4: Format generated Go files (deterministic gofmt)
# ---------------------------------------------------------------------------
echo "[gen-contracts] Step 4/5: Formatting generated Go files..."
gofmt -w pkg/api/generated/

# ---------------------------------------------------------------------------
# Step 5: Sync inboundschemas — copy canonical schemas to the embed directory
# so gateway boot-time validation always reflects the current contract.
# ---------------------------------------------------------------------------
echo "[gen-contracts] Step 5/5: Syncing inboundschemas from contracts/components/schemas/..."
rm -f pkg/gateway/inboundschemas/*.yaml
cp contracts/components/schemas/*.yaml pkg/gateway/inboundschemas/
echo "[gen-contracts] Synced $(ls pkg/gateway/inboundschemas/*.yaml | wc -l | tr -d ' ') schema files."

echo "[gen-contracts] Done. All contract artifacts are up to date."

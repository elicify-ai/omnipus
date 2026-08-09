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
#   - oapi-codegen v2 — PIN THE VERSION, do not use @latest:
#         go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.7.0
#     This MUST match the version pinned in .github/workflows/pr.yml, or
#     `make verify-contracts` fails for reasons that have nothing to do with
#     your change. v2.8.0 renames every enum constant (ExternalCli becomes
#     ExecutorConfigKindExternalCli, and so on): regenerating with it rewrites
#     ~5000 lines and breaks hand-written consumers such as
#     pkg/api/generated/fixtures.go. The generator version is stamped into the
#     header of pkg/api/generated/openapi_types.gen.go — check it if a
#     regeneration produces a diff far larger than your schema change.
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
# Step 0: Generator version guard
# ---------------------------------------------------------------------------
# oapi-codegen's output is version-dependent in ways that are NOT confined to
# your schema change. v2.8.0 renames every enum constant (ExternalCli ->
# ExecutorConfigKindExternalCli), which rewrites ~5000 lines and breaks
# hand-written consumers like pkg/api/generated/fixtures.go. Regenerating with
# the wrong version therefore produces a huge diff, a broken build, and a
# verify-contracts failure that looks like it came from your change.
#
# This guard exists because that happened. Keep REQUIRED_OAPI_CODEGEN_VERSION
# in lockstep with the version pinned in .github/workflows/pr.yml.
REQUIRED_OAPI_CODEGEN_VERSION="v2.7.0"
if command -v oapi-codegen >/dev/null 2>&1; then
  ACTUAL_OAPI_CODEGEN_VERSION="$(oapi-codegen --version 2>/dev/null | tail -1 | tr -d '[:space:]')"
  if [ "${ACTUAL_OAPI_CODEGEN_VERSION}" != "${REQUIRED_OAPI_CODEGEN_VERSION}" ]; then
    echo "[gen-contracts] ERROR: oapi-codegen version mismatch." >&2
    echo "  required: ${REQUIRED_OAPI_CODEGEN_VERSION} (matches .github/workflows/pr.yml)" >&2
    echo "  found:    ${ACTUAL_OAPI_CODEGEN_VERSION:-<unknown>}" >&2
    echo "  fix:      go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${REQUIRED_OAPI_CODEGEN_VERSION}" >&2
    echo "  why:      a different version rewrites unrelated generated code and breaks the build." >&2
    exit 1
  fi
else
  echo "[gen-contracts] ERROR: oapi-codegen not found on PATH." >&2
  echo "  fix: go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@${REQUIRED_OAPI_CODEGEN_VERSION}" >&2
  exit 1
fi
echo "[gen-contracts] oapi-codegen ${ACTUAL_OAPI_CODEGEN_VERSION} (pinned) OK"

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

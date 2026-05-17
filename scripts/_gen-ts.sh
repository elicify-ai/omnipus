#!/usr/bin/env bash
# _gen-ts.sh — TypeScript + Zod schema generation from contracts/
#
# Agent C will fold these commands into scripts/gen-contracts.sh.
# Run from the repository root.
#
# This script is IDEMPOTENT: running it twice produces identical byte-for-byte
# output. It achieves this by generating all content in a single pass per file,
# with no detect-and-skip logic.
#
# Prerequisites (installed in node_modules, no npm install needed):
#   - openapi-typescript   v7.13.0  (node_modules/.bin/openapi-typescript)
#   - openapi-zod-client   v1.18.3  (node_modules/.bin/openapi-zod-client)
#   - js-yaml              (node_modules/js-yaml — used by gen scripts)
#
# Outputs (all committed, never in .gitignore):
#   src/lib/api/generated/openapi-types.ts   — TS types from openapi.yaml
#   src/lib/api/generated/asyncapi-types.ts  — TS types from asyncapi.yaml
#   src/lib/api/generated/schemas.ts         — Zod schemas (REST + WS frames)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# node_modules live in the parent git repo root when running from a worktree.
# Resolve the canonical node_modules location (worktree → parent via git).
GIT_COMMON_DIR="$(git -C "$REPO_ROOT" rev-parse --git-common-dir 2>/dev/null || true)"
# In a worktree, --git-common-dir resolves to the .git dir of the main repo.
# Derive the main repo root as the parent of that .git dir.
if [ -n "$GIT_COMMON_DIR" ]; then
  MAIN_REPO_ROOT="$(cd "$(dirname "$GIT_COMMON_DIR")" && pwd)"
else
  MAIN_REPO_ROOT="$REPO_ROOT"
fi
if [ -d "$MAIN_REPO_ROOT/node_modules" ]; then
  NODE_BIN="$MAIN_REPO_ROOT/node_modules/.bin"
  NODE_MODULES="$MAIN_REPO_ROOT/node_modules"
else
  NODE_BIN="$REPO_ROOT/node_modules/.bin"
  NODE_MODULES="$REPO_ROOT/node_modules"
fi
# contracts/ lives in the main repo root, not the worktree root.
if [ -d "$MAIN_REPO_ROOT/contracts" ]; then
  CONTRACTS="$MAIN_REPO_ROOT/contracts"
else
  CONTRACTS="$REPO_ROOT/contracts"
fi
GEN="$REPO_ROOT/src/lib/api/generated"
TEMPLATE="$REPO_ROOT/scripts/_gen-ts-template.hbs"

mkdir -p "$GEN"

# ── Step 1: openapi-types.ts ─────────────────────────────────────────────────
# Generate base types from openapi.yaml, then append named re-exports.
echo "▸ Generating openapi-types.ts from contracts/openapi.yaml …"
"$NODE_BIN/openapi-typescript" \
  "$CONTRACTS/openapi.yaml" \
  -o "$GEN/_openapi-types.generated.tmp.ts"

# Append named type re-exports and write the final file atomically.
node - "$CONTRACTS/openapi.yaml" "$GEN/_openapi-types.generated.tmp.ts" "$GEN/openapi-types.ts" "$NODE_MODULES" <<'NODE_SCRIPT'
const fs = require("fs");
const [,, contractPath, basePath, outPath, nodeModules] = process.argv;
const yaml = require(nodeModules + "/js-yaml/index.js");
const doc = yaml.load(fs.readFileSync(contractPath, "utf8"));
const names = Object.keys(doc.components?.schemas ?? {});
const base = fs.readFileSync(basePath, "utf8");
const reexports = [
  "",
  "// ── Named type re-exports from components.schemas ────────────────────────────",
  "// Convenience exports so consumers can write `import type { Agent } from \"./openapi-types\"`",
  "// rather than `components[\"schemas\"][\"Agent\"]`.",
  "",
  ...names.map((n) => `export type ${n} = components["schemas"]["${n}"];`),
  "",
].join("\n");
fs.writeFileSync(outPath, base + reexports, "utf8");
NODE_SCRIPT
rm -f "$GEN/_openapi-types.generated.tmp.ts"
echo "  Written: $GEN/openapi-types.ts"

# ── Step 2: asyncapi-types.ts ────────────────────────────────────────────────
echo "▸ Generating asyncapi-types.ts from contracts/asyncapi.yaml …"
node "$REPO_ROOT/scripts/_gen-asyncapi-types.mjs"

# ── Step 3: schemas.ts ───────────────────────────────────────────────────────
# Generate OpenAPI Zod schemas, then append the generated AsyncAPI Zod schemas.
# The AsyncAPI Zod schemas are emitted by _gen-asyncapi-types.mjs (Step 2 above)
# into $GEN/_asyncapi-zod-schemas.generated.ts. The hand-written
# scripts/_asyncapi-zod-schemas.ts is no longer used; delete it if still present.
echo "▸ Generating schemas.ts (Zod) from contracts/openapi.yaml …"
"$NODE_BIN/openapi-zod-client" \
  "$CONTRACTS/openapi.yaml" \
  -o "$GEN/_schemas.generated.tmp.ts" \
  --export-schemas \
  --export-types \
  -t "$TEMPLATE"

# Append the generated AsyncAPI Zod schemas and write the final file atomically.
# Strip the `// @ts-nocheck` and "Fragment —" sentinel lines from the fragment
# before concatenating — those are only meaningful when the fragment is checked
# standalone by TypeScript; in the merged schemas.ts they are noise/invalid.
ASYNCAPI_FRAG="$GEN/_asyncapi-zod-schemas.generated.ts"
ASYNCAPI_FRAG_STRIPPED=$(grep -v '^// @ts-nocheck' "$ASYNCAPI_FRAG" | grep -v '^// Fragment')
{
  cat "$GEN/_schemas.generated.tmp.ts"
  printf '%s\n' "$ASYNCAPI_FRAG_STRIPPED"
} > "$GEN/schemas.ts"
rm -f "$GEN/_schemas.generated.tmp.ts"
echo "  Written: $GEN/schemas.ts"

# ── Quality gates ─────────────────────────────────────────────────────────────
echo "▸ Verifying TypeScript compilation …"
npx tsc --noEmit

echo "▸ Quality gates:"
OPENAPI_EXPORTS=$(grep -c "^export" "$GEN/openapi-types.ts")
SCHEMAS_EXPORTS=$(grep -c "^export const" "$GEN/schemas.ts")
echo "  openapi-types.ts ^export count : $OPENAPI_EXPORTS  (required ≥ 50)"
echo "  schemas.ts ^export const count : $SCHEMAS_EXPORTS  (required ≥ 50)"
[ "$OPENAPI_EXPORTS" -ge 50 ] || { echo "FAIL: openapi-types.ts export count < 50"; exit 1; }
[ "$SCHEMAS_EXPORTS" -ge 50 ] || { echo "FAIL: schemas.ts export const count < 50"; exit 1; }
echo "  All quality gates PASSED"

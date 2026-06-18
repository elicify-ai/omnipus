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
# contracts/ resolution: prefer the worktree's own contracts/ if it exists
# (so branch-specific schema changes are picked up), then fall back to the
# main-repo checkout, then finally the REPO_ROOT itself.
# Allow CONTRACTS env var override for any remaining edge cases.
if [ -z "${CONTRACTS:-}" ]; then
  if [ -d "$REPO_ROOT/contracts" ]; then
    CONTRACTS="$REPO_ROOT/contracts"
  elif [ -d "$MAIN_REPO_ROOT/contracts" ]; then
    CONTRACTS="$MAIN_REPO_ROOT/contracts"
  else
    CONTRACTS="$REPO_ROOT/contracts"
  fi
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
# into $GEN/_asyncapi-zod-schemas.generated.ts.
echo "▸ Generating schemas.ts (Zod) from contracts/openapi.yaml …"
"$NODE_BIN/openapi-zod-client" \
  "$CONTRACTS/openapi.yaml" \
  -o "$GEN/_schemas.generated.tmp.ts" \
  --export-schemas \
  --export-types \
  -t "$TEMPLATE"

# Post-process: append `.strict()` to schemas that opt into strict object
# validation via `additionalProperties: false` in their YAML schema. The
# openapi-zod-client tool (v1.18.3) does not honor this per-schema; the global
# `--strict-objects` flag is too broad (it would affect every object schema,
# including ones that intentionally allow extra fields). We use a narrow
# opt-in list — the contract is the YAML `additionalProperties: false`; this
# list is a deliberate manual mapping (W2-9 fix). The runtime result is the
# same as if the YAML were `additionalProperties: false` AND we chained
# `.strict()`: unknown fields are rejected.
STRICT_SCHEMAS=${STRICT_SCHEMAS:-"FallbackModel"}
STRICT_RAW="$GEN/_schemas.generated.tmp.ts"
for name in $STRICT_SCHEMAS; do
  if grep -q "^export const ${name}:" "$STRICT_RAW"; then
    # Append .strict() to the last .something() chain in the schema definition.
    # Generator emits:  export const Name: z.ZodType<Name> = z.object({...});
    #                  or: .object({...}).partial().passthrough();
    # Rewrite to:        ... .partial().passthrough().strict();
    #                  or: z.object({...}).strict();
    awk -v name="$name" '
      /^export const '"$name"':/ { in_block = 1; buf = $0; next }
      in_block && /;/ { buf = buf "\n" $0; gsub(/;$/, ".strict();", buf); print buf; in_block = 0; next }
      in_block { buf = buf "\n" $0; next }
      { print }
    ' "$STRICT_RAW" > "$STRICT_RAW.tmp" && mv "$STRICT_RAW.tmp" "$STRICT_RAW"
  fi
done

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

# ── Inject rationale comments for schemas with a non-default validation policy.
# The codegen faithfully reflects the YAML, but the rationale for *why* a
# schema diverges from the project default lives in YAML comments that do not
# survive into the generated artifact. Mirror the rationale next to the schema
# so the policy is grep-able from src/lib/api/generated/schemas.ts as well as
# from contracts/components/schemas/*.yaml.
#   - MessageFrame.metadata: passthrough (open). Forward-compat extension
#     channel. See contracts/components/schemas/MessageFrame.yaml.
node - "$GEN/schemas.ts" <<'NODE_SCRIPT'
const fs = require("fs");
const path = process.argv[2];
const src = fs.readFileSync(path, "utf8");

const POLICY_COMMENTS = {
  MessageFrame: `// ── Validation policy note (mirrors MessageFrame.yaml) ────────────────────────
// Outer MessageFrame is .strict(): unknown top-level keys are rejected (a
// server-added field surfaces as a visible schema failure, debuggable).
// The nested \`metadata\` object is intentionally .passthrough(): it is the
// wire's forward-compat extension channel. Adding a new optional metadata
// field server-side must NOT break already-shipped SPA clients — strict()
// would. Drift on a *newly required* metadata field is caught by the
// W2-29 outbound validator (src/lib/ws.ts) the moment the server starts
// requiring it. Inbound, the dev-mode console.debug in
// src/lib/ws.ts::_parseServerFrame lists any extra metadata keys so
// drift is grep-able even though strict-validation is off.
// See contracts/components/schemas/MessageFrame.yaml for the full rationale.
`,
};

let out = src;
for (const [name, comment] of Object.entries(POLICY_COMMENTS)) {
  // Anchor: insert *after* the entire `export const <name> = z … ;` block
  // ends. The codegen emits each definition as a chained expression ending
  // with `;` — possibly with intermediate calls like `.passthrough().optional()`.
  // Match the shortest span from `export const <name>` to the next `;\n` that
  // sits on its own line.
  const re = new RegExp(`(export const ${name} = z[\\s\\S]*?;\\n)`, "m");
  if (!re.test(out)) {
    console.warn(`  WARN: could not find ${name} definition to annotate`);
    continue;
  }
  out = out.replace(re, `$1\n${comment}`);
}

if (out !== src) {
  fs.writeFileSync(path, out, "utf8");
  console.log(`  Injected policy comments for: ${Object.keys(POLICY_COMMENTS).join(", ")}`);
}
NODE_SCRIPT

echo "  Written: $GEN/schemas.ts"

# ── Quality gates ─────────────────────────────────────────────────────────────
echo "▸ Verifying TypeScript compilation …"
if [ -n "${SKIP_TS_CHECK:-}" ]; then
  echo "  (skipped via SKIP_TS_CHECK=1 — downstream consumer errors expected during wire-schema migrations)"
else
  npx tsc -b --noEmit
fi

echo "▸ Quality gates:"
OPENAPI_EXPORTS=$(grep -c "^export" "$GEN/openapi-types.ts")
SCHEMAS_EXPORTS=$(grep -c "^export const" "$GEN/schemas.ts")
echo "  openapi-types.ts ^export count : $OPENAPI_EXPORTS  (required ≥ 50)"
echo "  schemas.ts ^export const count : $SCHEMAS_EXPORTS  (required ≥ 50)"
[ "$OPENAPI_EXPORTS" -ge 50 ] || { echo "FAIL: openapi-types.ts export count < 50"; exit 1; }
[ "$SCHEMAS_EXPORTS" -ge 50 ] || { echo "FAIL: schemas.ts export const count < 50"; exit 1; }
echo "  All quality gates PASSED"

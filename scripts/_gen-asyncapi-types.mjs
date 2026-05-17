/**
 * _gen-asyncapi-types.mjs
 *
 * Generates two files from contracts/asyncapi.yaml:
 *
 *   1. src/lib/api/generated/asyncapi-types.ts
 *      TypeScript interfaces for every AsyncAPI component schema.
 *
 *   2. src/lib/api/generated/_asyncapi-zod-schemas.generated.ts
 *      Zod runtime schemas for every AsyncAPI component schema.
 *      These replace the hand-written scripts/_asyncapi-zod-schemas.ts that
 *      was previously concatenated by _gen-ts.sh.
 *
 * Approach: parse the AsyncAPI YAML, extract components.schemas, convert each
 * JSON Schema to TypeScript + Zod using purpose-built converters.
 * This is intentionally minimal — AsyncAPI 3 codegen tooling is immature and
 * the schema set is small enough to convert deterministically.
 *
 * Run with: node scripts/_gen-asyncapi-types.mjs
 * (Node resolves node_modules from the project root.)
 */

import { readFileSync, writeFileSync, existsSync } from "fs";
import { resolve, dirname, join } from "path";
import { fileURLToPath } from "url";
import { createRequire } from "module";

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = resolve(__dirname, "..");

const require = createRequire(import.meta.url);

// Resolve js-yaml from the nearest node_modules (supports worktree layouts where
// node_modules live in the main repo root rather than the worktree root).
function resolveJsYaml() {
  // Try git to find the main repo root (works even from inside a worktree)
  try {
    const { execSync } = require("child_process");
    const gitCommonDir = execSync("git rev-parse --git-common-dir", {
      cwd: ROOT, encoding: "utf8"
    }).trim();
    // git-common-dir may be a relative path like ".git"; resolve against ROOT
    // so the returned candidate is always absolute (require() needs absolute
    // paths when invoked from a script directory other than node's cwd).
    const mainRepoRoot = resolve(ROOT, dirname(gitCommonDir));
    const candidate = join(mainRepoRoot, "node_modules", "js-yaml", "index.js");
    if (existsSync(candidate)) return candidate;
  } catch (_) {
    // fall through to manual search
  }
  // Fallback: walk up from the script dir
  const candidates = [
    join(ROOT, "node_modules", "js-yaml", "index.js"),
    join(ROOT, "..", "node_modules", "js-yaml", "index.js"),
    join(ROOT, "..", "..", "node_modules", "js-yaml", "index.js"),
    join(ROOT, "..", "..", "..", "node_modules", "js-yaml", "index.js"),
  ];
  for (const p of candidates) {
    if (existsSync(p)) return p;
  }
  throw new Error("js-yaml not found — install it in node_modules");
}

const yaml = require(resolveJsYaml());

// The contracts directory is always in the main git repo root.
// In a worktree, --git-common-dir resolves to the main repo's .git dir.
function resolveContractsDir() {
  const { execSync } = require("child_process");
  try {
    const gitCommonDir = execSync("git rev-parse --git-common-dir", {
      cwd: ROOT, encoding: "utf8"
    }).trim();
    // Resolve against ROOT so a relative ".git" becomes absolute.
    const mainRepoRoot = resolve(ROOT, dirname(gitCommonDir));
    const contractsPath = join(mainRepoRoot, "contracts");
    if (existsSync(contractsPath)) return contractsPath;
  } catch (_) {
    // fall through
  }
  // Fallback: try known relative locations
  for (const rel of ["contracts", "../contracts", "../../contracts", "../../../contracts"]) {
    const p = resolve(ROOT, rel);
    if (existsSync(p)) return p;
  }
  throw new Error("contracts/ directory not found");
}

const CONTRACTS_DIR = resolveContractsDir();

const asyncapiPath = resolve(CONTRACTS_DIR, "asyncapi.yaml");
const outPath = resolve(ROOT, "src/lib/api/generated/asyncapi-types.ts");
const zodOutPath = resolve(ROOT, "src/lib/api/generated/_asyncapi-zod-schemas.generated.ts");

const doc = yaml.load(readFileSync(asyncapiPath, "utf8"));
const schemas = doc.components?.schemas ?? {};

// ── JSON Schema → TypeScript converter ──────────────────────────────────────

/** Convert a single JSON Schema node to a TypeScript type string. */
function schemaToTs(schema, indent = 0, schemaName = "") {
  if (!schema) return "unknown";

  const pad = "  ".repeat(indent);
  const inner = "  ".repeat(indent + 1);

  // $ref to sibling schema — use the name directly
  if (schema.$ref) {
    const parts = schema.$ref.split("/");
    return parts[parts.length - 1];
  }

  // const literal
  if (schema.const !== undefined) {
    return JSON.stringify(schema.const);
  }

  // enum
  if (Array.isArray(schema.enum)) {
    return schema.enum.map((v) => JSON.stringify(v)).join(" | ");
  }

  // type-based
  const type = schema.type;

  if (type === "string") return "string";
  if (type === "number") return "number";
  if (type === "integer") return "number";
  if (type === "boolean") return "boolean";

  if (type === "array") {
    if (schema.items) {
      return `Array<${schemaToTs(schema.items, indent)}>`;
    }
    return "Array<unknown>";
  }

  if (type === "object" || schema.properties) {
    const required = new Set(schema.required ?? []);
    const props = schema.properties ?? {};
    const lines = [];
    for (const [key, propSchema] of Object.entries(props)) {
      const opt = required.has(key) ? "" : "?";
      const tsType = schemaToTs(propSchema, indent + 1);
      lines.push(`${inner}${key}${opt}: ${tsType};`);
    }
    if (schema.additionalProperties === true || (typeof schema.additionalProperties === "object" && schema.additionalProperties !== false)) {
      lines.push(`${inner}[key: string]: unknown;`);
    }
    if (lines.length === 0) {
      return "Record<string, unknown>";
    }
    return `{\n${lines.join("\n")}\n${pad}}`;
  }

  // empty schema (result: {}) — any JSON value
  if (!type && !schema.properties && !schema.items && !schema.$ref && !schema.enum && schema.const === undefined) {
    return "unknown";
  }

  return "unknown";
}

// ── JSON Schema → Zod expression converter ───────────────────────────────────

/**
 * Convert a single JSON Schema node to a Zod expression string.
 * Uses passthrough() for objects with additionalProperties, strict() otherwise.
 * All schemas are referenced by their const-names so forward/back references
 * resolve at expression-evaluation time (they're all `export const` in scope).
 *
 * @param schema  JSON Schema node
 * @param indent  number of leading spaces for the outermost expression line
 */
function schemaToZod(schema, indent = 0) {
  if (!schema) return "z.unknown()";

  const pad = " ".repeat(indent);
  const propPad = " ".repeat(indent + 4);

  // $ref to sibling schema — reference the generated const name directly
  if (schema.$ref) {
    const parts = schema.$ref.split("/");
    return parts[parts.length - 1];
  }

  // const literal → z.literal(...)
  if (schema.const !== undefined) {
    return `z.literal(${JSON.stringify(schema.const)})`;
  }

  // enum → z.enum([...]) or z.literal(...) for single-value
  if (Array.isArray(schema.enum)) {
    if (schema.enum.length === 1) {
      return `z.literal(${JSON.stringify(schema.enum[0])})`;
    }
    return `z.enum([${schema.enum.map((v) => JSON.stringify(v)).join(", ")}])`;
  }

  const type = schema.type;

  // Primitives
  if (type === "boolean") return "z.boolean()";

  if (type === "integer") {
    let expr = "z.number().int()";
    if (schema.minimum !== undefined) expr += `.min(${schema.minimum})`;
    if (schema.maximum !== undefined) expr += `.max(${schema.maximum})`;
    return expr;
  }

  if (type === "number") {
    let expr = "z.number()";
    if (schema.minimum !== undefined) expr += `.min(${schema.minimum})`;
    if (schema.maximum !== undefined) expr += `.max(${schema.maximum})`;
    return expr;
  }

  if (type === "string") {
    let expr = "z.string()";
    if (schema.minLength !== undefined) expr += `.min(${schema.minLength})`;
    if (schema.maxLength !== undefined) expr += `.max(${schema.maxLength})`;
    return expr;
  }

  // Array
  if (type === "array") {
    const itemsExpr = schema.items ? schemaToZod(schema.items, indent) : "z.unknown()";
    let expr = `z.array(${itemsExpr})`;
    if (schema.minItems !== undefined) expr += `.min(${schema.minItems})`;
    if (schema.maxItems !== undefined) expr += `.max(${schema.maxItems})`;
    return expr;
  }

  // Object
  if (type === "object" || schema.properties) {
    const required = new Set(schema.required ?? []);
    const props = schema.properties ?? {};
    const hasAdditional =
      schema.additionalProperties === true ||
      (typeof schema.additionalProperties === "object" &&
        schema.additionalProperties !== false);

    if (Object.keys(props).length === 0) {
      // Empty object with no defined properties — treat as record
      if (schema.additionalProperties !== false) {
        return "z.record(z.unknown())";
      }
      return `z.object({})${hasAdditional ? ".passthrough()" : ".strict()"}`;
    }

    const propLines = [];
    for (const [key, propSchema] of Object.entries(props)) {
      let propExpr = schemaToZod(propSchema, indent + 2);
      if (!required.has(key)) {
        propExpr += ".optional()";
      }
      propLines.push(`${propPad}${key}: ${propExpr},`);
    }

    const closing = hasAdditional ? ".passthrough()" : ".strict()";
    return `z\n${pad}  .object({\n${propLines.join("\n")}\n${pad}  })\n${pad}  ${closing}`;
  }

  // Untyped — accept any value
  return "z.unknown()";
}

// ── Generate TypeScript output ───────────────────────────────────────────────

const lines = [
  "/**",
  " * This file was auto-generated from contracts/asyncapi.yaml.",
  " * Do not make direct changes to the file.",
  " * Re-run: node scripts/_gen-asyncapi-types.mjs",
  " */",
  "",
  "// ── WebSocket frame type discriminator ──────────────────────────────────────",
  "",
];

// Emit WsFrameType enum first
const wsFrameType = schemas["WsFrameType"];
if (wsFrameType?.enum) {
  lines.push("export type WsFrameType =");
  wsFrameType.enum.forEach((v, i) => {
    const sep = i === wsFrameType.enum.length - 1 ? ";" : " |";
    lines.push(`  | "${v}"`);
  });
  lines[lines.length - 1] = lines[lines.length - 1].replace(/^\s*\| /, "  | ") + ";";
  lines.push("");
}

lines.push("// ── Frame payload types ─────────────────────────────────────────────────────");
lines.push("");

// Emit all other schemas in definition order
const skipNames = new Set(["WsFrameType"]);
for (const [name, schema] of Object.entries(schemas)) {
  if (skipNames.has(name)) continue;

  const tsBody = schemaToTs(schema, 0, name);
  lines.push(`export interface ${name} ${tsBody}`);
  lines.push("");
}

// ── Union type of all frame types ────────────────────────────────────────────
// Collect names that are "frame" types (have a `type` discriminator property)
const frameNames = Object.keys(schemas).filter(
  (name) =>
    name !== "WsFrameType" &&
    schemas[name].properties?.type?.const !== undefined
);

lines.push(
  "// ── Union of all WS frames (discriminated by the `type` field) ──────────────"
);
lines.push("");
lines.push("export type WsFrame =");
frameNames.forEach((name, i) => {
  const sep = i === frameNames.length - 1 ? ";" : "";
  lines.push(`  | ${name}${sep}`);
});
lines.push("");

// ── Client→server frame union ─────────────────────────────────────────────────
const clientFrames = [
  "AuthFrame",
  "MessageFrame",
  "CancelFrame",
  "ExecApprovalResponseFrame",
  "PingFrame",
  "AttachSessionFrame",
  "DevicePairingResponseFrame",
];

lines.push("// ── Client → server frames ──────────────────────────────────────────────────");
lines.push("");
lines.push("export type ClientFrame =");
clientFrames.forEach((name, i) => {
  const sep = i === clientFrames.length - 1 ? ";" : "";
  lines.push(`  | ${name}${sep}`);
});
lines.push("");

// ── Server→client frame union ─────────────────────────────────────────────────
const serverFrames = frameNames.filter((n) => !clientFrames.includes(n));
lines.push("// ── Server → client frames ──────────────────────────────────────────────────");
lines.push("");
lines.push("export type ServerFrame =");
serverFrames.forEach((name, i) => {
  const sep = i === serverFrames.length - 1 ? ";" : "";
  lines.push(`  | ${name}${sep}`);
});
lines.push("");

const output = lines.join("\n");
writeFileSync(outPath, output, "utf8");
console.log(`Generated ${outPath} (${output.split("\n").length} lines)`);

// ── Generate Zod schemas output ───────────────────────────────────────────────
//
// Emits src/lib/api/generated/_asyncapi-zod-schemas.generated.ts
// This replaces the hand-written scripts/_asyncapi-zod-schemas.ts.
// The generated file is concatenated into schemas.ts by _gen-ts.sh.

// NOTE: This fragment file is concatenated into schemas.ts by _gen-ts.sh.
// It intentionally references `z` without importing it — the import lives in
// the OpenAPI-generated prefix of schemas.ts. `// @ts-nocheck` suppresses
// the standalone TypeScript "cannot find name 'z'" errors for this file.
const zodLines = [
  "// @ts-nocheck",
  "// Fragment — concatenated into schemas.ts by _gen-ts.sh. Do not import directly.",
  "",
  "// ── AsyncAPI WebSocket frame schemas ─────────────────────────────────────────",
  "// Auto-generated from contracts/asyncapi.yaml components.schemas.",
  "// Do not edit directly — re-run: node scripts/_gen-asyncapi-types.mjs",
  "// These extend the REST schemas above with all WS frame types.",
  "",
];

// Emit WsFrameType enum schema first
const wsFrameTypeSchema = schemas["WsFrameType"];
if (wsFrameTypeSchema?.enum) {
  const enumVals = wsFrameTypeSchema.enum.map((v) => JSON.stringify(v)).join(", ");
  zodLines.push(`export const WsFrameType = z.enum([${enumVals}]);`);
  zodLines.push("");
}

// Schemas that need passthrough (contain additionalProperties or are free-form)
// Emitted in definition order; forward refs resolved by hoisting the const.
for (const [name, schema] of Object.entries(schemas)) {
  if (name === "WsFrameType") continue;
  const zodExpr = schemaToZod(schema, 0);
  zodLines.push(`export const ${name} = ${zodExpr};`);
  zodLines.push("");
}

// ── WS frame discriminated union ─────────────────────────────────────────────

zodLines.push("// ── WS frame discriminated union ─────────────────────────────────────────────");
zodLines.push("");
zodLines.push("export const WsFrame = z.discriminatedUnion(\"type\", [");
frameNames.forEach((name) => {
  zodLines.push(`  ${name},`);
});
zodLines.push("]);");
zodLines.push("");
zodLines.push("export type WsFrameType = z.infer<typeof WsFrameType>;");
zodLines.push("export type WsFrame = z.infer<typeof WsFrame>;");

const zodOutput = zodLines.join("\n");
writeFileSync(zodOutPath, zodOutput, "utf8");
console.log(`Generated ${zodOutPath} (${zodOutput.split("\n").length} lines)`);

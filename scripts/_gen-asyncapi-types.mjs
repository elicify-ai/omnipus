/**
 * _gen-asyncapi-types.mjs
 *
 * Generates three files from contracts/asyncapi.yaml:
 *
 *   1. src/lib/api/generated/asyncapi-types.ts
 *      TypeScript interfaces for every AsyncAPI component schema.
 *
 *   2. src/lib/api/generated/_asyncapi-zod-schemas.generated.ts
 *      Zod runtime schemas for every AsyncAPI component schema.
 *
 *   3. src/lib/api/generated/llm-error-messages.ts
 *      The LLMError user-facing copy catalogue, from the x-user-messages
 *      extension on components.schemas.LLMError. src/lib/llm-error.ts consumes
 *      it instead of hand-maintaining `codeToDisplay`; the Go half of the same
 *      catalogue is emitted by scripts/gen-asyncapi-go from the same block, so
 *      the chat bubble and the persisted transcript cannot drift apart.
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

// Resolve the contracts directory. Always prefer the worktree's own contracts/
// dir first (important when running from a git worktree with local changes).
// Falls back to the main repo root via --git-common-dir for cases where the
// contracts dir only lives in the main repo.
function resolveContractsDir() {
  // Primary: worktree-local contracts/ dir (handles local schema changes in worktrees)
  const localContracts = join(ROOT, "contracts");
  if (existsSync(localContracts)) return localContracts;

  // Secondary: main repo root via git
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
  for (const rel of ["../contracts", "../../contracts", "../../../contracts"]) {
    const p = resolve(ROOT, rel);
    if (existsSync(p)) return p;
  }
  throw new Error("contracts/ directory not found");
}

const CONTRACTS_DIR = resolveContractsDir();

const asyncapiPath = resolve(CONTRACTS_DIR, "asyncapi.yaml");
const outPath = resolve(ROOT, "src/lib/api/generated/asyncapi-types.ts");
const zodOutPath = resolve(ROOT, "src/lib/api/generated/_asyncapi-zod-schemas.generated.ts");
const messagesOutPath = resolve(ROOT, "src/lib/api/generated/llm-error-messages.ts");

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
    if (schema.pattern !== undefined) expr += `.regex(/${schema.pattern}/)`;
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
    // NOTE: a top-level `anyOf` sitting alongside `properties`/`type: object`
    // (e.g. MessageFrame's "content non-empty OR media non-empty") is
    // deliberately NOT folded in here — see emitSchemaConst below, which
    // splits such a schema into a plain `<Name>Base` object (used inside
    // z.discriminatedUnion, which requires plain ZodObject members) plus the
    // publicly-exported, .refine()-validated `<Name>`.
    return `z\n${pad}  .object({\n${propLines.join("\n")}\n${pad}  })\n${pad}  ${closing}`;
  }

  // Untyped — accept any value
  return "z.unknown()";
}

/**
 * Translate one JSON Schema `anyOf` branch (a partial object schema with
 * `required` and/or `properties` carrying minLength/minItems/maxLength/
 * maxItems) into a JS boolean expression string testing a runtime value
 * `v`. This is intentionally narrow — it covers exactly the constraint
 * keywords schemaToZod itself understands for primitives, not general JSON
 * Schema — but is not tied to any specific schema's field names, so it
 * composes correctly for any object+anyOf schema this generator is handed.
 */
function anyOfBranchPredicate(branch) {
  const required = new Set(branch.required ?? []);
  const props = branch.properties ?? {};
  const conditions = [];

  for (const [key, propSchema] of Object.entries(props)) {
    const accessor = `v[${JSON.stringify(key)}]`;
    if (propSchema.minLength !== undefined) {
      conditions.push(`(typeof ${accessor} === "string" && ${accessor}.length >= ${propSchema.minLength})`);
    }
    if (propSchema.minItems !== undefined) {
      conditions.push(`(Array.isArray(${accessor}) && ${accessor}.length >= ${propSchema.minItems})`);
    }
    if (propSchema.maxLength !== undefined) {
      conditions.push(`(typeof ${accessor} !== "string" || ${accessor}.length <= ${propSchema.maxLength})`);
    }
    if (propSchema.maxItems !== undefined) {
      conditions.push(`(!Array.isArray(${accessor}) || ${accessor}.length <= ${propSchema.maxItems})`);
    }
  }
  // A required key with no other constraint listed above still needs an
  // existence check; a required key that DOES have a min constraint above
  // already implies existence (an undefined value fails typeof/Array.isArray).
  for (const key of required) {
    if (!(key in props)) {
      conditions.push(`v[${JSON.stringify(key)}] !== undefined`);
    }
  }

  return conditions.length > 0 ? conditions.join(" && ") : "true";
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
  "PingFrame",
  "AttachSessionFrame",
  "DevicePairingResponseFrame",
  "SessionCloseFrame",
  "WhatsAppPairingSubscribeFrame",
  // AskUserQuestion card submission/cancel (askuserquestion-tool-spec v3 §3)
  // — client (SPA) → server on the chat channel.
  "AskUserAnswerFrame",
  // Browser live channel (ADR-038) — client → server frames.
  "BrowserAttachFrame",
  "BrowserInputFrame",
  "BrowserControlFrame",
  "BrowserDetachFrame",
  // Browser WebRTC signaling (ADR-047 D1/D4) — client (SPA) → server frame
  // on the SPA-facing `browser` channel.
  "BrowserWebRTCOfferFrame",
  // NOTE: BrowserCapture*Frame schemas (browser_capture_hello/offer/answer/
  // control) belong to the loopback-only browserCaptureIngest channel
  // between the gateway and the capture extension's encoder page — the SPA
  // never connects to that channel. This script has no per-channel scoping
  // (it unions every components.schemas entry into one flat WsFrame/
  // ServerFrame/ClientFrame set regardless of channel, same as every prior
  // schema), so those 4 names are NOT listed here — deliberately, so they
  // fall into `serverFrames` below and are therefore NEVER treated as a
  // valid client-direction type the SPA's own outbound path could construct.
  // They still appear as inert exported types/Zod schemas in the generated
  // SPA files (dead code, never imported by ws.ts/browserLiveWs.ts) because
  // the generator has no "exclude from SPA channels" bucket — see ADR-047
  // W1-D notes.
];

lines.push("// ── Client → server frames ──────────────────────────────────────────────────");
lines.push("");
lines.push("export type ClientFrame =");
clientFrames.forEach((name, i) => {
  const sep = i === clientFrames.length - 1 ? ";" : "";
  lines.push(`  | ${name}${sep}`);
});
lines.push("");

// ── ClientFrameTypes constant — derived from clientFrames, not hand-written ───
// This constant is the single source of truth for the set of type-discriminator
// strings that identify client→server frames. ws.ts must import this constant
// and construct CLIENT_FRAME_TYPES from it rather than maintaining a hand-written
// literal set. Emitted as `as const` so TypeScript narrows to a tuple of literals.
const clientFrameTypeValues = clientFrames.map((name) => {
  const schema = schemas[name];
  const typeConst = schema?.properties?.type?.const;
  if (typeConst === undefined) {
    throw new Error(`clientFrames entry "${name}" has no properties.type.const in asyncapi.yaml`);
  }
  return typeConst;
});

lines.push("// ── ClientFrameTypes constant — generated from spec, not hand-written ─────────");
lines.push("// Import this in ws.ts to build CLIENT_FRAME_TYPES set. Never edit directly.");
lines.push("");
lines.push(`export const ClientFrameTypes = [${clientFrameTypeValues.map((v) => JSON.stringify(v)).join(", ")}] as const`);
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

// discriminatedUnionBaseNames maps a schema name to the plain-ZodObject
// const name that must stand in for it inside z.discriminatedUnion (which
// requires every member to be a ZodObject, not a ZodEffects) — populated
// below for any schema that has a top-level `anyOf` alongside its object
// properties (e.g. MessageFrame). Every other frame name maps to itself.
const discriminatedUnionBaseNames = new Map();

// Schemas that need passthrough (contain additionalProperties or are free-form)
// Emitted in definition order; forward refs resolved by hoisting the const.
for (const [name, schema] of Object.entries(schemas)) {
  if (name === "WsFrameType") continue;
  const baseExpr = schemaToZod(schema, 0);
  const hasObjectAnyOf =
    Array.isArray(schema.anyOf) &&
    schema.anyOf.length > 0 &&
    (schema.type === "object" || schema.properties);

  if (!hasObjectAnyOf) {
    zodLines.push(`export const ${name} = ${baseExpr};`);
    zodLines.push("");
    continue;
  }

  // Split into a plain `<Name>Base` object (the z.discriminatedUnion member)
  // plus the publicly-exported `<Name>` — the SAME name every existing
  // consumer already imports — now additionally enforcing the anyOf
  // cross-field invariant via .refine(). This is a transparent upgrade: no
  // consumer needs to change which name it imports.
  const baseName = `${name}Base`;
  discriminatedUnionBaseNames.set(name, baseName);
  const branchExprs = schema.anyOf.map((branch) => anyOfBranchPredicate(branch));
  const predicate = branchExprs.map((b) => `(${b})`).join(" || ");
  zodLines.push(`export const ${baseName} = ${baseExpr};`);
  zodLines.push("");
  zodLines.push(`export const ${name} = ${baseName}.refine((v) => ${predicate}, {`);
  zodLines.push(`  message: "does not satisfy the schema's anyOf constraint",`);
  zodLines.push(`});`);
  zodLines.push("");
}

// ── WS frame discriminated union ─────────────────────────────────────────────

zodLines.push("// ── WS frame discriminated union ─────────────────────────────────────────────");
zodLines.push("");
zodLines.push("export const WsFrame = z.discriminatedUnion(\"type\", [");
frameNames.forEach((name) => {
  const memberName = discriminatedUnionBaseNames.get(name) ?? name;
  zodLines.push(`  ${memberName},`);
});
zodLines.push("]);");
zodLines.push("");
zodLines.push("export type WsFrameType = z.infer<typeof WsFrameType>;");
zodLines.push("export type WsFrame = z.infer<typeof WsFrame>;");

const zodOutput = zodLines.join("\n");
writeFileSync(zodOutPath, zodOutput, "utf8");
console.log(`Generated ${zodOutPath} (${zodOutput.split("\n").length} lines)`);

// ── Generate the LLMError user-facing copy catalogue ─────────────────────────
//
// Emits src/lib/api/generated/llm-error-messages.ts from the x-user-messages /
// x-user-message-attributions extensions on components.schemas.LLMError.
//
// Every check below THROWS. A code with no message, a catalogue entry for a
// code that is not in the enum, an empty message, or an attribution outside the
// declared vocabulary aborts codegen instead of shipping a catalogue with a
// hole in it. The Go emitter (scripts/gen-asyncapi-go/usermessages.go) applies
// the identical validation to the identical block, so the two halves of the
// catalogue are exhaustive and consistent by construction rather than by review.

/**
 * Read and validate the LLMError copy catalogue out of the parsed contract.
 * Returns { attributions, entries } with entries in contract (enum) order.
 */
function extractUserMessageCatalogue(allSchemas, schemaName) {
  const schema = allSchemas[schemaName];
  if (!schema) throw new Error(`missing components.schemas.${schemaName}`);

  const codes = schema.properties?.code?.enum;
  if (!Array.isArray(codes) || codes.length === 0) {
    throw new Error(`${schemaName}.properties.code.enum: must be a non-empty list`);
  }

  const attributions = schema["x-user-message-attributions"];
  if (!Array.isArray(attributions) || attributions.length === 0) {
    throw new Error(`${schemaName}.x-user-message-attributions: must declare at least one attribution`);
  }
  const allowed = new Set();
  for (const a of attributions) {
    if (typeof a !== "string") {
      throw new Error(`${schemaName}.x-user-message-attributions: expected strings, got ${typeof a}`);
    }
    if (allowed.has(a)) {
      throw new Error(`${schemaName}.x-user-message-attributions: duplicate attribution "${a}"`);
    }
    allowed.add(a);
  }

  const rawMessages = schema["x-user-messages"];
  if (!rawMessages || typeof rawMessages !== "object" || Array.isArray(rawMessages)) {
    throw new Error(
      `${schemaName}: missing or malformed x-user-messages (want a mapping of code → {message, attribution})`,
    );
  }

  const entries = [];
  const seen = new Set();
  for (const code of codes) {
    const entry = rawMessages[code];
    if (!entry || typeof entry !== "object") {
      throw new Error(
        `${schemaName}.x-user-messages: no entry for code "${code}" — every value of the code enum needs a message and an attribution`,
      );
    }
    if (typeof entry.message !== "string" || entry.message.trim() === "") {
      throw new Error(`${schemaName}.x-user-messages.${code}: message must be a non-empty string`);
    }
    if (!allowed.has(entry.attribution)) {
      throw new Error(
        `${schemaName}.x-user-messages.${code}: attribution "${entry.attribution}" is not in x-user-message-attributions (${attributions.join(", ")})`,
      );
    }
    entries.push({ code, message: entry.message, attribution: entry.attribution });
    seen.add(code);
  }

  const orphans = Object.keys(rawMessages).filter((c) => !seen.has(c)).sort();
  if (orphans.length > 0) {
    throw new Error(`${schemaName}.x-user-messages: entries for codes not in the enum: ${orphans.join(", ")}`);
  }

  return { attributions, entries };
}

const catalogue = extractUserMessageCatalogue(schemas, "LLMError");

const messageLines = [
  "/**",
  " * This file was auto-generated from contracts/asyncapi.yaml",
  " * (components.schemas.LLMError → x-user-messages).",
  " * Do not make direct changes to the file.",
  " * Re-run: node scripts/_gen-asyncapi-types.mjs",
  " *",
  " * The user-facing copy and the fault attribution for every LLMError code are",
  " * contract data, not code. Edit the x-user-messages block in",
  " * contracts/components/schemas/LLMError.yaml (and its three sibling copies)",
  " * and re-run `make gen-contracts`. The Go half of this catalogue,",
  " * pkg/api/generated/llm_error_messages.gen.go, is emitted from the same block.",
  " */",
  "",
  'import type { LLMError } from "./asyncapi-types"',
  "",
  "/** Every wire-stable LLMError code, as a union. */",
  "export type LLMErrorCode = LLMError[\"code\"]",
  "",
  "/**",
  " * Who owns the fault behind a code, so the UI (and a copy test) can tell an",
  " * upstream failure from one Omnipus caused.",
  " */",
  "export type LLMErrorAttribution =",
  ...catalogue.attributions.map((a) => `  | ${JSON.stringify(a)}`),
  "",
  "/** The closed attribution vocabulary, in contract order. */",
  `export const llmErrorAttributionValues = [${catalogue.attributions.map((a) => JSON.stringify(a)).join(", ")}] as const`,
  "",
  "/** Every LLMError code, in contract (enum) order. */",
  `export const llmErrorCodes = [${catalogue.entries.map((e) => JSON.stringify(e.code)).join(", ")}] as const`,
  "",
  "/**",
  " * The sentence a user sees for each code. Exhaustive by construction: codegen",
  " * aborts if any code lacks an entry, and the `Record<LLMErrorCode, string>`",
  " * annotation makes a stale catalogue a `tsc -b --noEmit` failure.",
  " */",
  "export const llmErrorUserMessages: Record<LLMErrorCode, string> = {",
  ...catalogue.entries.map((e) => `  ${e.code}: ${JSON.stringify(e.message)},`),
  "}",
  "",
  "/** The fault attribution for each code. Exhaustive by construction. */",
  "export const llmErrorUserAttributions: Record<LLMErrorCode, LLMErrorAttribution> = {",
  ...catalogue.entries.map((e) => `  ${e.code}: ${JSON.stringify(e.attribution)},`),
  "}",
  "",
];

const messagesOutput = messageLines.join("\n");
writeFileSync(messagesOutPath, messagesOutput, "utf8");
console.log(`Generated ${messagesOutPath} (${messagesOutput.split("\n").length} lines)`);

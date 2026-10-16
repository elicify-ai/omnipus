/**
 * asyncapi-card-error-runtime.test.mjs — #1343/#1329 fix round 1, architect
 * Decision A runtime half (evidence/queue-1329-fixround1-20261016/
 * architect-ruling.md).
 *
 * Loads the ACTUAL generated src/lib/api/generated/ws-schemas.ts — no
 * source-text inspection: the file is transpiled to CommonJS with the
 * already-installed typescript package and evaluated in this process, so
 * the exported Zod validators run exactly as the SPA runs them (src/lib/ws.ts
 * parseFrameSafe and src/workers/ws-parser.worker.ts both validate live
 * frames through the WsFrame discriminated union, and standalone imports
 * use the ErrorFrame export — BOTH exported validators must enforce the
 * dependencies, or the dependency never fires on the SPA's ingest path).
 *
 * ErrorFrame specificity lives ONLY in this file. The generic
 * dependentRequired converter coverage is
 * scripts/_gen-asyncapi-types.selftest.mjs.
 *
 * Expectations derive from the ruling and from
 * contracts/components/schemas/ErrorFrame.yaml — never from the current
 * generated output (which is the code under change): TODAY the generated
 * validators accept first_message_error without client_message_id and
 * reject any ask_user_card_id-bearing frame as an unknown key (.strict()),
 * so the dependency-negation and card-error-positive cases are the genuine
 * RED before the generator emits dependentRequired. The legacy controls
 * stay green throughout.
 *
 * Run: node --test scripts/asyncapi-card-error-runtime.test.mjs
 */

import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";
import assert from "node:assert/strict";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, "..");
const wsSchemasPath = join(repoRoot, "src", "lib", "api", "generated", "ws-schemas.ts");

// Resolve from the repo's node_modules, walking up so this also runs from a
// worktree whose node_modules live in the main checkout (the layout the
// generator's js-yaml resolver already handles).
function resolvePkg(name) {
  const req = createRequire(import.meta.url);
  try {
    return req(name);
  } catch (_) {
    // fall through to the walk-up
  }
  let dir = repoRoot;
  for (let hop = 0; hop < 6; hop += 1) {
    try {
      return req(join(dir, "node_modules", name));
    } catch (_) {
      dir = dirname(dir);
    }
  }
  throw new Error(`${name} not found — install dependencies at the repo root`);
}

const zod = resolvePkg("zod");
const ts = resolvePkg("typescript");

/**
 * Transpile the generated TS module to CommonJS and evaluate it in this
 * process, returning its exports. The generated file imports only zod; any
 * other require is a generation change this harness must not silently
 * absorb.
 */
function loadWsSchemas() {
  const source = readFileSync(wsSchemasPath, "utf8");
  const { outputText, diagnostics } = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
      esModuleInterop: true,
    },
    reportDiagnostics: true,
  });
  const fatal = (diagnostics ?? []).filter(
    (d) => d.category === ts.DiagnosticCategory.Error
  );
  if (fatal.length > 0) {
    throw new Error(
      `transpiling ${wsSchemasPath} failed: ` +
        fatal.map((d) => ts.flattenDiagnosticMessageText(d.messageText, "\n")).join("; ")
    );
  }
  const mod = { exports: {} };
  const localRequire = (name) => {
    if (name === "zod") return zod;
    throw new Error(`generated ws-schemas unexpectedly required "${name}"`);
  };
  new Function("require", "module", "exports", outputText)(
    localRequire,
    mod,
    mod.exports
  );
  return mod.exports;
}

const schemas = loadWsSchemas();

// Fixture constants mirror the Go pack: the card id is the ask_… hex shape
// the askuser fixtures use; the session id matches those fixtures too.
const cardId = "ask_0123456789abcdef0123456789abcdef";

/** Legacy ErrorFrame (ErrorFrame.yaml shape, no card id). */
const legacyError = () => ({
  type: "error",
  message: "Recording your answer failed — the card could not be updated.",
  session_id: "session_owner_1",
});

/** Decision A card-scoped error: card id plus the session_id it depends on. */
const cardError = () => ({ ...legacyError(), ask_user_card_id: cardId });

/** Card error with the session_id key deleted — the dependency violation. */
const cardErrorWithoutSession = () => {
  const frame = cardError();
  delete frame.session_id;
  return frame;
};

function assertValid(schema, label, fixture) {
  const result = schema.safeParse(fixture);
  assert.equal(
    result.success,
    true,
    `${label}: expected the frame to validate, got issues: ` +
      JSON.stringify(result.error?.issues ?? [])
  );
}

function assertInvalid(schema, label, fixture) {
  const result = schema.safeParse(fixture);
  assert.equal(
    result.success,
    false,
    `${label}: expected the frame to be rejected, but it parsed: ` +
      JSON.stringify(fixture)
  );
}

// Every case runs against BOTH exported validators — the standalone
// ErrorFrame export and the combined WsFrame discriminated union — because
// the SPA's live-frame path validates through WsFrame.
const validators = [
  ["ErrorFrame", () => schemas.ErrorFrame],
  ["WsFrame", () => schemas.WsFrame],
];

for (const [label, getSchema] of validators) {
  test(`${label}: legacy error frame without the new field is valid`, () => {
    assertValid(getSchema(), label, legacyError());
  });

  test(`${label}: card error frame with session_id is valid (Decision A)`, () => {
    assertValid(getSchema(), label, cardError());
  });

  test(`${label}: card error without session_id is rejected (dependency, after positive control)`, () => {
    // Positive control first: the identical frame WITH session_id parses,
    // so ask_user_card_id is a known property and the rejection below is
    // the dependency — never a strict-mode unknown-key rejection.
    assertValid(getSchema(), label, cardError());
    assertInvalid(getSchema(), label, cardErrorWithoutSession());
  });

  test(`${label}: empty ask_user_card_id is rejected (minLength 1)`, () => {
    const frame = cardError();
    frame.ask_user_card_id = "";
    assertInvalid(getSchema(), label, frame);
  });

  test(`${label}: non-string ask_user_card_id is rejected (type string)`, () => {
    const frame = cardError();
    frame.ask_user_card_id = 42;
    assertInvalid(getSchema(), label, frame);
  });

  test(`${label}: first_message_error without client_message_id is rejected (preserved dependency)`, () => {
    const frame = legacyError();
    frame.first_message_error = "not_saved"; // enum member, ErrorFrame.yaml
    assertInvalid(getSchema(), label, frame);
  });

  test(`${label}: first_message_error with client_message_id is valid`, () => {
    const frame = legacyError();
    frame.client_message_id = "cmid_01";
    frame.first_message_error = "not_saved";
    assertValid(getSchema(), label, frame);
  });

  test(`${label}: card error may carry client_message_id — no schema-side prohibition`, () => {
    // Decision A makes omitting client_message_id / first_message_error a
    // producer duty, not a schema rule: the approved YAML introduces no
    // exclusion for those properties.
    const frame = cardError();
    frame.client_message_id = "cmid_02";
    assertValid(getSchema(), label, frame);
  });
}

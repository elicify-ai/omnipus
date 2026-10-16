/**
 * _gen-asyncapi-types.selftest.mjs — tests-only helper for the GENERIC
 * dependentRequired support in scripts/_gen-asyncapi-types.mjs (#1343/#1329
 * fix round 1, architect Decision A).
 *
 * CORRECTION (qa-lead RED, 2026-10-16, on team-lead review of the authored
 * payload — replaces the first version; no expectation was weakened):
 *   1. The ordinary-frame fixture was missing its REQUIRED text property, so
 *      the "refined union still accepts an ordinary frame" assertion could
 *      never pass; the fixture now carries a valid text value.
 *   2. The first version placed schemaToZod(schema) outputs for DEPENDENT
 *      frames directly into z.discriminatedUnion as the unrefined control.
 *      Invalid instrument, not a production requirement: the brief requires
 *      schemaToZod to emit dependentRequired for top-level object schemas,
 *      so those emissions are refined ZodEffects — which
 *      z.discriminatedUnion rejects as members (it requires plain
 *      ZodObjects; the generator's own anyOf handling in
 *      _gen-asyncapi-types.mjs introduced the <Name>Base split for exactly
 *      this), and a full refined validator cannot simultaneously serve as
 *      the base-accepts-violations control. Corrected to TWO explicit forms
 *      per frame: <Name>Base from a shallow copy with ONLY top-level
 *      dependentRequired deleted (nested deps preserved), and <Name> from
 *      the intact schemaToZod(schema). baseUnionExpression references the
 *      plain <Name>Base consts; withFrameDependencies(…, frameSchemas)
 *      references the full named <Name> consts.
 *
 * The generator (backend-owned) wires its own converter into
 *
 *   runDependentRequiredSelfTests({ schemaToZod, withFrameDependencies, z })
 *
 * behind its --self-test flag, BEFORE normal generation. This module owns
 * the cases and the runtime oracle only; it imports nothing from the
 * generator, so running it performs no file writes. ErrorFrame specificity
 * lives ONLY in scripts/asyncapi-card-error-runtime.test.mjs — every schema
 * here is a generic mini frame.
 *
 * Contract for the injected API (any mismatch must fail loudly here, never
 * be papered over):
 *
 *   schemaToZod(schema, indent?) -> string
 *       The generator's JSON-Schema-node → Zod-expression converter,
 *       including its dependentRequired emission for object schemas at any
 *       depth (top-level, nested, array items).
 *
 *   withFrameDependencies(baseUnionExpression, frameSchemas) -> string
 *       Returns a REFINED UNION EXPRESSION STRING enforcing every named
 *       frame's top-level dependentRequired. baseUnionExpression is a
 *       z.discriminatedUnion expression over the plain <Name>Base consts;
 *       frameSchemas is an array of { name, schema } whose name is the FULL
 *       frame const name (<Name>) of the intact schemaToZod(schema)
 *       emission. The evaluation scope defines BOTH const sets — <Name>Base
 *       and <Name> — so the returned expression may reference either.
 *
 *   z
 *       The zod instance the emitted expressions evaluate against.
 *
 * Every emitted expression is compiled with Function("z", `return ${expr}`)(z)
 * and exercised through safeParse — no mock validators anywhere.
 *
 * Semantics under test (JSON Schema 2020-12 dependentRequired): PRESENCE of
 * the trigger property requires the siblings. The trigger's VALUE never
 * matters — including false, 0 and "" — and the requirement is strictly
 * one-directional: dependents without the trigger stay valid.
 *
 * Run via: node scripts/_gen-asyncapi-types.mjs --self-test
 */

import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import assert from "node:assert/strict";

// ── Mini frame schemas and fixtures (static data; ErrorFrame specificity
//    lives ONLY in the runtime test) ────────────────────────────────────────

const ordinarySchema = {
  type: "object",
  required: ["type", "text"],
  additionalProperties: false,
  properties: {
    type: { const: "ordinary" },
    text: { type: "string", minLength: 1 },
  },
};

// One trigger, one dependent.
const singleSchema = {
  type: "object",
  required: ["type"],
  additionalProperties: false,
  dependentRequired: { trigger: ["dependent"] },
  properties: {
    type: { const: "single" },
    trigger: { type: "string" },
    dependent: { type: "string" },
  },
};

// Multiple sibling requirements under one trigger; boolean trigger so the
// falsy-but-present probe (false) is schema-valid on the trigger itself.
const siblingsSchema = {
  type: "object",
  required: ["type"],
  additionalProperties: false,
  dependentRequired: { trigger: ["first_dep", "second_dep"] },
  properties: {
    type: { const: "siblings" },
    trigger: { type: "boolean" },
    first_dep: { type: "string" },
    second_dep: { type: "integer" },
  },
};

// Integer trigger so the falsy-but-present probe (0) is schema-valid.
const numericSchema = {
  type: "object",
  required: ["type"],
  additionalProperties: false,
  dependentRequired: { trigger: ["dependent"] },
  properties: {
    type: { const: "numeric" },
    trigger: { type: "integer" },
    dependent: { type: "string" },
  },
};

// Nested-object dependency, exercised through actual schemaToZod emission.
const nestedSchema = {
  type: "object",
  required: ["type", "outer"],
  additionalProperties: false,
  properties: {
    type: { const: "nested" },
    outer: {
      type: "object",
      additionalProperties: false,
      dependentRequired: { trigger: ["dependent"] },
      properties: {
        trigger: { type: "string" },
        dependent: { type: "string" },
      },
    },
  },
};

// Array-contained-object dependency, same way.
const arraySchema = {
  type: "object",
  required: ["type", "rows"],
  additionalProperties: false,
  properties: {
    type: { const: "rows" },
    rows: {
      type: "array",
      items: {
        type: "object",
        additionalProperties: false,
        dependentRequired: { trigger: ["dependent"] },
        properties: {
          trigger: { type: "integer" },
          dependent: { type: "string" },
        },
      },
    },
  },
};

/** Ordinary-frame instance — carries the required, non-empty text (correction 1). */
const ordinaryInstance = (overrides = {}) => ({
  type: "ordinary",
  text: "hello",
  ...overrides,
});

const instance = (kind, overrides = {}) => ({ type: kind, ...overrides });

// The base union expression references ONLY the plain <Name>Base consts —
// valid z.discriminatedUnion members (correction 2).
const BASE_UNION_EXPR =
  'z.discriminatedUnion("type", [OrdinaryFrameBase, SingleFrameBase, SiblingsFrameBase, NumericFrameBase])';

// ── Harness helpers ───────────────────────────────────────────────────────

/**
 * baseSchemaOf returns a deep copy of schema with ONLY its top-level
 * dependentRequired deleted — the plain-ZodObject <Name>Base form the union
 * needs as members. Nested dependentRequired survives the copy, so a Base
 * of a nested-dep schema still enforces the nested deps.
 */
function baseSchemaOf(schema) {
  const copy = structuredClone(schema);
  delete copy.dependentRequired;
  return copy;
}

/** Fails loudly on any injected-API mismatch — a skipped self-test is a green that verifies nothing. */
function assertInjectApi({ schemaToZod, withFrameDependencies, z }) {
  if (typeof schemaToZod !== "function") {
    throw new Error(
      "self-test API mismatch: schemaToZod must be a function — the generator must pass its own converter, not a reimplementation"
    );
  }
  if (typeof withFrameDependencies !== "function") {
    throw new Error(
      "self-test API mismatch: withFrameDependencies must be a function (baseUnionExpression, frameSchemas) -> refined union expression string"
    );
  }
  if (typeof z?.object !== "function" || typeof z?.discriminatedUnion !== "function") {
    throw new Error(
      "self-test API mismatch: z must be the zod instance the emitted expressions evaluate against"
    );
  }
}

/** Compiles an emitted expression and evaluates it against the injected z. */
function evalExpr(z, expr, label) {
  assert.equal(typeof expr, "string", `${label}: emitted expression must be a string, got ${typeof expr}`);
  return Function("z", `return (${expr})`)(z);
}

// ── Union-level cases ─────────────────────────────────────────────────────

/**
 * Compiles the <Name>Base and full <Name> consts from schemaToZod, builds
 * the plain base union, refines it via withFrameDependencies, and pins the
 * union-level dependentRequired semantics on both.
 */
function runUnionSelfTests({ schemaToZod, withFrameDependencies, z, check }) {
  const frameSchemas = [
    { name: "OrdinaryFrame", schema: ordinarySchema },
    { name: "SingleFrame", schema: singleSchema },
    { name: "SiblingsFrame", schema: siblingsSchema },
    { name: "NumericFrame", schema: numericSchema },
  ];
  const body = [
    ...frameSchemas.map(
      ({ name, schema }) => `const ${name}Base = ${schemaToZod(baseSchemaOf(schema))};`
    ),
    ...frameSchemas.map(({ name, schema }) => `const ${name} = ${schemaToZod(schema)};`),
    `const base = ${BASE_UNION_EXPR};`,
    `const refined = ${withFrameDependencies(BASE_UNION_EXPR, frameSchemas)};`,
    "return { base, refined };",
  ].join("\n");
  const { base, refined } = Function("z", body)(z);

  // The UNREFINED base union (plain <Name>Base members) must ACCEPT
  // dependency-violating instances — proving withFrameDependencies'
  // refinement is what enforces the dependencies at all.
  check("base union accepts a trigger without its dependent (single)", base.safeParse(instance("single", { trigger: "x" })).success === true);
  check("base union accepts one missing sibling (siblings)", base.safeParse(instance("siblings", { trigger: true, first_dep: "a" })).success === true);

  // Ordinary frame is unaffected by the refinement — no over-tightening.
  check("refined union still accepts an ordinary frame", refined.safeParse(ordinaryInstance()).success === true);

  // Absent trigger: no requirement fires.
  check("absent trigger does not require the dependent (single)", refined.safeParse(instance("single")).success === true);
  check("absent trigger does not require the siblings (siblings)", refined.safeParse(instance("siblings")).success === true);

  // One-directionality: dependents without the trigger stay valid.
  check("dependent without trigger is valid — dependency is one-directional", refined.safeParse(instance("single", { dependent: "y" })).success === true);

  // Satisfied dependencies validate.
  check("satisfied single dependency validates", refined.safeParse(instance("single", { trigger: "x", dependent: "y" })).success === true);
  check("satisfied sibling dependencies validate", refined.safeParse(instance("siblings", { trigger: true, first_dep: "a", second_dep: 2 })).success === true);

  // Present trigger with missing dependents is rejected.
  check("present trigger requires the dependent (single)", refined.safeParse(instance("single", { trigger: "x" })).success === false);
  check("present trigger requires ALL siblings — one missing is invalid", refined.safeParse(instance("siblings", { trigger: true, first_dep: "a" })).success === false);
  check("present trigger requires ALL siblings — other one missing is invalid", refined.safeParse(instance("siblings", { trigger: true, second_dep: 2 })).success === false);

  // PRESENCE, not truthiness: false / 0 / "" trigger values are present, so
  // they require the dependents. A truthiness-based implementation
  // (`if (obj.trigger)`) wrongly skips these.
  check('trigger value "" is present — dependency applies', refined.safeParse(instance("single", { trigger: "" })).success === false);
  check('trigger value "" with dependent satisfied validates', refined.safeParse(instance("single", { trigger: "", dependent: "y" })).success === true);
  check("trigger value false is present — dependency applies", refined.safeParse(instance("siblings", { trigger: false })).success === false);
  check("trigger value false with siblings satisfied validates", refined.safeParse(instance("siblings", { trigger: false, first_dep: "a", second_dep: 1 })).success === true);
  check("trigger value 0 is present — dependency applies", refined.safeParse(instance("numeric", { trigger: 0 })).success === false);
  check("trigger value 0 with dependent satisfied validates", refined.safeParse(instance("numeric", { trigger: 0, dependent: "y" })).success === true);
}

// ── Object-graph cases (nested objects, array items) ──────────────────────

/**
 * Pins dependentRequired inside nested object properties and array items —
 * both through the actual schemaToZod emission of the full intact schemas.
 */
function runObjectGraphSelfTests({ schemaToZod, z, check }) {
  const nested = evalExpr(z, schemaToZod(nestedSchema), "nested schema");
  check("nested: absent trigger is valid", nested.safeParse({ type: "nested", outer: {} }).success === true);
  check("nested: satisfied dependency is valid", nested.safeParse({ type: "nested", outer: { trigger: "x", dependent: "y" } }).success === true);
  check("nested: present trigger requires the dependent", nested.safeParse({ type: "nested", outer: { trigger: "x" } }).success === false);
  check("nested: falsy-but-present trigger still requires the dependent", nested.safeParse({ type: "nested", outer: { trigger: "" } }).success === false);

  const rows = evalExpr(z, schemaToZod(arraySchema), "array schema");
  check("array: empty rows are valid", rows.safeParse({ type: "rows", rows: [] }).success === true);
  check("array: absent trigger and satisfied dependency are valid", rows.safeParse({ type: "rows", rows: [{}, { trigger: 0, dependent: "y" }] }).success === true);
  check("array: element with falsy-but-present trigger requires its dependent", rows.safeParse({ type: "rows", rows: [{ trigger: 0 }] }).success === false);
  check("array: one violating element rejects the whole array", rows.safeParse({ type: "rows", rows: [{}, { trigger: 1 }] }).success === false);
}

// ── Entry point (the agreed API) ──────────────────────────────────────────

/**
 * Run all generic dependentRequired self-tests against the generator's own
 * converter. Returns the number of passed checks; throws (non-zero exit via
 * the generator's --self-test caller) on the first failure or API mismatch.
 */
export function runDependentRequiredSelfTests(injected = {}) {
  assertInjectApi(injected);
  const { schemaToZod, withFrameDependencies, z } = injected;

  let passed = 0;
  const check = (label, condition) => {
    assert.ok(condition, label);
    passed += 1;
  };

  runUnionSelfTests({ schemaToZod, withFrameDependencies, z, check });
  runObjectGraphSelfTests({ schemaToZod, z, check });

  console.log(`dependentRequired self-tests: ${passed} cases OK`);
  return passed;
}

// Invoked directly (instead of through the generator's --self-test flag):
// say so and fail, rather than registering nothing and exiting 0.
const invoked = process.argv[1]
  ? import.meta.url === pathToFileURL(resolve(process.argv[1])).href
  : false;
if (invoked) {
  console.error(
    "scripts/_gen-asyncapi-types.selftest.mjs is a helper module — run: node scripts/_gen-asyncapi-types.mjs --self-test"
  );
  process.exit(1);
}

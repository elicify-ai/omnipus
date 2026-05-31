# ADR-012 — OpenAPI 3.0.3 vs 3.1.0 Trade-off

**Status:** Accepted
**Date:** 2026-05-18
**Deciders:** architect, backend-lead, frontend-lead

---

## Context

Hard-constraint #8 makes `contracts/openapi.yaml` the single source of truth
for every REST request and response that crosses the gateway/SPA boundary.
Go types under `pkg/api/generated/` and TypeScript types + Zod schemas under
`src/lib/api/generated/` are produced from this file by `oapi-codegen`
(Go), `openapi-typescript` (TS types), and `openapi-zod-client` (Zod).

Phase 7 fix-R pinned the spec's `openapi:` version to `3.0.3` after an
attempt to use 3.1.0-only features produced unstable `oapi-codegen` output —
specifically, missing or malformed Go types on the next codegen run. The
question for this ADR is: do we leave the spec on `3.0.3`, or do we upgrade
to `3.1.0` and accept the codegen risk?

The expressiveness gap between the two versions matters because the
codebase has at least two real schemas where 3.1.0 would let us encode an
invariant directly in the schema:

1. **`ExecProxyStatus`** — when `running: true`, the `address` field is
   logically required, but optional otherwise. In 3.1.0, JSON Schema 2020-12
   `if/then/else` lets us encode this as a conditional schema. In 3.0.3, we
   must mark `address` as `optional` at the type level and enforce the
   invariant either inside the handler or via a `description:` prose
   warning.

2. **`ToolCallResultFrame.result`** — the result can be a primitive, an
   object, or an array. In 3.1.0, `type: [object, array, string, number,
   boolean, "null"]` is valid and emits a clean union type. In 3.0.3, the
   only way to express this is `oneOf:` with each branch listed
   individually, which `oapi-codegen` translates into a sum type with
   discrimination boilerplate.

Other 3.1.0 features that we surveyed but did not find load-bearing today:

- `$dynamicRef` (used for recursive polymorphic schemas — not needed here).
- `unevaluatedProperties` (stronger version of `additionalProperties` — see
  ADR-014).
- `prefixItems` (tuple schemas — we don't have tuples in wire types yet).
- `examples` as a JSON Schema array rather than the OpenAPI 3.0 single
  `example` field — cosmetic, not blocking.

`oapi-codegen` v2 (current pin: `v2.x`) supports 3.0.3 stably. Its 3.1.0
support is documented as experimental: the README explicitly calls out that
not all 3.1.0-only constructs round-trip cleanly, and Phase 7 fix-R's
attempt confirmed this with a reproducible breakage on `oneOf` + nullable
arrays.

---

## Decision

`contracts/openapi.yaml` stays pinned to `openapi: 3.0.3`.

The two real-world expressiveness gaps are accepted and mitigated as
follows:

| Gap | Mitigation |
|---|---|
| `ExecProxyStatus.address` conditional required | Schema marks `address` optional. Handler enforces `running ⇒ address != ""` and returns a typed validation error. The schema `description:` documents the invariant in prose so SPA consumers and external clients see it. |
| `ToolCallResultFrame.result` polymorphic value | Decompose into a `oneOf` discriminated union with explicit branches per result kind, OR (where the SPA is the only consumer) emit `additionalProperties: true` on a wrapper object and validate the shape at the SPA edge with a Zod refinement that lives next to the generated schema. |

The choice between the two `result` mitigations is per-schema and made by
the schema author; the rule is that the chosen mitigation MUST be
documented in the schema file's `description:` block, not left implicit.

---

## Rationale

- **Stability over expressiveness.** `oapi-codegen` v2 produces byte-stable
  Go output for 3.0.3. Across hundreds of `make gen-contracts` runs in CI
  there has been zero non-determinism. 3.1.0 support is experimental, and
  the codebase relies on `verify-contracts` failing on drift — any
  non-determinism in codegen would manifest as flaky CI.

- **The expressiveness gap is small and mitigable.** Both cases where we
  lose direct schema-level expressiveness have viable, well-understood
  workarounds (handler-side enforcement + prose, `oneOf` decomposition).
  Neither workaround leaks complexity into the wire format itself.

- **Cost of an upgrade is non-trivial.** Moving to 3.1.0 means re-running
  every codegen step, reviewing the diff across `pkg/api/generated/` and
  `src/lib/api/generated/`, re-validating that every Zod schema still
  matches its Go counterpart, and updating the contract test
  (`pkg/api/generated/contract_test.go`) to handle whatever new shapes
  3.1.0 emits. For the two schemas where it helps, the win does not
  justify that churn.

- **Ecosystem alignment.** Most production REST APIs in 2026 still ship
  3.0.x specs. Tooling (Swagger UI, Stoplight, Redoc, Postman) all support
  3.0.3 perfectly; 3.1.0 support is consistently described as "supported
  but with edge cases." Sticking with 3.0.3 keeps consumer tooling friction
  zero.

---

## Consequences

### Positive

- Codegen remains deterministic. `make gen-contracts` produces zero diff on
  a clean tree, every run, full stop. CI's `verify-contracts` gate stays
  meaningful.
- Consumer tooling (Swagger UI, generated SDK clients in other languages)
  works without caveats.
- Schema authors are forced to make invariants explicit either at the
  handler layer or via discriminated unions, which is good documentation
  hygiene regardless of OpenAPI version.

### Negative

- Cannot express conditional-required fields (`if/then`) at the schema
  level. Handler-side enforcement is invisible from the spec alone — a
  client reading the spec sees `address` as optional and may submit
  `running: true` without it, only to receive a 400. The
  `description:` prose mitigates this but does not prevent it.
- `type: [object, array, ...]` polymorphism is unavailable. `oneOf` is more
  verbose and produces heavier generated Go (sum types with
  marshal/unmarshal helpers).
- The schema cannot use `$dynamicRef` for recursive polymorphism. Not
  currently needed; flagged here in case a future feature requires it.

### Neutral

- Re-visit cost is bounded: when we do upgrade, the diff is contained to
  the two-to-three schemas with workarounds plus whatever the codegen
  emits across the generated files. There is no "big bang" migration risk.

---

## Alternatives Considered

### A. Upgrade to OpenAPI 3.1.0 now

- Pros: Direct schema expressiveness for the two known cases; future-proofs
  the spec for `$dynamicRef` / `unevaluatedProperties` if we ever need them.
- Cons: `oapi-codegen` v2 3.1.0 support is experimental; Phase 7 fix-R
  reproduced breakage. Codegen non-determinism would erode the
  `verify-contracts` gate.
- **Rejected** until `oapi-codegen` v3 stabilises (see trigger below).

### B. Hybrid: keep 3.0.3 for the main spec, use a separate 3.1.0 spec for the affected schemas

- Pros: Limits the experimental surface to only the schemas that benefit.
- Cons: Two codegen pipelines, two source-of-truth files, twice the lint
  surface. Doubles the complexity of `scripts/gen-contracts.sh` without
  delivering proportionate value.
- **Rejected** as more operational cost than the upgrade itself.

### C. Drop OpenAPI, hand-write schemas in JSON Schema 2020-12 + custom codegen

- Pros: Full JSON Schema expressiveness.
- Cons: Loses Swagger UI, loses the entire OpenAPI ecosystem, requires
  writing and maintaining a custom codegen pipeline. Massive scope.
- **Rejected** out of hand.

---

## Trigger to Revisit

This ADR should be re-opened when **any** of the following hold:

1. **`oapi-codegen` v3 reaches stable** and its release notes affirm full
   3.1.0 support (specifically: deterministic Go output across `oneOf`,
   nullable arrays, and `if/then`).
2. **A schema-level conditional invariant becomes load-bearing** — for
   example, a security-critical request shape where prose documentation is
   demonstrably insufficient and where handler-side enforcement creates a
   confusing API for external SDK consumers.
3. **`openapi-typescript` or `openapi-zod-client` drops 3.0.3 support** and
   3.1.0 becomes the only path to keep TS codegen working.

Until then, the cost-benefit favours staying on 3.0.3.

---

## Affected Components

- `contracts/openapi.yaml` — `openapi: 3.0.3` pinned.
- `contracts/components/schemas/ExecProxyStatus.yaml` — handler-enforced
  invariant; `description:` documents the rule.
- `contracts/components/schemas/ToolCallResultFrame.yaml` — `oneOf`
  decomposition with discriminator.
- `scripts/gen-contracts.sh` — no version-specific logic; the script is
  agnostic.
- `pkg/api/generated/` and `src/lib/api/generated/` — generated output
  unaffected by this decision (would change shape if we upgraded).

---

## References

- `CLAUDE.md` hard-constraint #8 — contract-first wire formats.
- Phase 7 fix-R — pinning to 3.0.3, breakage reproduction.
- `oapi-codegen` README — 3.1.0 experimental-support note.
- ADR-013 — inbound validation strategy (uses the same JSON Schema
  components).
- ADR-014 — `additionalProperties` policy.
- ADR-015 — `decodeAndValidate` pipeline.
- OpenAPI 3.1.0 release notes:
  https://www.openapis.org/blog/2021/02/16/migrating-from-openapi-3-0-to-3-1-0

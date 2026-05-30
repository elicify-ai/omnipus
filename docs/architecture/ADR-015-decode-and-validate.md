# ADR-015 — `decodeAndValidate` Pipeline Contract

**Status:** Accepted
**Date:** 2026-05-18
**Deciders:** architect, backend-lead

---

## Context

ADR-013 establishes inbound validation as the symmetric counterpart to
the SPA's Zod edge. Every REST handler that accepts a JSON body needs to:

1. Read the body within a sane size limit (to bound memory).
2. Decode the JSON into a Go struct (the generated request type).
3. If validation is enabled, validate the decoded payload against the
   JSON Schema named for this endpoint.
4. On any failure (read error, decode error, validation error, schema
   compile error), write a well-formed HTTP error response and return
   a signal to the caller to stop processing.

If every handler implements this pipeline locally, three problems arise:

- **Inconsistency.** Body-size limits, decode error formatting,
  validation error formatting, and HTTP status mapping drift across
  handlers.
- **Skip risk.** A handler can forget to call validation entirely, or
  accidentally validate the wrong schema, with no detection at
  build time.
- **Cost of adding validation.** Wiring a new endpoint becomes a
  multi-line ritual; engineers will copy-paste and the ritual will
  drift.

We need a single helper that every handler calls in one line, with the
schema-name string as the only per-handler parameter.

---

## Decision

Every REST handler that accepts a JSON body calls a single helper:

```go
var req SomeRequestType
if err := decodeAndValidate(w, r, "SomeRequestSchema", &req, validateEnabled); err != nil {
    return // response already written by the helper
}
// req is now safe to use
```

The helper has the following contract:

| Aspect | Specification |
|---|---|
| **Body size limit** | `io.LimitReader(r.Body, 1<<20)` — 1 MiB hard ceiling. |
| **Decode** | `json.NewDecoder(limited).Decode(dst)`. On error, write `400 Bad Request` with message `"invalid JSON: <decoder error>"`. |
| **Schema lookup** | Schema name string maps 1:1 to `pkg/gateway/inboundschemas/<SchemaName>.yaml`. Lookup uses the boot-time pre-compiled validator pool. |
| **Validation** | Only if `validateEnabled` is true. On failure, write `400 Bad Request` with message `"request body failed schema validation: <SchemaName>: <first violation>"`. |
| **Schema compile failure at request time** | Write `500 Internal Server Error`. This path should be unreachable because of boot pre-compile; reaching it indicates a contract bug or a hot-loaded schema bypassing pre-compile. |
| **Return value** | `nil` on success (caller continues); non-nil error on any failure (caller returns immediately — response already written). |
| **Logging** | Validation failures log at `INFO` with the schema name and the first violation path. No full body logged. |

The helper is the single funnel through which inbound JSON enters the
backend. All future REST handlers must use it; no handler may
hand-write a decode loop.

---

## Mechanism

### Boot pre-compile

`PreCompileAllInboundSchemas()` (see ADR-013) walks every YAML in
`pkg/gateway/inboundschemas/` at gateway startup and compiles each
schema into the runtime validator. Compile failures abort boot.

The pre-compile builds a `map[string]*jsonschema.Schema` keyed by
schema name (without extension). `decodeAndValidate` looks up the
schema by the caller-supplied name string.

### Body size limit

The 1 MiB limit is enforced via `io.LimitReader` on `r.Body`. If the
body exceeds the limit, `json.Decoder` returns an "unexpected EOF" or
similar error, and the helper writes `400` with that decoder message.

1 MiB is generous for any reasonable request body in this API
(largest current request type is well under 100 KiB). Operators can
override the limit via a future `gateway.max_request_body` config knob
if needed; not implemented today.

### Error response shape

The error response is a small JSON envelope:

```json
{
  "error": "request body failed schema validation: AgentCreateRequest: required property 'name' missing",
  "schema": "AgentCreateRequest"
}
```

The `schema` field lets the SPA emit a useful dev-mode toast and route
the error to the right contract-test failure path. The `error` field is
human-readable for log lines and external clients.

### Validation off path

When `validateEnabled` is `false`, the helper skips the validation
step entirely. Decode still runs, body size limit still applies, JSON
syntax errors still produce 400s. The only thing the flag controls is
whether the schema's structural rules are enforced.

This matters for the rollout in ADR-013: with the flag off, the
helper still gives consistent body-size and JSON-error handling, so
the pipeline is exercised in production from day one.

---

## Schema-Name-String Risk

The single biggest weakness of this design is that the schema name is
a **string literal** passed by the handler:

```go
decodeAndValidate(w, r, "AgentCreateRequest", &req, ...)
```

Risks:

1. **Typo.** `"AgenCreateRequest"` (missing `t`) compiles successfully
   and is only caught at boot pre-compile — *if* that schema name was
   referenced somewhere that boot pre-compile walks. Currently
   pre-compile walks the schema files, not the handler call sites, so
   a typo'd handler reference is **not** caught at boot.
2. **Stale reference.** A schema gets renamed; one handler doesn't get
   updated; the typo path applies.
3. **Wrong schema.** A handler accidentally passes a sibling schema
   name. Validation still passes (the schema exists and the payload
   happens to match), but the type-shape check is meaningless.

### Mitigations in place

- **Boot pre-compile** catches missing schema files: if a handler
  references a schema that does not exist as a file, the lookup at
  request time returns a "schema not found" error and the helper
  responds with `500`. The 500 is loud enough that any test exercising
  the handler will fail.
- **Contract test** (`pkg/api/generated/contract_test.go`) exercises
  every generated request type with a known-valid payload from the
  spec. If a handler passes the wrong schema name to
  `decodeAndValidate`, the contract test will fail because the
  validation will reject a payload that should pass.
- **Code review** is the third line of defence. Reviewers check that
  the schema name in `decodeAndValidate` matches the request type.

### Known limitation, future fix

A fully compile-time-checked version of the helper would require one of:

- **Code generation.** A `gen-handler-stubs.sh` step that emits a
  per-handler typed wrapper, e.g. `decodeAndValidateAgentCreate(w, r,
  &req, validateEnabled)` that has the schema name baked in.
- **Reflection.** `decodeAndValidate(w, r, &req, validateEnabled)` —
  derive the schema name from the type of `dst` via reflection,
  matched to a generated map from Go type to schema name.
- **Generic enum.** A generated Go enum `Schema.AgentCreateRequest`
  that the helper takes instead of a string. Typos become compile
  errors.

The reflection option is the cleanest but adds runtime cost; the enum
option is the most idiomatic Go. Both are deferred. The current string
form is acknowledged as a known limitation; the boot pre-compile + the
contract test + code review catch the realistic failure modes in
practice.

This is tracked as a follow-up in `docs/specs/` to be revisited when a
schema-name typo causes a real incident (or a quarter passes without
one — whichever first).

---

## Consequences

### Positive

- One funnel for inbound JSON. Adding validation to a new endpoint is
  a one-line change.
- Body-size limit applied uniformly — no DoS via giant bodies on
  forgotten endpoints.
- Error response shape is consistent — SPA can rely on it.
- Boot pre-compile catches missing schemas; contract test catches
  wrong-schema bugs.

### Negative

- Schema name as a string is not compile-time-checked. Realistic but
  not theoretical risk; mitigations are sufficient for current scale.
- One more helper to learn for new contributors. Documentation in
  `pkg/gateway/validate.go` package comment is the primary onboarding
  surface.
- Body-size limit is a fixed constant (1 MiB). Endpoints that
  legitimately need larger bodies (file uploads) bypass
  `decodeAndValidate` and use their own multipart handlers.

### Neutral

- Validation off path still applies the body limit and JSON decode.
  So flipping `validate_inbound: true` does not add risk to the read
  side, only to the schema-check side.

---

## Alternatives Considered

### A. Per-handler manual decode + validate

- Pros: No helper to learn; explicit control.
- Cons: Drift, copy-paste rot, inconsistent error responses. Schema
  validation will skip on forgotten endpoints.
- **Rejected** as not maintainable.

### B. Middleware-based validation (chain layer instead of helper)

- Pros: Handler stays clean; validation happens before the handler
  runs.
- Cons: Middleware has no access to the per-handler request type for
  decoding. Would have to validate raw bytes and pass the body
  forward, doubling the read cost. Schema name has to come from
  somewhere — typically a route table annotation, which adds a layer
  of configuration.
- **Rejected** — the helper is simpler and gives the handler the
  decoded struct in one call.

### C. Generated typed helpers per handler

- Pros: Compile-time schema-name checking; no string literals.
- Cons: Codegen complexity adds to `scripts/gen-contracts.sh`. The
  generated helpers are one-liner thin wrappers — high ceremony for
  low gain at current scale.
- **Deferred** — see "future fix" above. Worth revisiting when the
  handler count grows or when a real typo incident lands.

### D. Reflection-based helper

- Pros: No string literal; schema name derived from `dst` type.
- Cons: Reflection has a runtime cost on every request. Adds a
  generated type → schema name map. Compile-time safety only if the
  map is exhaustive, which requires the same generation step as
  option C.
- **Deferred** — same reasoning as C.

---

## Affected Components

- Backend:
  - `pkg/gateway/validate.go` — `decodeAndValidate` helper,
    `PreCompileAllInboundSchemas`, `InboundSchemaCompileFailures`.
  - `pkg/gateway/inboundschemas/` — embed FS containing the mirrored
    schemas.
  - All REST handlers with JSON bodies — call `decodeAndValidate`.
  - `pkg/api/generated/contract_test.go` — catches wrong-schema bugs
    via spec-derived test payloads.
- Tooling:
  - `scripts/gen-contracts.sh` step 5 — mirrors schemas into
    `pkg/gateway/inboundschemas/`.
  - `make verify-contracts` — fails if the mirror is stale.
- Future:
  - Codegen step or reflection map to make schema-name checks
    compile-time (deferred).
- Variants: applies to all three deployment modes equally.

---

## References

- `CLAUDE.md` hard-constraint #8 — contract-first wire formats.
- ADR-012 — OpenAPI 3.0.3 version pin.
- ADR-013 — inbound validation strategy (opt-in flag, pre-compile,
  counters).
- ADR-014 — `additionalProperties` policy (the rule that makes
  validation actually catch typos).
- `pkg/gateway/validate.go` — the helper's implementation.
- `pkg/api/generated/contract_test.go` — the safety net for
  wrong-schema bugs.

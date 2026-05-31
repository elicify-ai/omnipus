# ADR-014 — `additionalProperties` Policy (Requests Closed, Responses Per-Case)

**Status:** Accepted
**Date:** 2026-05-18
**Deciders:** architect, backend-lead, frontend-lead

---

## Context

JSON Schema's `additionalProperties` keyword controls whether a payload
may carry fields beyond those declared in the schema:

- `additionalProperties: true` (the default) — unknown fields are
  silently accepted and ignored by validation.
- `additionalProperties: false` — unknown fields cause validation to fail.
- `additionalProperties: { ...schema... }` — unknown fields are accepted
  only if they themselves match the inner schema.

OpenAPI 3.0.3 inherits this from JSON Schema, with the same default
(`true` / permissive). Most generated tooling (oapi-codegen, Zod
generators) follow the schema literally.

ADR-013 introduces strict inbound validation. With validation off, the
default of `true` is mostly invisible — Go's JSON unmarshaling just drops
unknown fields anyway. With validation **on**, the default becomes a
real policy question: do we accept unknown fields in requests, or reject
them?

Three classes of "unknown fields" exist in practice:

1. **Caller typos.** The SPA sends `nmae` instead of `name`. With
   permissive schemas, the backend silently uses the zero value for
   `name`. The bug ships.
2. **Stale clients.** An older SPA version sends a field that the
   current backend no longer recognises (e.g. a deprecated knob). The
   field is ignored; the request "succeeds" with subtly different
   semantics.
3. **Forward-compat extensions.** A newer SPA sends a field that the
   current backend does not yet know about. The backend ignores it; the
   feature degrades cleanly.

Each class wants a different policy. Typos and stale-client drift are
bugs to surface loudly. Forward-compat extensions are graceful
degradation we want to preserve.

The same considerations apply in reverse for responses: should the SPA's
Zod validator reject unknown fields in server responses, or accept them
for forward-compat?

---

## Decision

The policy is **per-direction, per-schema-class**:

| Schema class | `additionalProperties` value | Rationale |
|---|---|---|
| **Request schemas (`*Request.yaml`)** | `false` (closed) — **MUST** | Reject unknown fields. Surface caller typos and stale-client drift loudly. |
| **Response schemas (most)** | `false` (closed) — **SHOULD** | Type-tight contracts; SPA Zod rejects garbage from server. |
| **Response schemas (explicitly extensible)** | `true` (open) — **PERMITTED** with documented exception | When forward-compat is more valuable than strictness, the schema author opts in and documents why. |
| **WS frame payload schemas** | `false` (closed) — **MUST** for inbound frames; per-case for outbound | Inbound frames follow the request rule; outbound follow the response rule. |

A handful of schemas are necessarily open and are documented as
exceptions:

- **`PUT /config` request body** — the config is an open-ended map of
  keys; we cannot enumerate every possible config knob at schema-design
  time. The schema's `description:` MUST call this out explicitly.
- Other exceptions are added on a case-by-case basis by the schema
  author with a `description:` that explains why strictness is the wrong
  call.

---

## Rationale

- **Request strictness is cheap and high-value.** A typo'd field name in
  a request body is silent failure of the worst kind: the action
  partially succeeds with subtly wrong inputs. Closing request schemas
  catches these at the validation boundary with a 400 that names the
  bad field.
- **Response strictness aligns with the SPA's Zod edge.** The SPA
  already validates every response shape. Closing response schemas
  prevents server-side bugs (a handler accidentally including a
  debug-only field, an extra timestamp from a logging refactor) from
  shipping to clients.
- **Forward-compat is real but rare.** Most schemas don't need it. When
  they do, the schema author opts in explicitly — making the exception
  visible rather than the default.
- **Symmetry between REST request bodies and inbound WS frames.** Both
  represent "client tells server something." Both should reject unknown
  fields for the same reasons.

---

## What "closed" buys us

Concrete examples where `additionalProperties: false` catches real bugs:

1. **Typo'd field name.** `{"agnetId": "abc"}` instead of `agentId`.
   Closed schema → 400 with "additional property 'agnetId' not allowed".
   Open schema → handler reads `agentId` as `""`, downstream null deref.
2. **Renamed field across versions.** SPA sends `provider_key`; backend
   was renamed to `providerKey`. Closed schema → immediate 400 surfaces
   the version skew. Open schema → silent semantic drift.
3. **Wrong-shape nested object.** SPA sends `{"config": {"foo": 1}}`
   instead of `{"config": {"bar": 1}}`. Closed → 400. Open → silent
   defaulting.
4. **Smuggled internal fields.** A buggy frontend caches an internal-only
   field (e.g. `_internal_trace_id`) and POSTs it back. Closed → 400.
   Open → leaks into request logs.

---

## CI Enforcement (Future)

A future round can add a contract-lint check that fails CI if any
`*Request.yaml` in `contracts/components/schemas/` lacks
`additionalProperties: false`. The check is a simple YAML walk:

```python
# Pseudocode for the lint:
for path in glob("contracts/components/schemas/*Request.yaml"):
    schema = yaml.safe_load(path)
    if schema.get("additionalProperties") is not False:
        fail(f"{path} must set additionalProperties: false")
```

Until that lint is wired, the policy is enforced by code review. Schema
authors are responsible for setting the field correctly; reviewers
check it on every contract PR.

The lint should also flag response schemas that are missing
`additionalProperties` entirely (i.e. relying on the default of `true`)
and require either an explicit `false` or an explicit `true` with a
`description:` justifying the exception.

---

## Consequences

### Positive

- Caller typos and stale-client drift surface at the validation
  boundary, not as silent zero-value defaults.
- Schema becomes more honest documentation: a reader of the schema
  knows exactly which fields are valid.
- Symmetric strictness between REST and WS inbound traffic.
- Server-side bug leakage (accidental extra fields in responses)
  caught at the SPA edge.

### Negative

- Closed request schemas reject any field the schema author forgot to
  declare. Onboarding a new field requires updating the schema first
  (which is hard-constraint #8's stated process anyway).
- Stricter responses can break older SPA versions if a response sheds
  a field. Mitigation: don't shed fields without a version bump; mark
  deprecated fields with `description:` and remove only across major
  versions.
- The `PUT /config` exception is genuinely permissive. Operators can
  POST arbitrary keys; the handler enforces validity downstream.
  Documented explicitly in that schema.

### Neutral

- Default `additionalProperties: true` in OpenAPI 3.0.3 means every
  schema author must explicitly opt into strictness. The CI lint
  (when wired) makes this enforceable; until then, code review is the
  gate.

---

## Alternatives Considered

### A. Open by default everywhere

- Pros: Forward-compat for free. No schema author has to think about
  it.
- Cons: Loses all the bug-catching value of strict validation. Defeats
  the point of ADR-013. Typos ship silently.
- **Rejected** — ADR-013 only delivers value if schemas are closed.

### B. Closed everywhere, no exceptions

- Pros: Single rule, no per-case judgment.
- Cons: `PUT /config` legitimately accepts arbitrary keys; declaring
  every possible key in the schema is impossible. Some response
  schemas genuinely benefit from forward-compat.
- **Rejected** — the exception list is small enough to manage but real
  enough to acknowledge.

### C. Open requests, closed responses (asymmetric)

- Pros: Maximum compatibility for incoming traffic.
- Cons: Backwards. Requests are the direction where typos and drift
  cost the most because they change server state. Responses are where
  forward-compat matters most because the SPA must keep working as the
  server evolves.
- **Rejected** as backwards.

### D. `additionalProperties: { type: string }` for requests (typed open)

- Pros: Allows extension while constraining unknown-field types.
- Cons: Doesn't catch typos at all — `nmae: "..."` would pass. The bug
  scenarios that motivate closed-by-default require literal
  rejection of unknown names.
- **Rejected** as not actually solving the typo problem.

---

## Affected Components

- `contracts/components/schemas/*Request.yaml` — all set
  `additionalProperties: false`.
- `contracts/components/schemas/*Response.yaml` and others — set
  `additionalProperties: false` unless explicitly extensible.
- `contracts/components/schemas/ConfigPutRequest.yaml` (or equivalent)
  — documented exception: open with `description:` justification.
- Future: `scripts/lint-contracts.sh` (or extension to
  `scripts/gen-contracts.sh` step 0) — CI lint for the rule.
- Generated Go and TS types — picked up automatically from schema;
  validators (jsonschema in Go, Zod in TS) honour the keyword.

---

## References

- `CLAUDE.md` hard-constraint #8 — contract-first wire formats.
- ADR-012 — OpenAPI 3.0.3 version pin.
- ADR-013 — inbound validation strategy (closed schemas only matter
  when validation is on).
- ADR-015 — `decodeAndValidate` pipeline (consumer of the closed
  request schemas).
- JSON Schema spec, `additionalProperties` keyword:
  https://json-schema.org/draft/2020-12/json-schema-core.html#name-additionalproperties

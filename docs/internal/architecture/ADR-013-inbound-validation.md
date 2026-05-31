# ADR-013 — Inbound Validation Strategy (Opt-in, Fail-Closed, Default-Flip Target)

**Status:** Accepted
**Date:** 2026-05-18
**Deciders:** architect, backend-lead, security-lead

---

## Context

Hard-constraint #8 requires that every byte crossing the gateway/SPA
boundary be defined in `contracts/openapi.yaml` or
`contracts/asyncapi.yaml` and validated at runtime. The SPA edge enforces
this for inbound (server → SPA) traffic via Zod schemas generated from the
spec: every `request<T>()` call validates the response, drops on failure,
increments `_apiSchemaErrorCount`, and surfaces a dev-mode toast.

Before this ADR, the backend did **not** symmetrically validate
**outbound-from-SPA** traffic (SPA → server). REST handlers relied on Go's
type system at JSON unmarshal time: if the inbound JSON had fields the Go
struct expected, it parsed; if not, fields silently defaulted to zero
values. There was no per-handler check that the request body matched the
JSON Schema in the spec — only that it deserialised into the generated Go
type.

That gap matters for three reasons:

1. **Asymmetry violates hard-constraint #8.** If the schema is the source
   of truth, both directions must check against it. Otherwise the backend
   has two contracts — the schema and Go's lenient unmarshaling — and the
   schema becomes advisory.
2. **Silent acceptance of unknown fields** masks SPA bugs. A frontend
   change that adds a stray field never surfaces because the backend just
   ignores it. The bug ships to production; the backend logs nothing.
3. **Out-of-range values pass.** Go's JSON unmarshal accepts any integer
   into an `int` field. The schema's `minimum`/`maximum`/`enum` constraints
   are checked nowhere on the backend.

The naive fix — turn on strict validation everywhere immediately — risks
breaking legacy callers and external integrations that may rely on
permissive behaviour. We need a rollout path that lets us wire the safety
net, prove it works, and flip it on once we have evidence of zero false
positives.

---

## Decision

Introduce a single boolean config flag, `gateway.validate_inbound`,
defaulting to `false`. When `true`, every REST request body and WebSocket
inbound frame is validated against the corresponding JSON Schema before
reaching the handler. Validation failures return `400 Bad Request` for
REST and drop-with-counter for WS (matching the SPA's outbound handling).

The validation pipeline is **fully wired and tested** regardless of the
flag's value — the flag controls only whether the validator's verdict
gates the handler. The pre-compile boot guard runs unconditionally, so
schema-compile failures are caught even when validation is disabled.

**Target for flipping the default to `true`:** v0.2, once production logs
show zero validation 400s for 14 consecutive days on a staging deployment
that ran with `validate_inbound: true`.

---

## Rationale

- **Symmetry with the SPA edge.** The SPA already validates every inbound
  payload through Zod. Validating SPA → backend the same way closes the
  loop and makes hard-constraint #8 enforceable in both directions.
- **Graceful rollout.** Opt-in default means existing deployments do not
  break the moment they pull the new binary. Operators who want strict
  enforcement can flip the flag; those who cannot afford a regression
  window stay on the permissive path until v0.2.
- **Fail-closed on compile errors.** A schema that fails to compile at
  boot indicates a contract bug. Continuing to serve with that schema
  effectively disabled would silently weaken security. Aborting boot
  forces operators (and CI) to notice immediately.
- **Single config flag, not per-endpoint.** Per-endpoint opt-in would
  create matrix complexity (which endpoints validate? in which versions?)
  and would inevitably drift. One flag, on or off, for the whole gateway
  is auditable and simple.

---

## Mechanism

### 1. Schema mirror

`scripts/gen-contracts.sh` (step 5) copies every schema YAML from
`contracts/components/schemas/` into `pkg/gateway/inboundschemas/`. The
embed FS at `pkg/gateway/inboundschemas/embed.go` exposes the schemas to
the gateway at runtime via `//go:embed *.yaml`.

The mirror is committed to the repo (it is a generated artifact, but
checked-in like `pkg/api/generated/`). `verify-contracts` fails if the
mirror is stale relative to the source `contracts/components/schemas/`.

### 2. Boot pre-compile

At gateway boot, `PreCompileAllInboundSchemas()` walks every YAML in
`pkg/gateway/inboundschemas/`, parses it, and compiles it into the
JSON Schema runtime validator (currently `santhosh-tekuri/jsonschema`).
Compile failures abort boot with an error log naming the schema and the
underlying compile error.

The pre-compile runs **unconditionally** — independent of the
`validate_inbound` flag. A schema compile failure is a contract bug; we
never want to ship a binary that silently skips a schema because the flag
is off.

### 3. REST validation

Every REST handler that accepts a JSON body calls:

```go
var req SomeRequestType
if err := decodeAndValidate(w, r, "SomeRequestSchema", &req, validateEnabled); err != nil {
    return // response already written
}
```

`decodeAndValidate` (see ADR-015 for the full contract):

- Reads up to 1 MiB of body via `io.LimitReader`.
- Decodes JSON into `dst`.
- If `validateEnabled`, validates the decoded payload against the named
  schema.
- On validation failure: writes `400 Bad Request` with a short
  schema-name-referenced error message. Does not leak the full
  validator output (some validators emit deep tree paths that could
  reveal internal naming).
- On compile failure at request time (only possible if pre-compile was
  bypassed for some reason): writes `500 Internal Server Error`.

### 4. WebSocket validation

WebSocket inbound frames are validated symmetrically. `ValidateInboundFrameJSON`
(introduced in round-5 fix-AD) looks up the schema by the frame's `type`
field, validates the payload, and returns the validation verdict. The
gateway's WS receive loop drops the frame on failure and increments a
counter.

There is one notable asymmetry with REST: WS frames cannot return a `400`
to the sender because the protocol does not have a clean error-response
slot for arbitrary frames. The receive loop drops, counts, and (in dev
mode) logs at WARN level.

### 5. Counters

Observability for the validation layer:

- **Backend:** `InboundSchemaCompileFailures()` returns the count of
  schemas that failed to compile (should be `0` always; non-zero means a
  boot raced past pre-compile).
- **SPA:** `_apiSchemaErrorCount` (REST response validation failures),
  `_droppedFrameCount` (WS frame validation failures), `_unknownFrameTypeCount`
  (WS frame whose `type` is not in the registry), `_configCoercionCount`
  (config-shape soft-coercions performed at the SPA edge).

Both sides expose their counters via dev-tools hooks and (eventually)
gateway `/metrics` once the metrics endpoint lands.

---

## Default-Flip Target

`gateway.validate_inbound` ships at `false` for v0.1. The flip-to-`true`
criteria are:

1. The flag has been enabled on staging for ≥14 consecutive days.
2. Production logs (or staging logs at representative traffic levels) show
   **zero** unintended validation 400s during that window.
3. Any 400s that did occur trace back to actual schema violations from
   misbehaving clients, not contract drift.
4. The default-flip lands in v0.2 alongside other security hardening
   (issue #155).

Operators who want strict enforcement before v0.2 can set
`gateway.validate_inbound: true` in their `config.json` today. The
infrastructure is fully wired.

---

## Consequences

### Positive

- Symmetric contract enforcement: SPA validates server → SPA, server
  validates SPA → server. Hard-constraint #8 enforced in both directions.
- Schema bugs (typos in field names, wrong types, missing constraints)
  surface as 400s immediately rather than as silently dropped data.
- The pre-compile boot guard catches contract bugs at deploy time, not
  request time.
- Counters give operators a single number to watch for contract drift.

### Negative

- One more boot step that can abort the gateway. Operators who deploy a
  bad schema and don't catch it in CI will see boot failure rather than
  silently-degraded service. (We consider this a feature, not a bug.)
- Default-off in v0.1 means the safety net is not actually catching
  anything in production until v0.2. The wiring exists; the catch
  doesn't.
- Per-request validation has a small CPU cost (microseconds per request
  for typical schemas). Negligible at our traffic scale but real.
- The validation library is a new third-party dependency
  (`santhosh-tekuri/jsonschema`). Pure Go, but one more thing to keep
  patched.

### Neutral

- The flag is config-level, not build-time. SaaS and Desktop variants can
  enable it without a binary rebuild.
- The `inboundschemas/` embed FS adds a small amount of binary size (~10
  KiB of schema YAML). Negligible.

---

## Alternatives Considered

### A. Always validate, no flag

- Pros: Symmetric from day one. No rollout phase, no flag to forget.
- Cons: Any contract drift introduced before this ADR immediately becomes
  a 400 on every deployment. No graceful rollback path. Risk of breaking
  in-flight SPA versions.
- **Rejected** in favour of opt-in rollout.

### B. Validate only in dev/staging, never in production

- Pros: No production risk.
- Cons: Defeats the point — production is exactly where contract drift
  matters most. SPA dev-mode validation already catches the dev-side
  cases.
- **Rejected**.

### C. Per-handler `validate: true` flag on the spec

- Pros: Granular rollout. Could enable validation on new endpoints
  first.
- Cons: Matrix complexity. Operators have no single switch to audit. Spec
  becomes the place where validation policy lives, which is a strange
  layering.
- **Rejected** in favour of one gateway-wide flag.

### D. Use Go struct tags (`validate:"required,oneof=..."`) instead of JSON Schema

- Pros: One source of truth in Go.
- Cons: Loses the schema as the cross-language contract. SPA cannot
  consume Go struct tags. Hard-constraint #8 requires the spec to be
  authoritative.
- **Rejected** as a violation of hard-constraint #8.

---

## Affected Components

- Backend:
  - `pkg/gateway/inboundschemas/` — embed FS, mirror of
    `contracts/components/schemas/`.
  - `pkg/gateway/validate.go` — `PreCompileAllInboundSchemas`,
    `decodeAndValidate`, `ValidateInboundFrameJSON`,
    `InboundSchemaCompileFailures`.
  - `pkg/gateway/server.go` — boot calls `PreCompileAllInboundSchemas`;
    abort on error.
  - All REST handlers with JSON bodies — call `decodeAndValidate`.
  - `pkg/gateway/websocket.go` — receive loop calls
    `ValidateInboundFrameJSON`.
  - `pkg/config/` — `Gateway.ValidateInbound bool`.
- Frontend:
  - `src/lib/queryClient.ts` — already validates response payloads via
    Zod; unchanged.
  - Counters exposed on `window._apiSchemaErrorCount`, etc.
- Tooling:
  - `scripts/gen-contracts.sh` step 5 — schema mirror.
  - `make verify-contracts` — fails on mirror drift.
- Variants: applies to all three deployment modes equally.

---

## References

- `CLAUDE.md` hard-constraint #8 — contract-first wire formats.
- ADR-012 — OpenAPI 3.0.3 version pin (the schemas being validated).
- ADR-014 — `additionalProperties` policy (the rule that makes
  request validation maximally strict).
- ADR-015 — `decodeAndValidate` pipeline contract.
- Phase 7 fix-AD — WS frame inbound validation.
- `santhosh-tekuri/jsonschema` — runtime validator.

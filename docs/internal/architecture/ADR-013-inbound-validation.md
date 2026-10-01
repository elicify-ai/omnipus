# ADR-013 — Inbound Validation Strategy (Opt-in, Fail-Closed, Default-Flip Target)

> **Release-label note (2026-09-25):** the v0.2 / v0.3 release labels are retired — Omnipus ships a single v0.1.1 line (founder decision 2026-09-25). Version labels below are kept as historical record unless re-pointed at a tracked issue.

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

**Target for flipping the default to `true`:** not scheduled (the v0.2 target
label was retired 2026-09-25 — single v0.1.1 line), once production logs
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
  window stay on the permissive path (the v0.2 default-flip is unscheduled —
  the target label was retired 2026-09-25).
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

### 6. Schema dialect [addendum, 2026-10-01]

This ADR did not originally pin a JSON Schema dialect for the compiler
(`santhosh-tekuri/jsonschema/v6`, `pkg/gateway/rest_inbound_validate.go::initInboundValidator`).
None of the 460 files under `contracts/components/schemas/` declare a
`$schema` key (`contracts/openapi.yaml` is pinned to `openapi: 3.0.3`,
whose Schema Object is a superset of JSON Schema Wright Draft 00 — the
draft-04 family — and OpenAPI disallows `$schema` on a Schema Object), so
every embedded schema compiled under whatever dialect the compiler
defaulted to internally. `jsonschema/v6`'s own default is "the latest
draft supported" (`jsonschema.draftLatest = Draft2020`,
`santhosh-tekuri/jsonschema/v6@v6.0.2/draft.go`) — a dialect none of these
files are actually written in.

This surfaced as a total gateway boot failure on every platform: Draft
2020-12 requires `exclusiveMinimum`/`exclusiveMaximum` to be numeric
(`metaschemas/draft/2020-12/meta/validation`, `properties.exclusiveMinimum.type == "number"`),
while `ContextSettings.yaml`'s `tool_result_share_fraction` uses the
boolean form paired with `minimum` — the only form OpenAPI 3.0.3 permits
and that `redocly lint` accepts. Since `PreCompileAllInboundSchemas()`
runs unconditionally at boot (§2, above) and a compile failure aborts
boot, one dialect-mismatched schema took down the whole gateway.

**Decision:** `initInboundValidator` sets
`c.DefaultDraft(jsonschema.Draft4)` explicitly, matching the dialect
every one of these files is already authored in (OpenAPI 3.0.3 ≈
draft-04-family / Wright Draft 00). This is a dialect correction, not a
new design — it makes the compiler's default agree with the one dialect
contract authors already write, lint (`redocly lint`), and intend.

**Consequence — `required: []` is now a compile error, not a no-op.**
Draft-04's metaschema requires the `required` array to have at least one
item when present (`metaschemas/draft-04/schema`, `definitions.stringArray.minItems == 1`);
draft 2020-12 dropped that constraint. `required: []` is semantically a
no-op under both dialects (requires nothing — identical in meaning to
omitting the key) but is only syntactically legal under 2020-12. One file,
`ChannelRouting.yaml`, used the empty-array form; it was removed from the
canonical source (`contracts/components/schemas/ChannelRouting.yaml`) and
the embed mirror regenerated via `scripts/gen-contracts.sh` step 5
(`pkg/gateway/inboundschemas/ChannelRouting.yaml`) — not patched at the
Go loader. **Do not add a loader-side "treat empty `required` as omitted"
preprocessing step** — that would re-encode contract meaning in Go,
contradicting Hard Constraint #8's contract-first rule, and would leave
the next `required: []` schema to fail boot again with no lint signal to
catch it before CI. A new schema author who needs "no required fields"
simply omits the `required` key (the standard, already-idiomatic form);
`redocly lint` does not require the empty-array form.

Verified: `TestPreCompileAllInboundSchemas_AllSchemasCompile` (all 460
embedded schemas compile under Draft4) and
`TestContextSettings_PutRejectsOutOfRange` (field-boundary rejections,
including the share-fraction lower bound, still enforced) —
`pkg/gateway/rest_inbound_validate_test.go`, `pkg/gateway/rest_context_settings_test.go`.

### 7. Correction — global Draft4 silently disabled `const` [addendum, 2026-10-01]

**Correction: §6 said "this is a dialect correction, not a new design" and
approved a global `c.DefaultDraft(jsonschema.Draft4)`. That was incomplete —
it traded one gateway-boot-killing defect for a second, narrower but real,
validation-enforcement defect it did not check for. Correct fix: keep the
compiler's own modern default (2020-12) for 458 of 460 schemas; scope
Draft4 to only the 2 files that actually need it, via a load-time `$schema`
tag on those two documents, not a global compiler setting.**

**What broke.** `santhosh-tekuri/jsonschema/v6 v6.0.2`'s `const` keyword is
wired up only in `compileDraft6`
(`objcompiler.go::objCompiler.compileDraft6`), which `objCompiler.compile`
only calls `if s.DraftVersion >= 6` (`objcompiler.go::objCompiler.compile`,
line 46). `Draft4.version == 4` (`draft.go`, `Draft4 = &Draft{version: 4,
...}`), so under the global Draft4 pin `compileDraft6` never runs for any
embedded schema, `s.Const` stays `nil` for all of them, and
`validator.go`'s const check (`if s.Const != nil { ... }`, line 106) never
fires — a `const`-constrained field silently accepts any value of the
declared JSON type. This is not a corner case of the library; it is the
documented split between the draft-04 and draft-06 keyword sets, and it
is global because `DefaultDraft` sets `c.roots.defaultDraft`
(`compiler.go::Compiler.DefaultDraft`), which every schema document falls
back to unless it declares its own `$schema`, and none of the 460 embedded
files do.

**Scope, verified directly, not estimated.** `grep -rl "const:"
pkg/gateway/inboundschemas/*.yaml` → exactly 73 files. Every one uses
`const` on exactly one of two patterns: a `type` string discriminator (71
files — every `*Frame.yaml` client↔server WS frame shape) or a boolean tag
discriminator (`_ref`/`_truncated`, 2 files — `ToolResultRef.yaml`,
`TruncatedResult.yaml`, both embedded only inside
`ToolCallResultFrame.yaml`'s `result` field). No REST (`decodeAndValidate`)
schema uses `const` — confirmed against every schema name passed to
`decodeAndValidate(...)` across `pkg/gateway/rest_*.go`, `sse.go`,
`signin_provider.go`: zero overlap with the 73.

**Live blast radius is narrower than it first looks, but the defect is
real and currently RED, not a silent production hole.** Traced every call
site that reaches `ValidateInboundFrameJSON`:

- The chat WS (`pkg/gateway/websocket.go::wsHandler.readLoop`) and the
  browser WS (`pkg/gateway/browser_ws.go`, same pattern) both derive the
  schema name from the **same raw frame bytes** they then validate:
  `peek.Type` (or `typ.Type`) is unmarshalled from `data`, fed into
  `wsFrameSchemaName(peek.Type)` to pick the schema, and the unmodified
  `data` is then validated against that schema
  (`websocket.go::wsHandler.readLoop`, the
  `schemaName := wsFrameSchemaName(peek.Type)` /
  `ValidateInboundFrameJSON(schemaName, data)` pair). Because the
  discriminator that selects the schema and the discriminator inside the
  validated payload are the same bytes, a client cannot make the routed
  schema disagree with the payload's own `type` field — there is no
  "validate as X, dispatch as Y" confusion to exploit through this path.
- The 3 call sites that hardcode a schema name instead of deriving it from
  the payload (`browser_dedicated_input.go` ×2 → `BrowserInputOfferFrame`,
  `BrowserInputFrame`; `browser_webrtc.go` → `BrowserCaptureHelloFrame`)
  never read the decoded frame's `Type` field for any downstream decision
  (verified: `dispatchDedicatedInputOffer` and the dedicated-input send
  path use `f.InputEpoch`/`f.OfferId`/etc., never `f.Type`) — so an
  unenforced `const` on `type` here is inert too.
- `ToolResultRef`/`TruncatedResult`'s `const` fields are on a shape that is
  **server→client only** — built in `replay.go::buildResultFrame` and
  `websocket_forward_hub.go`, never unmarshalled from client-controlled
  bytes through `ValidateInboundFrameJSON` at all (not in
  `wsFrameSchemaName`'s switch, no literal call site). Zero live exposure.

So the gap does not currently give an attacker a working type-confusion
exploit against any wired handler. What it does do, verified by running
the suite, is break an existing contract test:
`TestWebRTCFrameSchemasRoundTrip` (`pkg/gateway/browser_webrtc_contract_test.go`)
asserts a wrong `type` const is rejected; under the global Draft4 pin it
is not, and `CGO_ENABLED=0 go test -tags goolm,stdjson -run
'^TestWebRTCFrameSchemasRoundTrip$' -p 1 ./pkg/gateway/` fails all 7
subtests on their "wrong const must be rejected" assertion (confirmed by a
local run — all 7 `--- FAIL`, overall `FAIL`, `exit=1`). That test
predates this stream and is unrelated to its own diff (`git log --
pkg/gateway/browser_webrtc_contract_test.go` shows it introduced on
`6e080c9d4`, "W2-C"; `git show origin/main:...` confirms it is absent from
`main` entirely — it only exists on this integration lineage) — but Hard
Constraint #7 makes it this stream's to fix regardless of origin, and it
blocks the gate as-is. It is also a forward-looking risk: any future
`oneOf`+discriminator schema added under this validator (the one Hard
Constraint #8 exception, ADR-034-style) would have its discriminator
silently stop discriminating under the same pin — today it happens to be
inert because nothing yet trusts `const` as an authority, but that is a
property of the current call graph, not of the fix, and should not be
relied on going forward.

**The squad lead's candidate fix (rejected as unnecessary, not as
wrong-in-kind) and the correct fix.**

The candidate — drop the global Draft4 pin, restore the compiler's 2020-12
default, and translate `exclusiveMinimum`/`exclusiveMaximum` from the
OpenAPI-3.0.3 boolean form into 2020-12's numeric form **for every
schema**, at load time in `inboundSchemaLoader.Load()` — gets the
direction right (drop the global pin) but over-scopes the mechanism.
`grep -rl "exclusiveMinimum\|exclusiveMaximum"
contracts/components/schemas/*.yaml` → exactly 2 files,
`ContextSettings.yaml` and `ContextSettingsUpdate.yaml` (both
`tool_result_share_fraction`), not "every schema." Translating a value
that doesn't exist in the other 458 files is dead code the moment it's
written, and a value-rewrite is the wrong tool even for those 2: it
reaches into the schema's own semantics (what `minimum`/`exclusiveMinimum`
*mean*) rather than telling the validator library which dialect a
document is already written in.

**Is this the same mistake as the rejected ChannelRouting loader
workaround?** No — verified the distinction, not assumed it. The
ChannelRouting case (§6, "Consequence") was `required: []`: a form that
means exactly the same thing ("nothing required") under every dialect,
where the fix — delete the key — is *more* idiomatic OpenAPI regardless of
dialect and was rejected as a loader workaround only because a strictly
equivalent, dialect-neutral edit existed at the source and was skipped in
favour of a Go-side special case. Here, no dialect-neutral source edit
exists:

- A numeric `exclusiveMinimum` in `ContextSettings.yaml` (the 2020-12
  form) is rejected by `redocly lint` — verified directly, not taken on
  the squad lead's word: edited `tool_result_share_fraction` to
  `maximum: 1` / `exclusiveMinimum: 0`, ran
  `npx --no-install @redocly/cli lint contracts/openapi.yaml
  --skip-rule no-server-example.com` → `exit=1`,
  `[1] contracts/components/schemas/ContextSettings.yaml:49:23 at
  #/properties/tool_result_share_fraction/exclusiveMinimum ... Error was
  generated by the struct rule`. File restored immediately after
  (`git status --short` clean).
- Adding `$schema` directly inside the `ContextSettings.yaml` component
  schema object is also rejected by the same linter — verified the same
  way: `exit=1`,
  `[1] contracts/components/schemas/ContextSettings.yaml:10:1 at
  #/$schema ... Property \`$schema\` is not expected here`. Restored,
  clean.
- OpenAPI 3.0.3's Schema Object is contractually the boolean-paired form
  (that is the entire reason §6 exists), and `redocly lint` is this
  project's own enforcement of that dialect on the committed YAML.
  draft 2020-12's metaschema requires `exclusiveMinimum` to be a number
  (`metaschemas/draft/2020-12/meta/validation`,
  `"exclusiveMinimum": {"type": "number"}`, read directly from the
  vendored module). The two mandatory constraints — OpenAPI 3.0.3 shape in
  the committed contract, correct `const` semantics in the validator — are
  structurally incompatible for these 2 fields specifically. There is no
  dialect-neutral YAML edit to fall back to, unlike ChannelRouting.

That is the dispositive difference: ChannelRouting's rejected fix moved a
*meaning* decision (is an empty `required` array the same as an absent
one?) into Go when an equally-correct, dialect-neutral YAML edit already
existed. The fix below moves no meaning anywhere — `exclusiveMinimum: true`
paired with `minimum: 0` stays exactly as authored, asserting exactly what
it always asserted — and only tells the third-party validator library
which spec dialect to read two specific documents under, which is
implementation plumbing for a chosen dependency, not contract content.

**Decision.**

1. In `initInboundValidator()` (`pkg/gateway/rest_inbound_validate.go`),
   replace `c.DefaultDraft(jsonschema.Draft4)` with
   `c.DefaultDraft(jsonschema.Draft2020)` — explicit, not a reliance on
   the library's unexported internal default (`draft.go::draftLatest`),
   so a future `jsonschema/v6` upgrade cannot silently change our dialect
   out from under us.
2. In `inboundSchemaLoader.Load()` (same file), after `yaml.Unmarshal`
   into `doc`, if the schema's base name (the `path` with `.yaml`
   stripped — the same derivation `PreCompileAllInboundSchemas` already
   uses) is `ContextSettings` or `ContextSettingsUpdate`, and `doc` is a
   `map[string]any`, set `doc["$schema"] =
   "http://json-schema.org/draft-04/schema"` (exact string, no trailing
   `#` — `draft.go::draftFromURL` rejects a URL with a fragment and falls
   through to a remote-fetch attempt) before returning. Use a small,
   explicit, named map/slice literal for the two names — do not infer
   "needs Draft4" by sniffing for a boolean `exclusiveMinimum` in the
   parsed doc; an explicit allow-list is auditable and fails loudly (a
   future third file using the boolean form without being added to the
   list fails CI's `TestPreCompileAllInboundSchemas_AllSchemasCompile`
   immediately, the same fail-closed property §2 already relies on).
3. Do **not** touch any file under `contracts/components/schemas/` or its
   generated mirror `pkg/gateway/inboundschemas/` — both rejected routes
   above are reasons, not just data points.
4. Do **not** implement the squad lead's numeric-translation loader step.
   It is unneeded (2 files, not all 460) and it is the wrong shape of fix
   even for those 2 — a dialect tag, not a value rewrite.

**Why this works, verified empirically, not just read off the API.**
`santhosh-tekuri/jsonschema/v6`'s `Compiler` has no per-URL/per-resource
dialect override method (`grep -n "^func (c \*Compiler)" compiler.go` —
full list checked, only `DefaultDraft` sets a dialect, and it is global);
the only other lever is a document's own `$schema` key, read in
`roots.go::_collectResources` → `loader.go::defaultLoader.getDraft`, and
it is read **per freshly-loaded root document** — `$ref`ing a different
file (e.g. `ContextSettings.yaml`'s `model_overrides` →
`./ContextModelOverride.yaml`) calls `roots.addRoot` for that new URL with
fallback dialect `rr.defaultDraft` (`roots.go::addRoot`, line 48), **not**
the referencing document's resolved dialect — confirmed by reading
`addRoot`/`collectResources`, where cross-document fallback is always
`rr.defaultDraft` and only same-document nested subschemas inherit the
parent's already-resolved dialect (`roots.go`, lines 185 and 223 use
`baseRes.dialect`/`base.dialect`, both scoped to resources already inside
the same root). So tagging exactly 2 files scopes Draft4 to exactly those
2 resources; everything they reference, and all 458 other files, get the
2020-12 default.

Verified directly with a throwaway local probe (not committed): a
compiler configured exactly as above (`DefaultDraft(Draft2020)`, loader
injecting `$schema` only for the 2 named files) —

- compiled all 460 embedded schemas with zero errors
  (`TestZZZArchitectProbeDraft2020Compile`, local run, `PASS`,
  `"all 460 embedded schemas compiled cleanly under Draft2020 with 2-file
  Draft4 override"`);
- correctly rejected a wrong `type` const on `CancelFrame`
  (`TestZZZArchitectProbeConstEnforced`, `PASS`,
  `at '/type': value must be 'cancel'`);
- correctly still rejects `tool_result_share_fraction: 0` and still
  accepts `0.5` on `ContextSettingsUpdate`
  (`TestZZZArchitectProbeContextSettingsStillWorks`, `PASS`,
  `exclusiveMinimum: got 0, want 0`, then no error for `0.5`).

The probe file was deleted after the run — it is not part of this
decision's shipped mechanism, only its verification.

**Required follow-up, not optional.** After the mechanism lands:
`TestWebRTCFrameSchemasRoundTrip` must go green on all 7 cases (both the
const-rejection and required-field-rejection assertions);
`TestPreCompileAllInboundSchemas_AllSchemasCompile` must still show 460/460;
`TestContextSettings_PutRejectsOutOfRange` must still pass unchanged. In
addition, qa-lead should add at least one regression test asserting
`const` rejection on one of the 71 `type`-discriminated frame schemas
beyond the 7 already covered by `TestWebRTCFrameSchemasRoundTrip` (e.g.
`CancelFrame` or `MessageFrame`, both reached through
`wsFrameSchemaName`'s derived-schema path rather than a hardcoded one) —
flagged to team-lead as a test-coverage gap, not fixed here.

---

## Default-Flip Target

`gateway.validate_inbound` ships at `false` for v0.1. The flip-to-`true`
criteria are:

1. The flag has been enabled on staging for ≥14 consecutive days.
2. Production logs (or staging logs at representative traffic levels) show
   **zero** unintended validation 400s during that window.
3. Any 400s that did occur trace back to actual schema violations from
   misbehaving clients, not contract drift.
4. The default-flip rides the next scheduled security hardening (the v0.2
   target label was retired 2026-09-25 — no release scheduled; #155 closed
   2026-05-04 without flipping the default).

Operators who want strict enforcement today can set
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
  anything in production (the default remains off; the flip is unscheduled
  after the label retirement of 2026-09-25). The wiring exists; the catch
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

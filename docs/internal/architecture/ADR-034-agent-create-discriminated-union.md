# ADR-034: Discriminated-union create contract for agents

- **Status:** Accepted (operator-approved 2026-07-03, implemented on `hotfix/v0.1.1`)
- **Date:** 2026-07-03
- **Amended: 2026-07-11 (ADR-037) — the `delegation_policy` per-type
  decision in Decision 6 below is retired.** [ADR-037](./ADR-037-remove-global-delegation-policy.md)
  subsequently deleted `config.AgentConfig.DelegationPolicy` and the
  `/agents/trust` delegation-graph editor entirely — the field is no longer
  create-writable (or update-writable) on ANY variant, `Main`/`Subagent`/
  `subagent_3p` alike. `PUT /api/v1/agents/{id}` (and, per this ADR's own
  Decision 4 strictness, the create path too) now 400s on a
  `delegation_policy` field in the request body regardless of variant,
  mirroring the ADR-035 `sandbox_profile` precedent. Delegation trust is
  workspace-scoped only (a workspace's own `Delegation[]` edge list, edited
  via that workspace's Team tab) — see ADR-037 for the full removal
  rationale. Every other per-type field decision in Decision 6 is
  unaffected by this amendment.
- **Related:** ADR-013 (inbound validation), ADR-032 (external agent execution),
  [ADR-037](./ADR-037-remove-global-delegation-policy.md) (removed
  `delegation_policy` entirely — see the amendment above),
  `docs/internal/architecture/agent-types-field-matrix.md` (authoritative
  per-type field allocation), `docs/internal/specs/agent-form-requirements.md`

## Context

The agent create endpoint (`POST /api/v1/agents`) accepted one flat
`AgentCreateRequest` for three very different agent types (`Main`, `Subagent`,
`subagent_3p`). Fields that a type cannot carry (e.g. `tools_cfg` on an
external-CLI worker, `voice` on any worker, `executor` on `Main`) were either
silently dropped by `json.Unmarshal`, silently persisted, or guarded by an
ever-growing pile of hand-written per-type runtime checks in the handler. The
SPA's create wizard and edit profile each re-derived their own version of
"which fields does this type have", and the three encodings drifted — a
2026-07-03 audit found silent-drop UI controls, missing fields, and payload
leaks on every surface.

## Decision

1. **`AgentCreateRequest` is a discriminated union.** One create variant per
   agent type — `AgentCreateRequestMain`, `AgentCreateRequestSubagent`,
   `AgentCreateRequestSubagent3p` — each `additionalProperties: false`,
   carrying EXACTLY the fields the field matrix allows that type. `type` is
   REQUIRED on every variant with a single-value enum pin. The per-type field
   sets live in `contracts/components/schemas/AgentCreateRequest*.yaml`.
2. **The union wrapper is hosted INLINE in `contracts/openapi.yaml`** (a
   `oneOf` + `discriminator` over internal `#/components/schemas/...` refs),
   NOT in its own schema file. This is a deliberate exception to the
   one-file-per-schema convention forced by codegen: oapi-codegen only
   generates named variant Go structs plus `As*/From*` accessors when the
   `oneOf` members are internal component refs — external file refs inside a
   `oneOf` are inlined as anonymous structs and the generated code does not
   compile. Any future discriminated union in this contract must follow the
   same pattern (see CLAUDE.md Constraint #8).
3. **The gateway dispatches by peeking `type`** from the raw body (the
   WsFrame precedent in `pkg/gateway/websocket.go`), validating against the
   named variant schema, and unmarshaling into the named generated variant
   struct — never through the generated union wrapper's accessor machinery.
4. **Create-time strictness is unconditional.** The variant unmarshal uses
   `json.Decoder.DisallowUnknownFields()`, so a field sent on the wrong
   variant is a 400 regardless of `gateway.validate_inbound` (which stays
   default-off per ADR-013 and, when enabled, adds richer schema errors in
   front of the strict decode). This closes the review finding that the
   union's central promise — "wrong-variant fields are rejected, never
   silently dropped" — held only for opted-in deployments.
5. **`AgentUpdateRequest` stays flat.** Type is immutable post-create; update
   enforcement is server-side per-type rejection owned by
   `pkg/gateway/agent_field_rules.go` (`subagent3pForbiddenUpdateFields`),
   which now includes `max_tool_iterations`. Reflection-based drift tests pin
   (a) the forbidden list against the actual update checks and (b) the create
   variant's field set against the forbidden list, so the create/update
   asymmetry cannot drift silently.
6. **Per-type field decisions** (operator, 2026-07-03): `subagent_3p`
   EXCLUDES `max_tool_iterations` (the external CLI runs its own tool loop)
   and KEEPS `timeout_seconds` (process-level kill for a hung CLI);
   ~~`delegation_policy` is allowed on ALL variants including `subagent_3p`
   create (previously 400)~~ **removed — ADR-037 (2026-07-11): see the
   amendment above. `delegation_policy` is no longer create- or
   update-writable on any variant; delegation trust moved to the
   workspace-scoped `Delegation[]` edge list.** Locked core agents expose
   EDITABLE execution knobs (sampling, rate limits, execution) in the
   profile UI while `name/description/soul/color/icon/skills` remain locked
   (403), with description/color/icon shown read-only.

## Breaking changes (integrator notes)

- **`type` is required on create.** The historical omit-`type`-defaults-to-
  `Main` behavior is retired; a body without `type` is a 400.
- **Unknown/wrong-variant fields are a 400**, including nested unknown keys.
  Previously they were silently dropped (default config) or, for
  `executor`-on-`Main`, coerced to native with a response warning.
- **`subagent_3p` must be created directly** with its required
  `executor` (`cli`, `cli_path`). The create-time reclassification of a
  `worker` + `external-cli` executor into `subagent_3p` is retired.
- **`executor.kind` is never create-writable.** The server derives `native`
  for `Subagent` and forces `external-cli` for `subagent_3p`; `remote-a2a`
  remains reachable only via PUT (still schema-pinned, not dispatched).

## Consequences

- The generated TS variant types give the SPA a compile-time payload guard:
  assigning a field a variant doesn't carry fails `npm run typecheck`
  (`payloadToCreateRequest` builds per-variant typed literals).
- The zod side generates a `z.discriminatedUnion`; the codegen template's
  `z.ZodType<Name>` annotation erases the `ZodObject`-ness the union needs,
  so `scripts/_gen-ts.sh` rewrites the union and its options to the
  equivalent `satisfies` form (same drift pin, no type erasure).
- Existing raw-API callers that posted sloppy payloads (two
  `tests/security` suites, dormant e2e specs) needed `type` added — anything
  similar outside the repo will surface as a 400 with a field-naming error.
- Three surfaces still encode the matrix (contract schemas, update field
  rules, SPA gates); the drift tests + generated types cover
  contract-vs-backend and contract-vs-SPA-payload. SPA *rendering* gates
  remain pinned only by component tests (show/hide pairs per type).

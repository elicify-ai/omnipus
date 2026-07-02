# ADR-029: Channel-Instance ↔ Workspace Binding with Workspace-Scoped Mandatory Agent Routing

- **Status:** **Accepted** (2026-07-02, ratified by operator direction: the binding feature was fully implemented on `feat/channel-workspace-binding` — 13 reviewer passes + grill-code, CI green — merged to `hotfix/v0.1.1` at the operator's direction, and the operator commissioned the Track-2 Channels UI that renders this model.) — *Grill history:* rounds 1–3 (R1: 3 CRITICAL + 8 MAJOR → R2: 1 CRITICAL + 4 MAJOR → R3: **0 CRITICAL** + 6 MAJOR, all folded in; convergence 3C→1C→0C). A later round-4 doc review (`…-review-round4.md`, parallel session) raised 1 CRITICAL + 7 MAJOR against the *document*; disposition: **F-01 (BoundInstance derivation ambiguity) is RESOLVED IN THE IMPLEMENTATION** — half-bound states fail loud at load (`pkg/config/config.go` `ErrHalfBoundChannelInstance`, `ValidateChannels`), `IsWorkspaceBound()` is the single authoritative predicate the routing layer consults, and workspace-delete unbinds (`WorkspaceID` + `Identity`, disable) *before* file removal with abort-500 on failure (`rest_workspaces.go` `unbindChannelInstancesForWorkspace`). F-02 (stale factory counts) is a doc-only errata. **Tracked follow-ups (not blockers, pre-existing in the merged feature):** F-03 (warn/reject when binding shadows pre-existing per-peer bindings), F-05 (cascade idempotency/orphan reconciliation), F-06 (remove/gate the legacy `instance_id` *metadata* fallback in `inboundInstanceID` — spoofing hardening; security follow-up).
- **Date:** 2026-07-02
- **Deciders:** Daniel Piatkowski (operator) + architecture
- **Evidence level (highest used):** 1 (user-provided requirements), grounded throughout in `[FACT]` from the codebase
- **Release phase:** v0.3 (Workspaces redesign — [#156]). Structural + touches workspaces/channels topology, so it does **not** belong in v0.1.x/v0.2 per the CLAUDE.md routing rule; multi-instance was already earmarked "v0.3 will lift" `[FACT: pkg/config/config.go:1491]`.

---

## 1. Problem Understanding

**Business objective.** An operator running several workspaces wants each workspace to own its own channel presence — e.g. *"5 workspaces could have 5 WhatsApp numbers"* (operator, verbatim). Concretely, two coupled capabilities are missing:

1. **Multiple instances of one channel type.** Today the system hard-caps **one instance per channel type** `[FACT: maxInstancesPerType = 1, pkg/config/config.go:1492; enforced by ValidateChannelsCap1 at config.go:1676-1705, called at load config.go:3170]`. Five WhatsApp numbers is impossible.
2. **A channel instance bound to a workspace, routing to a mandatory workspace-member agent.** Today a channel's routing is configured as an *optional* "**Default agent**" that falls back to a **global** default; the agent picker lists **every** agent with no workspace scoping. (Correction after review — M-7: an empty selection is **not** a silent no-op; `setChannelRouting` *deliberately removes* the channel-wildcard binding `[FACT: rest.go:6403-6430]` and the UI offers "(Global default)" as an intentional `__none__` choice `[FACT: ChannelConfigPanel.tsx:705-719]`. The change this ADR makes is therefore "empty must be **rejected** for a *bound* instance", not "stop saving silently".)

**The operator's explicit v-next requirement (this ADR's trigger), verbatim:**
> "user needs to select workspace in ui for the agent routing, in the ui only agents that are part of the workspace must be listed, no default agent, if agent is not selected the setting can not be saved and the user needs to get a hint in the ui."

**Stakeholders.** Operators/admins configuring channels; the routing subsystem (`pkg/routing`); the agent loop (`pkg/agent`); the Channels UI (`src/components/skills/ChannelConfigPanel.tsx`).

**Blast radius.** Config schema (`ChannelInstanceConfig`), the `/channels/{id}/routing` contract + handler, **the `ChannelId` wire enum that gates every `/channels/{id}` route** (C-1, below), **the inbound-message `InstanceID`-stamping path in ~13 channel adapters** (C-2, below), the routing resolver's default-fallback path, channel activation (factory plumbing), per-instance state isolation (esp. WhatsApp's SQLite store + per-instance credential refs), the Channels config UI, and — as a positive side effect — session→workspace linkage (a separately-reported gap). This is a **cross-cutting** change with **two one-way-door elements** (the config key scheme *and* the `ChannelId` enum) — both called out in §7.

**Current-state facts (grounded):**
- Channel instances live in `Config.Channels map[string]ChannelInstanceConfig` `[FACT: config.go:122]`; the map key is the **instance ID**, which today equals the channel **type** because the cap is 1/type `[FACT: config.go:1495-1496 comment; normalizeChannelMap backfills Type from key at config.go:1663]`.
- `ChannelInstanceConfig` carries **no** workspace field `[FACT: config.go:1504-1627]`. It **does** carry `Identity *ChannelIdentity` `[FACT: config.go:1508]`, a per-instance routing override `{Kind: "agent"|"user", ID: <agentID>}` `[FACT: config.go:1457-1460]`.
- **The per-instance-agent routing primitive already exists.** `ResolveRoute` **Priority 0** returns agent X directly when the inbound instance carries `Identity{kind:"agent", id:X}`, overriding every binding `[FACT: pkg/routing/route.go:87-100]`. Inbound wires this from `cfg.Channels[instanceID].Identity` `[FACT: loop.go resolveInboundIdentity ~8511-8525, resolveMessageRoute passes Identity ~4580]`.
- Today's `PUT /channels/{id}/routing` (`setChannelRouting`) does **not** use that primitive. It validates the agent **exists** and **is not a worker** (`400` "workers are not chat targets…") `[FACT: rest.go:6377-6398]`, then upserts a **channel-wildcard `AgentBinding`** into `cfg.Bindings` with `match.channel = <channelID>`, `match.account_id = "*"` `[FACT: rest.go:6400-6461]`. `BindingMatch` keys on channel **TYPE**, case-insensitive `[FACT: filterBindings route.go:147-166; BindingMatch config.go:1289-1295]`.
- The routing wire type is `ChannelRouting { default_agent_id?: string }`, documented "*Omitted or empty means fall back to the global default agent*" `[FACT: contracts/components/schemas/ChannelRouting.yaml; src/lib/api/generated/openapi-types.ts:7191-7198]`.
- The **default-agent fallback** is global: `resolveDefaultAgentID` = (agent marked `Default==true` & chat-target) → (first chat-target agent) → (`DefaultAgentID` const), invoked as Priority 7 `[FACT: route.go:296-333, used at route.go:144]`. **No workspace scoping anywhere in the cascade.**
- **Workspace membership is already modeled.** `Workspace.CoreTeam []string` (`core_team`) is the authoritative list of member agent IDs `[FACT: pkg/workspace/workspace.go; wire Workspace.core_team openapi-types.ts:7050-7120]`; `MemberConfigs` is keyed by agent ID and validated ⊆ `CoreTeam` `[FACT: workspace member-config validation]`. The SPA can already read it via `fetchWorkspaces()` / `fetchWorkspace(id)` `[FACT: src/lib/api.ts:2732-2748]`.
- The reverse link `AgentConfig.Workspace` is a **filesystem path**, not a Workspace-entity ID `[FACT: config.go:749 `Workspace string json:"workspace"`]`. There is no agent→workspace-ID index.
- **Sessions carry no workspace ID.** The `Session` struct is `{Key, Messages, Summary, Created, Updated}` `[FACT: pkg/session/manager.go:18-24]`; the session key is `agent + channel(TYPE) + chat` `[FACT: loop.go agentSessionKey ~4624-4628]`. This is the root of the operator's separately-reported "channel sessions don't link to a workspace" pain.
- Multi-instance is blocked by channel factories that hardcode `cfg.Channels["<type>"]` (e.g. `inst := cfg.Channels["whatsapp"]`) `[FACT: pkg/channels/whatsapp_native/init.go:16; telegram.go:66]`, and by channel registration keying on the **factory name** rather than instance ID `[FACT: manager.go initChannel m.channels[name]=ch ~515]`. There are **13 `RegisterFactory` call sites** (12 `*/init.go` + `weixin/weixin.go`) `[FACT: grep pkg/channels, excluding README + the func def — corrects the draft's wrong "11"/"19"]`; separately the `ChannelId` OpenAPI enum has 16 members and `validChannelIDs` allows 14 `[FACT: contracts + rest.go]`. The exact per-category subsets — factories that hardcode a type key for *config selection* vs. *inbound* sites that must stamp `InstanceID` (C-2) — are enumerated in the Gate-0/spike inventory.
- **WhatsApp state collides.** Two WhatsApp instances that both omit `SessionStorePath` default to the same `WorkspacePath()/whatsapp/store.db` `[FACT: whatsapp_native/init.go:18-21; store.db const whatsapp_native.go:48; WorkspacePath()=Agents.Defaults.Workspace config.go:3410]`.
- **(C-1) Every `/channels/{id}` route is gated by a closed enum of channel TYPES.** The channels router rejects any `{id}` not in `validChannelIDs[gen.ChannelId(channelID)]` with a `404` `[FACT: rest.go:5996-5999]` — `getChannelRouting`/`setChannelRouting`/`configureChannel`/enable/disable/test all sit behind it. A per-instance ID like `whatsapp:eu` **404s today**. `ChannelId` is an OpenAPI enum, so opening it is a **wire-contract change** (Constraint #8) and a second one-way door.
- **(C-2) No channel stamps `msg.InstanceID`.** Per-instance Priority-0 routing depends on `inboundInstanceID(msg)`, which reads `msg.InstanceID` (then a legacy metadata key) and **falls back to `msg.Channel` — the TYPE — lowercased — when both are empty** `[FACT: loop.go inboundInstanceID ~8493-8503]`. **No channel adapter populates `InstanceID` today** — the only assignments are bus test fixtures `[FACT: grep: pkg/bus/instanceid_test.go only; no `.InstanceID =` in pkg/channels]`. So two `whatsapp:*` instances both collapse to instanceID `"whatsapp"` and read the same `Identity` → misroute. Stamping inbound `InstanceID` is a distinct workstream from the outbound/activation factory plumbing.
- **(C-3) `resolveMessageRoute` decides *before* Priority-0, and has its OWN default fallback.** Inbound routing checks **explicit `agent_id` metadata first** (returns immediately for a chat-target) `[FACT: loop.go:4485-4519]`, **then a session/chat-scope handoff pin** (`sessionActiveAgent.Load(sessionScopeKey(msg))`) `[FACT: loop.go:4530-4545]`, and only then calls `ResolveRoute` where Priority-0 `Identity` lives. A bound instance's identity does **not** override an explicit `agent_id` or an active handoff pin. **Two** global-default fallbacks exist and both must be intercepted for a bound instance (NEW-1): `pickAgentID` → `resolveDefaultAgentID()` `[FACT: route.go:256-292]`, **and** `resolveMessageRoute` itself → `registry.GetDefaultAgent()` when the resolved agent isn't found `[FACT: loop.go:4583-4585]`, only rejecting (FR-015 unroutable) if *that* is also nil `[FACT: loop.go:4586-4596]`.
- **(MAJ-002) `normalizeChannelMap` DROPS keys not in `knownChannelTypes`.** It `continue`s (silently discards) any map key that isn't a recognized channel TYPE `[FACT: config.go:1654-1668]`. A namespaced key like `whatsapp:eu` is not a known type → it would **vanish at config load** unless the key-check is changed to match on the effective `Type` (parsed from the key/`inst.Type`), not the raw key. This is a hard blocker for the key scheme, not just a "backfill" tweak.
- **(MAJ-004) `ResolvedRoute` has no `Drop` field; `pickAgentID` returns a plain `string`.** `ResolvedRoute` carries `AgentID/Channel/AccountID/SessionKey/MainSessionKey/MatchedBy` `[FACT: route.go:31-38]` — no drop marker — and `pickAgentID(agentID, matchedBy string) string` `[FACT: route.go:255]` can only return an agent ID. So the drift drop-sentinel (NEW-1) requires an **additive `ResolvedRoute.Drop bool`** field + setting the marker in the P0 branch directly (not via pickAgentID's return). This is additive + one guarded branch — it does not reorder the cascade.
- **(MAJ-006) The session key is TYPE-scoped, not instance-scoped.** `agentSessionKey` `[FACT: loop.go:4624]` builds on `BuildAgentPeerSessionKey(SessionKeyParams{…Channel…})` `[FACT: route.go:68-75]` where `Channel` is the TYPE — `SessionKeyParams` has no `InstanceID`. Two `whatsapp:*` instances in different workspaces share the session-key namespace → FR-9 `workspace_id` attribution is non-deterministic and transcripts can collide. `SessionKeyParams` must gain `InstanceID` (Gate 0).

## 2. Extracted Requirements

### Functional
- **FR-1:** A channel instance MUST be associable with **exactly one** workspace. `[INFERENCE from "5 workspaces = 5 numbers"; the inverse (a workspace owning several instances, e.g. a WhatsApp + a Telegram) is allowed]`
- **FR-2:** Configuring an instance's routing MUST require the operator to **select a workspace first**. `[FACT: user requirement]`
- **FR-3:** The routing agent picker MUST list **only agents that are members of the selected workspace** (`Workspace.CoreTeam`), excluding workers. `[FACT: user requirement + rest.go:6394 worker exclusion]`
- **FR-4:** There MUST be **no "global default" option** for a workspace-bound instance's routing. `[FACT: user requirement]`
- **FR-5:** The routing setting MUST **not be saveable** without a selected agent; the UI MUST show a **hint** explaining why. `[FACT: user requirement]`
- **FR-6:** Inbound messages on a bound instance MUST route to its selected member agent whenever no *higher-precedence* per-message signal applies. The precedence is fixed by `resolveMessageRoute` (C-3): explicit `agent_id` metadata > active handoff pin > **Priority-0 instance `Identity`** > bindings > default. A bound instance therefore routes to its member agent for ordinary inbound (no explicit re-target, no active handoff), and a bound instance MUST NOT reach the global-default (P7) fallback — see FR-6a. `[INFERENCE + FACT: loop.go:4485-4545, route.go:87-100/288-292]`
- **FR-6a:** A bound instance MUST NOT silently degrade to the **global** default. The enforcement mechanism is specified in §6/D3 (NEW-1): a `BoundInstance` signal on the route that converts *both* default-fallback sites (`pickAgentID` and `resolveMessageRoute`'s `GetDefaultAgent`) into the **existing FR-015 unroutable-reject path** for a bound-instance drift, rather than a silent default. The drift trigger is precisely bounded: membership is validated at *write* time; at *route* time the binding routes to the persisted `Identity.ID` (a member later removed from `CoreTeam` but still existing routes stale-but-functional, UI warns), and the drop fires only when that agent is **deleted or becomes a worker**. `[FACT: the two fallback sites route.go:256-292 + loop.go:4583-4585 both violate this today — both must be intercepted]`
- **FR-7 (multi-instance):** The system MUST allow **N > 1 instances of the same channel type**, each independently configured and bound. `[FACT: "5 WhatsApp numbers"; requires lifting maxInstancesPerType]`
- **FR-8:** Per-instance state (WhatsApp session DB, media/session paths, **and credential `*_ref` keys**) MUST be **isolated per instance** so instances of the same type do not collide or read each other's secrets. `[INFERENCE from FR-7 + the store.db collision FACT + SEC-23 ref-key note]`
- **FR-9 (side benefit):** Sessions created by a bound instance SHOULD inherit the instance's `workspace_id`, resolving the "channel sessions don't link to a workspace" gap. Scope = *attach the ID at session creation*; downstream grouped-UI/memory-room work is explicitly out of scope. `[INFERENCE — natural consequence; operator flagged this pain separately]`
- **FR-10 (inbound stamping):** Every channel adapter MUST stamp `msg.InstanceID` with its own distinct instance key on each inbound message, sourced from the **trusted adapter** (never from message content — STRIDE spoofing, §7). Without this, per-instance routing is inert (C-2). `[FACT: no producers today]`
- **FR-11 (endpoint enum):** The `/channels/{id}` surface MUST accept per-instance IDs. Either open the `ChannelId` enum (pattern/string) or add an instance-CRUD surface keyed on instance ID (C-1). `[FACT: rest.go:5996-5999 gate]`

### Non-Functional
- **NFR-1 (correctness/routing integrity):** No inbound message may be dropped or misrouted during/after the change. Note the existing `pickAgentID` "log + fall back to default" behavior `[FACT: route.go:288-292]` is **safe for unbound channels but forbidden for bound instances** (FR-6a) — the drift policy (§6/§7) replaces it for bound instances, and observability (M-8, §7) must *measure* misroutes rather than assert their absence.
- **NFR-2 (blast radius / maintainability):** Prefer reusing the existing Priority-0 primitive over adding a new binding dimension. `[EXPERT REASONING]`
- **NFR-3 (single-binary, contract-first):** Any new wire field flows through `contracts/` → generated types; no hand-written wire structs (Constraint #8). `[FACT: CLAUDE.md]`
- **NFR-4 (scale):** Expected instance count is small (tens across all workspaces). No indexing/perf concern. `[ASSUMPTION — no stated target; `[UNKNOWN]` exact ceiling, but the map/loop model is O(instances) and adequate at this scale]`
- **NFR-5 (security):** Per-instance secrets already route through the encrypted store via `*_ref` fields (SEC-23); multi-instance must preserve that (one credential ref set per instance). `[FACT: CLAUDE.md channel-secrets note]`

### Constraints
- Single Go binary, pure Go, contract-first wire formats, deny-by-default `[FACT: CLAUDE.md Hard Constraints]`.
- v0.3 is a **fresh build, no back-compat** `[FACT: CLAUDE.md Release Strategy]` — which materially simplifies migration (see §3, §7).
- No new runtime deps.

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| 1 | Instance↔workspace cardinality | Single `workspace_id` field vs. a list | **1 instance → exactly 1 workspace**; a workspace may own many instances | Confirm an instance never fans out to multiple workspaces |
| 2 | One agent per instance vs. per-peer routing within an instance | Priority-0 override binds the *whole* instance to one agent; per-peer bindings become unreachable for that instance | **One agent per bound instance** (the requirement says "select an agent", singular) | Is finer-grained (per-peer/per-group) routing within a bound instance ever needed? |
| 3 | Is workspace binding mandatory for **all** instances, or optional? | Determines whether legacy/unbound instances keep global-default routing | **Mandatory for any instance configured via the Channels UI**; unbound = not-yet-configured | Confirm no "unbound but active" instance is desired |
| 4 | Handoff / delegation across the workspace boundary | A bound instance routes to a workspace member, but Mia can hand off to an agent (e.g. Ray) — must the handoff target also be a workspace member? | Handoff/delegation governed by the **workspace delegation graph** (`workspaces/<id>.json` `Delegation[]` edges — the SOLE runtime authority `[FACT: pkg/agent/delegation_context.go; see memory context-paging-delivery]`), independent of the routing membership constraint (no new constraint added here) | Should delegation targets be constrained to the instance's workspace? |
| 5 | Config key scheme for N instances | One-way-door: changing the map-key convention later forces a migration | Use a distinct instance ID (e.g. `whatsapp:work`, `whatsapp:eu`) with `Type` disambiguating; `normalizeChannelMap` must stop assuming key==type | Confirm the instance-ID format |
| 6 | Migration of existing single-instance configs | v0.3 is fresh-build, but a bridge may still be wanted | **None** (fresh build); if a bridge is needed, existing type-keyed instances map 1:1 to a default workspace | Confirm no migration bridge required for v0.3 |
| 7 | Session→workspace linkage depth | FR-9 is a side benefit; how far it propagates (UI grouping, memory rooms) is out of scope here | Attach `workspace_id` at session creation; downstream UI grouping is a **separate** work item | Confirm FR-9 scope is "attach the ID", not "build the grouped UI" |
| 8 (C-1) | `ChannelId` wire enum gates `/channels/{id}` | Per-instance IDs 404 today; blocks FR-6/FR-7 end-to-end; **one-way-door** contract change | Open the enum to a `string`/pattern **and/or** add instance-CRUD keyed on instance ID; decide in Gate 0 | Enum-open vs. instance-CRUD surface — which, and does the SPA route by type or instance? |
| 9 (C-2/FR-10) | Who stamps `msg.InstanceID`, and the trusted-source guarantee | Priority-0 is inert until every adapter stamps it; spoofable if sourced from message content | Adapter stamps its own instance key at inbound construction; **never** from payload | Confirm InstanceID is set by the trusted channel adapter (STRIDE spoofing) for all ~13 channels |
| 10 (C-3) | Exact routing precedence for a bound instance vs. explicit `agent_id` / handoff pin | Determines whether a bound instance can be "escaped" by a dropdown pick or an in-flight handoff | **Keep** existing precedence: explicit `agent_id` > handoff pin > instance `Identity`; binding does NOT override a live handoff | Confirm a bound instance may still be re-targeted by explicit selection / handoff (assumed yes) |
| 11 (M-3) | ~~Is `Session.workspace_id` a wire field?~~ **RESOLVED (NEW-2)** | — | `Session.yaml:85` **already defines `workspace_id`** `[FACT]` and the SPA already reads it; the *wire* field exists. FR-9 work = **populate** it for channel sessions (backend), NOT a contract change. (The internal transcript struct in `session/manager.go` has no such field — distinct from the wire `Session`.) | None — no longer a Gate-0 contract item |
| 12 (M-4) | Exact instance-key grammar | Interacts with `inboundInstanceID` lowercasing (loop.go:8495), the `ChannelId` enum, **and credential key derivation** (NEW-3); **one-way door** | `<type>:<slug>`, lowercase, `[a-z0-9-]` slug — but the delimiter MUST be legal wherever the key is embedded (see #13); if `:` is unsafe, use `-`/`__` | Lock grammar (charset/case/delimiter/length) in Gate 0 **and verify the delimiter against the cred-key charset** |
| 13 (M-5, revised NEW-3) | Credential delimiter safety (not namespacing) | `channelCredKey` **already** namespaces refs as `channel_<id>_<field>` `[FACT: rest.go:2985-2987]`, so per-instance isolation is nearly free once `<id>` is per-instance (FR-11). The **real** risk: M-4's `:` delimiter flows in verbatim → `channel_whatsapp:eu_token` — is `:` a legal credential-store key char? | Verify the delimiter against the cred-store key charset; pick a safe one if not. No new namespacing code needed | Confirm `:` (or the chosen delimiter) is legal in credential keys; else change the grammar |
| 14 (MAJ-005) | Instance lifecycle — create / delete / rename / workspace-deletion cascade | An instance is durable state; deleting a workspace with bound instances, deleting/renaming an instance, or unbinding must have defined behavior (orphaned instance? re-onboard? cred cleanup?) | On workspace delete → disable + unbind its instances (don't silently orphan); on instance delete → remove config + creds + WhatsApp store; rename = delete+create (key is identity); no in-place key rename | Define the CRUD + cascade rules (plan-spec) — MUST be covered before implementation |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Routing correctness & integrity | 0.25 | No drops/misroutes; safe degradation |
| Minimal blast radius / reuse existing primitives | 0.20 | Fewer moving parts in the hot path |
| Delivers the stated UX exactly | 0.20 | workspace-select → filtered agents → mandatory → hint |
| Multi-instance readiness (no rework) | 0.15 | Phase-1 work must survive Phase-2 |
| Session/workspace linkage (solves deferred pain) | 0.10 | FR-9 |
| Migration / contract simplicity | 0.10 | v0.3 fresh-build lowers this |

## 5. Option Analysis

Three options for the **primary decision** (how an instance binds to a workspace and routes to its mandatory agent). Multi-instance enablement (FR-7/8) and validation (FR-4/5) are treated as sub-decisions common to all — see §6.

### Option A — Per-instance `workspace_id` + reuse Priority-0 `Identity` routing
Add `WorkspaceID string` to `ChannelInstanceConfig`. Rewrite `setChannelRouting` to (1) require both `workspace_id` and `agent_id`, (2) validate `agent_id ∈ Workspace(workspace_id).CoreTeam` and non-worker, (3) persist `cfg.Channels[instanceID].WorkspaceID` **and set `cfg.Channels[instanceID].Identity = {kind:"agent", id:agent_id}`** — instead of writing a wildcard `AgentBinding`. Routing then flows through the **existing Priority-0 override** (route.go:87-100), *once inbound `InstanceID` is stamped* (FR-10) *and the endpoint enum accepts the instance ID* (FR-11). Sessions inherit `WorkspaceID`. UI adds a workspace picker gating a workspace-filtered agent picker.

**On reusing `Identity` (O-1):** `Identity`'s documented meaning is "this connection acts AS agent X"; here we mean "this instance routes TO agent X". They resolve identically through Priority-0 today, and reusing the primitive is what keeps the hot path untouched — but the semantics differ. Acceptable for now; if a future need separates *acting-as* from *routing-to*, introduce a dedicated `WorkspaceBinding` sub-struct. The `WorkspaceID` field is stored **independently of** `Identity.ID` (the durable binding is the `{workspace_id, agent_id}` pair; `Identity` is the derived routing mechanism).

| Dimension | Assessment |
|---|---|
| Strengths | Reuses the **already-tested** Priority-0 primitive — the routing **priority ordering** is unchanged (the drift mechanism adds two fields + one guarded branch, no existing match arm altered, MAJ-004); per-instance by construction (survives multi-instance); binding is a plain config field; solves FR-9 for free; the *routing-config* contract change is one additive field |
| Weaknesses | **Two prerequisites the ADR initially under-counted:** inbound `InstanceID` must be stamped by every adapter (C-2/FR-10) and the `ChannelId` endpoint enum must accept instance IDs (C-1/FR-11) — neither is "free". Priority-0 overrides *lower* bindings but **not** explicit `agent_id`/handoff pins (C-3), so per-peer routing within a bound instance is not expressible (Gap #2) and the precedence must be stated, not assumed; `setChannelRouting` semantics change (stops writing `cfg.Bindings`); "the channel-wildcard binding for this instance" must be redefined once channel≠instance (N-4 — update `isChannelWildcardRaw`, rest.go:6340) |
| Risks | If both an `Identity` override **and** a stale wildcard binding exist, Identity wins (correct) but the dead binding is confusing → mitigation: on save, remove any wildcard binding for that instance. Removing the default fallback for bound instances must not strand *unbound* channels (webchat etc.) → mitigation: fallback removal is scoped to instances that carry a workspace binding. Drift (agent leaves `CoreTeam`) must **not** silently degrade to global default (M-6/FR-6a) — see §7 drift policy |
| Complexity | **Low–Medium.** Backend: field + handler rewrite + validation; routing: none. UI: workspace picker + filter + validation |
| Cost implications | Build: low. Run: none. Scaling: none |
| Operational impact | No new infra; UI teaches the two-step select; deployment trivial |

### Option B — Extend the routing cascade with instance + workspace bindings
Add `InstanceID` (and optionally `WorkspaceID`) to `BindingMatch`; introduce a new priority (between P0 and the type-wildcard P6) that matches instance-scoped bindings; `setChannelRouting` writes instance-scoped `AgentBinding`s; suppress the P7 default for instances that have a binding.

| Dimension | Assessment |
|---|---|
| Strengths | Preserves the full binding model — **per-peer/guild/team routing within a bound instance stays possible** (addresses Gap #2 if it ever matters); keeps routing config in one place (`cfg.Bindings`) |
| Weaknesses | Touches the **routing hot path** and the `BindingMatch` contract; more state and more test surface; still needs a workspace field somewhere to enforce membership + FR-9; the mandatory-single-agent requirement doesn't actually need this flexibility |
| Risks | Cascade regressions (ordering bugs) are high-severity — every inbound message runs this code; larger contract churn |
| Complexity | **Medium–High.** New match dimension + priority + contract + migration of the existing wildcard-binding behavior |
| Cost implications | Build: medium. Run: negligible. Scaling: negligible |
| Operational impact | Same UI as A, but riskier core; more reviewer load (routing is a 7-reviewer-gate hotspot) |

### Option C — Derive workspace from the selected agent (no instance workspace field)
Store only `agent_id`; infer the workspace by looking up which `Workspace.CoreTeam` the agent belongs to.

| Dimension | Assessment |
|---|---|
| Strengths | No new config field |
| Weaknesses | **`AgentConfig.Workspace` is a filesystem path, not a Workspace ID** `[FACT: config.go:749]`; an agent can appear in **multiple** `CoreTeam` lists → the workspace is **ambiguous**; gives the instance no durable workspace ID, so FR-9 (session linkage) and "instance belongs to workspace" are unmet |
| Risks | Ambiguous/incorrect workspace attribution; breaks the moment an agent joins a second workspace |
| Complexity | Low to build, but **semantically wrong** |
| Cost implications | Low build, high correctness cost |
| Operational impact | Fragile; not recommendable |

## 6. Recommended Architecture

**Adopt Option A**, delivered via one interface-lock gate then parallel workstreams (D2), with the validation/routing sub-decision (FR-4/FR-5/FR-6/FR-6a) and multi-instance sub-decision (FR-7/FR-8) as below. Option A stands *with* the two prerequisites the review surfaced (inbound `InstanceID` stamping FR-10, endpoint-enum FR-11) folded into scope — they raise the cost but do not change the decision (B is still riskier, C still wrong).

**Why A over B and C (tied to §4):** A scores highest on *routing correctness* (0.25 — it leaves the priority *ordering* untouched, reusing the proven Priority-0 override, adding only two fields + one guarded drift branch — vs. B's new match dimension) and *minimal blast radius* (0.20). B buys per-peer-within-instance flexibility the requirement explicitly does not need ("select **an** agent"), at the cost of hot-path risk and contract churn. C is semantically wrong (workspace ambiguity, path-vs-ID). A delivers the stated UX (0.20), is per-instance by construction so it *survives* the multi-instance phase (0.15), and gives FR-9 for free (0.10).
- **Rejected — Option B:** unnecessary hot-path risk for flexibility the requirement doesn't ask for.
- **Rejected — Option C:** workspace attribution is ambiguous and non-durable.

**Primary-decision confidence:**
```
CONFIDENCE: High
  Basis         : The Priority-0 per-instance identity override already exists and is
                  tested (route.go:87-100); the change is additive config/route fields +
                  a handler rewrite + a UI filter + one guarded drift branch. The routing
                  priority ORDERING is unchanged (no existing match arm altered).
  Evidence      : Direct reads of route.go, rest.go setChannelRouting, config.go
                  ChannelInstanceConfig/Identity, Workspace.CoreTeam.
  Missing       : Confirmation of Gaps #1/#2/#3 (cardinality, single-agent, mandatory-for-all)
                  and #8-#13 (endpoint enum, InstanceID stamping, precedence, session-wire,
                  key grammar, cred-ref namespacing) — details, not the decision.
  Would improve : Operator answers on §3; a spike wiring one bound WhatsApp instance
                  end-to-end that specifically proves the inbound msg.InstanceID path
                  (O-2 — a config-read-only spike passes on the type-key fallback = false green).
```

### Sub-decision D2 — Multi-instance enablement (FR-7/FR-8): parallelizable workstreams behind one interface-lock gate
The two bodies of work — **(1) binding + mandatory routing** and **(2) multi-instance plumbing + state isolation** — are **not** sequentially dependent. Their only true coupling is a shared interface (the wire contract, the config key scheme, and the factory-input shape). **Lock that interface first (a small serial gate), then fan the rest out in parallel** (operator's mandated wave pattern). This supersedes the earlier "Phase 1 then Phase 2" framing: "Phase 1" (binding/routing/UX — the operator's explicit ask) and "Phase 2" (N-per-type) are now concurrent workstreams that meet only at the locked seam.
- **Binding/routing content** (delivers FR-1–FR-6, FR-9): add `WorkspaceID`, rewrite `setChannelRouting`, route via Priority-0, build the routing UI, attach `workspace_id` to sessions. **Per-instance by construction** — nothing here is redone when N-per-type lands.
- **Multi-instance content** (delivers FR-7/FR-8): decouple the map key from the type (Gap #5), thread `instanceID` through the 11 factories and `initChannel` registration, isolate per-instance state (WhatsApp store path → `.../whatsapp/<instanceID>/store.db`; audit media/session paths), replace `ValidateChannelsCap1`/`normalizeChannelMap`'s key==type assumption.

The concrete decomposition (serial gate → 6 parallel workstreams → integration) is in **§9 → Parallel Execution Plan**.

```
CONFIDENCE: Medium
  Basis         : Phase-1/Phase-2 split is clean because binding + Identity are already
                  per-instance; Phase-2 scope is enumerated (11 factories + state paths).
  Evidence      : Factory grep (init.go hardcoded type keys), WhatsApp store.db collision,
                  normalizeChannelMap key==type assumption — all cited [FACT].
  Missing       : The exact instance-ID key format (Gap #5); a full inventory of every
                  per-type fixed path beyond WhatsApp (media store, etc.).
  Would improve : A short spike enumerating all per-type filesystem paths + a decision on
                  the key format before Phase 2 begins.
```

### Sub-decision D3 — Mandatory selection, no default, and drift (FR-4/FR-5/FR-6a): enforce on both ends
- **Contract:** extend `ChannelRouting` with `workspace_id` (additive; regenerate types per Constraint #8). For a workspace-bound instance, `default_agent_id` is **required, non-empty**.
- **Backend — the full rejection set (M-1), all `422` unless noted:** (1) `workspace_id` present + `agent_id` empty → 422; (2) `agent_id ∉ Workspace(workspace_id).CoreTeam` → 422; (3) `workspace_id` unknown/archived → 404; (4) agent is a worker → 422 (existing 400 semantics may be kept, but standardize). The membership check is **server-side** (client filter is UX only — STRIDE tampering/EoP, §7). Chosen code: **422** (semantic validation failure) over 409 for (1)/(2).
- **Independent pair (M-2):** the binding persists `{workspace_id, agent_id}` as an **independent pair**; membership is validated `agent_id ∈ Workspace(workspace_id).CoreTeam` **at write time**. The agent's membership in *other* workspaces is irrelevant.
- **Precedence (C-3):** binding sets `Identity{kind:agent}` for ordinary inbound but **preserves** the existing higher-precedence signals — an explicit `agent_id` (SPA dropdown) and an active handoff pin still win (loop.go:4485-4545). Rationale: a workspace-bound WhatsApp number must still support Mia→Ray handoff and operator override. A bound instance is prevented from reaching **P7 (global default)** by construction: for ordinary inbound its `Identity` fires at P0; the only way to miss P0 is drift (below), which is intercepted — so P7 is unreachable for a bound instance.
- **Drift policy + enforcement mechanism (M-6 / NEW-1 / FR-6a) — the load-bearing detail:** the promise "a bound instance never reaches the global default" is **not** self-enforcing, because two default-fallback sites sit on the path and neither knows the route is bound (`pickAgentID`, route.go:256-292; `resolveMessageRoute`→`GetDefaultAgent`, loop.go:4583-4585). Mechanism:
  1. **Signal + carrier (MAJ-004):** add `RouteInput.BoundInstance bool`, set true by `resolveMessageRoute` when `resolveInboundIdentity(instanceID)` yields an agent-kind identity **and** the instance has a `WorkspaceID`; and add an **additive `ResolvedRoute.Drop bool`** field (the struct today has no drop marker — `route.go:31-38` — so this is a new field, set with `MatchedBy:"bound.drift.drop"`). `pickAgentID` returns a plain `string` and cannot carry the signal, so the drop is set in the **P0 branch of `ResolveRoute` directly**, not via `pickAgentID`.
  2. **Site 1 (`ResolveRoute` P0 branch):** when `BoundInstance` and the P0 identity agent is unresolvable — **deleted, disabled, or a worker (MAJ-003)** — construct a `ResolvedRoute{Drop:true, MatchedBy:"bound.drift.drop"}` instead of calling `pickAgentID`/`resolveDefaultAgentID()`. Non-bound callers are unaffected (the branch keys on the flag) — ordinary channels keep today's safe default behavior.
  3. **Site 2 (`resolveMessageRoute`):** when `route.Drop`, **skip `registry.GetDefaultAgent()`** and fall straight into the **already-existing FR-015 unroutable path** (structured reject + `WarnCF` log, loop.go:4586-4596) — extended with an operator notification and a `bound_instance_drift` counter. Reuses an existing reject path rather than inventing one.
  4. **Bound trigger only:** this fires solely on the *ordinary-inbound P0* path. Explicit `agent_id` and handoff pins return before `ResolveRoute` (deliberate overrides, not drift) and retain their own fallback semantics.
  - **Scope honesty:** this is **not** "zero routing changes" — it is *two additive fields* (`RouteInput.BoundInstance`, `ResolvedRoute.Drop`) + *one bound-guarded branch* in P0 + *one guard* in `resolveMessageRoute`. The **priority ordering is unchanged**; no existing match arm is altered. That is the accurate framing (correcting the earlier "zero-change" overstatement).
  - **Result:** a bound instance provably cannot reach P7/global-default — the only two escape hatches are closed by the flag, verified by the "bound + deleted agent → reject-not-default" BDD scenario (§9). The SPA additionally warns at config time when a bound agent is no longer a `CoreTeam` member. (Queue-and-retry instead of drop-and-reject is a v-next policy option.)
- **Empty CoreTeam (M-1):** if the selected workspace has no eligible (non-worker) members, the picker shows an empty state with "add a member to this workspace first" and Save stays disabled.
- **Frontend:** mirror the existing idiom — a `canSave` boolean gating `disabled={!canSave}` (as in `ScheduleFormSheet.tsx:425-430,650-654`) and an inline error-color hint (as in `AgentProfile.tsx` `heartbeat-body-required-hint`). Remove the "(Global default)" option **only in the bound flow** (a workspace is selected) — the unbound/legacy surface (webchat) keeps its current behavior (N-1/N-2); gate the agent `SmartSelect` on `workspaceId !== ''`.

```
CONFIDENCE: High
  Basis         : Both the validation idiom (canSave + disabled + hint) and the data
                  source (fetchWorkspaces → core_team; fetchAgents) already exist; the
                  precedence + drift decisions are now explicit and grounded.
  Evidence      : ChannelConfigPanel.tsx:326-330/678-724; api.ts fetchWorkspaces 2732-2748;
                  Workspace.core_team openapi-types.ts:7050-7120; loop.go:4485-4545 precedence;
                  route.go:288-292 forbidden-fallback; validation exemplars cited.
  Missing       : Whether drift is drop-and-alert vs. queue-and-retry (v-next option) — a
                  policy choice, not a blocker.
  Would improve : Operator confirmation of drop-and-alert vs. queue for drift.
```

## 7. Risks and Caveats

- **One-way door #1 — instance-ID key scheme.** Once N-per-type ships with a chosen key format, changing it later forces a config migration. Decide the grammar (Gap #12/M-4: charset, case, delimiter, length) **in Gate 0**, and trace it through `normalizeChannelMap`, `ValidateChannelsCap1`, `inboundInstanceID` lowercasing (loop.go:8495), and credential `*_ref` key derivation.
- **One-way door #2 — the `ChannelId` wire enum (C-1).** Opening `ChannelId` (or adding an instance-CRUD surface) is a contract change that clients bake in; reverting it is breaking. Decide the shape in Gate 0. Until then, *every* `/channels/{id}` call for a per-instance ID 404s `[FACT: rest.go:5996-5999]`.
- **Default-fallback removal must be scoped.** Removing the global default (FR-4) applies **only** to instances that carry a workspace binding. Unbound surfaces (webchat, an as-yet-unconfigured channel) must retain `resolveDefaultAgentID` so no message is stranded (NFR-1). Mitigation: gate the "no default" behavior on presence of `WorkspaceID`/`Identity`.
- **Dead wildcard bindings.** Phase 1 stops writing `cfg.Bindings` for these instances; a previously-written channel-wildcard binding would linger and confuse (Identity still wins, so behavior is correct, but the config is misleading). Mitigation: on save, delete any channel-wildcard binding for the instance.
- **WhatsApp store collision (Phase 2).** Two WhatsApp instances without distinct `SessionStorePath` corrupt one SQLite store `[FACT]`. Mitigation: default the path to `.../whatsapp/<instanceID>/store.db`; never share.
- **Cross-workspace handoff (Gap #4).** A bound instance routes to a workspace member, but a handoff (Mia→Ray) or delegation can target an agent outside that workspace via the delegation graph. This ADR **does not** add a workspace constraint to delegation; if the operator wants handoffs confined to the workspace, that is a **separate** decision. Flagged, not resolved.
- **Membership drift (M-6 — corrected).** The current `pickAgentID` mitigation falls back to `resolveDefaultAgentID` — the **global default** `[FACT: route.go:288-292]` — which is exactly what FR-4 forbids for a bound instance. The ADR's drift policy (D3/§6) **replaces** that path for bound instances with **drop-and-alert** (error audit + operator notification), never global-default degrade. A UI warning fires at config time when a bound agent leaves its `CoreTeam`.
- **Inbound `InstanceID` provenance (STRIDE spoofing).** `msg.InstanceID` MUST be set by the **trusted channel adapter** from its own configured instance key, never derived from message content — otherwise an attacker-controlled inbound could claim another instance's identity and route to a privileged agent (FR-10). Reviewer/QA must assert this on every adapter.
- **Per-instance credential isolation (M-5, revised per NEW-3).** `channelCredKey` **already** namespaces refs as `channel_<id>_<field>` `[FACT: rest.go:2985-2987]`, so per-instance isolation is nearly free once `<id>` becomes per-instance (FR-11) — *no new namespacing code needed*. The residual risk is the **delimiter**: the M-4 instance key is embedded verbatim, so `whatsapp:eu` yields `channel_whatsapp:eu_token`. Verify `:` (or the chosen delimiter) is a legal credential-store key character; if not, the key grammar (M-4/Gate 0) must use a safe delimiter. STRIDE info-disclosure is otherwise already mitigated by the existing per-`{id}` keying.
- **Contract discipline.** The `workspace_id` addition, the `ChannelId` enum change, an optional `Session.workspace_id`, and any other wire change MUST go through `contracts/` → `make gen-contracts` → committed generated diff (Constraint #8). No hand-written wire structs.

- **Instance lifecycle & workspace-deletion cascade (MAJ-005).** A bound instance is durable state with a real lifecycle the routing-only framing omitted. Rules to nail in plan-spec: **workspace delete** → its bound instances are disabled + unbound (never silently orphaned routing to a dead workspace); **instance delete** → remove config entry, credential refs (`channelCredKey`), and the per-instance WhatsApp store; **rename** → treated as delete+create (the key IS the identity — no in-place key mutation, which would strand creds/stores); **unbind** → revert to unconfigured (no default fallback engages because the instance is no longer bound). The `member_configs ⊆ CoreTeam` precedent (ADR-027) is the model for cascade discipline.

### Operability, rollout & observability (M-8)
- **Implicit feature flag = config presence.** An instance with no `WorkspaceID`/`Identity` behaves exactly as today (default routing); the new path only engages for bound instances. Make this explicit in the rollout note so an incident can be de-risked by clearing the binding.
- **Observability.** Extend the existing `matched_by` route field so a bound-instance route logs `identity.agent` and a fall-through logs `default` — a metric/log distinguishing the two is the *measurement* NFR-1 asserts (don't just claim "no drops"). Add a counter for drift drop-and-alert events.
- **Rollout ordering.** Gate 0 lands the contract inertly (no behavior change). If WS-A/E land but WS-B/C don't, only single-instance-per-type is reachable — still a valid, shippable state (the operator's core routing ask), because binding + routing don't depend on N-per-type.
- **3am runbook line.** "Inbound not reaching the bound agent → check (a) the adapter stamps `msg.InstanceID` with the instance key, (b) `cfg.Channels[<id>].Identity` is set, (c) the `/channels/<id>` route isn't 404ing on the `ChannelId` enum, (d) no active handoff pin / explicit `agent_id` is overriding."

## 8. Confidence Assessment

| Decision | Confidence | Dominant driver |
|---|---|---|
| **Primary** — Option A (workspace_id + Priority-0 routing) | **High** | Reuses a tested primitive; zero routing-*cascade* change. Cost rose after review (FR-10 stamping + FR-11 enum are real prerequisites) but the decision is unchanged — B still riskier, C still wrong |
| **D2** — Parallelizable workstreams behind Gate 0 | **Medium** | Fan-out is clean once the seam is locked; open items: key grammar (M-4), full per-type path/enum inventory, inbound-stamping surface |
| **D3** — Mandatory + no default + drift (both ends) | **High** | Idiom + data sources present; precedence (C-3) + drift (M-6) now explicit and grounded |

No global-only score is given; each decision carries its own block above. Overall: the *routing + UX* slice (WS-A/E) is High-confidence and ready to spec once Gate 0's two one-way doors (`ChannelId` enum, key grammar incl. delimiter safety) are decided; the *multi-instance* slice (WS-B/C/F) is Medium pending the enumerated open items. Round-1 CRITICALs (C-1 endpoint enum, C-2 inbound stamping, C-3/M-6 precedence+drift) → in-scope requirements FR-10/FR-11/FR-6a. Round-2 CRITICAL (NEW-1: the drift promise was unenforceable as placed) → a concrete `BoundInstance`-flag + additive `ResolvedRoute.Drop` + reuse-FR-015-reject mechanism spanning both fallback sites (§6/D3). Round-2 MAJORs: Session wire field already exists (NEW-2), cred namespacing already free — delimiter is the risk (NEW-3). Round-3 (0 CRITICAL): factory count corrected to **13** call sites (MAJ-001), `normalizeChannelMap` key-drop blocker (MAJ-002), `ResolvedRoute.Drop` carrier + disabled-agent trigger (MAJ-003/004), instance lifecycle + workspace-deletion cascade (MAJ-005), instance-scoped session key (MAJ-006). No architectural change across any round — Option A held throughout; the revisions were scope-accuracy and enforcement-mechanism detail.

## 9. Validation / Next Steps

1. **Resolve the blocking-ish gaps** (§3 rows 1–3, 5): cardinality (1 instance→1 workspace), single-agent-per-instance, mandatory-for-all-configured-instances, and the Phase-2 instance-ID key format. These change details, not the fundamental approach — Option A holds regardless.
2. **Re-red-team (round 2):** grill-spec round 1 returned REVISE (3 CRITICAL + 8 MAJOR, all folded in above). Re-run `/grill-spec docs/internal/architecture/ADR-029-channel-instance-workspace-binding.md` to confirm the CRITICALs are closed before speccing.
3. **Spec the chosen option:** `/plan-spec docs/internal/architecture/ADR-029-channel-instance-workspace-binding.md` — the routing+UX slice first (FR-1–FR-6a, FR-9 + D3), with BDD scenarios that MUST include (per review §4): workspace-select → filtered agents; empty-agent → save blocked + hint; **bound instance + active handoff pin** (handoff still wins); **bound instance + explicit `agent_id`** (dropdown still wins); bound-instance ordinary inbound → member agent via P0; membership-mismatch → 422; unknown/archived workspace → 404; empty CoreTeam → disabled Save; **drift (agent leaves CoreTeam) → drop-and-alert, NOT global default**; `PUT /channels/whatsapp:eu/routing` accepted (not 404); two same-type instances → distinct sessions/creds/stores/`InstanceID`.
4. **Spike (O-2 — must prove the inbound path):** wire one bound instance end-to-end and assert the **inbound `msg.InstanceID` stamping** actually drives Priority-0 — a config-read-only spike passes on the type-key fallback and gives a false green. Also enumerate every per-type fixed filesystem path (WhatsApp store confirmed; audit media/session stores) to de-risk the 11-factory + inbound-stamping refactor.

### Parallel Execution Plan

The work parallelizes cleanly once a small shared interface is agreed. **One serial gate, then a 6-way fan-out.** `[EXPERT REASONING — dependency analysis of the grounded facts]`

**Gate 0 — Interface lock (serial, small, blocks everything).** This is the *only* genuinely non-parallel work; it exists because every workstream consumes these decisions and settling them mid-flight causes rework and merge collisions. Expanded after review to include the two one-way doors the first draft missed.
- **Wire contract:** `ChannelRouting.workspace_id` (additive) + required-non-empty `default_agent_id` for bound instances; **the `ChannelId` enum decision (C-1/FR-11)** — open it to a `string`/pattern or add an instance-CRUD surface (`POST /channels` + `/channels/{instanceID}/…`). Note `Session.workspace_id` is **already in the contract** (`Session.yaml:85`, NEW-2) — no schema change; FR-9 just populates it (WS-D). Run `make gen-contracts`; land the generated diff first (Constraint #8).
- **Config key grammar (Gap #12/M-4, one-way door):** namespaced instance key `<type>:<slug>` with explicit `Type`; fix charset/case/delimiter/length; **verify the delimiter is legal in credential-store keys** since `channelCredKey` embeds it verbatim as `channel_<id>_<field>` (NEW-3). Two load-bearing consequences to fix in the same lane: (a) **`normalizeChannelMap` must match `knownChannelTypes` on the effective `Type`, not the raw key** — today it `continue`s (DROPS) any non-type key, so `whatsapp:eu` would vanish at load (MAJ-002); (b) `ValidateChannelsCap1` counts by effective `Type` (already does); (c) the key feeds `inboundInstanceID` lowercasing.
- **Session-key scope (MAJ-006):** `SessionKeyParams` + `agentSessionKey` must gain `InstanceID` so N same-type instances in different workspaces don't share a transcript namespace (route.go:68-75, loop.go:4624). Additive; decide the key shape here.
- **Factory-input + inbound-stamping shape:** decide how `instanceID` reaches factories (new `ChannelFactoryInput{cfg, instanceID, secrets, bus}` vs. added param) **and how it reaches the *running* adapter so it can stamp `msg.InstanceID` on inbound** (C-2/FR-10) — the two are different plumbing paths and both are seams the fan-out builds against.
- **Deliverable:** merged contract diff + Go interface stubs (compiling no-ops). Owner: architect + one backend-lead. **Everything below starts only after this lands.**

**Fan-out — 6 workstreams, developed concurrently against the locked seam:**

| WS | Scope | Primary files | Depends on | Collision risk |
|---|---|---|---|---|
| **A — Config, binding, endpoint & drift** (backend-lead) | `WorkspaceID` on `ChannelInstanceConfig`; `setChannelRouting` rewrite (require workspace+agent, validate ∈ `CoreTeam`, set `Identity`, drop stale wildcard binding via redefined `isChannelWildcardRaw` N-4, full 422/404 rejection set M-1); **the NEW-1 drift mechanism** — `RouteInput.BoundInstance` flag + drop sentinel in `pickAgentID` (route.go) **and** the skip-`GetDefaultAgent`→FR-015-reject in `resolveMessageRoute` (loop.go:4583-4596); **open the `ChannelId` endpoint enum / instance-CRUD (C-1)**; `matched_by`/`bound_instance_drift` observability (M-8) | `config.go` (struct), `rest.go` (handler + router enum), `route.go` (pickAgentID + BoundInstance), **`loop.go` (resolveMessageRoute drop path)** | Gate 0 | shares `config.go` with B (partition by symbol) and `loop.go` with B/D (partition by function — A owns `resolveMessageRoute`, B owns inbound stamping, D owns session creation) |
| **B — Multi-instance activation, stamping & keying** (backend-lead) | Thread `instanceID` through the type-keyed factories (of **13 `RegisterFactory` sites**, the subset hardcoding `cfg.Channels["<type>"]`) + `initChannel` registration keying; **stamp `msg.InstanceID` on inbound from the trusted adapter (C-2/FR-10)** — enumerate the exact inbound sites (Gate-0/spike inventory); **fix `normalizeChannelMap` to match `knownChannelTypes` on effective `Type` not the raw key (MAJ-002)**; **add `InstanceID` to `SessionKeyParams`/`agentSessionKey` (MAJ-006)**; audit `m.channels[name]` map for type-key collisions (O-3); replace the `ValidateChannelsCap1`/`normalizeChannelMap` key==type assumption | `manager.go`, `*/init.go` + inbound sites, `config.go` (validate/normalize), `route.go`+`loop.go` (session key) | Gate 0 (factory-input + stamping + key shapes) | see A |
| **C — Per-instance state & credential isolation** (backend-lead) | WhatsApp store → `.../whatsapp/<instanceID>/store.db`; audit + isolate other per-type fixed paths; **namespace credential `*_ref` keys by instanceID + collision guard (M-5)** | `whatsapp_native/*`, per-type path sites, credential-ref write path (`configureChannel`) | Gate 0 (instanceID + key grammar) — integrates with B | low (mostly isolated) |
| **D — Session→workspace linkage** (backend-lead) | **Populate** the already-existing `Session.workspace_id` wire field (`Session.yaml:85`, NEW-2) at channel session creation, inherited from the bound instance (FR-9). No contract change — backend wiring only | `loop.go` (session creation), `session/*` | Gate 0 + WS-A field | low |
| **E — Routing UX** (frontend-lead) | Workspace picker → workspace-filtered agent list → mandatory + hint; remove "(Global default)"; gate agent select on `workspaceId` | `ChannelConfigPanel.tsx` | Gate 0 contract only | shares files with F — partition by component region |
| **F — Multi-instance UX** (frontend-lead) | "Add instance" flow + per-type instance list | `ConnectorsScreen.tsx`, `ChannelConfigPanel.tsx` | Gate 0 contract only | see E |

**Ordering constraints (the only real ones):**
- Gate 0 → all. Nothing else is serial.
- **Development** of all six is concurrent (each codes against the locked contract/stubs). **Integration** couplings: WS-F's add-instance UI is only *exercisable* once WS-B lands (backend accepts N instances); WS-C integrates with WS-B's instanceID plumbing. These are integration-time joins, not development-time blocks.
- **Same-file collisions** are managed by partition, not serialization: A/B split `config.go` by symbol; E/F split the two SPA components by region. If partition proves fiddly, serialize *only* the overlapping file's edits within that language lane — the rest still runs parallel.

**Wave shape (operator's mandated pattern):** Gate 0 (2 agents) → Wave 1 fan-out (A–F, up to 6 dev agents in parallel) → 7-reviewer gate → fix wave → integration + e2e → 14-reviewer final sign-off before any PR to base.

**Critical path:** Gate 0 → WS-B (the factory + inbound-stamping refactor across the type-keyed subset of the 19 registered factories is the longest single lane) → integration → e2e. WS-A/E (the operator's explicit routing ask) is a *shorter* lane and can be demoed independently the moment it + Gate 0 land, even before WS-B finishes — preserving the "ship the UX first" value without a hard phase boundary.

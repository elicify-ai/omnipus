# Feature Spec — Channel-Instance ↔ Workspace Binding + Multi-Instance Routing

- **Source ADR:** [ADR-029](../architecture/ADR-029-channel-instance-workspace-binding.md) (Proposed, grill-spec rounds 1–3, 0 CRITICAL)
- **Date:** 2026-07-02
- **Release phase:** v0.3
- **Scope decision (operator-confirmed):** **Full feature** — routing/binding/UX slice **and** multi-instance (N-per-type). Binding model: **1 instance → 1 workspace → 1 mandatory member agent** (a workspace may own many instances; unbound instances retain today's default routing). Drift policy: **drop + alert** (never global default).
- **Status:** Revised after grill-spec round 1 (review: `…-spec-review.md`; 0 CRITICAL, 8 MAJOR + minors folded in, all re-verified against source). Ready for `/taskify`.
- **Locked decisions (were deferred; grill-spec required locking now):**
  - **Instance-key grammar (A-1):** `<type>.<slug>` — delimiter **`.`** (dot: filesystem-safe on Windows unlike `:`, legal as a JSON credential-store key, and no channel type contains a dot so the key is unambiguously splittable). `slug = [a-z0-9-]{1,32}`, **lowercase enforced (uppercase → 422)**. `Type` stored explicitly; `normalizeChannelMap` derives/validates Type from the pre-dot segment. Bare-type keys (`whatsapp`) remain valid for legacy single instances.
  - **Endpoint surface (A-2):** **open** the `ChannelId` wire type from a closed enum to a validated **pattern** `^[a-z0-9-]+(\.[a-z0-9-]+)?$`; the per-route handler then validates the id exists in `cfg.Channels` (unknown → 404 `unknown instance`). Instance-CRUD (`POST /channels`) is added for create; there is no separate "enum-gate 404" once the enum is a pattern.
  - **Interaction model (MAJ-001):** the routing section currently **auto-persists** on select (`doSaveRoutingDebounced`). The bound flow keeps that model — an **invalid selection is simply not persisted** (client guard) and an inline hint is shown; there is no new "Save button to disable". The behavioral requirement is *"an invalid routing config for a bound instance is neither auto-persisted nor accepted (422), with a hint"* — affordance-agnostic.
  - **Drift observability (MAJ-003):** emit a structured **audit event** `channel.routing.drift_drop` via the existing `auditLogger` (loop has `audit.Logger`) + increment an in-memory `atomic.Int64` (mirrors `mediaRefsDropped`); the SPA badge reads recent audit events. No new metrics subsystem (single-binary constraint).

---

## 1. Overview

### Problem
An operator running several workspaces cannot give each workspace its own channel presence. Two gaps: (a) only **one instance per channel type** is allowed (`maxInstancesPerType=1`), so "5 workspaces = 5 WhatsApp numbers" is impossible; (b) a channel's routing agent is an *optional* pick from **all** agents that falls back to a **global** default, with no workspace scoping and no mandatory selection.

### Solution (one line)
Bind each channel **instance** to exactly one **workspace**, route its inbound to a **mandatory member agent** via the existing Priority-0 identity override, lift the one-per-type cap with per-instance state isolation, and make the routing UI select workspace → filter agents → require an agent.

### Actors
- **Operator/Admin** — configures channels, selects workspace + agent, adds instances.
- **Routing subsystem** (`pkg/routing`) — resolves inbound → agent.
- **Agent loop** (`pkg/agent`) — inbound handling, session creation.
- **Channel adapters** (`pkg/channels/*`) — stamp `InstanceID`, isolate state.
- **SPA** (`src/`) — the Channels config UI.

### In scope
- `WorkspaceID` on channel instances; workspace-scoped mandatory agent routing (select → filter → require → hint).
- Bound-instance routing via Priority-0; precedence preservation; **drift drop+alert**.
- N instances per type: key scheme, `normalizeChannelMap` fix, 13-factory `instanceID` threading, `initChannel` keying, inbound `InstanceID` stamping, `SessionKeyParams.InstanceID`.
- Per-instance state isolation: WhatsApp store path, credential `*_ref` delimiter safety.
- `ChannelId` endpoint-enum / instance-CRUD; instance lifecycle + workspace-deletion cascade.
- Session `workspace_id` population (wire field already exists).
- Observability (`matched_by`, `bound_instance_drift` counter); contract-first wire changes.

### Out of scope
- Per-peer/guild routing *within* a bound instance (Priority-0 binds the whole instance to one agent).
- Constraining handoff/delegation targets to the instance's workspace (delegation graph unchanged).
- Grouped session UI / memory-room propagation of `workspace_id` (only the attach is in scope).
- Queue-and-retry drift (drop+alert chosen for v1).
- Back-compat migration (v0.3 fresh-build).

### Constraints
- Single Go binary, pure Go, no new runtime deps.
- Contract-first (Constraint #8): every wire change via `contracts/` → `make gen-contracts` → committed generated diff.
- Build tags `goolm,stdjson`; CI authoritative (never run full gateway suite locally).
- Deny-by-default; per-instance secrets stay in the encrypted store (`*_ref`).

---

## 2. Available Reference Patterns
N/A — no `docs/reference/` library applies (this is the Omnipus Go core, not the supastarter-derived reference set). Internal precedents reused instead: the **Priority-0 identity override** (`route.go:87-100`), the **`member_configs ⊆ CoreTeam`** cascade discipline (ADR-027), and the **FR-015 unroutable-reject** path (`loop.go:4586-4596`).

---

## 3. Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|--------|------|---------|
| `config.ChannelInstanceConfig` (config.go:1504-1627) | **modify** | Add `WorkspaceID string`; already has `Identity *ChannelIdentity`, `SessionStorePath`, `Type`, `Enabled` |
| `config.Config.Channels map[string]ChannelInstanceConfig` (config.go:122) | **extend** | Map key becomes a per-instance ID (`<type>:<slug>`), no longer == type |
| `maxInstancesPerType` (config.go:1492) + `ValidateChannelsCap1` (1676-1705) | **modify** | Lift cap; count by effective `Type` |
| `normalizeChannelMap` (config.go:1654-1668) | **modify** | Match `knownChannelTypes` on effective `Type`, not raw key (else namespaced keys DROPPED) |
| `setChannelRouting` (rest.go:6366-6476) | **REWRITE** (not modify — MAJ-004) | Today upserts a channel-wildcard `AgentBinding` in `cfg.Bindings`. New: write `cfg.Channels[id].WorkspaceID`+`Identity`, **remove** any wildcard binding for the id; require workspace+agent; validate ∈ CoreTeam; rejection set |
| `getChannelRouting` (rest.go:6352-6364) | **REWRITE** (MAJ-004) | Today reads the wildcard binding (`channelWildcardIdx`). New: for a **bound** instance read `{WorkspaceID, Identity.ID}` from `cfg.Channels[id]`; for an **unbound** instance read the legacy wildcard binding. The two representations are **mutually exclusive per instance**. Return `workspace_id` + the applicable `default_agent_id` |
| `channelCredKey` (rest.go:2985-2987) | **calls** | Already namespaces `channel_<id>_<field>`; delimiter must be cred-safe |
| channels router enum gate (rest.go:5996-5999) `validChannelIDs[gen.ChannelId]` | **modify** | Accept per-instance IDs (open enum / instance-CRUD) |
| `RouteResolver.ResolveRoute` (route.go:53-145) | **extend** | Add `BoundInstance` handling in the P0 branch; no reordering |
| `RouteInput` (route.go:11-28) | **modify** | Add `BoundInstance bool`. NOTE (MIN-001): `RouteInput.InstanceID` and `RouteInput.Identity` **already exist** and are already passed by `resolveMessageRoute` — the only net-new field is `BoundInstance` |
| `ResolvedRoute` (route.go:31-38) | **modify** | Add `Drop bool` (no such field today) |
| `pickAgentID` (route.go:255-293) | **calls** | Untouched signature; P0 branch bypasses it on bound drift |
| `resolveDefaultAgentID` (route.go:296-333) | **calls** | Global default — must be unreachable for bound instances |
| `resolveMessageRoute` (loop.go:4475-4600) | **modify** | Set `BoundInstance`; honor `Drop` (skip `GetDefaultAgent`); emit drift audit event |
| `inboundInstanceID` (loop.go:8493-8503) | **calls** | Reads `msg.InstanceID`; **already exists** — needs adapter producers (FR-020) |
| `resolveInboundIdentity` (loop.go:8511-8525) | **calls** | Reads `cfg.Channels[instanceID].Identity`; **already exists** |
| `agentSessionKey` (loop.go:4624-4630) | **modify** (MAJ-002 — the ACTUAL inbound session-key path) | Today = `agent:<id>:chat:<Channel(TYPE)>:<ChatID>` for channels (or `:session:<SessionID>` for webchat). Make it use the stamped `InstanceID` instead of `Channel` for channel inbound → `agent:<id>:chat:<InstanceID>:<ChatID>`. **`BuildAgentPeerSessionKey`/`SessionKeyParams` are NOT on the inbound transcript path** (their `SessionKey` is discarded by `resolveMessageRoute`) — do not rely on them for FR-023 |
| `auditLogger` (`audit.Logger`, loop.go:124) + `atomic.Int64` (pattern: `mediaRefsDropped` loop.go:109) | **calls/extend** | Emit `channel.routing.drift_drop` audit event + increment a drift counter (MAJ-003) |
| `MailboxConfig.WorkspaceID` + `ValidateMailboxesCap1` (config.go:2251-2275) | **reference precedent** (OBS-004) | Existing workspace-binding idiom (cap-1 mailbox-per-workspace). Reuse the field + validator shape; note ours is the inverse cardinality (many instances per workspace) |
| `weixin.go:40` + `googlechat` (`"google-chat"`) | **modify** (OBS-003) | Two irregular `RegisterFactory` sites outside the `*/init.go` pattern — WS-B must not miss them in a mechanical `init.go` sweep |
| `Manager.initChannels` / `initChannel` (manager.go:599-663 / ~515) | **modify** | Iterate instances; key `m.channels` by instanceID |
| 13 `RegisterFactory` sites (12 `*/init.go` + `weixin.go`) | **modify** | Accept `instanceID`; stamp inbound `msg.InstanceID` |
| `whatsapp_native` store path (init.go:16-21, whatsapp_native.go:48) | **modify** | `.../whatsapp/<instanceID>/store.db` |
| `Workspace.CoreTeam []string` (pkg/workspace/workspace.go) | **calls** | Membership source of truth |
| wire `ChannelRouting` (ChannelRouting.yaml) | **modify** | Add `workspace_id` |
| wire `Session.workspace_id` (Session.yaml:85) | **calls** | Already exists — populate only |
| `ChannelConfigPanel.tsx` (routing section 678-724, agent query 326-330) | **modify** | Workspace picker + filtered agents + mandatory + hint |
| `ConnectorsScreen.tsx` | **modify** | Add-instance flow + per-type instance list |
| `fetchWorkspaces`/`fetchWorkspace` (api.ts:2732-2748), `setChannelRouting` (api.ts:1709-1723) | **calls** | Data sources; add `workspace_id` to PUT body |

### Impact Assessment
| Symbol Modified | Risk | Direct Dependents (d=1) | Indirect (d=2/3) |
|----------------|------|------------------------|------------------|
| `RouteInput` / `ResolvedRoute` (add fields) | **MEDIUM** | `ResolveRoute`, `resolveMessageRoute`, all `choose()` returns | every inbound message path; routing tests |
| `ResolveRoute` P0 branch (drift) | **HIGH** | `resolveMessageRoute` | 100% of inbound routing — hot path |
| `resolveMessageRoute` (Drop honoring) | **HIGH** | agent loop inbound dispatch | every channel + webchat inbound |
| `config.Channels` key scheme + `normalizeChannelMap` | **HIGH** | config load, `initChannels`, all 13 factories, `configureChannel`, `channelCredKey` | boot; every channel; cred store |
| `agentSessionKey` (+InstanceID for channel inbound) | **MEDIUM** | `resolveMessageRoute`, session lookup/persistence | transcript continuity for all channels; two same-type instances must not share a chat key |
| `setChannelRouting` | **MEDIUM** | routing PUT handler, SPA | contract tests |
| `ChannelId` enum gate | **MEDIUM** | all `/channels/{id}` routes | SPA channel calls |
| 13 factories + inbound stamping | **HIGH (breadth)** | each channel adapter | per-channel e2e |

**Callout:** the two HIGH hot-path changes (`ResolveRoute` drift branch, `resolveMessageRoute` Drop) run on every inbound message. Regression tests around existing routing precedence (explicit `agent_id`, handoff pin, default fallback for *unbound* channels) are mandatory and must pass unchanged.

### Relevant Execution Flows
| Flow | Relevance |
|------|-----------|
| Inbound message → `resolveMessageRoute` → `ResolveRoute` → agent | Where bound-instance routing + drift enforcement insert |
| `configureChannel` / `setChannelRouting` → config write + cred refs | Where binding + validation persist |
| Gateway boot → `LoadConfig` → `normalizeChannelMap`/`ValidateChannelsCap1` → `initChannels` → factories | Where multi-instance activation happens |
| WhatsApp `Start()` → SQLite store open | Where per-instance isolation matters |
| SPA Channels → Configure panel → routing PUT | Where the UX requirement lives |

---

## 4. User Stories & Acceptance Criteria

### US-1 — Select a workspace for an instance's routing (P0)
As an operator, when I configure a channel instance's routing, I must first choose which workspace it belongs to, so the instance's traffic is scoped to that workspace.

- **Why P0:** the operator's explicit requirement; nothing else in the bound flow works without it.
- **Interaction model (MAJ-001):** the panel auto-persists routing on select today; the bound flow keeps that model. "Cannot save without an agent" = an incomplete/invalid bound selection is **not persisted** (client guard) and the backend independently rejects it (422). The agent `SmartSelect` gates on `workspaceId` via its existing `disabled` prop (MIN-003 — confirmed supported, no new component work).
- **Independent Test:** open the Configure panel for a channel; assert a workspace selector renders and the agent selector is disabled until a workspace is chosen.
- **Acceptance Scenarios:**
  1. **Given** the Configure panel is open for an unconfigured instance, **When** it renders, **Then** a workspace selector is shown and the agent selector is disabled (`SmartSelect disabled`).
  2. **Given** no workspace is selected, **When** the panel is in this state, **Then** no routing is persisted (no PUT fires).
  3. **Given** I select a workspace, **When** the selection commits, **Then** the agent selector becomes enabled and populates from that workspace.

### US-2 — Agent picker lists only the workspace's members (P0)
As an operator, after choosing a workspace I see only that workspace's member agents (non-workers) in the routing agent list.

- **Why P0:** core requirement; prevents binding to a non-member agent.
- **Independent Test:** select a workspace whose `core_team=[mia,ray]` and a global agent set of `[mia,ray,jim,worker1]`; assert the picker shows exactly `[mia,ray]`.
- **Acceptance Scenarios:**
  1. **Given** workspace W with `core_team=[mia,ray]`, **When** I open the agent picker, **Then** it lists exactly `mia` and `ray`.
  2. **Given** a worker agent is in `core_team`, **When** the picker lists members, **Then** the worker is excluded.
  3. **Given** workspace W has an empty `core_team`, **When** the picker renders, **Then** it shows an "add a member to this workspace first" state and no agent is selectable (so nothing persists).

### US-3 — Agent selection is mandatory; no global default (P0)
As an operator, I cannot save an instance's routing without selecting an agent, and there is no "(Global default)" option in the bound flow; a hint tells me why.

- **Why P0:** the crux of the requirement.
- **Independent Test:** select a workspace, leave the agent empty, assert no PUT fires + a hint is visible; attempt the raw PUT with empty agent and assert 422.
- **Acceptance Scenarios:**
  1. **Given** a workspace is selected and no agent chosen, **When** I view the panel, **Then** no routing is persisted and a hint explains an agent is required.
  2. **Given** the bound flow, **When** I open the agent picker, **Then** there is no "(Global default)" option.
  3. **Given** a client sends `PUT /channels/{id}/routing` with `workspace_id` set and empty `default_agent_id`, **When** the backend processes it, **Then** it returns **422** and does not persist a binding.

### US-4 — Bound instance routes inbound to its member agent (P0)
As an operator, inbound messages on a bound instance are handled by its selected member agent, while my explicit dropdown pick and any active handoff still take precedence.

- **Why P0:** the operational payoff of the binding.
- **Independent Test:** bind instance I→(W, ray); send an inbound with no explicit agent and no handoff pin; assert it routes to `ray`. Then send one with explicit `agent_id=jim`; assert `jim`.
- **Acceptance Scenarios:**
  1. **Given** instance I bound to `ray`, **When** an ordinary inbound arrives (no explicit agent, no handoff), **Then** it routes to `ray` via `identity.agent`.
  2. **Given** instance I bound to `ray` and an active handoff pin to `ava`, **When** an inbound arrives with no explicit agent, **Then** it routes to `ava` (handoff wins).
  3. **Given** instance I bound to `ray`, **When** an inbound carries explicit `agent_id=jim`, **Then** it routes to `jim` (explicit wins).

### US-5 — Bound instance never degrades to global default; drift drops+alerts (P0)
As an operator, if a bound instance's agent is deleted/disabled/becomes a worker, its inbound is rejected with an alert rather than silently answered by the global default.

- **Why P0:** FR-4 correctness; the ADR's round-2 CRITICAL.
- **Independent Test:** bind I→ray; delete `ray`; send an inbound; assert the message is rejected (unroutable path), a `bound_instance_drift` counter increments, and no message reaches the global default.
- **Acceptance Scenarios:**
  1. **Given** instance I bound to `ray` and `ray` deleted, **When** an ordinary inbound arrives, **Then** the route is a drop (`MatchedBy=bound.drift.drop`), the message is rejected, and an operator alert is emitted.
  2. **Given** the same, **When** routing resolves, **Then** `registry.GetDefaultAgent()` is NOT used and no global-default agent receives the message.
  3. **Given** `ray` is merely removed from `core_team` but still exists, **When** an inbound arrives, **Then** it still routes to `ray` (stale-but-functional) and the SPA shows a config-time warning.

### US-6 — Multiple instances per channel type (P1)
As an operator, I can create N instances of the same channel type (e.g. 5 WhatsApp numbers), each independently configured and bound.

- **Why P1:** the "5 numbers" enabler; larger surface, builds on US-1..5.
- **Independent Test:** create `whatsapp.eu` and `whatsapp.us`; assert both survive config load, both activate, and each is independently bindable.
- **Acceptance Scenarios:**
  1. **Given** a config with keys `whatsapp.eu` and `whatsapp.us` (both `type: whatsapp`), **When** the gateway loads config, **Then** both entries survive `normalizeChannelMap` and pass `ValidateChannelsCap1`.
  2. **Given** two active WhatsApp instances, **When** the manager initializes channels, **Then** both are registered under distinct keys and start.
  3. **Given** two bound instances, **When** each receives inbound, **Then** each routes to its own workspace's member agent.

### US-7 — Per-instance state & credential isolation (P1)
As an operator, two instances of one type do not share session storage or credentials.

- **Why P1:** correctness/security for multi-instance; without it two WhatsApp numbers corrupt one store.
- **Independent Test:** start `whatsapp.eu` and `whatsapp.us`; assert distinct store directories and distinct credential keys; assert no shared `store.db`.
- **Acceptance Scenarios:**
  1. **Given** two WhatsApp instances with default paths, **When** they start, **Then** they use `.../whatsapp/whatsapp.eu/store.db` and `.../whatsapp/whatsapp.us/store.db` respectively.
  2. **Given** two instances of one type with tokens, **When** secrets are stored, **Then** the credential keys are distinct (`channel_<id>_<field>`) and the chosen key delimiter is legal in credential keys.
  3. **Given** instance A's credentials, **When** instance B resolves its secret, **Then** B cannot read A's ref.

### US-8 — Inbound InstanceID stamping (P1)
As the routing subsystem, every inbound message carries the trusted instance key so per-instance routing actually engages.

- **Why P1:** Priority-0 per-instance routing is inert without it (ADR C-2).
- **Independent Test:** send an inbound through the WhatsApp `whatsapp.eu` adapter; assert `msg.InstanceID == "whatsapp.eu"` and that `inboundInstanceID` returns it (not the `whatsapp` type fallback).
- **Acceptance Scenarios:**
  1. **Given** an inbound on instance `whatsapp.eu`, **When** the adapter constructs the bus message, **Then** `msg.InstanceID = "whatsapp.eu"`.
  2. **Given** a stamped inbound, **When** `resolveInboundIdentity` runs, **Then** it reads `cfg.Channels["whatsapp.eu"].Identity` (not `cfg.Channels["whatsapp"]`).
  3. **Given** message content containing a spoofed instance id, **When** the adapter stamps, **Then** `InstanceID` comes from the adapter's configured key, never from message content.

### US-9 — Session inherits workspace_id (P1)
As an operator, a session created by a bound instance is linked to that instance's workspace.

- **Why P1:** resolves the "channel sessions don't link to a workspace" pain; wire field already exists.
- **Independent Test:** send an inbound on a bound instance; read the created session; assert `workspace_id` equals the instance's workspace.
- **Acceptance Scenarios:**
  1. **Given** instance I bound to workspace W, **When** an inbound creates a session, **Then** the session's `workspace_id == W`.
  2. **Given** two same-type instances in different workspaces, **When** each creates a session, **Then** the session keys differ by `InstanceID` and do not collide.

### US-10 — Instance lifecycle & workspace-deletion cascade (P2)
As an operator, creating/deleting/renaming instances and deleting a workspace behave predictably without orphaning routing.

- **Why P2:** durability/correctness; not needed for the first happy path but required before GA.
- **Independent Test:** delete a workspace that owns a bound instance; assert the instance is disabled+unbound (not routing to a dead workspace).
- **Acceptance Scenarios:**
  1. **Given** workspace W owns bound instance I, **When** W is deleted, **Then** I is disabled and unbound.
  2. **Given** instance I with credentials + WhatsApp store, **When** I is deleted, **Then** its config entry, credential refs, and store directory are removed.
  3. **Given** instance I, **When** a "rename" is requested, **Then** it is handled as delete+create (no in-place key mutation).

### US-11 — Endpoint accepts per-instance IDs (P1)
As the SPA, `/channels/{id}` routes work for per-instance IDs.

- **Why P1:** without it every bound-instance config call 404s (ADR C-1). Today `ChannelId` is a closed 15-member enum; `validChannelIDs` allows 14 (all but `webchat`). Per the locked A-2 decision, the enum opens to the pattern `^[a-z0-9-]+(\.[a-z0-9-]+)?$` and per-route existence is validated against `cfg.Channels`.
- **Independent Test:** `PUT /channels/whatsapp.eu/routing` with a valid body for an existing instance; assert not 404. Then an id for a non-existent instance; assert 404 "unknown instance".
- **Acceptance Scenarios:**
  1. **Given** an existing instance `whatsapp.eu`, **When** `GET/PUT /channels/whatsapp.eu/routing` is called, **Then** the id passes the pattern and the route is served.
  2. **Given** a well-formed id `whatsapp.zzz` with no such instance, **When** a routing call is made, **Then** it returns 404 with a body indicating "unknown instance" (design-agnostic — there is no separate enum-gate outcome once the enum is a pattern).
  3. **Given** a malformed id (e.g. uppercase or illegal chars), **When** a routing call is made, **Then** it returns 400/404 for a malformed id, distinct from "unknown instance".

### Edge Cases
- Same agent selected for two instances in two different workspaces (both must be members of their own workspace) → allowed; independent bindings.
- Agent belongs to multiple workspaces' `core_team` → membership validated against the *selected* workspace only.
- Workspace archived after binding → treated like unknown on write (404); existing binding routes stale until reconfigured, SPA warns.
- Instance key with illegal characters / uppercase / reserved delimiter → rejected at config validation.
- `default_agent_id` set but `workspace_id` absent (legacy/unbound path) → unbound behavior (default routing) preserved; no new mandatory rule applied.
- Two instances default to the same WhatsApp store because `SessionStorePath` unset → must NOT collide (per-instance default path).
- Inbound arrives during a config reload that changed the binding → routes per the config snapshot at resolution time.
- Handoff pin set to an agent outside the instance's workspace → still honored (delegation graph authority; out of scope to constrain).

---

## 5. Behavioral Contract & Boundaries

### Behavioral Contract
- When an operator opens routing config, the system requires a workspace selection before enabling agent selection.
- When a workspace is selected, the system lists only that workspace's non-worker members as routing candidates.
- When no agent is selected for a bound instance, the system blocks save (UI) and rejects the PUT with 422 (backend).
- When a bound instance receives ordinary inbound, the system routes to its member agent via Priority-0 identity.
- When an explicit `agent_id` or active handoff pin is present, the system honors that over the instance binding.
- When a bound instance's agent is unresolvable, the system drops-and-alerts and never routes to the global default.
- When N instances of one type exist, the system loads, activates, isolates state, and routes each independently.
- When a bound instance creates a session, the system stamps the session's `workspace_id`.
- When an unbound instance receives inbound, the system uses today's default routing unchanged.

### Explicit Non-Behaviors
- The system must not fall back to the global default for a *bound* instance, because FR-4/US-5 forbid it.
- The system must not override an explicit `agent_id` or an active handoff pin with the instance binding, because operator/hand-off intent must win.
- The system must not add a workspace constraint to handoff/delegation targets, because the delegation graph is the sole authority (out of scope).
- The system must not derive an instance's workspace from the agent, because an agent may be in multiple workspaces (ambiguous).
- The system must not share a WhatsApp store or credential ref between two instances, because it corrupts state / leaks secrets.
- The system must not source `msg.InstanceID` from message content, because that enables identity spoofing.
- The system must not drop a namespaced config key at load (`normalizeChannelMap` currently warns-and-drops non-type keys), because instances would vanish — FR-016 matches on effective `Type` instead.
- The system must not change the routing priority *ordering* — only add a bound-guarded drift branch.

### Integration Boundaries
- **Credential store** (`channelCredKey`): keys `channel_<id>_<field>`; instance id embedded — the key delimiter MUST be a legal credential-key character. Failure mode: illegal delimiter → cred write/read fails → channel can't authenticate. Dev approach: real store; a unit test asserts the delimiter round-trips.
- **WhatsApp / whatsmeow SQLite store**: per-instance directory `.../whatsapp/<instanceID>/store.db`. Failure mode: shared path → lock contention/corruption. Dev approach: real store per instance; test asserts distinct paths.
- **Workspace store** (`Workspace.CoreTeam`): read at write-time to validate membership; read to populate the picker. Failure mode: workspace missing/archived → 404. Dev approach: real store.
- **Contracts (OpenAPI)**: `ChannelRouting.workspace_id` additive; `ChannelId` enum opened / instance-CRUD; `Session.workspace_id` already present. Failure mode: stale generated types → `make verify-contracts` fails. Dev approach: regenerate + commit.

---

## 6. BDD Scenarios

> Format per `bdd-template.md`. Every scenario has `Traces to:` (US + acceptance #) and a category.

```gherkin
Feature: Workspace-scoped mandatory routing config

  Scenario: Agent selector disabled until a workspace is chosen        # Happy Path
    Traces to: US-1 / AC-1
    Given the Configure panel is open for an unconfigured instance
    When the panel renders
    Then a workspace selector is visible
    And the agent selector is disabled

  Scenario: Selecting a workspace enables and filters the agent list   # Happy Path
    Traces to: US-1 / AC-3, US-2 / AC-1
    Given a global agent set [mia, ray, jim, worker1]
    And workspace "sales" has core_team [mia, ray]
    When I select workspace "sales"
    Then the agent selector is enabled
    And it lists exactly [mia, ray]

  Scenario: Worker agents are excluded from the picker                 # Alternate Path
    Traces to: US-2 / AC-2
    Given workspace "sales" has core_team [mia, worker1]
    When I open the agent picker for "sales"
    Then it lists [mia]
    And it does not list worker1

  Scenario: Empty core_team blocks save with guidance                  # Edge Case
    Traces to: US-2 / AC-3
    Given workspace "empty" has core_team []
    When I select workspace "empty"
    Then the agent picker shows an "add a member first" state
    And Save is disabled

  Scenario: Save blocked and hint shown when no agent selected         # Error Path
    Traces to: US-3 / AC-1
    Given workspace "sales" is selected and no agent chosen
    When I view the panel
    Then Save is disabled
    And a hint states an agent is required

  Scenario: No global-default option in the bound flow                 # Happy Path
    Traces to: US-3 / AC-2
    Given a workspace is selected
    When I open the agent picker
    Then there is no "(Global default)" option

  Scenario Outline: Backend rejects invalid bound routing              # Error Path
    Traces to: US-3 / AC-3, US-2 / AC-1
    Given a PUT to "/channels/whatsapp.eu/routing"
    When the body is <body>
    Then the response status is <status>
    And no binding is persisted

    Examples:
      | body                                              | status |
      | {workspace_id: "sales", default_agent_id: ""}     | 422    |
      | {workspace_id: "sales", default_agent_id: "jim"}  | 422    |   # jim not in core_team
      | {workspace_id: "sales", default_agent_id: "worker1"} | 422 |   # worker
      | {workspace_id: "ghost", default_agent_id: "mia"}  | 404    |   # unknown workspace

  Scenario: Valid bound routing persists and sets instance identity    # Happy Path
    Traces to: US-3 / AC-3, US-4 / AC-1
    Given workspace "sales" has core_team [mia, ray]
    When I PUT {workspace_id: "sales", default_agent_id: "ray"} to "/channels/whatsapp.eu/routing"
    Then the response is 200
    And cfg.Channels["whatsapp.eu"].WorkspaceID == "sales"
    And cfg.Channels["whatsapp.eu"].Identity == {kind: "agent", id: "ray"}
    And any stale channel-wildcard binding for the instance is removed

  Scenario: GET round-trip of a bound instance reads Identity, not the binding # Happy Path
    Traces to: US-3 / AC-3 (MAJ-004)
    Given instance "whatsapp.eu" was bound via PUT to workspace "sales" agent "ray"
    When I GET "/channels/whatsapp.eu/routing"
    Then the response has workspace_id "sales" and default_agent_id "ray"
    And the value is read from cfg.Channels["whatsapp.eu"], not a wildcard binding

Feature: Bound-instance inbound routing and drift

  Scenario: Ordinary inbound routes to the bound member agent          # Happy Path
    Traces to: US-4 / AC-1
    Given instance "whatsapp.eu" bound to workspace "sales" agent "ray"
    And an inbound message with no explicit agent_id and no handoff pin
    When the message is routed
    Then it routes to "ray" with MatchedBy "identity.agent"

  Scenario: Active handoff pin wins over the binding                   # Alternate Path
    Traces to: US-4 / AC-2
    Given instance "whatsapp.eu" bound to "ray"
    And an active handoff pin to "ava" for the chat
    And an inbound with no explicit agent_id
    When the message is routed
    Then it routes to "ava"

  Scenario: Explicit agent_id wins over the binding                    # Alternate Path
    Traces to: US-4 / AC-3
    Given instance "whatsapp.eu" bound to "ray"
    And an inbound carrying explicit agent_id "jim"
    When the message is routed
    Then it routes to "jim"

  Scenario Outline: Drift drops-and-alerts, never global default       # Error Path
    Traces to: US-5 / AC-1, US-5 / AC-2, US-5 / AC-3
    Given instance "whatsapp.eu" bound to "ray"
    And "ray" is <ray_state>
    And an ordinary inbound message
    When the message is routed
    Then the route outcome is <outcome>
    And the global default agent is <default_used>

    Examples:
      | ray_state              | outcome                        | default_used |
      | deleted                | drop (bound.drift.drop) + alert | not used     |
      | disabled               | drop (bound.drift.drop) + alert | not used     |
      | a worker               | drop (bound.drift.drop) + alert | not used     |
      | removed from core_team | routed to ray (stale) + UI warn | not used     |

  Scenario: Unbound channel keeps today's default routing             # Happy Path (regression)
    Traces to: US-5 / AC-2 (non-behavior), Regression
    Given an instance with no WorkspaceID and no Identity
    And an inbound with no explicit agent_id and no handoff pin
    When the message is routed
    Then it routes via the existing default cascade unchanged

Feature: Multi-instance activation and isolation

  Scenario: Namespaced keys survive config load                       # Happy Path
    Traces to: US-6 / AC-1
    Given config channels keyed "whatsapp.eu" and "whatsapp.us" both type "whatsapp"
    When the config is loaded
    Then both entries are present after normalizeChannelMap
    And ValidateChannelsCap1 passes

  Scenario: Two instances of one type both activate under distinct keys # Happy Path
    Traces to: US-6 / AC-2
    Given two enabled WhatsApp instances "whatsapp.eu" and "whatsapp.us"
    When the manager initializes channels
    Then both are registered under distinct keys
    And both start without error

  Scenario: Per-instance WhatsApp store isolation                     # Happy Path
    Traces to: US-7 / AC-1
    Given two WhatsApp instances with unset SessionStorePath
    When they start
    Then they use distinct store directories keyed by instanceID
    And no store.db is shared

  Scenario: Credential keys are per-instance and delimiter-safe        # Edge Case
    Traces to: US-7 / AC-2, US-7 / AC-3
    Given the chosen instance-key delimiter
    When a token is stored for "whatsapp.eu"
    Then the credential key channel_whatsapp.eu_token round-trips in the store
    And instance "whatsapp.us" cannot read it

  Scenario: Inbound is stamped with the trusted instance id           # Happy Path
    Traces to: US-8 / AC-1, US-8 / AC-2
    Given an inbound on the "whatsapp.eu" adapter
    When the adapter constructs the bus message
    Then msg.InstanceID == "whatsapp.eu"
    And resolveInboundIdentity reads cfg.Channels["whatsapp.eu"].Identity

  Scenario: InstanceID is never sourced from message content          # Error Path (security)
    Traces to: US-8 / AC-3
    Given an inbound whose content embeds "instance_id=whatsapp.us"
    When the "whatsapp.eu" adapter stamps the message
    Then msg.InstanceID == "whatsapp.eu"

  Scenario: Session inherits the instance workspace                   # Happy Path
    Traces to: US-9 / AC-1, US-9 / AC-2
    Given instance "whatsapp.eu" bound to workspace "sales"
    When an inbound creates a session
    Then the session workspace_id == "sales"
    And the session key includes the instance id

Feature: Endpoint and lifecycle

  Scenario: Routing endpoint accepts a per-instance id                # Happy Path
    Traces to: US-11 / AC-1
    Given an instance "whatsapp.eu"
    When I GET "/channels/whatsapp.eu/routing"
    Then the response is not a 404 enum-gate rejection

  Scenario: Unknown well-formed instance id is 404 "unknown instance"  # Error Path
    Traces to: US-11 / AC-2
    Given the ChannelId pattern accepts "whatsapp.zzz" but no such instance exists
    When I GET "/channels/whatsapp.zzz/routing"
    Then the response is 404 with body indicating "unknown instance"

  Scenario: Workspace deletion cascades to its bound instances        # Alternate Path
    Traces to: US-10 / AC-1
    Given workspace "sales" owns bound instance "whatsapp.eu"
    When workspace "sales" is deleted
    Then "whatsapp.eu" is disabled and unbound

  Scenario: Instance deletion removes config, creds, and store        # Alternate Path
    Traces to: US-10 / AC-2
    Given instance "whatsapp.eu" with credentials and a store directory
    When the instance is deleted
    Then its config entry, credential refs, and store directory are removed
```

---

## 7. Test-Driven Development Plan

| Order | Test Name | Level | Traces to BDD | Description |
|-------|-----------|-------|---------------|-------------|
| 1 | `TestNormalizeChannelMap_KeepsNamespacedKeys` | Unit | Namespaced keys survive load | `whatsapp.eu` not dropped; matches on effective Type |
| 2 | `TestValidateChannelsCap_AllowsNPerType` | Unit | Two instances activate | cap lifted; counts by Type |
| 3 | `TestInstanceKeyGrammar_Validation` | Unit | Cred delimiter-safe | reject illegal chars/case; delimiter legal in cred key |
| 4 | `TestChannelCredKey_PerInstance_RoundTrip` | Unit | Cred keys per-instance | `channel_whatsapp.eu_token` stores + reads; B can't read A |
| 5 | `TestResolvedRoute_DropField` + `TestRouteInput_BoundInstance` | Unit | Drift carrier | additive fields present; default zero-value safe |
| 6 | `TestResolveRoute_BoundInstance_RoutesToMember` | Unit | US-4 AC-1 | P0 identity → member agent |
| 7 | `TestResolveRoute_BoundDrift_Drops_NotDefault` | Unit | US-5 drift outline | deleted/disabled/worker → Drop, `resolveDefaultAgentID` NOT called |
| 8 | `TestResolveRoute_StaleMember_StillRoutes` | Unit | US-5 AC-3 | removed-from-CoreTeam but existing → routes stale |
| 9 | `TestResolveMessageRoute_ExplicitAndHandoff_WinOverBinding` | Unit | US-4 AC-2/AC-3 | precedence preserved (extends existing precedence tests) |
| 10 | `TestResolveMessageRoute_BoundDrop_SkipsGetDefaultAgent` | Unit | US-5 AC-2 | Drop → FR-015 reject, no `GetDefaultAgent` |
| 11 | `TestResolveMessageRoute_Unbound_DefaultUnchanged` | Unit (regression) | Unbound regression | existing default cascade intact |
| 12 | `TestAgentSessionKey_IncludesInstanceID` | Unit | US-9 AC-2 | assert on `agentSessionKey` (the real inbound path): two same-type instances with the same ChatID → distinct keys. Do NOT assert on `BuildAgentPeerSessionKey` (false-green — MAJ-002) |
| 13 | `TestSetChannelRouting_RejectionSet` | Integration | US-3 outline | 422 empty/∉CoreTeam/worker; 404 unknown workspace |
| 14 | `TestSetChannelRouting_ValidBinding_SetsIdentity` | Integration | valid persists | WorkspaceID + Identity set; wildcard binding removed |
| 15 | `TestGetChannelRouting_BoundReadsIdentity` | Integration | GET round-trip (MAJ-004) | bound instance: GET reads `{WorkspaceID, Identity.ID}` (not the wildcard binding); unbound: reads the wildcard binding |
| 16 | `TestChannelsRouter_AcceptsInstanceID` | Integration | US-11 AC-1/2 | enum gate accepts `whatsapp.eu`; unknown → 404 |
| 17 | `TestInitChannels_NInstancesPerType` | Integration | US-6 AC-2 | both register + start under distinct keys |
| 18 | `TestWhatsApp_PerInstanceStorePath` | Integration | US-7 AC-1 | distinct store dirs; no shared db |
| 19 | `TestInbound_StampsInstanceID_TrustedSource` | Integration | US-8 all | adapter stamps its key; not from content |
| 20 | `TestSessionCreation_InheritsWorkspaceID` | Integration | US-9 AC-1 | session `workspace_id` == instance workspace |
| 21 | `TestWorkspaceDelete_CascadesToInstances` | Integration | US-10 AC-1 | bound instances disabled+unbound |
| 22 | `TestInstanceDelete_RemovesConfigCredsStore` | Integration | US-10 AC-2 | full teardown |
| 23 | `contract_test` additions (Go + zod) | Integration | contract | `ChannelRouting.workspace_id` valid JSON; `ChannelId` pattern accepts `whatsapp.eu` |
| 23a | `TestWorkspaceDelete_PartialCascade_AbortsIntact` | Integration | MAJ-005 | config-unbind fails → whole delete aborts (500), workspace + bindings intact (no orphan) |
| 23b | `TestInstanceID_DrivesPriority0_EndToEnd` (the O-2 spike, promoted) | Integration | OBS-002, MAJ-002, FR-020 | send a real inbound through a stamping adapter; assert P0 fires on the STAMPED `InstanceID`, not the type-key fallback — the false-green guard before the 13-factory fan-out |
| 23c | `TestConfigRepair_RejectsBothRepresentations` | Integration | FR-029/OBS-001 | an instance with both `Identity` and a stale wildcard binding is normalized/rejected at load |
| 23d | `TestSetChannelRouting_EmitsAuditEvent` | Integration | FR-030 | a re-bind writes a routing-change audit event |
| 24 | `channel-routing.spec.ts` (Playwright) | E2E | US-1/2/3 | workspace select → filtered agents → invalid-not-persisted + hint |
| 25 | `multi-instance-routing.spec.ts` (Playwright) | E2E | US-6/7/9 | two instances route independently, distinct sessions |
| 26 | `bound-drift.spec.ts` (Playwright) | E2E | US-5 | delete bound agent → inbound rejected, not default |

**Order:** Unit (1–12) → Integration (13–23) → E2E (24–26). Within levels, foundations (config/key/route fields) before consumers.

### Test Datasets

**DS-1 — Routing rejection (setChannelRouting)**
| # | workspace_id | default_agent_id | core_team | expected | Traces to |
|---|---|---|---|---|---|
| 1 | sales | ray | [mia,ray] | 200 | valid-binding |
| 2 | sales | "" | [mia,ray] | 422 | US-3/AC-3 |
| 3 | sales | jim | [mia,ray] | 422 | US-2/AC-1 (∉ team) |
| 4 | sales | worker1 | [mia,worker1] | 422 | US-2/AC-2 |
| 5 | ghost | mia | (missing) | 404 | US-3 outline |
| 6 | archived1 | mia | [mia] (archived) | 404 | edge |
| 7 | sales | ray | [mia,ray] | 200 | valid (agent ids are not case-folded here; the lowercase rule is for instance keys, DS-2) |

**DS-2 — Instance key grammar** (locked: `<type>.<slug>`, `slug=[a-z0-9-]{1,32}`, lowercase, delimiter `.`)
| # | key | valid? | reason | Traces to |
|---|---|---|---|---|
| 1 | whatsapp.eu | yes | well-formed | US-6/AC-1, FR-017 |
| 2 | whatsapp.US | no | uppercase slug → 422 | FR-017 |
| 3 | whatsapp. | no | empty slug | boundary |
| 4 | whatsapp.eu.2 | no | second `.` (slug may not contain `.`) | boundary |
| 5 | telegram.main-bot | yes | hyphen legal in slug | US-6 |
| 6 | google-chat.sales | yes | hyphenated type; split on first `.` | irregular type |
| 7 | whatsapp | yes | bare type = legacy single instance | back-compat |
| 8 | whatsapp.<33 chars> | no | slug exceeds 32-char max (FR-017) | boundary |

**DS-3 — Drift trigger states**
| # | agent state | outcome | default used | Traces to |
|---|---|---|---|---|
| 1 | present, in team | routed | no | US-4/AC-1 |
| 2 | deleted | drop+alert | no | US-5/AC-1 |
| 3 | disabled | drop+alert | no | US-5 (MAJ-003) |
| 4 | worker | drop+alert | no | US-5 |
| 5 | removed from team, exists | routed (stale)+warn | no | US-5/AC-3 |
| 6 | in team, **workspace archived** | routed (stale)+warn | no | MAJ-006/FR-013 |
| 7 | in team, **workspace deleted** | n/a — instance unbound by cascade (FR-025) | no | US-10/AC-1 |

**DS-4 — Inbound InstanceID stamping**
| # | adapter | msg content | expected InstanceID | Traces to |
|---|---|---|---|---|
| 1 | whatsapp.eu | "hi" | whatsapp.eu | US-8/AC-1 |
| 2 | whatsapp.eu | "instance_id=whatsapp.us" | whatsapp.eu | US-8/AC-3 |
| 3 | telegram.main | "hi" | telegram.main | US-8 |
| 4 | (legacy, no instance) | "hi" | <type> (fallback) | back-compat |

### Regression Test Requirements
This **modifies existing routing**. Behaviors that MUST be preserved:
1. Explicit `agent_id` metadata precedence (existing `route_explicit_priority_test.go` — must pass unchanged).
2. Handoff-pin precedence incl. the chat-scope fix (`TestResolveMessageRoute_ChannelHandoffOverride_RoutesByChatScope`).
3. Default cascade for **unbound** channels (P1–P7) — `resolveDefaultAgentID` behavior for non-bound routes.
4. `pickAgentID` worker/unknown → default fallback for **non-bound** callers.

New regression tests: `TestResolveMessageRoute_Unbound_DefaultUnchanged` (#11), and re-run of the full `route_explicit_priority_test.go` suite. Regression dataset: DS-3 row 1 + the unbound scenario confirm old behavior on non-bound paths.

---

## 8. Requirements & Success Criteria

### Functional Requirements
- **FR-001**: The UI MUST require a workspace selection before enabling agent selection in the bound routing flow.
- **FR-002**: The UI MUST list only the selected workspace's `core_team` members, excluding workers.
- **FR-003**: The bound routing flow MUST NOT present a "(Global default)" option.
- **FR-004**: The UI MUST disable Save and show a hint when no agent is selected for a bound instance.
- **FR-005**: `setChannelRouting` MUST return 422 when `workspace_id` is present and `default_agent_id` is empty.
- **FR-006**: `setChannelRouting` MUST return 422 when the agent is not in the workspace's `core_team`.
- **FR-007**: `setChannelRouting` MUST return 404 when `workspace_id` is unknown or archived.
- **FR-008**: `setChannelRouting` MUST reject a worker agent (422). NOTE (MIN-002): the live handler returns **400** here today — this standardizes it to 422; check for any existing test/SPA asserting 400 on this path.
- **FR-009**: The UI MUST show an "add a member first" state and keep Save disabled when the workspace's `core_team` is empty.
- **FR-010**: A bound instance MUST route ordinary inbound to its member agent via the Priority-0 identity override.
- **FR-011**: Routing precedence MUST remain: explicit `agent_id` > active handoff pin > instance identity > bindings > default.
- **FR-012**: A bound instance MUST NOT route to the global default; drift MUST drop-and-alert.
- **FR-013**: The drift trigger MUST include agent deleted, disabled, or worker; a member merely removed from `core_team` but still existing MUST route stale. **Workspace state (MAJ-006):** an *archived* (not deleted) workspace whose bound agent is still valid MUST route **stale-but-functional** (mirrors the CoreTeam-removal rule), with a SPA config-time warning — drift drops only on *agent* state, never merely because the workspace is archived. A *deleted* workspace cannot route because FR-025 unbinds its instances. (Write-time, FR-007, still rejects binding to an archived/unknown workspace with 404 — write-time and route-time are distinct.)
- **FR-014**: The system MUST carry the drift signal via additive `RouteInput.BoundInstance` and `ResolvedRoute.Drop`, without reordering the priority cascade.
- **FR-015**: The system MUST allow N>1 instances of the same channel type.
- **FR-016**: `normalizeChannelMap` MUST match `knownChannelTypes` on the effective `Type`, not the raw key.
- **FR-017 (locked, MAJ-008/A-1)**: The instance key MUST be `<type>.<slug>` — delimiter **`.`**, `slug` matching `[a-z0-9-]{1,32}`, all lowercase. Uppercase or illegal characters MUST be rejected at write time (422). The `.` delimiter is verified legal in credential-store keys (`channel_<id>_<field>` is a JSON map key) and in filesystem directory names (WhatsApp store path uses the id) on all supported platforms including Windows (`:` would not be). `Type` is stored explicitly and cross-checked against the pre-`.` segment.
- **FR-018**: Each WhatsApp instance MUST use a per-instance store path (no shared `store.db`).
- **FR-019**: Each instance's credentials MUST be stored under a per-instance key with no cross-instance read.
- **FR-020**: Every channel adapter MUST stamp `msg.InstanceID` with its configured instance key on inbound.
- **FR-021**: `msg.InstanceID` MUST be sourced from the trusted adapter, never from message content.
- **FR-022**: A session created by a bound instance MUST have `workspace_id` set to the instance's workspace.
- **FR-023**: The **`agentSessionKey`** used for channel inbound MUST incorporate the stamped `InstanceID` (`agent:<id>:chat:<InstanceID>:<ChatID>`) so two same-type instances don't share a transcript key. This is the code path the inbound router actually uses — NOT `SessionKeyParams`/`BuildAgentPeerSessionKey` (whose `SessionKey` is discarded downstream). Depends on FR-020 (stamping).
- **FR-024 (locked, MAJ-007/A-2)**: The `ChannelId` wire type MUST open from a closed enum to the pattern `^[a-z0-9-]+(\.[a-z0-9-]+)?$`; each `/channels/{id}` handler MUST then validate the id exists in `cfg.Channels` (unknown well-formed id → 404 "unknown instance"; malformed id → 400/404 malformed). Instance creation is via `POST /channels` (instance-CRUD).
- **FR-025**: Instance lifecycle MUST be defined: create, delete (config+creds+store), rename=delete+create. The **workspace-delete cascade** (MAJ-005) MUST: (a) run the channel disable+unbind in `handleWorkspaceDelete` **before** the workspace-file removal, so a failure aborts with the workspace + bindings still consistent (HTTP 500, nothing deleted); (b) "unbind" clears **both** `WorkspaceID` and `Identity` and sets `Enabled=false` (leaving `Identity` would make the next inbound drift on a now-missing workspace); (c) a partial cascade (config write fails after the workspace file is gone) MUST NOT be possible given the ordering in (a).
- **FR-026**: All wire changes MUST go through `contracts/` + regenerated types (Constraint #8).
- **FR-027**: Routing MUST distinguish `identity.agent`, `default`, and `bound.drift.drop` via the existing `MatchedBy` log field.
- **FR-028 (drift observability — concrete, MAJ-003)**: On a `bound.drift.drop`, `resolveMessageRoute` MUST (a) emit a structured **audit event** of type `channel.routing.drift_drop` via the existing `auditLogger` (`audit.Logger`, loop.go:124), carrying `{instance_id, workspace_id, intended_agent_id, chat_id, reason}`; and (b) increment an in-memory `atomic.Int64` drift counter (mirroring `mediaRefsDropped`, loop.go:109). No new metrics subsystem (single-binary constraint). The SPA drift **alert** is a channels-screen warning badge sourced from recent `channel.routing.drift_drop` audit events.
- **FR-029 (two-representation rule, MAJ-004)**: A bound instance persists routing as `cfg.Channels[id].{WorkspaceID, Identity}`; an unbound instance persists it as a wildcard `AgentBinding` in `cfg.Bindings`. The two are **mutually exclusive per instance** — writing a binding removes any stale wildcard binding for the id, and `getChannelRouting` reads whichever applies. A `pkg/config` **load-time repair** MUST reject/normalize an instance that carries both (mirrors the existing multi-default repair — OBS-001).
- **FR-030 (routing-change audit, STRIDE repudiation)**: `setChannelRouting` MUST emit an audit event recording who re-bound an instance to which agent/workspace, so a silent re-bind of a workspace's channel leaves a trail.

### Success Criteria
- **SC-001**: Over the inbound messages exercised by `multi-instance-routing.spec.ts` (`whatsapp.eu`→sales/ray, `whatsapp.us`→ops/mia), 100% of `eu` inbound route to `ray` and 100% of `us` inbound to `mia`; 0 cross-routes.
- **SC-002**: 100% of PUTs with a bound `workspace_id` and empty agent return 422; 0 persist a binding.
- **SC-003**: The agent picker renders exactly `|core_team \ workers|` entries for the selected workspace (verified for ≥3 workspace fixtures).
- **SC-004**: Over the inbound messages in `bound-drift.spec.ts`, with a bound agent deleted, 0 reach the global default and 100% emit a `channel.routing.drift_drop` audit event (asserted by reading the audit log) and increment the drift counter.
- **SC-005**: Two same-type instances use 2 distinct store directories and 2 distinct credential keys; 0 shared files.
- **SC-006**: A config with a `whatsapp.eu` key loads with that entry present (0 dropped by `normalizeChannelMap`).
- **SC-007**: In `TestSessionCreation_InheritsWorkspaceID` (#20) and `multi-instance-routing.spec.ts`, 100% of sessions created by a bound instance have `workspace_id` == the instance's workspace.
- **SC-008**: `make verify-contracts`, `go test -tags goolm,stdjson`, `npm run typecheck`, `npx vitest run` all exit 0; the 3 new E2E specs pass on the CI worker.
- **SC-009**: The existing `route_explicit_priority_test.go` suite passes unchanged (regression).

### Traceability Matrix
| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|--------------|
| FR-001 | US-1 | Agent selector disabled until workspace chosen | #24 |
| FR-002 | US-2 | Selecting a workspace filters; Worker excluded | #24 |
| FR-003 | US-3 | No global-default option | #24 |
| FR-004 | US-3 | Save blocked + hint | #24 |
| FR-005 | US-3 | Backend rejects invalid (row empty) | #13, DS-1/2 |
| FR-006 | US-2/3 | Backend rejects invalid (∉ team) | #13, DS-1/3 |
| FR-007 | US-3 | Backend rejects invalid (unknown ws) | #13, DS-1/5 |
| FR-008 | US-2 | Backend rejects invalid (worker) | #13, DS-1/4 |
| FR-009 | US-2 | Empty core_team blocks save | #24 |
| FR-010 | US-4 | Ordinary inbound → member | #6, #25 |
| FR-011 | US-4 | Handoff/explicit win | #9 |
| FR-012 | US-5 | Drift drops, never default | #7, #10, #26 |
| FR-013 | US-5 | Drift outline (deleted/disabled/worker/stale) | #7, #8, DS-3 |
| FR-014 | US-5 | Drop carrier fields | #5 |
| FR-015 | US-6 | N instances activate | #2, #17 |
| FR-016 | US-6 | Namespaced keys survive load | #1 |
| FR-017 | US-6/7 | Key grammar + cred delimiter | #3, #4, DS-2 |
| FR-018 | US-7 | Per-instance store | #18 |
| FR-019 | US-7 | Per-instance creds | #4 |
| FR-020 | US-8 | Inbound stamping | #19, DS-4 |
| FR-021 | US-8 | Stamping not from content | #19, DS-4/2 |
| FR-022 | US-9 | Session inherits workspace_id | #20 |
| FR-023 | US-9 | Instance-scoped session key | #12 |
| FR-024 | US-11 | Endpoint accepts instance id | #16 |
| FR-025 | US-10 | Workspace/instance lifecycle | #21, #22 |
| FR-026 | (cross) | Contract additions | #23 |
| FR-027 | US-5 | `MatchedBy` distinguishes routes | #7 |
| FR-028 | US-5 | Drift audit event + counter | #26, SC-004 |
| FR-029 | US-3 | GET round-trip of a bound instance | #15, #23c |
| FR-030 | US-3 | (STRIDE repudiation) | #23d |

---

## 9. Ambiguity Warnings (self-audit)

| # | What's ambiguous | Likely agent assumption | Question to resolve / status |
|---|---|---|---|
| A-1 | Exact instance-key grammar | — | **RESOLVED** (FR-017): `<type>.<slug>`, delimiter `.`, `slug=[a-z0-9-]{1,32}`, lowercase→422. `.` verified cred-key- and filesystem-safe (Windows too). |
| A-2 | ChannelId enum vs instance-CRUD | — | **RESOLVED** (FR-024): open the enum to pattern `^[a-z0-9-]+(\.[a-z0-9-]+)?$` + per-route existence check; `POST /channels` for create. |
| A-3 | UI for the drift operator alert (toast? channels-screen badge? audit only?) | Audit log + a channels-screen warning badge; no blocking modal | Accepted assumption; refine in frontend task. |
| A-4 | Whether `default_agent_id` without `workspace_id` remains legal (unbound legacy) | Yes — unbound path preserved (default routing) | Accepted (US-5 non-behavior). |
| A-5 | Rename semantics surfaced in UI (or delete+create only) | Delete+create only; no in-place rename control in v1 | Accepted assumption (US-10/AC-3). |
| A-6 | Session-key change impact on existing transcripts (migration) | v0.3 fresh-build → no migration; new key format forward-only | Accepted (v0.3 constraint). |

These are flagged for the user/`/grill-spec`; A-1 and A-2 are Gate-0 decisions that block implementation start but not spec approval.

---

## 10. Holdout Evaluation Scenarios
*(Post-implementation verification only — NOT in the TDD plan or traceability matrix.)*

**Happy path**
- H-1: Configure two real WhatsApp numbers in two workspaces; message each; confirm each is answered only by its own workspace's agent.
- H-2: In the UI, pick a workspace with 3 members; confirm exactly those 3 (minus any worker) appear and one can be saved.
- H-3: Send a message to a bound Telegram instance; open the resulting session; confirm it shows the correct workspace.

**Error**
- H-4: Attempt to save routing with the agent field cleared; confirm the UI blocks it and the server rejects it.
- H-5: Delete the agent a WhatsApp instance is bound to; message the number; confirm the message is not answered by any other/default agent and an operator alert appears.

**Edge**
- H-6: Create two instances of the same type that both leave the store path default; confirm both pair independently (no store corruption / cross-login).
- H-7: Select the same agent for two instances in two different workspaces where the agent is a member of both; confirm both save and route independently.

---

## Notes for `/taskify`
Decompose along the ADR's Gate 0 → 6-workstream fan-out (§9 of the ADR): Gate 0 (contract + key grammar + ChannelId + factory/stamping/session-key shapes) is a **blocking prerequisite epic**; WS-A (config/binding/endpoint/drift), WS-B (activation/stamping/keying), WS-C (state+cred isolation), WS-D (session workspace_id), WS-E (routing UX), WS-F (multi-instance UX + lifecycle) fan out after. Same-file collisions (config.go A/B; loop.go A/B/D; ChannelConfigPanel E/F) partition by symbol/region.

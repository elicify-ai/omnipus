# ADR-019: v0.1.0 "Foundation" — Workspaces Redesign Structural Shapes

- **Status:** Proposed (pending GATE A confirmation + `/grill-spec`)
- **Date:** 2026-06-13
- **Deciders:** Daniel Piatkowski (operator/owner) · Albert (architecture)
- **Evidence level (highest used):** 1 (user-decided direction, committed concept) + 2 (codebase `[FACT]`)
- **Mode:** Ratifying ADR — the direction was decided and committed in the `.preview-doc/` concept (16 pages). This records, grounds, and rates it; it does not re-litigate settled calls.
- **Revision:** Rev 2 — incorporates the `/grill-spec` review (`ADR-019-v01-workspaces-foundation-review.md`, verdict REVISE). Closes the 5 MAJOR findings: F-1 (adds bus `instance_id` FR-4b + freezes the log-record formats in FR-7), F-2/F-3/F-7/F-8 (grounding corrections), F-4 (inert-field schema-pinning → NFR-7), F-5 (`blocked_by` delete/runtime semantics in FR-8).

---

## 1. Problem Understanding

Omnipus is moving from its current shape (5 hardcoded core agents, system-wide singleton channels, agent-global flat `MEMORY.md`, no task ordering, LLM-only provider UI, no plugin/marketplace surface) to the **Workspaces redesign** (issue #156). The redesign is large and spans memory, agents, delegation, channels, tasks, skills, plugins, and protocols.

**The architectural decision this ADR ratifies is *not* "what is the redesign" — that is settled in the concept. It is *how to sequence the build so the foundation is never re-cut*:** land every persisted/structural **shape** additively in v0.1.0, do the **one** deliberate breaking migration (Connection-as-instance) up front, start the append-only **logs**, and defer all *behaviour* and *derived data* to later releases.

- **Business objective:** ship a stable v0.1.0 that exposes every core concept in the data model + IA, so v0.2.0–v1.0.0 only *add behaviour or lift caps* — never reshape wire formats, scoping keys, or storage. `[FACT: concept roadmap.html — "front-load the shapes, defer the behaviour"]`
- **Blast radius:** the whole product. The two highest-blast-radius items are the `project→workspace` rename (touches contracts, storage, SPA) and the Connection-as-instance config migration (the only deliberate breaking change).
- **Stakeholders:** single operator/owner (single-user product); the downstream `/plan-spec` authors and implementation subagents who consume this ADR.
- **Mode of build:** **greenfield** — fresh-build, no back-compat, no existing-install data migration. `[FACT: CLAUDE.md "v0.3 = Fresh-build, no back-compat"; user decision Q6]`

---

## 2. Extracted Requirements

### Functional (the 12 shape-areas — full decision detail in §6)

- **FR-1 — Workspace key + rename:** the system MUST introduce a `Workspace` scoping key on tasks/memory/calendar/connections and rename `project→workspace` end-to-end (code · contracts · storage paths). `[FACT: concept decisions.html "Code rename: project → workspace end-to-end — a v0.1.0 migration"]`
- **FR-1.7 (owner = pure attribution) — operator decision C-1, `/grill-spec` round 1:** the live code **enforces** `owner` as a #406 access gate (`caller.canAccess` / `denyIfNoAccess`, **23 sites**, SEC-2 cross-owner 404). Per the single-user posture, the operator chose to **deliberately REMOVE that gate** — `owner` becomes pure attribution; the `canAccess`/`denyIfNoAccess` checks are deleted (the `Owner` field is retained for attribution + future re-introduction). **This consciously reverses part of this branch's (#406) tenancy enforcement; it is a documented security-posture change, not a silent strip.** `[FACT: pkg/gateway/rest_board.go + rest_milestones.go — 23 canAccess/denyIfNoAccess sites; operator C-1]`
- **FR-2 — Connection-as-instance:** channels MUST move from typed-singleton config to `map[string]ChannelInstanceConfig`, capped at 1 instance per type in v0.1.0, with credential-ref keys and a **modified** `ChannelEntry`/`ChannelConfigureRequest` contract (**these schemas already exist — this is a contract *modification*, not new** `[FACT: contracts/components/schemas/ChannelEntry.yaml + ChannelConfigureRequest.yaml; pkg/api/generated/openapi_types.gen.go]`); plus a Connectors UI and basic IMAP/SMTP email (one mailbox). `[FACT: pkg/config/config.go:775-778 typed fields; pkg/channels/manager.go:582 initChannels if-ladder]`
- **FR-3 — Agents roster:** the 5 cores MUST be re-cast to 4 base agents — Mia·Assistant ⭐ (default) · Jim·Orchestrator · Ray·Scout · Ava·Builder — with Max retired from base; built-in agents stay identity-write-protected (prompts not surfaced); custom base + sub agents allowed, creation ungated, sensitive capability grants gated. **Agent classification already exists as `AgentConfig.Type` (`AgentType`: system/core/custom) — reused, NOT a new field; the only new agent field is the nullable `voice`.** `[FACT: pkg/coreagent/core.go — 5 cores w/ locked identity; pkg/config/config.go:461-499 AgentConfig.Type + AgentType enum]`
- **FR-4 — Sub-agent/executor tier:** sub-agent workers MUST carry an `executor` (`native | external-cli{claude-code,opencode,codex} | reserved remote-a2a`); not chat targets; depth-limited; ephemeral memory; with a per-runner **connection test** (health: spawn/auth/handshake). `[FACT: user decision Q2]`
- **FR-4b — Bus `instance_id`:** an **optional `instance_id` field on the in-process bus structs** (`Peer` / `SenderInfo` / `InboundMessage` / `OutboundMessage`), defaulted — a Go field, not a contract change; later releases populate it. Omitting it would force a struct reshape later (NFR-1). `[FACT: pkg/bus/types.go structs; concept roadmap.html:37,70 names it a v0.1.0 shape]`
- **FR-5 — External-agent runners:** an `ExternalAgentRunner` interface MUST be bidirectional (events-out + control/permission-in), consent-routed, and resumable; universal transport = CLI + JSON streaming. **Claude Code + Codex have existing CLI providers to build on** `[FACT: pkg/providers/claude_cli_provider.go + codex_cli_provider.go]`; the **opencode driver is net-new** — there is no opencode runner in `pkg/` (`opencode-zen` is only a model-ref alias). `[FACT: pkg/agent/hook_process.go JSON-RPC/stdio; pkg/sandbox/hardened_exec.go]`
- **FR-6 — Delegation:** the full policy contract (`to · accept_from · modes · depth · budget`) MUST ship additively; only `to`+`modes` enforced/surfaced now (trust-graph screen + gate the 2 ungated work paths); handover stays open; work-target is an agent-reference; orchestration = `blocked_by` DAG run by the seeded coordinator (Jim·Orchestrator); a password-gated "Max parallel agents" setting with a CPU/RAM recommendation.
- **FR-7 — Memory (structure + logs):** two rooms (private per-agent + shared workspace, keyed to Workspace); full per-memory file format; 3 tools (`remember/recall/retrospective`) replacing monolithic `MEMORY.md`; bleve FTS; **freeze the append-only LOG RECORD FORMATS in v0.1.0** — the `sessions/` firehose record, the `counters.jsonl` access/citation record, and the `born_in`/`cited_in` entry schemas are pinned now, not merely "started", so v0.2.0 ranking/graph reads them with **no backfill** (NFR-1). No embeddings; MinHash dedup; no SQLite. Behaviour (ranking/Dreamcatcher/weights) = v0.2.0, config-tunable. `[FACT: docs/internal/design/memory-redesign-2026-05.md; concept memory.html + roadmap.html "start the logs so v0.2 never needs a backfill"]`
- **FR-8 — Tasks/Calendar shapes:** task fields `start·due·recurrence·blocked_by` (additive); `blocked_by` ships **with** its cycle/orphan validator (edges live, DAG runs). **Delete/runtime semantics are pinned:** (a) the validator rejects cycle-creating edges at **write time**, so no runtime cycle can exist; (b) **deleting a task cascade-cleans its inbound + outbound `blocked_by` edges** and surfaces the affected dependents (consistent with the cascade-delete concept); (c) an edge whose referenced task is missing is an **orphan the validator drops** on load. Calendar/Automations shell (per-workspace). `[FACT: blocked_by absent from pkg/boardtask/boardtask.go today; user decision Q1 = include validator; cascade-delete per tasks-redesign-2026-05 concept]`
- **FR-9 — Skills + self-improvement:** WIRE the existing stub tools (`system.skill.{install,remove,search,list}`) to the real `pkg/skills` engine; ADD `system.skill.create` + `system.skill.edit` (the authoring/self-improvement verb); default embedded set via `go:embed` + first-boot seed (`summarize · skill-authoring · plan · daily-briefing`); per-agent allowlist + progressive disclosure; skill writes consent-gated + versioned. `[FACT: pkg/sysagent/tools/skill.go — install/remove/search/list are stubs; no create/edit]`
- **FR-10 — Plugins/marketplaces (shape only):** the component-level-hybrid bundle manifest SHAPE; the marketplace-provider LIST (`RegistryConfig` single→list; ClawHub+GitHub first-class). Installer/UI deferred. `[FACT: pkg/skills/registry.go — RegistryManager coordinates N registries; RegistryConfig holds single ClawHub]`
- **FR-11 — Protocols (hooks only):** MCP present (`go-sdk v1.4.1`); ACP/A2A deferred-protocol but hooked-shape (bidirectional runner = ACP-ready; agent-reference target + Card-projectable identity = A2A-ready); A2A wire format external → Constraint #8 untouched. `[FACT: go.mod modelcontextprotocol/go-sdk v1.4.1]`
- **FR-12 — Integrations + auth:** Integrations provider-picker UI + composer mic (surface existing search/voice-in providers); single-user/one-password; sensitive settings = password re-type (maps onto `RequireNotBypass`); Profile vs Settings; 3-step onboarding → auto-provision Mia·Assistant. `[FACT: pkg/gateway/middleware RequireNotBypass exists; pkg/tools/web.go:91 SearchProvider interface + pkg/voice/ Transcriber exist]`

### Non-Functional

- **NFR-1 — No re-cut (maintainability):** no later release may change a v0.1.0 wire format, scoping key, or storage shape. This is the governing NFR. `[FACT: concept thesis]`
- **NFR-2 — Single binary / pure-Go / no-CGo:** all shapes compile into one binary; external code runs out-of-process only (MCP/sidecar/CLI). `[FACT: CLAUDE.md Hard Constraint #1/#2]`
- **NFR-3 — Contract-first (Constraint #8):** every cross-boundary type is defined in `contracts/` and generated before code. The rename and Connection-instance change MUST flow through `contracts/` + regeneration. `[FACT: CLAUDE.md Constraint #8]`
- **NFR-4 — Security/sovereignty:** external runners + skill writes are kernel-sandboxed (Landlock/seccomp), consent-gated, deny-by-default. `[FACT: pkg/sandbox; concept plugins.html]`
- **NFR-5 — Footprint:** security-feature RAM overhead < 10MB. `[FACT: CLAUDE.md Hard Constraint #3]`
- **NFR-6 — Memory weight tuning:** deferred to v0.2.0, config-tunable, tuned on real logs. `[FACT: user decision Q4]`
- **NFR-7 — Inert fields fully schema-pinned (the real NFR-1 guard):** every field that ships *inert* in v0.1.0 — `accept_from`, `budget`, `depth` (delegation), `voice` (agent), the plugin-manifest fields, and the frozen memory log records — MUST have its **complete schema/type pinned now** (in `contracts/` / config / on-disk format), so enabling its behaviour later adds **no** contract or format change. Hiding an inert field from the UI mitigates *user confusion* (R3); it does **not** satisfy NFR-1 — only the pinned schema does. `[INFERENCE from NFR-1; grill F-4]`

### Constraints

- Single Go binary, pure-Go, file-based storage (JSON/JSONL), embedded SPA. `[FACT: CLAUDE.md]`
- Contract-first wire formats; generated types only. `[FACT: Constraint #8]`
- Single-user; `owner=username` is attribution, never an access gate. `[FACT: concept; #406]`
- Greenfield (no migration of existing installs). `[FACT: Q6]`

---

## 3. Gaps and Ambiguities

The open questions were resolved with the operator before this ADR (one-by-one). Residual gaps are downstream (plan-spec) detail, not architecture-blocking.

| # | Item | Why it matters | Resolution / assumption | Status |
|---|---|---|---|---|
| 1 | `blocked_by` validation depth | cycles/orphans corrupt the DAG | **Include the cycle/orphan validator in v0.1.0**; edges live | RESOLVED (Q1) |
| 2 | Sub-agent invocation affordance | runner config needs validation | **Connection test** (spawn/auth/handshake), not a full work run | RESOLVED (Q2) |
| 3 | Custom-agent creation gating | friction vs safety | **Creation ungated**; built-ins write-protected; sensitive grants gated | RESOLVED (Q3) |
| 4 | Memory weights | ranking quality | **Deferred to v0.2.0, config-tunable**; v0.1.0 starts logs | RESOLVED (Q4) |
| 5 | Seed/migration | existing installs | **Greenfield** — none; seed "My Workspace" | RESOLVED (Q6) |
| 6 | `project→workspace` rename surface area | breakage risk | Exact file/contract inventory is plan-spec scope (Spec-1) | OPEN → plan-spec |
| 7 | Connection-instance contract delta | breakage risk | Exact `ChannelEntry`/`ChannelConfigureRequest` schema is plan-spec scope (Spec-2) | OPEN → plan-spec |
| 8 | `accept_from`/`budget` storage now, enforce later | silent no-op risk | Ship in schema, NOT surfaced in UI until enforced | RESOLVED (concept) |
| 9 | Provider OAuth (Q5), realtime (Q7) | — | **v1.0.0 — out of v0.1.0 scope** | DEFERRED |

---

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Avoids later re-cut of shapes (NFR-1) | 0.35 | The whole point of v0.1.0 |
| Honors single-binary / contract-first / sovereignty | 0.20 | Hard constraints; non-negotiable |
| Minimizes v0.1.0 implementation + review cost | 0.15 | Greenfield but large |
| Risk containment (the 2 breaking items) | 0.15 | One-way doors |
| Ships a usable, demonstrable product | 0.15 | Not just empty shapes |

---

## 5. Option Analysis

### Option A — Foundation-first (front-load all shapes + one migration; defer behaviour) — **CHOSEN**

| Dimension | Assessment |
|---|---|
| Strengths | Every later release is additive — no wire/scoping/storage re-cut (NFR-1). The one breaking change (Connections) is done once, up front. Shapes are cheap; behaviour is where the cost/risk is, and it's deferred to when there's data to tune against. |
| Weaknesses | v0.1.0 ships many shapes that are *inert* (e.g. `accept_from`, `voice`, plugin manifest) — surface-area without immediate payoff; risk of "shapes nobody uses yet." |
| Risks | The `project→workspace` rename and Connection-instance migration are large one-way doors; mis-shaping a contract now forces the very re-cut we're avoiding. |
| Complexity | High breadth, low depth — many small additive changes + 2 migrations + contract regen. |
| Cost | Build: high (broad). Run: negligible (inert shapes cost nothing). Scaling: defers the expensive behaviour. |
| Operational | Heavy contract-regen + SPA sync discipline; parallelizable across disjoint areas. |

### Option B — Incremental (add each shape only when its behaviour is built)

| Dimension | Assessment |
|---|---|
| Strengths | No inert shapes; smaller v0.1.0; each change is justified by immediate use. |
| Weaknesses | **Directly violates NFR-1** — adding the Workspace key / Connection instance / executor field later means reshaping live wire formats + stored data = the re-cut we must avoid. |
| Risks | The Connection breaking change lands in a *feature* release as a surprise migration (the exact failure the concept calls out). |
| Complexity | Lower per-release, higher total (repeated migrations). |
| Cost | Build: lower now, higher cumulatively (re-migrations). |
| Operational | Repeated breaking migrations against real data. |

### Option C — Big-bang (build all of v0.3 — shapes + behaviour — at once)

| Dimension | Assessment |
|---|---|
| Strengths | One coherent delivery; no inert shapes; no phasing seams. |
| Weaknesses | Enormous unreviewable surface; behaviour (memory ranking, Dreamcatcher, orchestration) built before any real-usage data exists to tune it; ship date unbounded. |
| Risks | High; no stable intermediate release; tuning blind. |
| Complexity | Maximal. |
| Cost | Highest upfront; highest risk of rework from untuned behaviour. |
| Operational | No stable foundation to iterate on. |

---

## 6. Recommended Architecture — Foundation-first (Option A)

**Justification vs criteria:** Option A is the only one that satisfies NFR-1 (0.35 weight) and contains the breaking change to a single up-front migration (0.15). Option B loses on NFR-1 (re-cuts shapes later); Option C loses on cost/risk and tunes behaviour blind. The inert-shapes weakness is mitigated by keeping inert fields *out of the UI* until enforced (so they don't mislead users) — e.g. `accept_from`, `voice`.

> **Rejected in one line:** Option B — defers the breaking migration into a feature release (the exact anti-pattern); Option C — unbounded surface + tunes behaviour with no data.

### The ratified shape inventory (FR-1…FR-12)

Each row: the decision, its grounding, the one-line rejected alternative, and per-shape confidence. Full `CONFIDENCE` blocks follow for the high-stakes decisions.

| # | Shape (v0.1.0) | Grounding | Rejected alternative (1-line) | Conf. |
|---|---|---|---|---|
| 1 | **Workspace key + `project→workspace` rename** (code·contracts·storage) | `[FACT]` concept; greenfield | Keep `project` in code, alias in UI — leaves a permanent naming seam | **Med** |
| 2 | **Connection-as-instance** `map[string]…` cap-1 + Connectors UI + basic email | `[FACT]` config.go:775, manager.go:582; ChannelEntry/ConfigureRequest = **modify** existing | Keep typed singletons, add instances later — breaking change in a feature release | **Med** |
| 3 | **4-base roster re-cast** (Mia/Jim/Ray/Ava; Max retired) + new `voice` field; built-ins locked | `[FACT]` coreagent locked; `AgentConfig.Type` exists (config.go:461) — **only `voice` new** | New identities (churn) / keep 5 cores (contradicts de-bloat) | **High** |
| 4 | **Sub-agent `executor` tier** + connection test | `[FACT]` Q2 | Invoke-only (no health check) — can't validate runners | **High** |
| 4b | **Bus `instance_id`** (optional Go field on bus structs, defaulted) | `[FACT]` pkg/bus/types.go; roadmap names it a v0.1.0 shape | Add later — forces a bus-struct reshape (NFR-1) | **High** |
| 5 | **`ExternalAgentRunner`** bidirectional·consent-routed·resumable; CLI+JSON | `[FACT]` hook_process, claude+codex providers, hardened_exec (**opencode driver net-new**) | Output-only one-shot — reshape needed for ACP/permissions later | **High** |
| 6 | **Full delegation policy schema** (enforce `to`+`modes`); agent-reference target; Orchestrator on `blocked_by` DAG; Max-parallel setting | `[FACT]` concept; Q3 | Ship only `to`+`modes` in schema — adding `accept_from` later = contract change | **High** |
| 7 | **Memory 2 rooms + full file format + 3 tools + bleve + frozen log-record formats** | `[FACT]` memory-redesign doc | Thin format / unfrozen logs now — migrates files or backfills logs later | **High** |
| 8 | **Task fields** + `blocked_by` **with validator**; Calendar shell | `[FACT]` blocked_by absent; Q1 | Inert `blocked_by` (Q1 lean) — operator chose live+validated | **High** |
| 9 | **Wire skill stubs to engine + add `system.skill.create/edit`**; embed default set; per-agent allowlist | `[FACT]` skill.go stubs | Keep stubs / no authoring verb — no self-improvement loop | **High** |
| 10 | **Plugin manifest SHAPE + marketplace-provider LIST** (`RegistryConfig` single→list) | `[FACT]` registry.go RegistryManager | Native-only manifest / single registry — no ecosystem import, later re-cut | **Med-High** |
| 11 | **Protocol hooks** (MCP have; ACP/A2A shape-reserved) | `[FACT]` go-sdk v1.4.1 | Build ACP/A2A now — premature; or no hooks — reshape later | **High** |
| 12 | **Integrations UI + mic; single-user/one-password; Profile vs Settings; onboarding** | `[FACT]` RequireNotBypass; web.go:91 SearchProvider + voice Transcriber | Multi-user/RBAC (over-engineered for single-user) | **High** |

#### High-stakes confidence blocks

```
DECISION 1 — project→workspace rename (HIGHEST RISK)
CONFIDENCE: Medium
  Basis         : Greenfield removes data-migration risk; but it's a wide code+contract+SPA rename touching generated types (Constraint #8) and storage paths.
  Evidence      : Concept decision; greenfield (Q6); contract-first pipeline exists.
  Missing       : The exact file/symbol/contract inventory and regen blast radius (rest_projects.go → rest_workspaces.go, project_id → workspace_id, Project*.yaml, SPA routes/state).
  Would improve : A mechanical inventory pass in Spec-1 (grep the rename surface) + a dry-run contract regen.
```

```
DECISION 2 — Connection-as-instance migration (HIGH RISK — only deliberate breaking change)
CONFIDENCE: Medium
  Basis         : The target shape (map keyed by instance) is standard; the as-is typed-singleton + if-ladder is confirmed in code. Cap-at-1 limits behavioural blast radius.
  Evidence      : config.go:775-778 (typed fields), manager.go:582 (initChannels if-ladder); concept channels.html.
  Missing       : Exact ChannelEntry/ChannelConfigureRequest schema delta + credential-ref key scheme + the initChannels loop rewrite.
  Would improve : Spec-2 defines the contract delta + a migration test; greenfield means no data to migrate, only config shape.
```

```
DECISION 5 — ExternalAgentRunner shape (bidirectional/consent-routed/resumable)
CONFIDENCE: High
  Basis         : The bidirectional shape is required by the CLI floor itself (Claude Code stream-json + --permission-prompt-tool + --resume), not ACP-speculative; building blocks exist in-code.
  Evidence      : hook_process.go (JSON-RPC/stdio), claude_cli/codex_cli providers, hardened_exec.go.
  Missing       : The exact Go interface signature + the consent-routing hook into the policy layer.
  Would improve : Spec-4 pins the interface + a Claude-Code-driver conformance test.
```

```
DECISION (architecture) — Foundation-first sequencing
CONFIDENCE: High
  Basis         : Directly optimizes the governing NFR (no re-cut); the alternatives demonstrably violate it or tune behaviour blind.
  Evidence      : The concept's foundation-first rule; standard "stabilize the contract first" practice.
  Missing       : None material at the architecture level.
  Would improve : N/A — this is the ratified thesis.
```

---

## 7. Risks and Caveats

- **R1 — `project→workspace` rename (one-way door).** A mis-shaped workspace contract now forces the re-cut we're avoiding. *Mitigation:* do it first (Spec-1), mechanical inventory + contract regen + `make verify-contracts`, full grep sweep; greenfield removes data risk.
- **R2 — Connection-as-instance (one-way door).** *Mitigation:* cap at 1 (behaviour unchanged), define the contract delta explicitly, migration test; v0.3 only lifts the cap.
- **R3 — Inert shapes mislead.** Fields like `accept_from`, `budget`, `depth`, `voice`, plugin manifest ship unused. *Mitigation:* keep them out of the UI until enforced; document them as reserved. **The re-cut risk is mitigated by NFR-7 (pin each inert field's full schema now) — UI-hiding addresses only user-confusion, not NFR-1.**
- **R4 — Breadth overwhelms review.** v0.1.0 is large. *Mitigation:* the 6-spec decomposition + per-spec grill + the cross-spec consistency pass + parallel fan-out implementation.
- **R5 — Skill `create/edit` is a self-modifying-agent surface.** *Mitigation:* consent-gated + versioned writes; kernel sandbox; deny-by-default.
- **R6 — External runners run powerful CLIs.** *Mitigation:* own-process, Landlock/seccomp, worktree isolation, egress allowlist, connection test before trust.
- **R7 — Removing the #406 owner gate is a deliberate security-posture reduction (operator C-1).** Stripping `canAccess`/`denyIfNoAccess` (23 sites) removes the cross-owner-access control just hardened on this branch. *Justification:* single-user — there is exactly one owner, so the gate can never deny; it is dead weight + a multi-user assumption the product no longer makes. *Mitigation / reversibility:* the `Owner` field is **retained** (attribution), so re-introducing enforcement later is additive, not a reshape; the removal is recorded here + in Spec-1 (FR-1.9) so it is auditable, not silent. *One-way-door note:* if multi-user ever returns, the gate must be re-derived — accepted.
- **Caveat:** "one decision per ADR" (template note) is intentionally relaxed here — the single decision is *adopt foundation-first with this shape inventory*; the 12 shapes are its sub-decisions, each separately rated. The downstream specs are where each becomes its own buildable unit.

---

## 8. Confidence Assessment

- **Architecture (foundation-first sequencing): High** — it is the only option satisfying the governing NFR.
- **Shape inventory: High overall**, with **two Medium** items — the `project→workspace` rename (R1) and Connection-as-instance (R2) — both gated behind explicit plan-specs with inventory + migration tests. No shape is Low.
- **Open questions: resolved** (Q1–Q4, Q6) with the operator; Q5/Q7 deferred to v1.0.0.
- **Residual uncertainty** is implementation-detail (exact contract deltas, rename surface), correctly pushed to plan-spec — not architecture-blocking.

---

## 9. Validation / Next Steps

- **Red-team this ADR (next):** `/grill-spec docs/internal/architecture/ADR-019-v01-workspaces-foundation.md` — must reach **PASS** before plan-spec (GATE A2).
- **Then spec the chosen architecture** (decomposed into ~6 area plan-specs, per the operator's "smaller specs" directive):
  - Spec-1 `/plan-spec` — Workspace key + `project→workspace` rename + seed (FR-1) **[do first; highest risk]**
  - Spec-2 — Connections-as-instance + Connectors UI + basic email (FR-2)
  - Spec-3 — Agents roster + delegation policy + Orchestrator + Max-parallel (FR-3, FR-6)
  - Spec-4 — External-agent runners + executor tier + connection test (FR-4, FR-5)
  - Spec-5 — Memory rooms/format/tools/logs + task fields + `blocked_by` validator + Calendar shell (FR-7, FR-8)
  - Spec-6 — Skills (wire+create/edit+defaults) + plugin manifest/marketplace shape + protocol hooks + Integrations/mic + single-user/Profile/onboarding (FR-9…FR-12)
  - Each consumes this ADR; each `/grill-spec` to PASS.
- **Cross-spec consistency pass** (Phase 3.5) before any implementation — shared types (the `Workspace` key, the `executor`/agent-reference shapes, the runner interface, the contract regen) cross spec boundaries and must agree.
- **Experiments to raise confidence on the two Medium items:** a mechanical rename-surface inventory (grep `project`/`Project`/`project_id` across `pkg/`, `contracts/`, `src/`) and a dry-run contract regen for the Connection-instance delta — both belong in Spec-1/Spec-2.

— Albert

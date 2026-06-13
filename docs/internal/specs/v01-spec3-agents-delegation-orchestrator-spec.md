# Spec-3 — Agent Roster Re-cast, Delegation Policy, Orchestrator & Max-Parallel (v0.1.0 Foundation)

- **Spec:** 3 of 6 (v0.1.0 Foundation)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-3 + FR-6
- **Status:** Draft → pending `/grill-spec` (GATE C)
- **Cross-spec (Phase 3.5):** consumes Spec-1's `Workspace` key + the renamed `system.workspace.*` tools; the sub-agent **`executor`** field + external runners are **Spec-4** (Spec-3 owns the base roster, delegation policy, the Orchestrator agent, and Max-parallel); the Orchestrator's DAG hook depends on Spec-5's `blocked_by` + the existing `task_status_changed` event.
- **Lessons pre-applied:** completeness-by-construction (compiler) for the roster re-cast; contract-first for the policy schema; CI-authority for tests; greenfield; new fields fully schema-pinned (NFR-7); no new deps.

## 1. Overview

Re-cast the 5 seeded core agents into the **4-base roster** (Mia·Assistant ⭐ · Jim·Orchestrator · Ray·Scout · Ava·Builder; **Max retired from the seeded base**); ship the **full delegation-policy contract** (`to · accept_from · modes · depth · budget`) additively while **enforcing only `to`+`modes`** in v0.1.0 (+ a trust-graph screen, gating the 2 currently-ungated work paths; **handover stays open**); make the **work-target an agent-reference** (local now, `remote-a2a` reserved); seed **Jim·Orchestrator** as the coordinator that runs `blocked_by` task-DAGs on `task_status_changed`; and add a **Max-parallel-agents** setting wired to the existing `AdmissionController`.

**In scope:** the roster re-cast (`pkg/coreagent/core.go` Name/Subtitle/role for Mia/Jim/Ray/Ava; Max removed from the seeded base set; identity stays `Locked`); the agent **`voice`** field on the 4 base personas (VoiceConfig exists); the full delegation-policy schema (contract) + enforcement of `to`+`modes` + the trust-graph UI; gating the 2 ungated work paths (sync subagent · spawn/task); the agent-reference target; the Orchestrator coordinator; the Max-parallel setting → `AdmissionController`; custom base+sub agent creation (ungated; sensitive grants gated).
**Out of scope:** `accept_from`/`budget` *enforcement* (schema ships, enforcement later); the sub-agent `executor`/external runners (Spec-4); the `blocked_by` task fields themselves (Spec-5); marketplace agent packs (later); per-agent memory (Spec-5).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `pkg/coreagent/core.go` — `IDMia/IDJim/IDAva/IDRay/IDMax`, `All()`, `SeedConfig`, `Name`/`Subtitle` | **re-cast** Mia/Jim/Ray/Ava roles; **remove Max from the seeded base** (`All()` → 4); keep `Locked=true`, `AgentTypeCore`, Mia=default | `core.go:27-31,147,230,287` |
| `config.AgentConfig` (`Default`, `Model`, tool policy) + `VoiceConfig` (config.go:127) | ensure `voice` on the 4 base personas | voice schema already exists — confirm agent-scoping (NFR-7: fully pinned) |
| delegation policy (the 2 allowlists / `pkg/agent` + routing) | **add** full schema `to·accept_from·modes·depth·budget`; enforce `to`+`modes` | ground the exact as-is policy struct during impl |
| the 2 ungated work paths — **sync subagent** + **spawn/task** (`pkg/agent/{subturn,session_worker,instance,registry}.go`) | **gate** via the policy | per delegation-audit (concept) |
| `task_status_changed` WS frame + `blocked_by` (Spec-5) | Orchestrator coordinator hook | `asyncapi_types.gen.go:408`; blocked_by = Spec-5 |
| `AdmissionController` (`pkg/agent/admission.go:12`, "soft-cap gate for concurrent session workers") | **wire** the Max-parallel setting to it | exists — extend, not greenfield |
| `ChannelEntry.identity{agent\|user}` (Spec-2) + delegation `to` | share the **agent-reference** shape | Phase-3.5 cross-spec |

### Impact Assessment
| Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| coreagent roster re-cast | MEDIUM | `SeedConfig`, the seed tests, the SPA Agents screen | onboarding (Mia auto-provision), routing default |
| delegation-policy schema (contract) | **HIGH** (contract) | generated types + the trust-graph UI + the policy enforcement points | the 2 work paths |
| Max removed from seeded base | MEDIUM | `All()`, seed tests, any `IDMax` reference | — |
| Max-parallel → AdmissionController | LOW | admission gate, Settings UI | concurrency behaviour |

## 3. User Stories

**US-1 — 4-base roster, Max retired (P0).** **Independent test:** fresh seed yields 4 base agents (Mia·Assistant ⭐ default · Jim·Orchestrator · Ray·Scout · Ava·Builder); Max is not a seeded base chat agent; `go build` compiles (any dangling `IDMax` base-seed reference is a compile error). 1. **Given** fresh install, **When** seeded, **Then** `coreagent.All()` returns the 4 base agents with the re-cast Names/roles, Mia default, all `Locked`. 2. **Given** the re-cast, **When** I read built-in identity, **Then** it is write-protected and prompts are not surfaced (Spec-1 carried; preserved).

**US-2 — voice on base personas (P0, NFR-7).** 1. **Given** the 4 base personas, **When** I read the schema, **Then** each has a nullable `voice` field (full schema pinned, unused until TTS v0.2.0).

**US-3 — Full delegation-policy schema, `to`+`modes` enforced (P0).** 1. **Given** the policy contract, **When** `make verify-contracts` runs, **Then** it carries `to · accept_from · modes · depth · budget` and exits 0. 2. **Given** v0.1.0 enforcement, **When** an agent delegates, **Then** `to` (target allowed) + `modes` (await/background/task) are enforced; `accept_from`/`budget` are present-but-not-enforced (no UI surface). 3. **Given** the trust-graph screen, **When** I view it, **Then** it shows the `to` edges + modes (not `accept_from`).

**US-4 — Gate the 2 ungated work paths (P0).** 1. **Given** the sync-subagent + spawn/task paths, **When** an agent invokes them, **Then** the delegation policy gates them (deny if `to` disallows); **handover stays open** (ungated — it only moves the conversation).

**US-5 — Agent-reference target (P0).** 1. **Given** delegation `to`, **When** set, **Then** it is an agent-reference resolving to a local agent in v0.1.0 with a reserved `remote-a2a` kind (shares Spec-2's `identity{kind,id}` shape) — no reshape later.

**US-6 — Orchestrator coordinator (P1).** 1. **Given** Jim·Orchestrator + a `blocked_by` task-DAG (Spec-5), **When** a task's status changes (`task_status_changed`), **Then** the coordinator advances ready tasks (unblocked) within the Max-parallel window. (DAG fields = Spec-5; this spec = the coordinator + the hook.)

**US-7 — Max parallel agents setting (P1).** 1. **Given** Settings → Performance (password re-type), **When** I set "Max parallel agents", **Then** it caps concurrent runs via `AdmissionController` (excess queues). 2. **Given** my hardware, **Then** the UI recommends `clamp(min(cores−2, RAM_GB÷1.5),2,16)` with an over-limit warning.

**US-8 — Custom agents (P0, Spec-1 carried).** 1. **Given** single-user, **When** I create a custom base or sub agent, **Then** creation is ungated; sensitive capability grants (system.* tools, egress) still require the password re-type; built-in agents stay write-protected.

### Edge Cases
- A delegation to a non-allowed target → denied (policy). · Handover to any agent → allowed (open). · `depth` exceeded → denied (depth is enforced as a safety cap even pre-`accept_from`? — decide: depth enforced, budget not). · Max-parallel = 0 or > clamp → warning + clamp. · `task_status_changed` for a task with no dependents → no-op. · Re-cast must not break the `IDMax` references elsewhere (compiler surfaces them).

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Contract:** 4 base agents seeded (Mia default, Max retired); voice on personas; full policy schema, `to`+`modes`+`depth` enforced, `accept_from`+`budget` present-not-enforced; handover open; the 2 work paths gated; agent-reference target; Orchestrator advances DAGs on `task_status_changed` within the Max-parallel window.

**Non-behaviors:** must **not** enforce `accept_from`/`budget` (schema only); must **not** surface `accept_from` in the trust-graph UI (NFR-7 — present in schema, not UI); must **not** gate handover; must **not** seed Max as a base chat agent; must **not** change built-in identity write-protection; must **not** introduce new deps; must **not** run the full Go suite locally (CI authority); greenfield.

**Integration boundaries:** none external. Internal: the delegation policy is enforced at the 2 work-path call sites + routing; the Orchestrator subscribes to `task_status_changed`; Max-parallel reads the config value into `AdmissionController`.

## 5. BDD Scenarios

```gherkin
Scenario: Fresh seed yields the 4-base roster, Max retired
  Traces to: US-1 / AC-1
  Category: Happy Path
  Given an empty install
  When core agents are seeded
  Then coreagent.All() returns Mia(Assistant,default), Jim(Orchestrator), Ray(Scout), Ava(Builder)
  And Max is not among the seeded base agents
  And all four are Locked

Scenario: Delegation policy enforces to+modes but not accept_from/budget
  Traces to: US-3 / AC-2
  Category: Happy Path
  Given a policy with to=[ray] modes=[await]
  When jim delegates to ray in await mode
  Then it is allowed
  When jim delegates to ava (not in to)
  Then it is denied
  And accept_from/budget are present in the schema but not evaluated

Scenario: Handover stays open
  Traces to: US-4
  Category: Alternate Path
  Given any two agents
  When one hands over the conversation to the other
  Then it is allowed regardless of delegation policy

Scenario: A previously-ungated work path is now gated
  Traces to: US-4 / AC-1
  Category: Error Path
  Given an agent whose policy disallows target X
  When it invokes the sync-subagent or spawn/task path targeting X
  Then the policy denies it

Scenario: Orchestrator advances a DAG on task_status_changed
  Traces to: US-6 / AC-1
  Category: Happy Path
  Given a blocked_by DAG where task B depends on task A
  When task A completes (task_status_changed)
  Then the coordinator dispatches task B (now unblocked) within the Max-parallel window

Scenario: Max-parallel caps concurrency via AdmissionController
  Traces to: US-7 / AC-1
  Category: Happy Path
  Given Max parallel agents = 2
  When 3 runs are requested
  Then 2 run concurrently and the 3rd queues

Scenario: Policy contract regenerates clean
  Traces to: US-3 / AC-1
  Category: Happy Path
  Given the policy schema with to·accept_from·modes·depth·budget
  When make verify-contracts runs
  Then exit 0
```

## 6. TDD Plan

| Order | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | `TestSeed_FourBaseRoster_MaxRetired` | Unit | "4-base roster" | All()==4, Mia default, Locked |
| 2 | `TestVoiceField_OnBasePersonas` | Unit | US-2 | voice present (nullable) |
| 3 | `TestDelegationPolicy_EnforceToModes_NotAcceptFromBudget` | Unit | "enforces to+modes…" | enforcement split |
| 4 | `TestHandover_AlwaysOpen` | Unit | "handover stays open" | ungated |
| 5 | `TestWorkPaths_GatedByPolicy` | Integration | "previously-ungated path gated" | the 2 paths |
| 6 | `TestOrchestrator_AdvancesDagOnStatusChange` | Integration | "advances a DAG" | coordinator (mocks Spec-5 DAG) |
| 7 | `TestMaxParallel_AdmissionControllerCap` | Integration (`-race`) | "caps concurrency" | AdmissionController wire |
| 8 | `TestAgentReference_LocalNowRemoteReserved` | Unit | US-5 | reference shape |
| 9 | `verify-contracts` (CI) | CI | "contract regen" | drift = fail |
| 10 | `e2e: trust-graph shows to+modes (not accept_from); Max-parallel setting` | E2E | US-3/US-7 | SPA |

**Test Datasets**: seed→4; to=[ray]→allow ray/deny ava; handover→always; depth>cap→deny; max-parallel 0/2/99→clamp+warn; status-change no-dependents→no-op.

**Regression:** modifies existing functionality (the 5-core seed, the 2 work paths). (1) The seed/onboarding (Mia auto-provision) still works; (2) the existing routing default (Mia) holds; (3) the existing `AdmissionController` behaviour is preserved + now config-driven; (4) NEW: policy enforcement, gating, orchestrator, voice. **CI authority; local scoped only.**

## 7. Functional Requirements & Success Criteria

- **FR-3.1:** MUST re-cast Mia→Assistant ⭐(default)/Jim→Orchestrator/Ray→Scout/Ava→Builder in `coreagent`; **remove Max from the seeded base** (`All()`→4); keep `Locked`+`AgentTypeCore`; built-in prompts not surfaced.
- **FR-3.2:** MUST ensure a nullable `voice` field on the 4 base personas (NFR-7 — pinned, unused until v0.2.0 TTS).
- **FR-6.1:** MUST add the full delegation-policy contract (`to·accept_from·modes·depth·budget`) additively; `verify-contracts` exits 0.
- **FR-6.2:** MUST enforce `to`+`modes`(+`depth` as a safety cap) in v0.1.0; `accept_from`+`budget` present-but-not-enforced and **not surfaced in the trust-graph UI**.
- **FR-6.3:** MUST gate the 2 currently-ungated work paths (sync subagent · spawn/task) via the policy; **handover stays open**.
- **FR-6.4:** MUST model the delegation `to` target as an **agent-reference** (local resolution now; `remote-a2a` kind reserved; shares Spec-2 `identity{kind,id}`).
- **FR-6.5:** MUST seed Jim·Orchestrator as the coordinator that advances `blocked_by` DAGs (Spec-5) on `task_status_changed`, within the Max-parallel window.
- **FR-6.6:** MUST add a "Max parallel agents" setting (Settings→Performance, password-gated) wired to `AdmissionController`, with the `clamp(min(cores−2,RAM_GB÷1.5),2,16)` recommendation + over-limit warning.
- **FR-3.3:** MUST allow ungated creation of custom base + sub agents; sensitive capability grants gated; built-ins write-protected. (Spec-1 carried.)

**Success Criteria**
- **SC-1:** `verify-contracts` exits 0 (CI). · **SC-2:** build + typecheck exit 0 (CI authority; local scoped). · **SC-3:** seed → exactly 4 base agents, Mia default, Max absent. · **SC-4:** `to`/`modes`/`depth` enforced; `accept_from`/`budget` evaluable=false + absent from UI. · **SC-5:** the 2 work paths deny on disallowed `to`; handover never denied. · **SC-6:** Max-parallel caps concurrency (queue beyond N). · **SC-7:** Orchestrator dispatches unblocked tasks on status change.

## 8. Traceability Matrix

| Req | US | BDD | Test |
|---|---|---|---|
| FR-3.1 | US-1 | "4-base roster" | #1 |
| FR-3.2 | US-2 | (voice) | #2 |
| FR-6.1 | US-3 | "contract regen" | #9 |
| FR-6.2 | US-3 | "enforces to+modes…" | #3 |
| FR-6.3 | US-4 | "handover open" / "path gated" | #4,#5 |
| FR-6.4 | US-5 | (reference) | #8 |
| FR-6.5 | US-6 | "advances a DAG" | #6 |
| FR-6.6 | US-7 | "caps concurrency" | #7 |
| FR-3.3 | US-8 | (custom agents) | #1 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | `depth` enforced now or later | enforce depth (safety) | RESOLVED — depth enforced as a cap; accept_from/budget not |
| 2 | voice agent-scoped vs global | VoiceConfig exists — confirm scoping | verify at impl; if global, add per-agent override (additive) |
| 3 | Max fate | removed from seeded base | RESOLVED — retired from base; ID may remain for a future pack |
| 4 | as-is delegation policy struct | ground at impl | the exact 2-allowlist struct is grounded in pkg/agent during impl; schema is additive over it |
| 5 | Orchestrator = agent vs coordinator code | Jim is the named coordinator persona; the DAG-runner is code | RESOLVED — Jim·Orchestrator is the persona; the coordinator runs on task_status_changed |

## 10. Holdout Evaluation Scenarios *(post-impl; NOT in traceability)*
- H1: fresh install → Agents screen shows Mia·Assistant ⭐, Jim·Orchestrator, Ray·Scout, Ava·Builder; no Max.
- H2: configure a policy → delegate to an allowed agent works, a disallowed one is denied; handover always works.
- H3: set Max-parallel=2 → a 3-agent fan-out queues the 3rd.
- H4: a 2-task DAG → completing the first advances the second.
- H5: grep the diff → no dangling `IDMax` base-seed reference (compiler-clean).

## 11. Assumptions
- Greenfield seed; Mia auto-provisioned (Spec-1/onboarding). `[ADR]`
- `VoiceConfig` exists; voice is pinned on base personas (NFR-7), unused until v0.2.0. `[FACT: config.go:127]`
- `AdmissionController` is the existing concurrency gate Max-parallel wires to. `[FACT: admission.go:12]`
- `blocked_by` fields are Spec-5; this spec consumes them + the existing `task_status_changed` event. `[FACT: asyncapi]`
- The agent-reference `to` shares Spec-2's `identity{kind,id}` shape (Phase-3.5). `[cross-spec]`
- The exact as-is delegation policy struct is grounded during implementation; the new schema is additive over it.

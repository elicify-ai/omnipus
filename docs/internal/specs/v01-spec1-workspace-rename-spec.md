# Spec-1 — Workspace Scoping Key, `project → workspace` Rename & Owner-Gate Removal (v0.1.0 Foundation)

- **Spec:** 1 of 6 (v0.1.0 Foundation decomposition)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-1 + FR-1.7; risk R1 (rename) + R7 (gate removal)
- **Status:** Rev 3 (addresses round-2 REVISE — agent-tool rename [C-2, operator: rename in Spec-1], guard GCP-exclusion [C-1], corrected counts + SEC-2 test scoping) → pending re-`/grill-spec`
- **Operator decisions:** full **atomic** rename · **strip** the #406 owner gate (C-1)
- **Grounding note:** the §2 inventory + the gate/seed facts are grounded against live code via the round-1 review and a direct `grep` pass; the round-1 figures (143/36, "34 projects/ paths") were wrong and are corrected here.

## 1. Overview

Establish **`Workspace`** as the single scoping-key entity by renaming `Project` **end-to-end and atomically** (contracts → generated types → Go handlers/symbols/storage → SPA), **and** — per operator decision C-1 — **remove the #406 `owner` access gate** so `owner` becomes pure attribution. Greenfield-seed one default **"My Workspace"** on fresh install.

This spec is **behaviour-preserving except for one deliberate, documented change: the owner access gate is removed** (FR-1.9). Tasks/memory/calendar/connections otherwise behave exactly as today, keyed by `workspace_id`.

**In scope:** the `Project→Workspace` entity rename; every `project_id→workspace_id` (contracts, Go, on-disk JSON, SPA) atomically; type regen; storage-dir rename; SPA route/state rename; **removal of all `canAccess`/`denyIfNoAccess` gate sites** (owner field retained); **the `system.project.*` agent tools → `system.workspace.*`** (tool names · LLM `project_id`→`workspace_id` param · `confirmation`/`ratelimit`/`rbac` keys · per-agent policy seeds) [operator C-2]; greenfield seed of "My Workspace".
**Excluded from the rename (unrelated — GCP/OAuth `project_id`, a Google-Cloud project id):** `pkg/auth/`, `pkg/providers/antigravity_provider.go`, `cmd/omnipus/internal/auth/`.
**Out of scope:** existing-install migration (greenfield); any *other* behaviour change to tasks/memory/calendar/connections (their use of the key = Specs 2–6); deletability/cascade rules of the default workspace beyond "it exists and is delete-protected" (Spec-5); re-introducing any access control (explicitly removed here).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol / artifact | Role | Context (grounded) |
|---|---|---|
| `contracts/components/schemas/Project*.yaml` (Project, ProjectCreateRequest, ProjectUpdateRequest, ProjectSessionLink) | rename → `Workspace*` | + `openapi.yaml` paths `/projects`→`/workspaces` |
| `project_id` in `Session.yaml`, `Milestone.yaml`, `BoardTask*.yaml` | modify → `workspace_id` | **atomic**, one regen |
| `pkg/gateway/rest_projects.go` (+`_test`) | rename → `rest_workspaces.go` | incl. `ensureInboxProject` (seed), `readProjectFile` (lazy `agent_ids→core_team` migration), `storedProject{Owner,CoreTeam,IsDefault}` |
| `ProjectID`/`project_id` — **216 occ / 23 files** (round-1 review) / ~127 `pkg/` Go occ (token-narrow) | modify → `Workspace*` | symbols (corrects round-1 "143/36") |
| storage dir — **~5 `filepath.Join(home,"projects",…)` sites** (NOT 34 literal paths) | modify → `"workspaces"` | `~/.omnipus/projects/{id}.json` → `workspaces/{id}.json` |
| **`canAccess` / `denyIfNoAccess`** — all sites (≈19–20 calls + the 2 fn defs) (`rest_board.go`, `rest_milestones.go`, …) | **REMOVE** (C-1) | the #406 gate; `Owner` field stays |
| `validateMilestoneFK` SEC-2 cross-owner 404 (`rest_milestones.go`) | **REMOVE the cross-owner denial**; keep FK-existence | part of gate removal |
| `pkg/api/generated/` (2), `src/lib/api/generated/` (3) | regenerate | never hand-edit |
| `src/routes/_app/projects.index.tsx`, `projects.$projectId.tsx` (+ ~36 `src/` files) | rename → `workspaces.*` | SPA routes/state/api |
| **`pkg/sysagent/tools/project.go`** (`system.project.*` tools ~37 occ · `projectsDir` · LLM `project_id` param) | rename → `system.workspace.*` (C-2) | + `task.go`/`pin.go`/`project_session_links.go` |
| `system.project.*` keys in `confirmation.go`/`ratelimit.go`/`rbac.go` + **per-agent policy seeds** (`coreagent`/`config`) | rename → `system.workspace.*` | tool-contract + policy-seed change |
| `pkg/boardtask/boardtask.go:56`, `pkg/session/daypartition.go:81` (`ProjectID`) · `pkg/agent/project_linker_hook.go` | modify → `WorkspaceID` | data structs + linker hook |
| **GCP `project_id`** — `pkg/auth/`, `antigravity_provider.go`, `cmd/.../auth/` | **EXCLUDE** (unrelated Google-Cloud project) | NOT renamed; guard must skip |

### Impact Assessment
| Symbol Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| `project_id` field (atomic) | **CRITICAL** | every generated type + handler + SPA consumer | tasks/memory/calendar/connections (Specs 2–6) |
| `canAccess`/`denyIfNoAccess` removal | **CRITICAL** (security posture) | all 23 call sites + their tests | the SEC-2/#406 test suite (must be updated, not silently broken) |
| `projects/` storage dir (~5 Join sites) | HIGH | session/task/milestone file IO | data-dir layout |
| `ensureInboxProject` seed | MEDIUM | onboarding/first-boot | the delete-protected default |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| Contract regen (`scripts/gen-contracts.sh` → `make verify-contracts`) | rename MUST flow through here; drift = fail |
| Owner-gate path (`callerIdentity → canAccess/denyIfNoAccess`) | **removed** — every handler that called it now serves without the check |
| `ensureInboxProject` first-boot seed | seeds the default workspace (lock-safe — see M-1) |
| Entity keyed by scope id | reads `workspace_id` post-rename (cross-spec → Phase 3.5) |

## 3. User Stories

**US-1 — Workspace is the scoping entity (P0).** *Operator wants the container called "Workspace" everywhere.* **Independent test:** the rename-completeness guard (US-2) is green. 1. **Given** the renamed code, **When** I read contracts/Go/SPA, **Then** the entity is `Workspace`/`workspace_id`.

**US-2 — Atomic-rename completeness, testable (P0).** *Maintainer wants a guard that actually catches a partial rename and does NOT false-positive on unrelated `project_id`.* **Independent test (token-precise, scoped):** (a) `git grep -nE '\b(ProjectID|project_id)\b'` over live `pkg/`+`src/` **excluding** `docs/internal/_archive/`, `*BRD*`, test *fixtures*, **and the GCP/OAuth set** (`pkg/auth/`, `pkg/providers/antigravity_provider.go`, `cmd/omnipus/internal/auth/`) == 0; (b) every `filepath.Join(...,"projects",...)` site renamed to `"workspaces"` (incl. `pkg/sysagent/tools/project.go:37 projectsDir`); (c) no `/projects` route or `projectId` param in `src/`; (d) no `system.project.*` tool name remains (all `system.workspace.*`). 1. **Given** a fully renamed branch, **When** the guard runs, **Then** all four checks are 0. 2. **Given** a branch that renamed symbols but not the storage `filepath.Join`, **When** the guard runs, **Then** check (b) fails. 3. **Given** the GCP `AuthCredential.ProjectID`, **When** the guard runs, **Then** it is NOT flagged (excluded).

**US-3 — `make verify-contracts` green (P0).** 1. **Given** renamed `contracts/`, **When** `scripts/gen-contracts.sh` then `make verify-contracts` run, **Then** exit 0, generated types contain `Workspace` not `Project`.

**US-4 — Owner gate removed; owner is attribution (P0, operator C-1).** *Operator wants single-user simplification — no cross-owner 404.* **Independent test:** a request for a resource whose `owner` differs from the caller returns the resource (200), not 404. 1. **Given** the gate removed, **When** any handler that previously called `denyIfNoAccess` serves a resource with a non-matching owner, **Then** it returns 200 (no denial). 2. **Given** a created resource, **When** I read its `owner`, **Then** it is stamped (attribution) but never gates access. 3. **Given** the round-1 SEC-2/#406 enumeration tests, **When** I update them, **Then** they assert *no denial* (the security control is intentionally gone) — they are not left asserting the old behaviour.

**US-5 — No OTHER behaviour regression (P0).** *Everything except the gate behaves as before, keyed by `workspace_id`.* 1. **Given** a workspace, **When** I create/list/update a task, **Then** parity with the pre-rename project-scoped task (minus the owner gate).

**US-6 — Fresh install seeds one default "My Workspace" (P0).** 1. **Given** an empty `OMNIPUS_HOME`, **When** the gateway boots, **Then** exactly one workspace "My Workspace" exists, `is_default`, delete-protected, `owner`=username. 2. **Given** concurrent first-boots, **When** seed runs, **Then** still exactly one (no double-seed — see M-1).

**US-7 — SPA under `/workspaces` (P1).** 1. **Given** the renamed SPA, **When** I navigate `/workspaces`, **Then** it renders; `/projects` is gone; `npm run typecheck` passes.

### Edge Cases
- Empty dir → exactly one seed (never 0/2). · Concurrent boot → no double-seed (lock-safe). · Default workspace delete → 409 (retained from Inbox protection). · `workspace_id` stable across reboots. · On-disk legacy `agent_ids` field — **greenfield: no legacy files exist**, so the `agent_ids→core_team` lazy migration in `readProjectFile` is **dropped** in `readWorkspaceFile` (M-4); document as a deliberate greenfield simplification. · Generated file hand-edited → verify-contracts fails closed.

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Behavioral contract:** empty boot → one "My Workspace" (default, delete-protected, owner-stamped); `verify-contracts` → exit 0; old `/projects*` → served under `/workspaces*`; any resource → keyed by `workspace_id`; **cross-owner request → 200 (gate removed)**.

**Explicit non-behaviors:** must **not** migrate/read pre-existing `projects/` data (greenfield); must **not** retain any `canAccess`/`denyIfNoAccess` call (gate removed) **nor** leave round-1 SEC-2 tests asserting the old denial; must **not** change task/memory/calendar/connection behaviour beyond the gate; must **not** leave a live `ProjectID`/`project_id` symbol or a `filepath.Join("projects")` site; must **not** hand-edit generated files; must **not** run the full Go suite locally (CLAUDE.md — CI is authority; see M-6).

**Integration boundaries:** none external. Internal seam = the contract-regen pipeline (real, not mocked).

## 5. BDD Scenarios

```gherkin
Scenario: Rename-completeness guard catches a partial storage rename
  Traces to: US-2 / AC-2
  Category: Error Path
  Given a branch where ProjectID symbols were renamed but a filepath.Join(home,"projects",id) site was not
  When the completeness guard runs
  Then check (b) reports the un-renamed storage site and fails

Scenario: Contract regen is clean after the atomic rename
  Traces to: US-3 / AC-1
  Category: Happy Path
  Given contracts/ uses Workspace and workspace_id everywhere
  When scripts/gen-contracts.sh and make verify-contracts run
  Then it exits 0 and generated types contain "Workspace" and no "Project" type

Scenario: Cross-owner access now returns the resource (gate removed)
  Traces to: US-4 / AC-1
  Category: Happy Path
  Given the owner gate has been removed
  And a board task whose owner != the caller
  When the caller GETs that task
  Then the response is 200 with the task (not 404)

Scenario: Owner is still stamped as attribution
  Traces to: US-4 / AC-2
  Category: Happy Path
  Given the gate removed
  When a user creates a workspace
  Then its owner equals the username
  And that owner never causes a denial

Scenario: Round-1 SEC-2 enumeration tests are updated, not broken
  Traces to: US-4 / AC-3
  Category: Edge Case
  Given the pre-existing #406/SEC-2 cross-owner-404 tests
  When the gate is removed
  Then those tests are rewritten to assert no denial
  And none are left silently failing or deleted without replacement

Scenario: Fresh install seeds exactly one default workspace, lock-safe
  Traces to: US-6 / AC-1, AC-2
  Category: Happy Path
  Given an empty OMNIPUS_HOME
  When two gateway boots race the seed
  Then exactly one workspace "My Workspace" exists, is_default, delete-protected, owner=username

Scenario: Task behaviour preserved under workspace_id
  Traces to: US-5 / AC-1
  Category: Happy Path
  Given a seeded workspace
  When I create and list a board task
  Then it behaves as the pre-rename project-scoped task (minus the gate)

Scenario: Default workspace cannot be deleted
  Traces to: US-6 (Inbox protection retained)
  Category: Error Path
  Given the default "My Workspace"
  When I attempt to delete it
  Then the API returns 409
```

## 6. TDD Plan

| Order | Test | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestSeed_FreshInstall_OneDefaultWorkspace` | Unit | "Fresh install seeds…" | empty → 1 "My Workspace", default, owner=username |
| 2 | `TestSeed_ConcurrentBoot_NoDoubleSeed` | Integration (`-race`) | "…lock-safe" | two racing seeds → exactly one (M-1) |
| 3 | `TestOwnerGateRemoved_CrossOwner200` | Unit | "Cross-owner access… 200" | non-matching owner → 200 (US-4) |
| 4 | `TestOwner_StampedAsAttribution` | Unit | "Owner is still stamped" | owner set, never denies |
| 5 | `TestSEC2Tests_AssertNoDenial` | Unit | "SEC-2 tests updated" | round-1 enumeration tests rewritten |
| 6 | `TestBoardTask_WorkspaceScoped_ParityMinusGate` | Integration | "Task behaviour preserved" | CRUD parity |
| 7 | `TestDefaultWorkspace_DeleteReturns409` | Unit | "…cannot be deleted" | delete protection retained |
| 8 | `TestRenameGuard_TokensAndStorageSites` | Integration (CI grep-gate) | "…partial storage rename" | the 3-check guard (US-2) |
| 9 | `verify-contracts` (CI) | CI | "Contract regen is clean" | drift = fail (NOT a local full-suite run — M-6) |
| 10 | `e2e: /workspaces renders, /projects gone` | E2E (Playwright) | US-7 | SPA nav |

**Test Datasets**

| Case | Input | Expected | Traces |
|---|---|---|---|
| empty dir | none | 1× "My Workspace" default+protected+owner | #1 |
| race boot | 2 concurrent seeds | exactly 1 | #2 |
| cross-owner GET | owner≠caller | 200 + resource | #3 |
| delete default | default ws | 409 | #7 |
| partial rename | symbols renamed, Join not | guard fails (b) | #8 |
| drift | edited generated file | verify-contracts ≠ 0 | #9 |

**Regression:** the feature **modifies existing functionality** (rename + gate removal). (1) Port `rest_projects_test.go` behaviours to `rest_workspaces_test.go`, logic unchanged **except** the owner-gate assertions, which **must be rewritten to assert no-denial** (#5) — not deleted, not left failing. (2) Existing task/session suites pass under `workspace_id`. (3) NEW: the completeness guard (#8) + the no-double-seed `-race` test (#2). (4) **CI is the authority for the full suite (CLAUDE.md); locally run only scoped tests (`-run '^TestName$' -p 1`).**

## 7. Functional Requirements & Success Criteria

- **FR-1.1:** MUST rename `Project*` schemas → `Workspace*` in `contracts/`.
- **FR-1.2:** MUST rename every `project_id`→`workspace_id` (contracts + on-disk JSON) atomically.
- **FR-1.3:** MUST regenerate `pkg/api/generated/` + `src/lib/api/generated/`; `make verify-contracts` exits 0.
- **FR-1.4:** MUST rename the Go handler, `ProjectID`/`project_id` symbols, and the `filepath.Join(...,"projects",...)` storage sites → `workspaces`.
- **FR-1.5:** MUST rename SPA routes (`projects.*`→`workspaces.*`) + state/api; `npm run typecheck` passes.
- **FR-1.6:** On fresh install MUST seed exactly one default workspace "My Workspace" (`is_default`, delete-protected, `owner`=username), **lock-safe** (no double-seed).
- **FR-1.7:** MUST treat `owner` as attribution only.
- **FR-1.8:** MUST NOT leave a live `ProjectID`/`project_id` symbol or `filepath.Join("projects")` site (completeness guard).
- **FR-1.9 (C-1):** MUST **remove** the #406 owner access gate — delete **all** `canAccess`/`denyIfNoAccess` call sites (≈19–20) **and the `canAccess`/`denyIfNoAccess` functions themselves** and the SEC-2 cross-owner-404 denial; **retain the `Owner` field**. **Tests:** **delete** the `canAccess`-*function* tests (`TestCanAccess_Table`) along with the function; **rewrite** the workspace owner-gate *behaviour* tests (`pkg/gateway/tenancy_regression_test.go`) to assert **no-denial**; **explicitly EXCLUDE** `rest_patch_ownership_test.go` — that is **agent-RBAC** (agent Rule-2 ownership), a *different* control that MUST NOT be removed.
- **FR-1.10 (M-4):** `readWorkspaceFile` MUST drop the legacy `agent_ids→core_team` lazy migration (greenfield — no legacy files).
- **FR-1.11 (C-2, operator):** MUST rename the **`system.project.*` agent tools → `system.workspace.*`** — the tool names (`pkg/sysagent/tools/project.go`, ~37 occ), the LLM-facing **`project_id`→`workspace_id` param**, the keyed entries in `confirmation.go`/`ratelimit.go`/`rbac.go`, the `projectsDir` storage helper, **the agent system-prompt text that names the tools to the LLM** (`pkg/sysagent/prompt.go:63,118` — load-bearing: a stale prompt would tell the model to call a removed tool), and `pkg/agent/loop.go:3857`. This is a **tool-contract change** — agents/skills/configs calling the old tool name or param break (documented breaking change). (No `system.project.*` policy seeds exist in `coreagent`/`config` — verified.) Also covers `pkg/boardtask/boardtask.go:56`, `pkg/session/daypartition.go:81`, `pkg/agent/project_linker_hook.go`, `project_session_links.go`.

**Success Criteria**
- **SC-1:** `make verify-contracts` exits 0 (CI).
- **SC-2:** `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` and `npm run typecheck` exit 0 — **verified in CI; locally only scoped builds/tests** (CLAUDE.md).
- **SC-3:** Fresh-install boot → exactly 1 "My Workspace" (default, protected, owner-stamped); concurrent boot → still 1.
- **SC-4:** Completeness guard: 0 `ProjectID`/`project_id` symbols + 0 `filepath.Join("projects")` sites in live `pkg/`+`src/`.
- **SC-5:** 0 `canAccess`/`denyIfNoAccess` call sites + functions remain; `tenancy_regression` behaviour tests assert no-denial; the `canAccess`-function tests are deleted; agent-RBAC `rest_patch_ownership` tests untouched.
- **SC-7:** 0 `system.project.*` references remain anywhere in live code — **incl. `pkg/sysagent/prompt.go` and `*_test.go`** (the US-2(d) guard MUST NOT exclude tests) — all `system.workspace.*`; the LLM param is `workspace_id`.
- **SC-6:** Pre-rename task/session behaviours pass under `workspace_id` (parity minus the gate).

## 8. Traceability Matrix

| Requirement | User Story | BDD Scenario | Test |
|---|---|---|---|
| FR-1.1/1.2 | US-1 | "partial storage rename" | #8 |
| FR-1.3 | US-3 | "Contract regen is clean" | #9 |
| FR-1.4/1.8 | US-1/US-2 | "partial storage rename" | #8 |
| FR-1.5 | US-7 | (e2e) | #10 |
| FR-1.6 | US-6 | "Fresh install… lock-safe" / "cannot be deleted" | #1, #2, #7 |
| FR-1.7 | US-4 | "Owner is still stamped" | #4 |
| FR-1.9 | US-4 | "Cross-owner… 200" / "SEC-2 tests updated" | #3, #5 |
| FR-1.10 | US-5 (edge) | (greenfield reader) | #6 |
| FR-1.x parity | US-5 | "Task behaviour preserved" | #6 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | rename scope | full atomic | RESOLVED (operator) |
| 2 | owner = gate or attribution | gate (live code) | **RESOLVED — strip gate (operator C-1); FR-1.9** |
| 3 | seed name/owner | "Main", no owner (today) | RESOLVED — "My Workspace" + owner stamped |
| 4 | default-workspace cascade rules | can't delete (409) | Delete-protection retained; full cascade = Spec-5 |
| 5 | legacy agent_ids migration | keep it | RESOLVED — drop (greenfield), FR-1.10 |

## 10. Holdout Evaluation Scenarios *(post-implementation; NOT in traceability)*
- H1: clean install + boot → UI shows "My Workspace"; task persists across reboot.
- H2: regen contracts from scratch → no diff.
- H3: branch diff adds zero `project_id`/`canAccess` occurrences.
- H4: corrupt a generated file → `verify-contracts` fails + names it.
- H5: as a "second user" (dev-bypass), GET another owner's task → 200 (gate gone).
- H6: delete the default workspace → 409.
- H7: two rapid boots on empty dir → exactly one workspace (no race).

## 11. Assumptions
- Greenfield: no `projects/` data read/migrated; no legacy `agent_ids` files. `[Q6]`
- Removing the owner gate is the operator's deliberate, documented posture change (ADR R7). `[C-1]`
- The sidebar workspace switcher exists; only label/key change. `[concept]`
- `docs/internal/_archive/` + `BRD/` retain "project" wording (historical) — excluded from the guard.

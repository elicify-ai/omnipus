# Adversarial Review — ADR-027 (Workspace-scoped heartbeat + global memory UI)

- **Reviewed file:** `docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md`
- **Mode:** generic-markdown (ADR; no FR/SC IDs or traceability matrix)
- **Reviewer:** grill-spec (read-only, adversarial)
- **Date:** 2026-06-30

---

## 1. Executive Summary

This ADR is well-grounded in code (the `[FACT]` citations check out) and resolves real scope questions, but it ships on top of a **fundamental terminology conflation** that, if implemented as written, produces a heartbeat that reads the *wrong* HEARTBEAT.md and a migration that targets the *wrong* container. Two concepts are both called "workspace": (a) the agent's on-disk home directory (`OMNIPUS_HOME/agents/<id>`, where `HEARTBEAT.md` actually lives and is read by the current reconcile), and (b) the `pkg/workspace.Workspace` metadata record with `CoreTeam` (where the ADR wants to store `member_configs`). The ADR treats them as one. Several downstream decisions (D2, D5, D6, gap #1) inherit this ambiguity.

- **Findings:** 4 CRITICAL, 7 MAJOR, 5 MINOR, 3 OBSERVATION.
- **Verdict:** **BLOCK.**

The direction (split heartbeat→workspace, memory→global, undeletable session) is sound. The ADR is blocked on the workspace-identity conflation, the unspecified per-member HEARTBEAT.md body resolution, and the unresolved many-to-many agent↔workspace data model that the migration silently assumes is one-to-one.

---

## 2. Findings Table

| ID | Sev | Lens | Section | Finding | Recommended fix |
|----|-----|------|---------|---------|-----------------|
| C-1 | CRITICAL | Incorrectness / Ambiguity | D2, D6, §1 FACT-5 | **"Workspace" is two different things.** The heartbeat today reads `HEARTBEAT.md` from `agentWorkspacePath()` = the agent's **own dir** `OMNIPUS_HOME/agents/<id>` (`pkg/gateway/rest.go:1191-1234`), NOT from a `pkg/workspace.Workspace` record. The ADR stores `member_configs` on the `Workspace` *metadata* record (`pkg/workspace/workspace.go`) and says heartbeat "already resolves the agent's workspace." These are different containers. A per-(workspace,agent) heartbeat that points at the `Workspace` record still has **no defined HEARTBEAT.md location** for that pairing — there is no filesystem dir per (workspace,agent). | Define explicitly which "workspace" owns the config vs. which provides the HEARTBEAT.md body. Either: (i) config on the `Workspace` record + body resolved from `member_configs[agent].heartbeat.body_override` (no file), or (ii) introduce a per-(ws,agent) dir. State that `agentWorkspacePath` (agent home dir) is unrelated to the `Workspace` record and pick one source for the body. |
| C-2 | CRITICAL | Incorrectness / Incompleteness | D5, §1 FACT-4, gap #3 | **Migration assumes one workspace per agent; the data model is many-to-many.** D5 seeds the per-agent heartbeat into the agent's "*current* `Workspace.member_configs[agentId]`." But an agent ID can appear in **multiple** `Workspace.CoreTeam` lists and in **zero**, and `AgentConfig.Workspace` is a free-text *directory path* (`pkg/config/config.go:747`), not a `Workspace.id` FK — it is never validated against any `Workspace` record. "The agent's current workspace" is undefined. Mia (the only seeded heartbeat) has `Workspace` unset → resolves to her agent dir, which maps to no `Workspace.id` at all. | Define the migration target deterministically: pick the default workspace, or every workspace whose CoreTeam contains the agent, or fail loudly. Decide what happens for an agent in 0 or N>1 workspaces. Clarify whether `AgentConfig.Workspace` (a path) is being repurposed as a workspace-id (it is not one today). |
| C-3 | CRITICAL | Insecurity (DoS/EoP) / Incompleteness | D2, D7 | **No authorization or validation specified for `member_configs` writes.** `handleWorkspacePut` (`pkg/gateway/rest_workspaces.go:609`) is field-by-field merge with explicit caps (name 200, core_team 20, etc.). The ADR adds a free-form `member_configs` map keyed by arbitrary agentId strings with no stated bounds. Unbounded map size → storage-exhaustion DoS; arbitrary keys → orphan/ghost configs for non-member or non-existent agents; no check that `agentId ∈ CoreTeam`. Constraint #8 also forbids hand-written wire structs — a `map[string]…` must be a generated type. | Specify: max keys (tie to CoreTeam ≤20), reject keys not in CoreTeam, validate `interval_seconds` bounds (the form min is 60s per Step2; reconcile floors at 5min), define who may write (same gate as the rest of workspace PUT). Define the generated schema for the map value, not an inline object. |
| C-4 | CRITICAL | Incompleteness | D4, D5, D6 | **Stale config-field read path is left live, not removed.** The blast-radius scan found ~15+ readers of `HeartbeatEnabled`/`HeartbeatIsEnabled()`/`HeartbeatInterval` across `pkg/gateway/rest.go` (create/update/PUT/bulk/wire-conversion at L395, L1315, L1571, L1638, L1785, L1927, L2187, L2627, L2723, L2808, L2825, L2878), `pkg/gateway/heartbeat_schedule.go:94/103`, and `pkg/coreagent/core.go:776/835` (Mia seed). D5 says "stop reading the agent-level fields" but only names the reconcile + create form. If the seed (`core.go`) keeps writing Mia's per-agent heartbeat while reconcile reads `member_configs`, Mia's heartbeat **silently stops** post-migration. | Enumerate every reader/writer that must change in the ADR (or defer to plan-spec but flag it as the dominant cost). Specifically address: does `coreagent.SeedConfig` now seed into a `Workspace.member_configs` instead of `AgentConfig.HeartbeatEnabled`? What is the default workspace for a fresh-install Mia seed before any user workspace exists? |
| M-1 | MAJOR | Incorrectness | §1 FACT-5, "already resolves" | **"Heartbeat already resolves the agent's workspace" is misleading.** It resolves the agent's *home directory*, not a Workspace record (see C-1). The ADR uses this FACT to claim D6 is a small change ("iterate (workspace × member) instead of flat list"). The actual change is larger: reconcile must now join `Workspace.CoreTeam × member_configs`, resolve a body per pairing, and the function signature/`agentWorkspaceFunc` injection changes meaning. | Re-state FACT-5 precisely and re-scope D6 accordingly. |
| M-2 | MAJOR | Incompleteness | D6, Risk table | **Job-key migration is hand-waved and the body-drift case is unhandled.** Reconcile updates a job in place when `interval/message/agent/kind` drift (`heartbeat_schedule.go:170-188`). Changing the key from `heartbeat:<agent>` to `heartbeat:<ws>:<agent>` means *every* existing heartbeat job is "not desired" under the new naming and gets removed (L216-221), while a new one is created — but the new job gets a **fresh `SessionID`**, abandoning the continued session (and its accumulated context). The "delete legacy jobs after seeding" mitigation does not preserve `SessionID`. | Specify SessionID hand-off: when migrating `heartbeat:<agent>` → `heartbeat:<ws>:<agent>`, carry the old job's `SessionID` onto the new job so the standing session survives. Otherwise D4's "stable, continued, undeletable session" is broken by D6's own migration. |
| M-3 | MAJOR | Insecurity (Repudiation) / Inoperability | D4 | **Delete-guard is a linear scan with a documented TOCTOU window and no audit trail.** `deleteSession` (`rest.go:825`) has no cron access today and `CronService` offers no SessionID index — the guard must scan `ListJobs(true)` O(n) on every delete (`pkg/cron/service.go`). The ADR's own risk row admits a check-then-delete race; "409 is advisory, idempotent retry safe" does not prevent deleting a session that a heartbeat is *mid-run* on. No audit entry specified for a blocked/forced delete. | Specify the guard runs under the same cron read used by the delete, what happens if a run is in-flight (not just "enabled job exists"), and add an audit log line for 409. Document the O(n) scan as acceptable given small job counts, or add an index. |
| M-4 | MAJOR | Incompleteness | D5, gap #3 | **No config GC defined; verified absent today.** Removing an agent from `CoreTeam` via `handleWorkspacePut` / `update_workspace` tool does **not** clean any per-member state (confirmed: no GC exists). `member_configs[removedAgent]` becomes an orphan that still drives a heartbeat job (reconcile keys off member_configs, not CoreTeam, unless specified otherwise). | Define the contract: is `member_configs` GC'd to `CoreTeam` on every workspace write? Does reconcile ignore `member_configs` keys not in `CoreTeam`? An orphan config that keeps firing a heartbeat (and holding an undeletable session) is a cost+lifecycle bug. |
| M-5 | MAJOR | Ambiguity / Incompleteness | gap #1, D2 `body_override?` | **HEARTBEAT.md body scope is the load-bearing open question and it is left "Needs operator confirm."** Per C-1 there is no per-(ws,agent) file. If body is agent-level default + optional `body_override`, then for shared agents the *default* body lives in the agent dir (one file) and overrides live in `member_configs`. The ADR does not state which wins, how an empty override behaves (falls back vs. blanks), or whether `buildHeartbeatMessage` (reads one file) is replaced. | Resolve gap #1 before plan-spec. Specify precedence (override > agent default > empty), empty-string semantics, and that `buildHeartbeatMessage(workspace)` is refactored to take a resolved body string, not a directory. |
| M-6 | MAJOR | Inconsistency | D6, build context | **Heartbeat reconcile (and the whole `pkg/gateway`) is `//go:build !cgo`-gated.** `heartbeat_schedule.go` and `gateway.go` both carry `//go:build !cgo`; the canonical CLI builds `CGO_ENABLED=0` (Makefile `GO?=CGO_ENABLED=0 go`), and `make build-web` uses `CGO_ENABLED=1`. So any heartbeat work lands only in `!cgo` builds. This is *consistent* with the package today (not a new defect), but the ADR is silent on it — a plan-spec author who adds a `cgo` path, or a reviewer expecting heartbeat in a cgo build, will be surprised. | Add one line noting heartbeat lives in the `!cgo` gateway build and the change set is `!cgo`-scoped; no cgo variant is needed/created. |
| M-7 | MAJOR | Infeasibility / Inconsistency | D3, §1 FACT-3 | **The "conditional 6th tab" claim rests on a tab list that is wrong, and on workspace-context that the slide-over cannot currently see.** AgentProfile tabs are **not** the fixed "Basics·Personality·Tools·Runtime·Advanced" — `Runtime` renders only for `subagent_3p` (AgentProfile.tsx ~L1777). And `openEditAgentSlideOver(agentId)` (`store/ui.ts:130`) carries no workspaceId; the Team tab has `workspace.id` (`WorkspaceTeamTab.tsx:60`) but does not pass it (L237). The "single change: thread workspaceId" understates that the slide-over state, the store action signature, and every caller must change, and that tab visibility now depends on agent *type* AND workspace presence simultaneously. | Correct the tab enumeration. State the store/action/caller changes as the real surface. Specify the matrix: {Main, Subagent, subagent_3p} × {workspace-present, absent} → which tabs show. |
| m-1 | MINOR | Ambiguity | D2 | Endpoint shape unresolved ("extend `PUT /workspaces/{id}` **or** a `…/members/{agentId}/config` sub-path"). Two options left open inside the Decision section (should be in Considered Options or resolved). | Pick one; a sub-path is cleaner for per-member GC and auth than overloading the workspace PUT merge. |
| m-2 | MINOR | Ambiguity | D7 | `kind`/`origin` field is "Recommend marking on the wire" but the Session wire type already has `workspace_id` and `type` (incl. `scheduled`) and no `kind`/`origin` (confirmed in `openapi-types.ts`). The ADR neither commits to adding the field nor to inferring from `type=scheduled`. | Decide: add a generated `origin: "heartbeat"` (Constraint #8 → schema first) or resolve from the heartbeat job's SessionID server-side. Don't leave "recommend." |
| m-3 | MINOR | Incompleteness | D5 | "Deprecated, kept readable for one release" — no version named, no removal trigger, no test that old configs still parse. | Name the release (e.g. v0.3.x reads both, v0.4 removes) and require a parse-compat test for pre-migration `config.json`. |
| m-4 | MINOR | Ambiguity | D3 | Interval units are inconsistent across the system: wire is **seconds** (`heartbeat_interval`, Step2 min 60), config is **minutes** (`HeartbeatInterval`), reconcile floors at **5 min**. `member_configs.heartbeat.interval_seconds` adds a third surface. | State the canonical unit on `member_configs` and the min/floor; ensure one conversion point. |
| m-5 | MINOR | Inoperability | D4/D7 | No observability specified: no metric/log for "heartbeat session delete blocked (409)", no count of active heartbeat sessions, no alert if reconcile orphans a job. | Add a one-line observability note (structured log on 409, gauge of active heartbeat jobs). |
| O-1 | OBSERVATION | Overcomplexity | D2/D7 | `body_override?` per (ws,agent) plus an agent-level default plus a possible wire `origin` field plus a sub-path resource is a lot of new surface for a feature whose only live user is Mia's single heartbeat. Consider shipping per-(ws,agent) enable+interval first, defer `body_override` (gap #1) to a follow-up. | Cut `body_override` from v1 unless an operator names the use case. |
| O-2 | OBSERVATION | Overcomplexity | D4 | "Undeletable session" via cron-ref guard is good, but consider whether 409 should be *force-overridable* (`?force=true`) for ops cleanup, rather than truly undeletable (which can strand a session if reconcile wedges). | Offer an admin force-delete that also disables the job. |
| O-3 | OBSERVATION | Incompleteness | §3 options | Option D (dedicated `/workspaces/{id}/agents/{agentId}/config` resource) was "rejected for now" but is exactly what m-1/M-4 (GC, auth, per-member writes) push back toward. Worth revisiting now rather than retrofitting. | Reconsider D for the write path even if storage stays on the Workspace record. |

---

## 3. Structural Integrity (narrative — generic-markdown mode)

- **Scope clarity:** Good. In/out is explicit (config scope + session lifecycle in; execution model out, gap #5). 
- **Actors:** Mostly identified, but the **fresh-install seed actor** (`coreagent.SeedConfig`) is missing from the decision — it writes the only live heartbeat and is not reconciled with the new storage (C-4).
- **Success criteria:** Weak. No measurable exit condition ("migration complete," "no orphan jobs," "session survives migration"). The Decision Confidence table is a good self-assessment but is not acceptance criteria.
- **Failure modes:** Partially addressed (risk table covers orphan jobs, delete race, cost). Misses: agent in 0/N workspaces (C-2), seed-vs-storage divergence (C-4), SessionID loss on rekey (M-2), orphan member_config firing a heartbeat (M-4).
- **Implementation detail:** Sufficient for an architect, NOT yet for plan-spec — C-1/C-2/M-5 must resolve first (which container, which body, which workspace).
- **Assumptions stated:** The one-workspace-per-agent assumption is **implicit and false** (C-2). The "workspace == agent home dir" assumption is implicit and conflated (C-1).
- **Constraints documented:** Constraint #8 acknowledged; the `member_configs` map's generated-type obligation is not (C-3). `!cgo` build scoping undocumented (M-6).

---

## 4. Test Coverage Assessment

The ADR defers tests to plan-spec, which is fine, but it must flag the high-risk test surfaces so they are not lost:

- **Migration correctness:** existing `config.json` with per-agent Mia heartbeat → exactly one `member_configs` entry, no orphan `heartbeat:<agent>` job, **same SessionID retained** (M-2). No test named.
- **Many-to-many migration:** agent in 0 workspaces, agent in 2 workspaces → defined, deterministic outcome (C-2). No test named.
- **Delete-guard:** 409 while enabled; 200 after disable; race (disable between scan and delete); in-flight run (M-3). No test named.
- **GC:** remove agent from CoreTeam → member_config removed and heartbeat job removed (M-4). No test named.
- **Reconcile idempotency** under the new key + workspace iteration (existing test is `//go:build !cgo` — `heartbeat_schedule_test.go`; must extend, not replace).
- **Contract round-trip:** `WorkspaceMemberConfig.yaml` → generated Go/TS → `make verify-contracts` green (C-3).
- **Parse-compat:** pre-migration config still loads during the deprecation window (m-3).

---

## 5. STRIDE Threat Summary

| Component / flow | Threats identified |
|---|---|
| `PUT /workspaces/{id}` with `member_configs` map | **Tampering/DoS:** unbounded map, arbitrary agentId keys, no CoreTeam membership check, no interval bounds (C-3). **EoP:** auth gate for member_configs unspecified vs. the rest of the workspace PUT. |
| `deleteSession` + cron-ref guard | **Repudiation:** no audit on blocked/forced delete (M-3). **TOCTOU:** check-then-delete race acknowledged but not closed. **DoS (self-inflicted):** truly-undeletable session can strand storage if reconcile wedges (O-2). |
| Heartbeat reconcile (per ws×member) | **DoS/cost:** shared agent × many workspaces multiplies standing sessions; mitigated by opt-in/OFF-default, but orphan member_config (M-4) re-opens it. |
| Migration (per-agent → member_configs) | **Info-integrity:** SessionID loss → context loss (M-2); ambiguous target workspace → config lands nowhere or in the wrong workspace (C-2). |
| Settings → Memory tab (global config PUT) | **EoP:** writes cost/retention knobs (`AutoRecapEnabled`, budgets, retention days) — must be admin-gated; ADR does not say. |

---

## 6. Unasked Questions (for the author)

1. Which "workspace" owns `member_configs`, and where does the per-(ws,agent) HEARTBEAT.md body come from when there is no per-pairing directory? (C-1)
2. When an agent is in zero or multiple `Workspace.CoreTeam` lists, what is "the agent's current workspace" for migration? Is `AgentConfig.Workspace` (a path) being treated as a workspace-id? (C-2)
3. Does `coreagent.SeedConfig` (Mia's heartbeat) now write to a `Workspace.member_configs`? Into which workspace, before any user workspace exists on fresh install? (C-4)
4. When the heartbeat job is rekeyed `heartbeat:<agent>` → `heartbeat:<ws>:<agent>`, is the old `SessionID` carried over (so D4's standing session survives D6's migration)? (M-2)
5. Is `member_configs` GC'd to `CoreTeam` on every workspace write, and does reconcile ignore non-CoreTeam keys? (M-4)
6. What are the bounds/auth on the `member_configs` write — max keys, membership check, interval min, who may set it? (C-3)
7. Does the Settings → Memory tab require admin (it writes budgets/retention)? 
8. Is the heartbeat session identified on the wire (`origin:"heartbeat"`) or inferred server-side? (m-2)
9. What is the canonical interval unit and floor on `member_configs.heartbeat`, given the seconds/minutes/5-min-floor mismatch already in the system? (m-4)
10. Which release reads-both vs. removes the deprecated agent-level fields, and is there a parse-compat test? (m-3)

---

## Verdict

**BLOCK**

Review written to: `docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui-review.md`

Address the CRITICAL findings (C-1 workspace-identity conflation, C-2 many-to-many migration target, C-3 member_configs auth/bounds/contract, C-4 stale read/seed paths) — they change the Decision itself, not just the spec — then re-run:

```
/grill-spec docs/internal/architecture/ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md
```

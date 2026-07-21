# Grill Review: Autonomous Agent Plan Authoring & Execution — Spec (ROUND 2, final gate)

- **Spec:** `docs/internal/specs/autonomous-agent-plan-execution-spec.md` (plan-spec r2, post-GS-01..25 revision)
- **Companion ADR:** `docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md` (r5)
- **Review mode:** plan-spec (full structural checks). Round 1 (2 BLOCK · 9 MAJOR · 10 MINOR · 4 OBS, verdict REVISE) is superseded by this report; its findings are re-verified individually below. Round-1 content is preserved in git history.
- **Stance:** locked decisions NOT re-litigated. Mission: (1) verify every round-1 fix landed and is internally consistent; (2) fresh sweep of what round 1 didn't grill (chat `/goal` caller, execute_plan end-to-end, restart contract, UI wave, matrix actuals); (3) buildability gate.
- **Grounding:** new code claims introduced by the revision re-verified against the working tree: `processTaskDirect` EXISTS (`pkg/agent/task_executor.go:259` → `pkg/agent/loop.go`) — FR-011's turn primitive is real; `PartitionStore` (`pkg/session/daypartition.go:61`); `read_file`/`list_directory` in `allStaticToolNames` (`pkg/coreagent/core.go:282`); `buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:693`) and `repairAndValidateToolPolicyCoverage` (`gateway.go:735`); sysagent `blocked_by` already present (`pkg/sysagent/tools/task.go:144`); `PausedReason` (`pkg/plan/plan.go:229`); `inFlightJudge map[string]bool` (`plan_engine.go:150-155`); `allMembersTerminal` (`plan_engine.go:536`, called at `:480`); `task.IsTerminal = done||failed` (`pkg/task/task.go:63`); `AdvanceBlockedDependents` advances only dependents of `done` deps (`plan_engine.go:494-509`); idle sweep → `FailedReasonIdleExpired` (`plan_engine.go:754,797`); goal-loop caller builds a `KindProse` criterion and calls `JudgeCriteria` at `goal_loop.go:292` with the chat session as `TranscriptSessionID`.

## Executive Summary

Both round-1 BLOCKs are genuinely fixed and buildable: the verifier tool set now uses real catalog names everywhere, and the `runVerifierAdjudication` seam is specified tightly (fresh-session-per-adjudication, register-before-dispatch, synchronous turn via the verified `processTaskDirect` primitive, fail-closed verdict parse with its own Test 28). The canonical Stop fan-out (GS-04) is used identically at all six sites, restart→`approved`-under-cap is consistent everywhere, and all claimed artifact counts verify exactly (13 US / 41 FR / 14 SC / 24 BDD / 29 tests / 7 DS). However, **the claim that all 21 round-1 findings were fixed is false**: the fix wave systematically updated the FR section but left stale text in the earlier narrative/US/BDD sections. Three of GS-08's five stale `CancelFunc`/judge-handle passages survive verbatim (one of them in Integration Boundaries, directing the forbidden parallel cancel design); US-4 Acceptance 2 still asserts the boot-abort GS-03 disproved; and US-13 Acceptance 6 + its BDD scenario still mandate the Usage cost-exclusion GS-11 flagged, directly contradicting the fixed FR-036/SC-014. GS-05's fix (FR-041) covers only the all-members-terminal branch — the code-verified idle-expiry branch (cancelled member with `blocked` dependents, the normal DAG case) still deterministically ends unrestartable. The fresh sweep adds two invent-a-design gaps: the chat `/goal` caller's verifier conversion has no window source, no `inspect_session` scope, no registry entry, and no cancel handle (and it is the very caller where the motivating "5 searches" false-fail occurred, undermining SC-012's elimination claim); and `run_task` has no seed grant for anyone — as specified, every agent including Jim resolves `deny`, making US-10.1 unimplementable.

**Totals: 0 BLOCK · 6 MAJOR · 13 MINOR · 5 OBSERVATION.**

**Verdict: REVISE.** No re-architecting needed: three findings are mechanical contradiction-purges (R2-01/02/03), and three are small design pins (R2-04/05/06). After those, the spec is buildable; the minors are enumerated and tolerable.

---

## Part 1 — Round-1 Fix Verification

| R1 finding | Status | Evidence |
|---|---|---|
| **GS-01** (verifier tools `read`/`glob`/`grep` nonexistent) | **FIXED** | FR-012(c), US-13.1, G8 row, DS-6, Tests 19/21/23 all use `read_file`+`list_directory`+`inspect_session`; glob/grep explicitly "optional FUTURE work, NOT seeded". `core.go:282` confirms the names. ADR §Judge item 3 aligned. |
| **GS-02** (verifier seam unspecified) | **FIXED** | FR-011 pins: seam name (`runVerifierAdjudication` inside `JudgeCriteria`, sync signature kept for all 3 callers), cardinality (fresh session per adjudication), register-BEFORE-dispatch (FR-037), turn primitive (`processTaskDirect` — verified to exist), fail-closed structured-verdict parse (Test 28, BDD "malformed verifier verdict fails closed"), unregister+cleanup. Residues: "closes the session" undefined (R2-16); malformed-verdict unmet-vs-D7-pause wording tension (R2-17). |
| **GS-03** (boot aborts vs backfills) | **PARTIAL — 1 surviving abort claim** | FR-006, SC-009, Test 2, DS-6, BDD "coverage gap backfilled", and US-4's independent test all restated to backfill-to-deny. **US-4 Acceptance 2 (spec :125) still says "it aborts listing the agent × tool gap"** → R2-02. |
| **GS-04** (fan-out defined 3 ways; "plan session") | **FIXED** | Canonical set — {`in_progress` member sessions} + {registered verifier sessions, member- and plan-level} — identical in US-6 (:138,142), FR-009 (:604), Behavioral Contract (:229), Non-Behaviors (:238), BDD :321-330, DS-4 (:545-552), Test 8 (:490). "Plan session" survives only as negations (FR-009, FR-033). ADR §6.4a/§6.9 aligned (but see ADR feedback: §Judge `inspect_session` para :189 still says "the plan session"). |
| **GS-05** (member-cancel → unrestartable plan) | **PARTIAL — idle-expiry branch open** | FR-041 + US-7.3 + BDD :348-354 + Test 29 cover the all-members-terminal branch. The blocked-dependents branch is NOT covered → R2-04 (code-verified). |
| **GS-06** (`behavior` payload undefined) | **FIXED (residues minor)** | FR-034 defines `{tool, min_count(default 1), max_count?, scope: attempt\|task_session(default)}`, successful-calls-only comparator, log source; DS-7 covers max-exceeded, scope=attempt, failed-calls, unknown-tool rows. Residues: unknown-tool guard has no FR sentence; validation bounds and `min_count=0` ("never call X") unaddressed → R2-15. |
| **GS-07** (window feed unnamed) | **FIXED (residues minor)** | FR-032 names PartitionStore read path (verified), existing renderer + estimator, `PlanningConfig.VerifierWindowTokens` (default 20000), task scope = session tail, plan scope = structured composition (not raw concat); Test 26 updated. Residues: entry-boundary truncation unstated (R2-16); **goal scope entirely missing** (R2-05). |
| **GS-08** (5 stale pre-r3 passages) | **PARTIAL — 3 of 5 survive** | Item 4 (Regression item 5 → matrix UNCHANGED) and item 5 (Behavioral Contract) fixed. **Items 1–3 survive verbatim** (Symbols :33, Execution Flows :84, Integration Boundaries :250) → R2-01. |
| **GS-09** (create_plan untested; FR-040 phantom; FR-021 test) | **PARTIAL** | create_plan happy path: BDD :332-338 + Test 27 + matrix FR-001 ✓. FR-040 remapped to Test 21, phantom ref gone ✓. Residues: US-1.2 (deny path) still has no BDD/test — the scenario's second half is a no-DoD reject mislabeled as Acceptance 2, and the tiered-DoD-at-create rule has no FR (R2-08); FR-021's Test 13 annotation doesn't match Test 13's description (R2-09). |
| **GS-10** (contract-scope self-contradiction) | **PARTIAL** | FR-023 fixed (tool schemas excluded, genuinely-new wire list correct, `PlanCreateRequest.yaml` reuse stated). **Test 14 (:496) still lists "create_plan/execute_plan" schemas** → R2-07. |
| **GS-11** (Usage hides verifier spend) | **PARTIAL — 3 surviving exclusion sites** | FR-036 and SC-014 fixed (UsageScreen INCLUDES). **US-13 Acceptance 6 (:210), BDD scenario :404 ("the SPA Sidebar, Search, and Usage exclude them"), and Symbols row :40 still mandate Usage exclusion**; Test 25's "Sidebar/Search/Usage honor it" is ambiguous → R2-03. |
| GS-12 (wrong file for `buildKnownBuiltinToolNames`) | PARTIAL | FR-027 corrected (`gateway.go:693` ✓ verified). Symbols M4 row (:46) still cites `config/validate.go` → R2-13. |
| GS-13 (`blocked_by` already on sysagent tool) | FIXED | FR-002 states it precisely with `[FACT]` citations (verified: `task.go:144`). G3 row's older phrasing survives but FR-002 governs. |
| GS-14 (unlocatable mailbox precedent) | FIXED | Citation removed from FR-038. (Still present in ADR :128 — ADR feedback.) |
| GS-15 (reason-field clears) | PARTIAL | FR-016 clears plan `FailedReason` ✓. Task `cancel_reason` clear on `failed→next` still unstated; no test asserts either clear → R2-10. |
| GS-16 (pause interplay) | FIXED (adequate) | FR-016 note: pause is orthogonal; a `running+paused_reason` plan is not restartable, not terminal. Stop-on-paused is implicitly covered by FR-009 + the zero-in-flight edge case. No DS row (cosmetic). |
| GS-17 (FR-036 endpoints unnamed) | **NOT FIXED** | FR-036 still names no endpoint, no param name, no detail-route (`GET /sessions/{id}`) exemption for the drill-down → R2-11. |
| GS-18 (untestable affordance) | FIXED | FR-021 now a concrete confirmation modal with pinned warning text. |
| GS-19 (in-plan task restart via curl) | PARTIAL | FR-026 pins the 409 ✓. Test 18's description and DS omit the assertion → folded into R2-14. |
| GS-20 (memory knob design) | FIXED | FR-039 picks the per-agent config field `memory_enabled` (default true), Judge seeded false. Site citation still absent (cosmetic); wire status unstated → R2-18. |
| GS-21 (dataset cosmetics) | PARTIAL | "(+plan session)" removed ✓. DS-7 still precedes DS-6 (:562/:574); FR-030's matrix row now cites a nonexistent BDD scenario "empty-plan reject" (:686) — a new dangling ref → R2-12. |
| GS-22..25 (observations) | Not adopted | GS-23 (task-assignment ≠ delegation-trust Non-Behaviors line), GS-24 (marker-is-a-filter sentence), GS-25 (transcript-copy retention note) all still absent — carried forward as observations. |

**Mission-specific verifications:** canonical fan-out identical at all sites ✓; restart→`approved`-under-cap consistent in US-9/FR-016/DS-1/DS-5/BDD/Tests 1-11-18 and ADR §6.7/§6.9 ✓; FR-011 seam buildable ✓ (`processTaskDirect` verified); `PlanningConfig.VerifierWindowTokens` + structured plan composition buildable ✓; `min_count=0` NOT explicitly expressible (R2-15); FR-041 NOT consistent with the idle-expiry path (R2-04); one abort claim survives (R2-02).

---

## Part 2 — Round-2 Findings

### MAJOR

---

**R2-01 — MAJOR — Inconsistency (GS-08 residue): three stale `CancelFunc`/judge-handle passages survive, directing the forbidden parallel cancel design**

- **Locations:** Symbols table, `PlanEngine.runPlanJudgeRound` row — spec `:33` "(needs a `CancelFunc` registry)"; Relevant Execution Flows — `:84` "Detached ctx → needs a `CancelFunc` registry for Stop"; Integration Boundaries — `:250` "new `StopPlan(planID)` + judge `CancelFunc` registry … still cancel its judge handle if present".
- **Why it matters:** all three contradict FR-011 ("no judge-cancel path") and FR-037 (the registry stores verifier **session ids**, cancelled via `RequestCancelForSession`). Integration Boundaries is exactly the section a backend implementer reads to shape `StopPlan` — following `:250` builds the parallel cancel machinery Non-Behaviors (:238) forbids. Round-1 GS-08 listed these three explicitly; only items 4–5 were fixed.
- **Fix:** rewrite all three to the r3 model: Symbols row → "plan judge on a detached ctx — replaced by the own-session verifier; Stop reaches it via the FR-037 session registry"; Execution Flows row → "runPlanJudgeRound dispatches `runVerifierAdjudication`; the registered verifier session is the Stop handle"; Integration Boundaries → "new `StopPlan(planID)` + the verifier-session registry (FR-037); … still cancel its registered verifier session if present".

---

**R2-02 — MAJOR — Inconsistency/Infeasibility (GS-03 residue): US-4 Acceptance 2 still asserts a boot abort the shipped pipeline cannot produce, contradicting its own BDD scenario**

- **Location:** US-4 Acceptance 2 — spec `:125`: "**Then** it aborts listing the `agent × tool` gap".
- **Why it matters:** FR-006, SC-009, Test 2, DS-6, and the BDD scenario "a coverage gap is backfilled to explicit deny at boot" (`:297-304`, which **traces to this very acceptance**) all state the verified behavior: `repairAndValidateToolPolicyCoverage` (`gateway.go:735`) backfills to explicit deny and boots. The acceptance asserts the opposite — a qa-lead writing acceptance tests from US-4.2 writes an impossible test. (A charitable "seed-level gap vs config gap" reading doesn't save it: a seed gap becomes a config gap before repair runs and is backfilled the same way.)
- **Fix:** restate US-4.2: "**Given** a config missing an explicit entry for these tools for some agent, **When** the gateway boots, **Then** the gap is backfilled to explicit `deny` with a WARN naming the (agent, tool) pairs, boot proceeds, and no agent resolves an implicit allow."

---

**R2-03 — MAJOR — Inconsistency/Inoperability (GS-11 residue): US-13 Acceptance 6 and its BDD scenario still mandate the Usage cost-exclusion FR-036/SC-014 now forbid**

- **Locations:** US-13 Acceptance 6 — `:210` ("SPA surfaces (Sidebar/Search/Usage) honoring it" — "it" = the default exclusion); BDD "verifier sessions are hidden by default but auditable" — `:404`: "And the SPA Sidebar, Search, and **Usage exclude them**"; Symbols table session-meta row — `:40` ("Sidebar/Search/Usage honor it"); Test 25 — `:507` ("SPA Sidebar/Search/Usage honor it" — ambiguous).
- **Why it matters:** FR-036 (`:631`) and SC-014 (`:652`) were fixed to "UsageScreen INCLUDES them (the operator must see verifier LLM spend)". The BDD block is what qa-lead builds vitest assertions from — as written it encodes the exact spend-misreporting GS-11 flagged, and the suite would then contradict SC-014's "100% of UsageScreen cost reporting".
- **Fix:** US-13.6 → "…default exclusion in session-list APIs; Sidebar and SearchModal honor it; **UsageScreen includes verifier sessions in cost aggregates**; ActivityPanel + drill-down surface them on demand." BDD `:404` → "And the SPA Sidebar and Search exclude them / And the UsageScreen still includes their token cost". Symbols `:40` and Test 25 → "Sidebar/Search exclude; Usage includes".

---

**R2-04 — MAJOR — Incompleteness (GS-05 residue, code-verified): FR-041 covers only the all-members-terminal branch; a cancelled member with dependents still deterministically ends in unrestartable `idle_expired`**

- **Locations:** FR-041 — `:636` ("When ALL of a running plan's members are terminal and ≥1 is cancelled"); US-7.3 — `:154`; BDD `:348-354`; Test 29 — `:511`.
- **Evidence (verified this round):** `task.IsTerminal = done || failed` (`pkg/task/task.go:63`) — `blocked` is non-terminal. `AdvanceBlockedDependents` promotes dependents only when the dep is `done` (`plan_engine.go:494-509`). FR-025 itself mandates "M's dependents stay `blocked`". So after a member-Stop of any member that HAS dependents (the normal DAG case): the dependents never leave `blocked` → `allMembersTerminal` (`plan_engine.go:536`, gate at `:480`) never fires → FR-041 never triggers → `idleExpirySweep` fails the plan with `FailedReasonIdleExpired` after 7 idle days (`plan_engine.go:754,797`) → FR-018 rejects restart. The only recovery is the operator manually plan-Stopping within the 7-day window — the same undocumented timing-dependent contract round 1 rejected. Round 1 named this branch explicitly ("(b) if M's dependents stay blocked…"); the fix addressed only branch (a).
- **Fix (pick one and pin it, ADR too — §6.10 has the same all-terminal trigger):** (i) widen FR-041's trigger to "no member is `in_progress`/`next` and every non-terminal member is transitively blocked on a terminal non-`done` member, and ≥1 member is cancelled → fail `stopped_by_user` (restartable) without judge rounds"; or (ii) have member-cancel mark the plan immediately once nothing remains dispatchable. Add a DS-4/DS-5 row (cancelled member + blocked dependent + others done) and extend Test 29.

---

**R2-05 — MAJOR — Incompleteness/Buildability (fresh): the chat `/goal` caller's verifier conversion is silently under-specified — no window source, no `inspect_session` scope, no registry entry, no cancel handle, no BDD**

- **Locations:** FR-032 — `:627` (scope enumerated for TASK and PLAN only); FR-033 — `:628` (scope referents: task session, plan member sessions only); FR-037 — `:632` ("registry mapping **plan/task** → verifier session id"); Test 21 — `:503` (the only goal-caller coverage, a parity assertion); SC-012 — `:650`; US-12.2 — `:197`.
- **Evidence:** the spec converts all 3 `JudgeCriteria` callers (FR-011 names `goal_loop.go:292`), but every mechanism FR is written for the task/plan scopes. Verified at the call site: the goal loop builds a single `KindProse` criterion from `meta.GoalCondition` with the **chat session** as the working session (`goal_loop.go:270-297`). Unspecified for the goal scope: (a) the window source — presumably the chat session's tail, but FR-032 never says so; (b) the `inspect_session` target lock — FR-033 gives the engine-set ctx no goal-scope referent; (c) registry participation — FR-037's "plan/task →" omits goal adjudications, so an in-flight goal verifier session has **no Stop handle** (the old inline `Provider.Chat` died with the turn ctx; an own-session verifier does not); (d) US/BDD coverage — no scenario exercises the goal caller. Sharpener: the motivating "run 5 web searches" false-fail was observed **on a chat `/goal`** (ADR §Judge, Problem), the goal condition is `prose` (so the FR-034 behavior rung never reaches it), and without a defined goal-scope window the verifier for that exact caller stays blind — SC-012's "the observed '5 searches' false-fail class is eliminated" is unsupported for the caller where it was observed.
- **Fix:** extend FR-032 with "GOAL verification window = the chat session's tail (same `VerifierWindowTokens` budget)"; extend FR-033's referents with "goal verification → that chat session only"; extend FR-037 to "plan/task/goal-session → verifier session id" and state the goal-verifier cancel path (registry entry keyed by the chat session; cancelled when the goal is cleared/session cancelled, plus the existing `goalJudgeRoundTimeout` ctx bound); add one BDD scenario ("a chat /goal adjudication feeds the chat-session window and renders one fail-closed verdict") traced to Test 21 or a new test.

---

**R2-06 — MAJOR — Incompleteness/Security (fresh): `run_task` has no seed grant for any agent — as specified, US-10.1 is unimplementable**

- **Locations:** FR-005 — `:600` (seeds `execute_plan`/`create_plan` only); FR-027 — `:622` (registers `run_task` + ceiling **deny**); DS-6 — `:574-581` (no `run_task` column); Test 2 — `:484`; US-10.1 — `:178` ("When `run_task(T)` / ▶"); G6 row — `:19`.
- **Why it matters:** Constraint #6 + the backfill mean the seed IS the feature switch. With FR-027's ceiling deny and no FR seeding an allow, `run_task` resolves explicit `deny` for **every** agent including Jim — the agent-tool half of US-10 is dead on a fresh install (the UI ▶ path survives via REST). This is the same "feature silently off" class the spec itself elevates in FR-038, but for fresh installs.
- **Fix:** state the seed posture in FR-005 (presumably Jim `allow`, all others + ceiling explicit `deny`, mirroring `execute_plan`) and add a `run_task` column to DS-6 + the Test 2 assertion. If the intent is UI-only in v1, delete `run_task` from the agent tool set (G6/FR-019/US-10) instead — either way the spec must choose.

---

### MINOR

---

**R2-07 (GS-10 residue)** — Test 14 (`:496`) still lists "create_plan/execute_plan" among the schemas `make verify-contracts` must cover, contradicting FR-023 (`:618`) which excludes agent-tool schemas from contract scope. Strike them from Test 14's description.

**R2-08 (GS-09 residue)** — US-1 Acceptance 2 (create_plan denied by policy, `:99`) still has no BDD/test: scenario `:332-338` claims "Traces to: US-1, Acceptance 1 and 2" but its second assertion is a **no-DoD reject**, not the deny path; Test 27 (`:509`) covers happy + no-DoD only. Additionally the tiered-DoD-at-create rule ("agent-authored plans require a DoD") exists only in that scenario + Test 27 — no FR states it (FR-001 lists params only). Add the deny assertion (or retrace to DS-6/Test 2 explicitly) and add one FR sentence for the DoD requirement.

**R2-09 (GS-09 residue)** — FR-021's matrix cell "Test 13 (modal presence assertion)" (`:677`): Test 13 (`:495`) is the board-surface button-matrix vitest; its description never mentions the tool-policy **grant** UI where FR-021's security modal lives. Extend Test 13's description to name the grant surface, or add the dedicated grant-surface vitest round 1 asked for.

**R2-10 (GS-15 residue)** — Task `cancel_reason` lifecycle on restart still unstated: FR-016 (`:611`) clears plan `FailedReason` but neither FR-016/017 nor FR-028 clears the member's `cancel_reason` on `failed→next`; no DS row; Tests 1/11/18/20 assert neither clear. A restarted member would carry stale `stopped_by_user`. Add the clear + one DS-5 cell + an assertion in Test 11.

**R2-11 (GS-17 residue — not fixed)** — FR-036 (`:631`) still names neither the list endpoints (`GET /sessions` — which already has a `type` param — and `GET /agents/{id}/sessions`), the exclusion param name, nor whether `GET /sessions/{id}` (the drill-down's dependency) is exempt. Enumerate them; the contract work is already flagged in FR-023, only the names are missing.

**R2-12 (GS-21 residue + new)** — Traceability nits: (a) matrix FR-030 row (`:686`) cites BDD "empty-plan reject" — no such scenario exists (dangling ref introduced by the fix wave; parenthesize it or add the scenario); (b) BDD scenario "a malformed verifier verdict fails closed" (`:340-346`) and Test 28 appear in **no** matrix row — FR-011's cell (`:667`) lists Tests 7, 21 only; add Test 28 + the scenario there; (c) DS-7 still appears before DS-6 (`:562`/`:574`); (d) FR-038's cell "Test 19 (upgrade fixture)" (`:694`) — Test 19's description (`:501`) mentions no upgrade fixture.

**R2-13 (GS-12 residue)** — Symbols M4 row (`:46`) still cites `config/validate.go` in the file list for `buildKnownBuiltinToolNames`; FR-027 has the verified `gateway.go:693`. Align the row.

**R2-14 (fresh)** — Restart endpoints' HTTP contract underpinned: US-9.2 (`:170`) says "rejected (409/400)" — pin one code (FR-026 uses 409 for the in-plan task case; use 409 for reason-guard rejects too, or say why not); request/response shapes (empty body? returns the updated Plan?), the 404 unknown-id case, and auth (state "standard `withAuth`") are unstated; and the accepted task-states for `POST /tasks/{id}/restart` are undefined — the ADR §6.8 matrix offers ▶ Play on `inbox`/`next`/`failed`/`cancelled` standalone tasks, so is "restart" of a never-run task legal on the same route, and what does the UI ▶ call for it? Also fold GS-19's residue here: Test 18 (`:500`) and a DS row should assert the in-plan 409.

**R2-15 (fresh, GS-06 residue)** — FR-034 (`:629`) residues: (a) DS-7's unknown-tool fail-closed+flag row still has no FR sentence; (b) payload validation bounds unstated (is `min_count: 0` legal? must `max_count ≥ min_count`? non-empty `tool`?); (c) the zero-count goal ("never call X" = `min_count:0, max_count:0`) is neither blessed nor excluded. Three sentences close all of it.

**R2-16 (fresh, GS-02/GS-07 residue)** — FR-011 step 5 (`:606`) "unregisters + **closes** the session": sessions have no close operation (`PartitionStore` is append-only JSONL) — say what it means (registry cleanup + meta finalization only; the session persists per FR-036). FR-032 (`:627`) still doesn't state truncation at entry boundaries for the rendered window (mid-entry truncation can split a tool call from its result).

**R2-17 (fresh)** — "D7-pause semantics" (FR-011 step 4 `:606`, BDD `:345`, Test 28 `:510`) is never defined in this spec (it is ADR-049/planning-spec vocabulary: judge-Unavailable → round NOT consumed, idle clock untouched). Worse, FR-011 says malformed verdict = "unmet (fail-closed)" AND "D7-pause preserved" in one breath — unmet **consumes** a round; D7-pause is the **Unavailable, 0-rounds** path. State the two failure classes explicitly: provider/transport error → Unavailable (D7, round not consumed); successful turn with missing/malformed verdict block → all-unmet, round consumed. Two engineers currently could build either.

**R2-18 (fresh)** — FR-039's `memory_enabled` (`:634`): unstated whether it crosses the wire (Agent.yaml). If operators can set it on custom verifiers via API/UI (which FR-040's "assignable role" implies), it is a wire change and belongs in FR-023's contract list; if v1 is seed/config-file-only, say so.

**R2-19 (fresh)** — `execute_plan` on an already-`approved`/`running`/terminal plan is only an edge-case bullet (`:215` "no-op / idempotent") — no FR/BDD/test defines the tool's response per state (what does the agent see for `running`? for `done`/`failed`?). One FR sentence + a DS-2 row.

---

### OBSERVATIONS

- **R2-20 (carried GS-23):** the Non-Behaviors still don't declare that agent task-assignment (`create_task(agent_id=X)` + `execute_plan`) intentionally bypasses the workspace delegation-trust gate (`trust_set` governs `delegate` only). One line makes it a decision, not an oversight.
- **R2-21 (carried GS-24/GS-25):** FR-035 still lacks the "the marker gate is a cheap filter, the verifier is the real check" sentence; FR-036 still lacks the "verifier sessions contain copies of other sessions' content (retention/PII/export implications)" sentence.
- **R2-22 (ADR feedback — divergences to reflect into ADR-052 r5):** (a) §Judge `inspect_session` scoping paragraph (ADR `:189`) still says "plan-level verification → **the plan session** + that plan's member sessions" — the term the spec's own FR-033 kills (GS-04); (b) §6.9 still says "plan `failed(cancelled)`" (ADR `:154`) — the reason is `stopped_by_user`; (c) §6.1 still cites the unlocatable "mailbox upgrade-grant precedent" (ADR `:128`, spec's GS-14 fix removed it); (d) §6.10's member-cancel outcome carries the same all-terminal-only trigger as FR-041 — R2-04's fix must land in both.
- **R2-23:** the fix pattern that produced R2-01/02/03 — FRs updated, earlier narrative/US/BDD sections not — suggests the next revision pass should grep the whole spec for each changed term (`CancelFunc`, `abort`, `Usage`, `plan session`) rather than editing by section.
- **R2-24:** fresh-verifier-session-per-adjudication × `DefaultGoalMaxRounds`(20) on chat goals (GS-22) is now an accepted, documented economy ("session reuse = future direction"). Fine — but R2-05's goal-scope registry fix should note that goal verifier sessions inherit the same hidden-by-default visibility (FR-036) or the Sidebar fills with them.

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS |
| Every acceptance scenario has ≥1 BDD scenario | **FAIL** — US-1.2 deny path (R2-08); US-4.2's scenario contradicts the acceptance it traces to (R2-02); US-11.4/FR-021 test-only (tolerated) |
| Every BDD scenario has a `Traces to:` back-reference | PASS (24/24) |
| Every BDD scenario has a corresponding TDD test | PASS (incl. scenario→Test 28) |
| Every FR appears in the traceability matrix | PASS (FR-001..041, 41/41) |
| Every BDD scenario appears in the matrix | **FAIL** — "malformed verifier verdict fails closed" absent; FR-030 cites a nonexistent "empty-plan reject" (R2-12) |
| Claimed artifact counts match actuals | **PASS** — 13 US / 41 FR / 14 SC / 24 BDD / 29 tests / 7 DS all verified exact |
| Test datasets cover boundaries/edges/errors | PARTIAL — missing: cancelled-member-with-blocked-dependents (R2-04), reason-clears (R2-10), behavior validation bounds + zero-count (R2-15), restart status-code/states (R2-14), run_task seed (R2-06) |
| Regression impact explicitly addressed | PARTIAL — item 5 fixed ✓, but Integration Boundaries `:250` still directs the contradicting cancel design (R2-01) |
| Success criteria measurable, no subjective language | PARTIAL — SC-012's elimination claim unsupported for the chat caller (R2-05); SC-014 contradicted by US-13.6/BDD (R2-03) |

## Test Coverage Assessment

The 29-test plan holds up well where round 1 pressed: the three Stop races remain genuinely asserted (Tests 7/8/10 under `planDecisionMu`, register-before-dispatch, ≥100-iteration stress), the restart wrong-implementation traps are pinned (Tests 1/11/18 + DS-1 matrix-vs-store-guard separation, Regression item 5 now correct), and the two former BLOCKs gained real tests (Tests 27/28). Gaps: no test can currently pass for US-4.2 as written (R2-02); Test 25/BDD encode the wrong Usage assertion (R2-03); Test 29 misses the blocked-dependents branch (R2-04); the goal caller has only the Test 21 parity line — no window/registry/cancel assertion (R2-05); no test of `run_task`'s seed posture (R2-06); no reason-clear assertions (R2-10); no execute_plan state-idempotency test (R2-19); Test 14 asserts schemas FR-023 forbids (R2-07); Test 13's description doesn't cover the FR-021 grant modal it is now credited with (R2-09).

## STRIDE Threat Summary

| Component | Notes (delta from round 1) |
|---|---|
| `execute_plan`/`create_plan`/`run_task` tools | Gate = tool policy (locked) — but **R2-06**: `run_task` seed absent (deny-everywhere is fail-safe, yet contradicts US-10); **R2-20** (EoP declaration) still outstanding |
| Verifier session | GS-01/GS-02 closed with real names + seam; **R2-05**: goal-scope verifier unregistered → un-cancellable handle (availability/DoS-adjacent: an orphaned verifier turn survives a user cancel); Usage-spend visibility fixed in FR-036 but contradicted at 3 sites (R2-03) |
| `inspect_session` | Sound (ctx-lock, verified `WithRunningTaskID` precedent) — goal-scope referent missing (R2-05) |
| Restart endpoints | Reason-guard sound; status codes/auth unpinned (R2-14); in-plan 409 pinned in FR-026 ✓ |
| Boot seeding | Backfill-to-deny fail-safe ✓; surviving abort text is a spec defect only (R2-02) |

## Unasked Questions (the spec should answer)

1. What feeds, scopes, registers, and cancels a **chat `/goal`** verifier? (R2-05)
2. Who holds `run_task` on a fresh install? (R2-06)
3. What happens to a plan whose cancelled member has blocked dependents after the others finish — before the 7-day idle clock? (R2-04)
4. Is a malformed verdict "unmet, round consumed" or "Unavailable, round not consumed"? (R2-17)
5. Is `min_count: 0` (never-call goals) a legal behavior payload? (R2-15)
6. Does `memory_enabled` cross the wire? (R2-18)
7. Which endpoint/param names implement the verifier session exclusion, and is the detail route exempt? (R2-11)
8. What does ▶ call for a never-run standalone task, and which states does `POST /tasks/{id}/restart` accept? (R2-14)

---

## Verdict

**REVISE.**

Priority order for the revision pass:
1. **R2-01 + R2-02 + R2-03** — mechanical contradiction-purges (grep-driven: `CancelFunc`, "aborts", "Usage", "judge handle"); zero design work, highest mislead risk.
2. **R2-04** (pin the blocked-dependents outcome — also amend ADR §6.10), **R2-05** (goal-scope window/lock/registry/cancel + one BDD), **R2-06** (run_task seed row) — the three remaining design pins.
3. MINOR sweep R2-07..R2-19 — mostly single sentences, one matrix row, and test-description edits.

No BLOCK findings; both round-1 BLOCKs are verified fixed. After wave 1+2 above, the spec is buildable by implementing agents without invented design.

To address these findings, run:
  `/plan-spec --revise docs/internal/specs/autonomous-agent-plan-execution-spec.md docs/internal/specs/autonomous-agent-plan-execution-spec-review.md`

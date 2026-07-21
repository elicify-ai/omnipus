# Grill review — ADR-052: Autonomous agent plan authoring & execution (ROUND 3)

**Reviewed:** `docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md` (r4 — Judge/Verifier architecture added)
**Mode:** generic-markdown (ADR review), round 3. This round targets the NEW Judge/Verifier section + operator decisions A1–A5, and re-verifies the prior fixes. The locked direction is not re-litigated. This file supersedes the round-2 review text (preserved in git history; its findings F1–F10 are re-checked below).
**Date:** 2026-07-20
**Grounding:** graphify (rebuilt this session) + direct code verification at every cited site.

## Executive summary

The core mechanics of the new judge architecture check out against the code: the shortcut is exactly where the ADR says (`judge.go:505`, a no-tools `Provider.Chat` with `nil` tools), all THREE `JudgeCriteria` callers flow through the single entrypoint (`task_executor.go:481` via `finishTaskRun` at `:260`, `plan_engine.go:630`, `goal_loop.go:292`) so the d=1 blast-radius claim is accurate, the session store already records per-entry tool calls (`ToolCall{Tool,Status,Parameters}`, `daypartition.go:319-331`) so rung 2's raw data exists, `EntryTypeJudgeVerdict` already exists (`daypartition.go:45`), task runs already execute in their own engine-created sessions (`processTaskDirect`, `task_executor.go:252-259`) so the own-session verifier has a working precedent, and `RequestCancelForSession` (`cancel.go:457`) is the proven cancel primitive.

**But the judge section leans on three "reuse" claims the code actively contradicts** — the locked System-Agent prompt/tool model (soul rejected on locked agents, all-deny re-enforced every boot, Judge excluded from the compiled-prompt map) fights both "soul = rubric" and "read-only tools for the verifier"; and the cited visibility mechanisms are entry-level, not session-level, so hiding the verifier's own session from history is new, contract-touching work. Separately, the restart design skips the cap (reopening the exact hole class A1 just closed, against the prior review's explicit fix guidance), §8/§9.2 still normatively describe the superseded r3 cancel machinery, and the seeded `execute_plan` allow never reaches Jim on upgraded installs.

- **7 MAJOR**, **6 MINOR**, **2 OBSERVATION**. No BLOCK — nothing here invalidates the decision; every finding is correctable in the ADR text or explicitly assignable to plan-spec with honest "new work" framing.
- **Prior fixes:** A1, A4, M2, M4, M6, F3(r2), F5(r2), F6(r2), F7(r2) all still hold (details below); the two defects are R3-4 (restart vs cap — a regression against r2-F1's own fix guidance) and R3-5 (stale r3 text).
- **Verdict: REVISE.**

## Findings table

| ID | Sev | Lens | ADR section | Summary |
|----|-----|------|-------------|---------|
| R3-1 | MAJOR | Inconsistency / Incorrectness | Judge/Verifier item 2 ("Soul = the rubric") | "Soul-not-Rubric" fights three verified invariants of the System-Agent model; it is new work + a breaking wire change framed as simplification. |
| R3-2 | MAJOR | Incorrectness | Judge/Verifier item 3 (read-only tools) | `seedSystemAgents` **re-enforces all-deny on EVERY boot as a hard invariant** — any verifier tool grant is reverted unless that invariant is redefined; the ADR never mentions it. |
| R3-3 | MAJOR | Incompleteness / Inoperability | §6.1 (tool seeding) | Upgraded installs never receive Jim's `execute_plan` allow: catalog growth is backfilled to explicit **deny** and core-agent seeding preserves existing tool maps. The seeded posture is fresh-install-only. |
| R3-4 | MAJOR | Incorrectness / Insecurity | §6.7, §6.9 vs §7 | Restart `failed[stopped_by_user]→running` **skips cap admission** (`Admit` lives only in `tryStartApprovedPlan`) — the same hole class A1 closed for PUT, and the r2 review's F1 fix guidance explicitly said route restart through the gated/cap path. |
| R3-5 | MAJOR | Inconsistency | §8, §9.2 vs §6.4/G1/G8 | §8 and §9.2 still prescribe the superseded r3 machinery (`TaskExecutor.CancelTask`, plan-judge `CancelFunc`, judge registry) that the r4 own-session decision explicitly abolishes. |
| R3-6 | MAJOR | Incompleteness | Judge/Verifier "Visibility / audit" | Hiding the verifier's OWN session from chat/history is NEW work: all three cited mechanisms are entry-/render-level; the history list has no default exclusion, the SPA fetches all sessions unfiltered, and "surface" is not a session-meta field. |
| R3-7 | MAJOR | Incorrectness (race) | §6.4(a), Judge/Verifier "Cancel" | The fan-out over "each active verifier session" has no registry to read (engine tracks `inFlightJudge` as `map[planID]bool` only) and M1's register-before-dispatch fix is not extended to verifier sessions — a Stop in the creation window misses the verifier (A2 violated for up to `planJudgeRoundTimeout`). |
| R3-8 | MINOR | Ambiguity / Infeasibility | Three-rung ladder, rung 2 | Rung 2's "deterministic from the tool-call log" has no criterion-kind or mechanism: criteria kinds are `check`/`prose` only; a deterministic behavioral rung needs a new kind + engine-side transcript scanner (wire change), else it degenerates into rung 3. |
| R3-9 | MINOR | Incompleteness | Judge/Verifier item 1 (memory OFF) | No memory-off knob exists — `ContextBuilder` unconditionally constructs and injects the `MemoryStore`. Acknowledged in the missing-list, but the body text should say "new flag", not imply an existing switch. |
| R3-10 | MINOR | Insecurity | `inspect_session` scoping | The target-session lock is NOT expressible in tool policy (per-agent×tool only); it must be engine-set request context (precedent: `tools.WithRunningTaskID`). Unnamed in the ADR, plan-spec could wrongly trust an LLM-chosen `session_id` argument. |
| R3-11 | MINOR | Ambiguity | Context model (window feed) | For the PLAN-scope round, "the working session" has no single referent — owner `plan:<id>` chat session, N member sessions, or a concatenation. Unspecified. |
| R3-12 | MINOR | Inoperability | Judge/Verifier (own session) | Verifier-session proliferation: one session per adjudication × attempts × members × rounds on the file-based store; lifecycle is acknowledged but volume/retention/cleanup policy is not named. |
| R3-13 | MINOR | Inconsistency | Evidence-marker gate | `[goal:evidence]` must harmonize with the EXISTING ADR-043 `TASK_STATUS`/`TASK_SUMMARY` marker protocol and the staged `PendingJudgeClaim` path — the ADR reads as a parallel marker grammar; the re-prompt loop is new turn-end interception code. |
| R3-14 | OBS | Consistency | §6.5 | "No pause/resume (deferred)" coexists with the engine's EXISTING `PausedReason` pause substrate (FR-065: a paused plan neither dispatches nor judges, `plan_engine.go:435-437`). One sentence distinguishing Stop from the existing pause mechanism would prevent implementer confusion. |
| R3-15 | OBS | Completeness | Judge/Verifier (conversion) | Where the D7 retry-forever/backoff loop lives after conversion (engine-side wrapper vs inside the verifier turn) is undecided; it interacts with cancel (a verifier between backoff retries has no active turn for `RequestCancelForSession` to fire on). |

---

## R3-1 (MAJOR) — "Soul = rubric" fights the locked System-Agent prompt model on three verified fronts

**Claim (Judge/Verifier item 2):** "judging standards live in its `SOUL.md` like every agent; drop the bespoke `AgentConfig.Rubric` field — it was only ever 'an editable soul.' Editability is a flag, not a separate store." And item header: "everything else is identical (same loop, same `ContextBuilder`/`SOUL.md` …)".

**Code says otherwise, three times:**

1. **Locked agents REJECT soul edits — Rubric exists precisely because of that.** `pkg/config/config.go:813-820`: Rubric "is the ONE prompt-equivalent field a locked System Agent may edit (**soul is rejected on locked agents**)". The field is not "an editable soul that happens to be separate"; it is the deliberate carve-out around the locked-agent soul-rejection rule. Moving the rubric into `SOUL.md` requires amending that rule (a lockable-identity invariant) for verifier-role agents.
2. **System/core agents have no on-disk SOUL.md path at all.** `pkg/coreagent/core.go:1498-1501`: compiled prompts "are NOT stored on disk (no SOUL.md) so users cannot read them. The ContextBuilder calls GetPrompt(agentID) to inject these as the SOUL content." And the Judge is **excluded even from that**: `core.go:1475-1483` — "Its 'prompt' is the editable Rubric stored on AgentConfig …, NOT a compiled entry in the prompts map — which is why the Judge is deliberately excluded from All() and from init()'s compiled-prompt invariant." Running the Judge through the standard loop today yields NO soul: `loadBootstrapFilesWithDef` (`pkg/agent/context.go:710-745`) emits the Soul section only from an on-disk agent definition, and the rubric injection is bespoke (`judgeRubricFromConfig`, `judge.go:613-623`). "Give the Judge a SOUL" = build a SOUL.md storage/authoring path for a System Agent that has never had one.
3. **Dropping `Rubric` is a breaking wire change.** The `rubric` field lives in `contracts/components/schemas/Agent.yaml` and `AgentUpdateRequest.yaml`, in both generated artifacts, and is edited in the SPA (`src/components/agents/AgentProfile.tsx`). Removal is a Constraint #8 five-step contract change + SPA change; the ADR's NFR-3 covers wire changes generically but the judge section presents the drop as a field deletion.

**Fix:** Rewrite item 2 to state what it actually is: (a) a SOUL.md path for System/verifier agents (new storage + seeding of `judgeDefaultRubric` as the seeded SOUL content), (b) an amendment to the locked-agent soul-edit rejection scoped to verifier-role agents (name the invariant being relaxed), (c) a contract-first removal of the `rubric` wire field + `AgentProfile.tsx` migration of the rubric editor to the soul editor. Alternatively keep `Rubric` as the storage and treat "soul" as its presentation — either is fine, but the ADR must pick one and stop calling it pure reuse.

## R3-2 (MAJOR) — Verifier tools collide with the every-boot all-deny re-enforcement invariant

**Claim (item 3):** the verifier gets "file `read`/`glob`/`grep` + a scoped read-only `inspect_session`", authorized by ordinary seeded policy ("seeded policy allow=verifier, deny=all others").

**Code:** `seedSystemAgents` (`pkg/coreagent/core.go:1226-1233`) — "**Re-enforce the all-deny tool policy on EVERY boot.** This is stricter than the core-agent loop (which preserves operator tool edits) BECAUSE the System Agent's **no-tools contract is a hard invariant** (it executes as a no-tools structured call)". Any allow written to the Judge's policy map — by seed, operator, or REST — is reverted to all-deny on the next boot. The Judge's own constructor documents the same contract (`core.go:1477-1479, 1493-1494`).

**Consequence:** the verifier design requires **redefining this invariant** (re-enforce a new canonical set: read-only tools + `inspect_session` allow, everything else deny), which is a deliberate amendment to a tamper-protection mechanism, not policy data. Silver lining the ADR should claim on purpose: because the System-Agent path re-enforces on every boot, amending `systemAgentSeed` **is** the upgrade-delivery mechanism for the verifier's tools on existing installs (unlike Jim — see R3-3). Also note the re-enforcement means the seeded Judge's tool set is NOT operator-tunable — consistent with "verifier is a role," but the ADR's "(… seeded+locked are ordinary knobs …)" parenthetical implies otherwise.

**Fix:** add one paragraph: the no-tools hard invariant of ADR-049 D3 is superseded; `systemAgentSeed(IDJudge)` becomes the canonical read-only+`inspect_session` set, still boot-re-enforced (tamper protection retained, contract changed); this doubles as the upgrade path for verifier tooling.

## R3-3 (MAJOR) — The seeded `execute_plan`/`create_plan` posture never reaches existing installs

**Claim (§6.1):** "Seed: Jim allow; every other seeded agent explicit deny — including the global ceiling."

**Code:** catalog growth on an existing config is handled by `repairAndValidateToolPolicyCoverage` (`pkg/gateway/gateway.go:715-742`): every missing (agent, tool) entry is **backfilled to explicit "deny"** so validation doesn't abort. The core-agent seeding loop appends full policy maps only for agents **not already present** (`core.go:~1100-1137`) and, for existing core agents, "preserves operator tool edits" (contrast drawn at `core.go:1227-1228`). Net: on every already-installed instance, Jim gets `execute_plan: deny` — the feature the ADR calls "the default path for complex goals" is silently off everywhere except fresh installs, and nothing surfaces that to the operator. Precedent that this needs explicit code: the mailbox upgrade-grant (`pkg/gateway/rest_mailbox.go:626-680` — "granted email tool allows (deny/missing email-tool policy, mailbox enabled)").

**Fix:** the ADR must specify upgrade behavior as a decision: either (a) deliberate secure-default — existing installs stay deny and the Plans UI surfaces "grant `execute_plan` to Jim" with the F6 security affordance, or (b) a one-shot targeted upgrade grant à la the mailbox precedent. Either is defensible; silence is not, because the ADR's own success criterion ("default path") fails on the installed base.

## R3-4 (MAJOR) — Restart skips the cap; reopens the G2 hole class the ADR itself just closed

**Claim (§6.7):** "at plan level only `failed[reason=stopped_by_user]`**→running**` is un-frozen … the restart handler performs an explicit guarded transition." §6.9: "set = restart → `running` …, engine re-drives the non-`done` members."

**Code:** cap admission (`Admit`) exists **only** in `tryStartApprovedPlan` (`plan_engine.go:388-393`), on the `approved→running` promotion. A restart handler that transitions `failed→running` directly enters execution with **zero cap check** — with 16 running plans, restart makes it 17. This is precisely the "PUT `state:"running"` skips the cap" hole (§3 G2 / §6.3) re-created on the new endpoint, and it directly contradicts §7's "keep … cap=16". The r2 review's F1 fix guidance anticipated this verbatim: "add `failed→approved` — routing restart back through the gated/cap path, **not straight to `running`**." The r4 text adopted the narrow un-freeze but chose the ungated target state. Additionally, the store itself enforces the matrix (`ValidateStateTransition` inside `updateLocked`, `pkg/plan/store.go:362-368`), so the "handler performs the transition" needs a privileged store path either way — the amendment is store-level, not handler-only.

**Fix:** restart transitions `failed[stopped_by_user]→approved` (after resetting members/counters per A4), and the engine promotes under `Admit` exactly like first-run. This keeps ONE gate to `running` (the §6.3 invariant: "every transition goes through a dedicated endpoint … + the engine") and makes the restart queue-behind-cap behavior consistent with G5's async semantics. Update the button matrix's Play description accordingly ("queued if at cap").

## R3-5 (MAJOR) — §8 and §9.2 still normatively prescribe the superseded r3 cancel machinery

**§8:** "the residual work is: … the locked plan-Stop fan-out (under `planDecisionMu`) cancelling turn + member-judge (**`TaskExecutor.CancelTask`**) + plan-judge (**`CancelFunc`**)". **§9.2:** "the locked plan-Stop fan-out (turn + **`TaskExecutor.CancelTask`** + plan-judge **`CancelFunc`**), and the judge-completion state re-check".

Both contradict the r4 decision they sit next to: §6.4 — "**No `TaskExecutor.CancelTask`, no judge registry**, no goal-drain — one mechanism"; G1/G8 — "RESOLVED (judge = real agent, own session …) No new cancel machinery"; the Judge/Verifier Cancel paragraph — "no special judge-cancel path … supersedes the earlier 'judge as a sub-turn' idea." An implementer (or the plan-spec author §9 hands off to) reading §8/§9.2 as the work list will build the abolished registry/CancelFunc. §9.1's claim that "all 4 MAJORs … addressed" is also now partially inaccurate for F4, which is addressed by the own-session model, not by `TaskExecutor.CancelTask`.

**Fix:** rewrite §8's residual-work sentence and §9.2's second bullet to the r4 mechanism: fan-out (under `planDecisionMu`) over member sessions + registered verifier sessions + the plan session; verifier-session lifecycle/registry; state re-check. Mechanical edit, but mandatory — these are the sections that drive the next phase.

## R3-6 (MAJOR) — Session-level hiding of the verifier session is new work; the cited mechanisms don't do it

**Claim (Visibility/audit):** hidden from chat thread + session-history list "reusing `EntryTypeSystem` (`daypartition.go:34`), the delegate internal-narration visibility filter (`daypartition.go:295`), and the `toolVisibility.ts` hide-infra pattern … System/plan-verifier sessions are filtered from user history by surface/agent-type."

**Verified — all three exist, none hides a SESSION:**
- `EntryTypeSystem` (`daypartition.go:34-35`) classifies an **entry** within a transcript.
- The delegate filter (`IsDelegateChildEntry`, `daypartition.go:279-302`; applied in `rest.go:801-810` and replay) suppresses **entries inside a SHARED transcript**, keyed on `ParentSpawnCallID` — which exists because sub-turns share the parent's transcript. A verifier in its OWN session has no parent spawn call and no shared transcript; the predicate can never match it.
- `toolVisibility.ts` hides tool-call **renders** in the thread.
- The history list has **no default exclusion**: `listSessions` (`rest.go:746-765`) filters only by opt-in `agent_id`/`type` query params, and the SPA fetches everything — `Sidebar.tsx:165` `fetchSessions()` with no type argument, filtered only by `workspace_id` (`:442`); same for `SearchModal.tsx` and `UsageScreen.tsx`. (Task sessions are already visible there today.)
- `UnifiedSessionType` is a **closed set** — `chat|task|channel|scheduled|heartbeat` (`pkg/session/unified.go:27-50`); there is no "verifier"/"system" value and no `surface` field on session meta.

**Consequence:** a verifier session created today would appear in the sidebar history like any task session. The actual work: a new `UnifiedSessionType` value (wire enum → Session contract change, Constraint #8) or a server-side default exclusion by system-agent `agent_id`, plus exclusion logic in the three SPA consumers, plus the on-demand drill-down surface. All buildable — but it is new, contract-touching work the ADR presents as three-mechanism reuse, and "filtered … by surface" references a field that does not exist.

**Fix:** replace the reuse sentence with the real mechanism decision (recommended: new `SessionTypeVerifier` value + server-side default exclusion in `listSessions` unless `?include=verifier`, mirroring the delegate filter's server-side-not-client-side principle documented at `daypartition.go:293-299`), and list the Session-schema contract delta in §9.2's contracts bullet.

## R3-7 (MAJOR) — The Stop fan-out has no verifier-session registry, and M1 is not extended to verifiers

**Claim (§6.4a):** the fan-out cancels "each `in_progress` member session + **each active verifier session** + the plan session," with M1 guaranteeing members' `SessionID` is "assigned synchronously before dispatch so the fan-out always has a handle."

**Code:** the engine's only judge bookkeeping is `inFlightJudge map[string]bool` (`plan_engine.go:441-446, 580-582`) — a flag, not a session handle; the member-task judge today runs synchronously inside `finishTaskRun` with no session at all. Nothing stores "the verifier session for plan P / task T," so the fan-out as specified has nothing to iterate. The ADR's missing-list does say "verifier session lifecycle (create/register/cleanup) … plan-spec," but the **ordering requirement is the load-bearing part and is stated only for members**: `beginPlanJudgeRound` flips the flag and spawns the goroutine (`plan_engine.go:580-585`); if the verifier's session is created inside that goroutine, a Stop landing in the gap cancels nothing — the verifier runs on for up to `planJudgeRoundTimeout` (A2 violated in that window; the §6.4(b) state re-check only prevents the stale verdict from being applied, it doesn't stop the burn). This is the same race class r2-F3/M1 fixed for members.

**Fix:** one sentence in §6.4a: "M1 applies to verifier sessions identically — the verifier session ID is created/registered (taskID/planID → sessionID, under the engine's lock) BEFORE the verifier turn is dispatched; the fan-out reads that registry." Plus cleanup on round completion.

## MINOR findings

**R3-8 — Rung 2 needs a mechanism decision.** The data exists and is sufficient: every transcript entry records `ToolCalls[]` with `Tool`, `Status`, `Parameters` (`daypartition.go:205, 319-331`), so "called `web_search` 5×" is computable from the session store, and within-window cases need no `inspect_session` at all — the ENGINE can count deterministically, exactly like `runMachineCheck` runs commands. But criteria have exactly two kinds — `check`/`prose` (`judge.go:170-187`, fail-closed on unknown kinds) — so a first-class deterministic rung 2 needs a new criterion kind (wire enum on AcceptanceCriterion → contract change) + an engine-side transcript scanner. If instead rung 2 is "the verifier LLM reads the tool-call log," it is rung 3 with better evidence and the word "deterministic" must go. The ADR should pick (recommended: new `kind: behavioral` engine-checked, no LLM, no tool — cheapest and unspoofable; note the unknown-kind fail-closed switch means an old binary reading a new-kind criterion fails closed, which is the right degradation).

**R3-9 — Memory OFF is a new flag.** `ContextBuilder` unconditionally constructs the store (`context.go:250`) and injects `GetMemoryContext()` output (`context.go:400-402`); no off-knob exists anywhere. The missing-list admits "the memory-off `ContextBuilder` variant" — correct; just make the body text match ("a new construction option," not an existing switch), and note memory-TOOL exclusion is separately handled by the verifier's policy map (data, covered by R3-2's seed set).

**R3-10 — Name the target-session-lock mechanism for `inspect_session`.** The tool does not exist anywhere today (verified: zero hits in `pkg/`, `src/`, `contracts/`). Per-agent×tool policy CAN express "verifier allow, all others deny incl. ceiling" (`denyAllThenOverride` `core.go:364`, `tightenGlobalCeiling` `core.go:389` — Constraint #6-compatible, and catalog growth is deny-backfilled for all other agents, which is the safe direction). But policy cannot express "only the session under review" — that lock must be an engine-set request-context value the tool validates against, with the LLM-supplied argument ignored or checked (exact precedent: `tools.WithRunningTaskID`, set at `task_executor.go:250`, consumed by the task tools). One sentence naming this closes the "hand-wavy scoping" risk before plan-spec invents an argument-trusting design.

**R3-11 — Plan-scope window source is ambiguous.** For task and `/goal` scopes "the working session" is well-defined (the task session; the chat session). For the PLAN-level round there are N member sessions plus the owner wake-channel (`ChatID: "plan:"+planID`, `plan_engine.go:858-861`). Last-N-tokens *of what*? Say it (e.g. per-member tail slices + the plan chat, budget-split; or members-only).

**R3-12 — Verifier session volume/retention.** Own-session per adjudication means sessions ≈ attempts×members×rounds per plan on the file store (`sessions/<id>/<date>.jsonl` each). With attempts=3 and rounds up to the cap this is dozens of session dirs per plan. Name the lifecycle: reuse one verifier session per (task|plan) across rounds vs one per round, and the retention/cleanup rule (default 90-day retention applies?).

**R3-13 — Marker-grammar collision.** The worker completion protocol already has ADR-043's `TASK_STATUS`/`TASK_SUMMARY` markers and the staged `PendingJudgeClaim` path (`finishTaskRun`, `task_executor.go:263-299`). `[goal:evidence]` must be integrated into THAT grammar (e.g. an `TASK_EVIDENCE` sibling or a required section of the summary) with the auto-reject+re-prompt hook at the existing marker-parse point — not a second, opencode-flavored marker syntax bolted beside it. The re-prompt loop is new turn-end interception code either way; flag it as such.

## OBSERVATIONS

**R3-14** — §6.5 "No pause/resume (deferred)" sits beside an engine that already HAS a pause substrate: `PausedReason` short-circuits dispatch+judging (`plan_engine.go:435-437`, FR-065). Stop≠pause is presumably the intent; one sentence saying "the existing FR-065 pause substrate is unchanged and distinct from Stop" prevents an implementer from conflating or removing it.

**R3-15** — Post-conversion, decide where D7's retry-forever/backoff lives (today inside `judgeProseCriteria`, `judge.go:479-517`). If it moves engine-side (recommended), note that a verifier BETWEEN backoff retries has no active turn, so `RequestCancelForSession` fires false (`cancel_test.go:322`) — the engine's own `State==running` gate must be what stops re-entry, which §6.4 already states ("the engine starts no round unless State==running"). Also pin the preserved behaviors the ADR already flags: SEC-26 (the standard turn loop applies the same gates the bespoke `checkJudgeSEC26` mirrors — `judge.go:557-561`), temperature-0/prompt-cache, and fail-closed structured parsing (`parseJudgeResponse`/`failClosedProseVerdicts`).

## Prior-fix verification (round-2 findings + operator decisions, against r4)

| Item | Status | Evidence |
|---|---|---|
| A1 — PUT forbidden from setting ANY state | HOLDS (coherent) | PUT sets `patch.State` today (`rest_plans.go:651-654` verified); A1 + dedicated `POST /restart` endpoints are consistent; store-side matrix check (`store.go:362-368`) backs it. Residual: R3-4 (the new restart endpoint itself must be cap-gated). |
| A4 — reset `attempt_count` + `JudgeRounds` | HOLDS | Both counters exist: `Task.AttemptCount` (`task.go:235-238`), `Plan.JudgeRounds` (patched in `runPlanJudgeRound`, `plan_engine.go:659-670`). |
| M2 — reason-gated transition outside the matrix | HOLDS (sharpen) | Matrix is a reason-free closed allow-list (`plan.go:79-105` verified). Sharpen: the STORE enforces it too (`updateLocked`), so the guarded transition needs a privileged store path — fold into R3-4's `failed→approved` fix. |
| M4 — tool-seeding incl. explicit ceiling deny | HOLDS structurally | `denyAllThenOverride`/`tightenGlobalCeiling`/boot coverage validation all verified. Residuals: R3-2 (system-agent re-enforcement must be redefined), R3-3 (upgrade delivery for Jim). |
| M6 — task cancel field | HOLDS (genuinely new) | Task has NO typed reason today — task-stop writes prose `Result` "stopped by user request" (`rest_tasks.go:1404-1405`); plan side has `FailedReasonStoppedByUser` (`plan.go:153`). New field correctly implied; add it to the contracts bullet. |
| F3(r2) — fan-out under `planDecisionMu` | HOLDS | §6.4a says INSIDE the engine under the lock; `processPlan` locking verified (`plan_engine.go:420-422`). |
| F5(r2)/F6(r2)/F7(r2) | HOLD | Ceiling deny in §6.1; security affordance in §7/§9; state re-check in §6.4(b). |
| F1(r2) restart | PARTIAL | Narrow reason-gated un-freeze adopted ✓; but the "route through the gated/cap path" half of the fix was dropped → R3-4. |
| F4(r2)/G8 member-judge cancel | RESOLVED by own-session model | ✓ — but §8/§9.2 text not updated → R3-5. |
| G7 chat-`/goal` persistence | Still open, correctly framed | `GoalCondition` is session-meta (`daypartition.go:102-110`); plan/task goals persist. Unchanged. |

## Grounded-claim audit (new judge section)

Claims verified TRUE: `judge.go:505` shortcut (nil tools, one-shot, temp 0, 2048 max_tokens); `judge.go:26-29` input = criteria+evidence+claim only; three callers, single entrypoint (d=1); `config.go:820`; `EntryTypeSystem` `daypartition.go:34`; delegate filter at `daypartition.go:~295`; tool-call log exists; `handlePlanStop` sets state only; `ComputeProgress` counts only `done` (`store.go:485-499`); `FailedReasonStoppedByUser` exists; own-session feasibility via the `processTaskDirect` precedent; SEC-26 machinery present; "plan session" referent exists (`plan:<id>` wake ChatID).

Claims contradicted or unowned as new work: soul-for-System-Agents (R3-1), verifier tools vs all-deny invariant (R3-2), session-level history hiding "by surface/agent-type" (R3-6), fan-out's verifier-session handle (R3-7), rung-2 mechanism (R3-8), `[goal:evidence]` vs ADR-043 markers (R3-13).

## Structural integrity (generic-markdown, narrative)

Scope, actors, options, and confidence blocks remain strong; the FACT-annotation discipline held up under spot-checks except where noted. Failure modes for the new judge (verifier unavailability, escaped-cancel, session bloat) are thinner than the r2-hardened plan-Stop analysis. The biggest structural defect is internal: §8/§9 were not regenerated after the r4 judge section landed (R3-5), so the ADR currently contains two incompatible work lists.

## Test coverage assessment

§9.3's spikes remain right. Add, driven by this round: (1) an upgrade-config test — load a pre-ADR-052 config, assert Jim's `execute_plan` posture matches the DECIDED upgrade behavior (R3-3) and the Judge's re-enforced tool set is the new canonical one (R3-2); (2) restart-at-cap — 16 running plans + restart → plan queues at `approved`, never a 17th `running` (R3-4); (3) Stop-during-verifier-creation-window — cancel lands before the verifier's first provider call; assert no orphaned verifier turn beyond the abort window (R3-7); (4) verifier-session hidden — `listSessions` default response and the Sidebar exclude it; drill-down retrieves it (R3-6); (5) rung-2 determinism — same transcript → same behavioral verdict with zero LLM calls, if the engine-checked kind is chosen (R3-8).

## STRIDE summary (new surface only)

| Component | Threats |
|---|---|
| `inspect_session` | **E**levation/**I**nfo-disclosure: cross-session read is the point — the target-lock MUST be engine-ctx, not LLM-argument (R3-10); policy deny for all non-verifier agents is backfill-safe. **T**ampering: read-only enforced by tool implementation; keep it out of `load_tool` discovery for non-verifiers (policy covers). |
| Verifier own session | **I**nfo-disclosure: worker transcript window may contain sensitive user content now copied into a second persisted session — retention/cleanup (R3-12) is also a data-minimization question. **R**epudiation: persistence + `EntryTypeJudgeVerdict` give a good audit trail — better than the current unpersisted `Provider.Chat`. |
| Restart endpoint | **D**oS: cap bypass (R3-4). **E**: gate with the same authz as approve/stop. |
| Evidence-marker gate | **S**poofing: a worker can emit fake `[goal:evidence]` text — fine, it is a filter before the verifier, not evidence itself; the ADR says this correctly ("a claim, never a verdict"). |
| Prompt-injection at the verifier | Worker prose enters the verifier's context by design; "data-not-instructions" framing + skeptical rubric are stated mitigations — acceptable for a transcript-first platform, and the independent tool cross-check is the backstop. Coherent with "independent verdict" as argued (§ Impartiality principle). |

## Unasked questions

1. Does a verifier session count against SEC-26's per-agent LLM rate limit as the JUDGE's agent id in the standard loop (it should — confirm the standard turnLoop gate keys on the executing agent), and does a tool-using multi-turn verifier multiply judge cost per round (N default should budget for >1 call on escalation)?
2. When the seeded Judge is later pointed at per-agent verifiers ("an agent's config points at a verifier agent"), what stops a normal chat agent being named verifier and thereby gaining `inspect_session` over arbitrary task sessions? (Answer should be: the allow is per-AGENT policy + the engine-ctx target lock — but say it.)
3. Does the `/goal` scope get an own-session verifier too (goal_loop's judge runs mid-turn today with a turn-derived timeout) — and if so, per round or per goal?
4. What does the "view judge reasoning" drill-down render for a machine-only verdict (no verifier session exists — `JudgeCriteria` skips the LLM entirely when all criteria are `check`)?

## Verdict

**REVISE.**

Priority order:
1. **R3-4** (restart→`approved`, cap-gated — guardrail hole; smallest edit, highest stakes)
2. **R3-5** (§8/§9.2 rewrite to the r4 mechanism — removes the internal contradiction that would mis-drive plan-spec)
3. **R3-1 + R3-2** (re-frame soul/rubric and verifier-tools as the invariant amendments + contract deltas they are)
4. **R3-3** (decide upgrade seeding for Jim)
5. **R3-6 + R3-7** (visibility mechanism decision + M1-for-verifiers sentence)
6. R3-8..R3-13 as ADR one-liners or explicitly-assigned plan-spec items.

Review written to: `docs/internal/architecture/ADR-052-autonomous-agent-plan-execution-review.md`

Address the findings, then re-run:
  `/grill-spec docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md`

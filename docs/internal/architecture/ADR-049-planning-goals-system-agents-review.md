# Adversarial Review: ADR-049 — Planning & Goals, Plan entity, evidence-ladder judge, System Agents

**Spec reviewed**: `docs/internal/architecture/ADR-049-planning-goals-system-agents.md`
**Review date**: 2026-07-19
**Review mode**: generic-markdown (ADR, ratification format)
**Verdict**: BLOCK

## Executive Summary

The ADR's decision *directions* (D1–D8) are largely well-grounded and most of its
file:line citations verify against the codebase — but its highest-risk decision (D3)
is premised on a function that no longer exists: `forceCompression` was replaced by
`windowTrim` (eviction-only, zero LLM calls) under ADR-028 context paging, so the
"Summarizer reroute" targets the wrong seam and the `[FACT]` tag on it is false.
Two further security interactions the ADR itself asked this review to stress are
unresolved: reviving AgentType `system` silently grants SEC-26 rate-limit and
cost-cap exemptions to the Judge, and the machine-check execution model leaves the
`ask` policy value, execution path, timeouts, and criterion authorship undefined.
3 CRITICAL, 9 MAJOR, 4 MINOR, 3 OBSERVATION findings.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 9 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **19** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] D3 is premised on deleted code: `forceCompression` no longer exists

- **Lens**: Incorrectness
- **Affected section**: D3 (System Agents), §1 blast radius ("memory compaction path"), §7 "Compaction reroute", §8 confidence, §9 step 4 (spike), FR-7. Cited as `[FACT]`: "(`forceCompression`, `pkg/agent/loop.go:5749+`)".
- **Description**: `forceCompression` has been removed from the codebase, including
  on the branch base this epic cuts from (`96a1c1ab`, verified in the working tree).
  `pkg/agent/loop.go:8779` states: *"windowTrim replaces forceCompression
  (FR-001/002/003/004). It evicts the oldest whole Turn(s)... deleting zero bytes
  from disk."* `windowTrim` makes **no LLM call at all** — it is token-budget
  eviction. The LLM summarization that still exists is
  `maybeSummarize`/`summarizeSession` (`pkg/agent/loop.go:8654`, `:9312`), and its
  output is rendered as an *inert, reference-only* legacy block
  (`pkg/agent/context.go:877-885`: "the authoritative history is the sliding
  window, not the summary"). The ADR inherited the stale claim from CLAUDE.md
  (which still describes single-layer `forceCompression` at `loop.go:5749-5800+`);
  by the repo's own rule, code wins over docs.
- **Impact**: The epic's single Medium-confidence decision — the one the ADR asked
  grill-spec to stress — is scoped, risk-assessed ("context-compression hot path"),
  and cost-justified ("same LLM work, now attributed") against a function that does
  not exist. The actual hot path (`windowTrim`) needs no reroute because it makes no
  LLM calls; the actual reroutable call (`summarizeSession`) produces a summary that
  is already semi-deprecated reference text under ADR-028. Implementers following
  this ADR would either hunt for dead code or bolt a System Agent onto the wrong
  seam; the promised "compaction regression harness around forceCompression"
  (§9 step 4) is unbuildable as written.
- **Recommendation**: Re-ground D3 on the real seams before plan-spec: (a) name
  `summarizeSession`/`maybeSummarize` as the Summarizer reroute target and restate
  the risk (summary-quality/model-change regression on a reference-only artifact,
  NOT the eviction hot path); (b) state explicitly that `windowTrim` is untouched;
  (c) re-evaluate whether rerouting a legacy-inert summary path justifies same-epic
  scope at all (see MAJ-008); (d) fix the citations and drop the false `[FACT]`
  tag; (e) file a CLAUDE.md correction alongside (OBS-001).

---

#### [CRIT-002] Reviving AgentType `system` grants SEC-26 rate-limit and cost-cap exemptions to System Agents

- **Lens**: Insecurity (Elevation of Privilege / Denial of Service — cost)
- **Affected section**: D3, FR-7, Gap #3, NFR-1/D7 (no token brakes).
- **Description**: `security.IsPrivilegedAgent` (`pkg/security/ratelimit.go:17-22`)
  returns true for agent types `"core"` **and `"system"`**, exempting them from
  per-agent LLM call rate limits and daily cost caps per SEC-26 (enforcement seams:
  `pkg/agent/loop.go:6289`, `:6321`, `:7880`; cap checks `ratelimit.go:193`,
  `:270`). D3's plan is explicitly to "revive" the legacy `system` enum for the
  new System Agents. The Judge fires at least once per task attempt, across a
  default 8-lane concurrent dispatcher (D4/Gap #7), inside loops whose only brakes
  are counts and calendar (NFR-1). The ADR never mentions this interaction.
- **Impact**: The single highest-volume new LLM caller in the system (the Judge) is
  exempt from every runtime spend limiter by an implicit property of its type
  string, in an epic whose stated cost-control posture is "no token/money brakes."
  A runaway or maliciously-provoked loop farm generates unmetered, un-rate-limited
  provider spend — the 3 AM incident here is a provider bill, and SEC-26's control
  was silently bypassed rather than deliberately waived.
- **Recommendation**: Decide explicitly in the ADR, either: (1) System Agents are
  NOT privileged — introduce a distinct type value (e.g. `system_service`) or
  change `IsPrivilegedAgent` to enumerate core only, and cover the new type in the
  SEC-26 tests; or (2) the exemption is intended — record it as a deliberate
  decision with the compensating control (count brakes + Gap #7 lane cap) named,
  and add a Judge-specific call ceiling. Silence is not an option for a security
  control bypass.

---

#### [CRIT-003] Machine-check execution model is underspecified on the four axes that make it a security surface

- **Lens**: Insecurity (Elevation of Privilege / Tampering / DoS)
- **Affected section**: FR-3, D2, Gap #2, §7 "Machine-check execution is a security surface".
- **Description**: The ADR's mitigation — checks "inherit the executing agent's
  sandbox + explicit `bash` policy; never a privileged side channel" — resolves
  only the *deny* case. Four axes remain open:
  1. **`ask` is undefined.** Tool policies are the triad allow/ask/deny
     (`pkg/config/validate.go:46-53`). A judge-time check under `bash: ask` in an
     unattended loop: block forever? auto-deny? emit an approval prompt per
     attempt? Nothing is specified.
  2. **Execution path is unspecified.** Checks run at judge time, outside the
     assignee's turn. Whether the plan engine invokes the existing `bash` tool
     dispatch (policy + sandbox enforced by construction) or a new direct exec
     call (which must re-implement Landlock/seccomp confinement, the env
     allowlist in `pkg/sandbox/hardened_exec.go`, and audit) is exactly the
     "privileged side channel" the ADR forbids — but it forbids it without
     mandating the mechanism that prevents it.
  3. **Criterion authorship vs execution identity.** Cross-agent task
     creation/reassignment is delegation-gated (`pkg/sysagent/tools/task.go:144-160`,
     `:253-260` — trust_set + mode "task" + depth), which bounds who can plant a
     check on another agent. But a machine check executes *mechanically*: unlike a
     delegated prompt, the assignee's LLM never gets to refuse. A trust edge to a
     `bash: allow` agent (e.g. Jim on a fresh install) becomes the right to run
     arbitrary shell under that agent's sandbox with zero model judgment in the
     loop. Criterion author is not recorded; `update_task` can also edit criteria
     post-approval.
  4. **No timeout or resource bound.** A check command that hangs stalls the judge
     and the loop; the ADR's brakes are count + calendar only. Does a hung check
     count as "active" against the 7-day idle expiry?
- **Impact**: As written, an implementer can "comply" with Gap #2's assumption while
  building a second, judge-owned exec path; `ask`-policied agents behave
  nondeterministically; and any trusted-delegation edge is quietly upgraded to
  unattended arbitrary command execution on the target.
- **Recommendation**: Amend Gap #2's likely-assumption into ratified text: (a)
  checks MUST be dispatched through the assignee's existing `bash` tool machinery
  (same registry, policy resolution, sandbox, audit) — a parallel exec path is
  forbidden; (b) `ask` resolves to deny in unattended check context (fail closed),
  stated explicitly; (c) record criterion author on each criterion; plan-spec must
  decide whether cross-agent-authored machine checks require confirmation by the
  assignee's owner or a workspace setting; (d) per-check timeout (suggest default
  60s, configurable) and output cap, with timeout = criterion failed (closed).

---

### MAJOR Findings

#### [MAJ-001] System Agents enter every agent-enumerating surface, not just the policy matrix

- **Lens**: Incompleteness
- **Affected section**: D3, FR-7, Gap #3.
- **Description**: Gap #3 addresses only the Constraint #6 coverage matrix (which is
  in fact the easy part — `RepairIncompleteToolPolicyCoverage`
  (`pkg/config/validate.go:568`) already backfills all-deny, and
  `ValidateToolPolicyCoverage` (`:491`) iterates `cfg.Agents.List` uniformly). But
  agents in `Agents.List` also surface in: the default-agent fallback —
  `resolveDefaultAgentID` falls back to the **first ENABLED agent** when none is
  marked default, so a System Agent can silently become the chat-routing default;
  channel routing bindings (nothing type-checks a binding target); `list_agents`
  and delegation/agent pickers; workspace team rosters. None of these exclusions
  are specified.
- **Impact**: An operator who unsets the default flag (or a repair path that drops
  it) can end up with Telegram messages routed to the Judge; the Agents screen and
  delegation pickers grow noise; a misconfigured binding sends user chat into a
  no-tools rubric prompt.
- **Recommendation**: Add to D3: System Agents are excluded from default-agent
  fallback resolution, invalid as routing-binding targets (400 on write), excluded
  from delegation target enumeration and workspace team pickers, and rendered only
  in the new Agents-screen "System" section. Enumerate these as requirements for
  plan-spec.

---

#### [MAJ-002] Milestone→tag migration: silent loss of `due_date`, name-collision merging, and unspecified mechanics

- **Lens**: Incompleteness
- **Affected section**: Gap #1, D1, FR-2.
- **Description**: The likely-assumption "auto-convert each milestone to a tag
  (`milestone:<name>`) on tasks; drop due_date/progress" has three holes: (1)
  `DueDate` is stored, user-entered data (generated `Milestone` struct,
  `pkg/api/generated/openapi_types.gen.go:5578`) — dropped without notice; (2)
  milestone *names* are not guaranteed unique — two milestones named "Q3" collapse
  into one tag, silently merging their task groupings (IDs are unique; names are
  the tag key); (3) "progress" is computed read-time over task files
  (`pkg/gateway/rest_milestones.go:121-166`, FR-L2-010) — there is nothing to
  drop, so the assumption misdescribes the data model. Migration mechanics (runs
  where — config-load repair? boot one-shot?; idempotency; crash-mid-migration
  behavior; what happens to `Task.MilestoneID` fields pointing at nothing) are all
  deferred with no stated requirements.
- **Impact**: Upgrading users lose deadline data with no warning and can find
  distinct release buckets merged; a crash mid-migration leaves half-tagged tasks
  with no re-run guarantee.
- **Recommendation**: Ratify migration *requirements* now (mechanics stay
  plan-spec): idempotent, runs at config/task-store load, disambiguates
  name collisions (e.g. `milestone:<name>-2`), maps milestone `due_date` onto
  either the tag metadata or each member task's empty `Due` field — or explicitly
  records the operator's decision to discard it, in the ADR, as a one-way door.

---

#### [MAJ-003] Evidence capture will store and display secrets; no redaction requirement

- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: FR-3 ("evidence recorded"), Gap #5, D2 operational impact.
- **Description**: Machine-check evidence is raw command output. Build/test/deploy
  commands routinely echo env vars, tokens, and connection strings. The ADR
  specifies storage ("capped + truncated") and retention as open questions but
  never requires redaction, despite the codebase already having a
  sensitive-value registration mechanism at credential boot
  (`RegisterSensitiveValues`, ADR-004 flow). Evidence is also, by design (NFR-5
  transparency), a UI-visible artifact.
- **Impact**: A check like `deploy.sh && curl -v $API_URL` writes bearer tokens
  into `tasks/<id>.evidence.jsonl` in plaintext and renders them in the SPA —
  persisting past the session and outside the encrypted credential store.
- **Recommendation**: Add to D2/Gap #5: evidence MUST pass through the registered
  sensitive-value redaction before persistence; cap per-attempt evidence size;
  define retention aligned with the existing 90-day session default; evidence
  files live under `$OMNIPUS_HOME` with the same permissions posture as sessions.

---

#### [MAJ-004] Loop durability, recovery, and expiry mechanics are unspecified

- **Lens**: Inoperability / Incompleteness
- **Affected section**: D4, D6, D7, NFR-3, §6 (goal/loop session state).
- **Description**: Nothing states what survives a gateway restart: /goal round
  counters, plan-engine in-flight state, idle-expiry timestamps, self-paced /loop
  chains. Cron jobs persist (`pkg/cron/service.go` restores jobs; one-time "at"
  jobs delete after run — `:784-801`), so /loop interval mode likely survives, but
  the plan engine "consumes task-completion events server-side" — if those events
  are in-process bus messages, a crash mid-plan orphans the plan unless the engine
  reconciles from task statuses at boot. The 7-day idle expiry needs a sweeper
  nobody owns. NFR-3's handover summary has no stated destination (task result?
  plan record? session transcript?).
- **Impact**: A restart mid-plan strands tasks in `in_progress` with no
  coordinator; expired goals linger forever if the sweeper is never specified;
  on-call has no runbook answer for "the plan stopped advancing."
- **Recommendation**: Ratify: all loop/goal/plan counters and timestamps are
  persisted fields (plan entity + `UnifiedMeta` extension); plan engine performs
  boot-time reconciliation from the task store (statuses are the source of truth,
  events are an optimization); idle expiry runs on the existing cron service;
  handover text is written to the plan/task record AND the owning session.

---

#### [MAJ-005] DoD enforcement (D5/FR-6) has unaddressed bypass and edge paths

- **Lens**: Ambiguity
- **Affected section**: D5, FR-6, FR-4.
- **Description**: (1) `update_task` can edit tasks — can an agent create with a
  throwaway criterion then strip or trivialize it? Criteria mutation rules are
  unstated. (2) `set_todos` creates disk-only Scratchpad tasks
  (`pkg/task/task.go` `Scratchpad` field) through a tool path — strict FR-6 as
  written would break the todos facade; exemption unstated. (3) The human-path
  fallback is "prompt-as-criterion", but `Prompt` is optional — a title-only board
  card assigned to an agent has no prompt; what does the judge evaluate? (4)
  Recurring `Trigger` tasks (Gap #8): are criteria re-evaluated per run, and can a
  run mutate them?
- **Impact**: Two engineers implement different enforcement: one blocks
  `set_todos`, one exempts all internal paths; agents trivially bypass strict
  create via update; judge behavior on prompt-less human tasks is undefined
  (and NFR-2 says absence of evidence must fail closed — which would fail every
  bare human task).
- **Recommendation**: Extend D5: criteria edits on agent tool paths cannot reduce
  the count below 1; Scratchpad tasks are exempt (never executed as goal loops);
  human tasks with neither criteria nor prompt fall back to
  title+description-as-criterion, and this is stated; recurring-run criteria are
  immutable per run.

---

#### [MAJ-006] NFR-5 visibility claims contradict the ActivityPanel's actual data model; spend visibility has no attribution path

- **Lens**: Infeasibility / Inoperability
- **Affected section**: NFR-5, D2 ("judge visible in ActivityPanel"), NFR-1/D7 ("token spend visible").
- **Description**: The ActivityPanel shows exactly two span types — subagent spans
  and background bash sessions — capped at the 8 most-recently-finished
  (`RECENTLY_FINISHED_CAP`, `useRunningActivity.ts`; documented in CLAUDE.md UI
  rules). Judge and Summarizer calls are "no-tools structured calls" — neither
  span type. As written, "judge visible in ActivityPanel" is infeasible without
  extending the panel's data model, which is unstated work. Similarly, "token
  spend visible, never enforcing" requires judge/summarizer calls — which run
  *outside* `runTurn` and its usage accounting — to flow into the usage/metering
  pipeline with goal/plan attribution; no mechanism is named.
- **Impact**: The transparency rationale that motivates the entire System Agents
  category (D3's stated purpose) ships without a rendering surface or a metering
  path; operators cannot see the very spend the no-brakes posture depends on them
  watching.
- **Recommendation**: Add explicit scope: a new ActivityPanel span type (or a
  dedicated judge-verdict entry in the session transcript wire contract) for
  System-Agent calls, and a requirement that all out-of-turn structured calls
  report usage through the same provider-metering path with
  `agent_id = <system agent>` plus plan/task/goal correlation IDs.

---

#### [MAJ-007] Release-phase routing and branch-base consequences are unaddressed

- **Lens**: Inconsistency
- **Affected section**: Header ("cut from `release/v0.1.1`"), §2 Constraints, §7.
- **Description**: CLAUDE.md's routing rule sends "Memory / tasks / agents /
  workspaces / plugins" work to v0.3; this epic rewrites the task data model,
  extends the agent taxonomy, and touches the memory-summarization path, yet
  ships from a patch-release base and deletes a shipped feature (Milestones)
  there. The ADR records the branch as `[FACT]` but never records the phase
  decision the repo's own rule demands ("ask which phase it belongs to first").
  Two mechanical consequences also go unmentioned: PRs merged into a non-`main`
  base do not auto-close issues (documented repo convention), and the epic's
  relationship to the `.preview-doc/` v0.3 Workspaces direction (which owns
  "tasks" and "agents" re-casting) is not reconciled.
- **Impact**: The largest planning-surface change since the task system lands on a
  patch line, potentially colliding with or pre-empting v0.3 structural decisions,
  with issue-closure hygiene breaking silently.
- **Recommendation**: Add one ratified paragraph: the operator's phase placement
  for this epic, why `release/v0.1.1` is the base, the intended path back to
  `main`, and a sentence reconciling Plan/tags/System-Agents with the
  `.preview-doc/` v0.3 concept (or explicitly deferring that reconciliation).

---

#### [MAJ-008] Summarizer-in-this-epic is scope creep against both stated problems — and CRIT-001 removes its premise

- **Lens**: Overcomplexity
- **Affected section**: D3, FR-7, §8.
- **Description**: The two operator problems (§1) are "plans have no container" and
  "no goals/loops." The Judge serves both; the Memory Summarizer serves neither —
  it rides along for transparency parity. The ADR itself rates D3 Medium solely
  because of the reroute, and §9 already hedges with a pre-implementation spike.
  CRIT-001 shows the reroute target is misidentified and the surviving target
  (`summarizeSession`) produces a reference-only legacy artifact under ADR-028 —
  the payoff for taking a hot-path-adjacent regression risk in this epic is
  smaller than the ADR believed when it accepted the risk.
- **Impact**: The epic's one Medium-confidence element widens the review surface,
  couples Planning & Goals delivery to a memory-subsystem regression risk, and —
  post-CRIT-001 — does so for a semi-deprecated output.
- **Recommendation**: Split the Summarizer reroute into a fast-follow behind the
  category work: ship the System Agents *category* + Judge in this epic (the
  category is the reusable asset), land the Summarizer as its own small PR after
  the re-grounded regression harness exists. This converts D3 to High confidence
  without losing the operator's decision.

---

#### [MAJ-009] Count brakes compose multiplicatively; no global ceiling on concurrent loops

- **Lens**: Incompleteness (DoS / cost)
- **Affected section**: D7, FR-9, Gap #7.
- **Description**: Every brake is per-level: /loop 100 runs, /goal 20 rounds, task
  3 attempts, plan judge rounds (unspecified), each attempt adding a judge call.
  Nothing bounds the *product* (a /loop whose each run opens a plan of N tasks
  legitimately multiplies to 100 × N × 3 attempts × judge calls) nor the total
  number of simultaneously active goal loops (the Gap #7 lane caps concurrent
  dispatch, not the population of live loops). NFR-1's rejection of token brakes
  is respected — but count brakes only work as a cost posture if some count is
  global.
- **Impact**: An operator (or a single enthusiastic agent with loop-creating
  tools) can accumulate dozens of live loops, each individually within bounds,
  collectively generating unbounded judge/attempt volume — the exact failure the
  brake system exists to prevent, and (per CRIT-002) possibly exempt from rate
  limits.
- **Recommendation**: Add to D7: a global cap on concurrently active goal loops
  (count-based, configurable, default modest — e.g. 16) and a per-plan round
  bound symmetrical with /goal's 20. Surface "active loops: N/cap" in the /goal
  and /loop status output.

---

### MINOR Findings

#### [MIN-001] Undefined operational terms: "round", "idle", "cheapest model", concurrent goals

- **Lens**: Ambiguity
- **Affected section**: D7, Gap #4, FR-5.
- **Description**: "20 rounds" — is a round one agent turn, one judge cycle, or one
  turn+judge pair? "7-day idle expiry" — idle means no state transition, no
  attempt, or no user interaction (does a hung check or a waiting `ask` count as
  active)? Gap #4's "cheapest configured provider/model" is not machine-derivable
  without a price table — name the heuristic (static rank list? smallest context
  model? operator-set). Can one session hold multiple concurrent `/goal`s, and
  what does `/goal clear` clear then?
- **Recommendation**: Define all four in plan-spec glossary terms; the ADR should
  at minimum define "round" since the D7 defaults are ratified numbers attached to
  an undefined unit.

#### [MIN-002] Guaranteed-fail configuration: machine-only criteria assigned to a bash-denied agent

- **Lens**: Incorrectness
- **Affected section**: Gap #2 assumption + FR-6 interaction.
- **Description**: Fail-closed on denied `bash` (correct) plus mandatory criteria
  means a task whose criteria are all machine checks, assigned to an agent with
  `bash: deny`, fails 3 attempts deterministically, every time, then wakes the
  owner — a structurally unsatisfiable task the system happily dispatches.
- **Recommendation**: Create/update-time validation warning (agent tool paths:
  reject; UI: warn) when every criterion is machine-type and the assignee's
  effective `bash` policy is deny.

#### [MIN-003] Tag schema and conventions unstated despite tag-proliferation being a named risk

- **Lens**: Ambiguity
- **Affected section**: D1, FR-2.
- **Description**: D1's own risk row says "tag proliferation without conventions",
  but no tag constraints exist anywhere: charset, length, case-sensitivity,
  per-task cap, workspace-scoped vs global namespace, free-form vs curated.
  The migration convention `milestone:<name>` implies a `prefix:value` convention
  nothing else establishes.
- **Recommendation**: Ratify minimal tag rules now (lowercase, bounded length,
  bounded count per task, workspace-scoped) so the wire schema (Constraint #8)
  isn't designed twice.

#### [MIN-004] Five-level bound cascade is heavier than the day-one need

- **Lens**: Overcomplexity
- **Affected section**: FR-9, D7.
- **Description**: global → workspace → plan → task → goal is five resolution
  layers for integers whose defaults (3/20/100/7d) will rarely be touched. Each
  layer is config surface, wire surface, UI surface, and precedence-bug surface.
- **Recommendation**: Ship global + per-entity (the entity the loop runs on)
  first; add workspace-level only when a concrete need appears. If the operator
  ratified all five knowingly, record that this was weighed against Lens-8 cost.

---

### Observations

#### [OBS-001] Citation hygiene is otherwise strong — and CLAUDE.md is the stale source for the one failure

- **Lens**: Incorrectness
- **Affected section**: §1 leverage list, D4–D7 citations.
- **Suggestion**: Verified accurate: `finishTaskRun` (`task_executor.go:250`),
  `turnLoop`/hard ceiling (`loop.go:6257`/`6272`), `handleCommand`/
  `applyExplicitSkillCommand` (`loop.go:9582`/`9693`), async-notifier Goals
  comments (`async_notifier.go:37/118/292`), `AdvanceBlockedDependents`
  (`blocked_by.go:181`), `MetaPatch.TaskID` (`unified.go:~62`), cron
  `maxConcurrentRuns` default 8 + `at` one-shot jobs, `Agent.yaml` `system` enum,
  milestone REST surface, `DeliveryAgent` in `pkg/commands`. The single failure is
  `forceCompression` (CRIT-001), copied from CLAUDE.md's own stale Storage
  paragraph — fix CLAUDE.md in the same PR that amends the ADR.

#### [OBS-002] Enumerate the full Constraint #8 wire-type list before plan-spec BDD

- **Lens**: Incompleteness
- **Affected section**: §7 "Contract surface growth".
- **Suggestion**: This epic plausibly adds: `Plan`, `PlanState`, `Tag`(s on Task),
  `AcceptanceCriterion`, `Evidence`, `JudgeVerdict`, goal/loop status frames
  (AsyncAPI), System-Agent additions to `Agent`, and command registry entries.
  Listing them as a table in plan-spec §data-model first will serialize the
  five-step contract process instead of discovering types mid-wave.

#### [OBS-003] Feed evidence, not transcript, to the prose judge

- **Lens**: Insecurity (anti-gaming)
- **Affected section**: D2 risks ("LLM judge fooled by transcript claims").
- **Suggestion**: The mitigation named is "prefer machine checks", but the prose
  path can also be hardened cheaply: give the judge the machine-check evidence
  records and workspace file diffs as primary input and the agent's own summary
  last, with an instruction that unevidenced claims score unmet. Aligns with
  NFR-2 at zero architectural cost.

---

## Structural / Narrative Assessment (generic-markdown mode)

- **Scope clarity**: Good. Two named problems, eight enumerated decisions,
  explicit rejected alternatives, explicit "no /taskify" and pipeline position.
  Weakness: phase/release placement missing (MAJ-007).
- **Actors**: Agents, operator, judge, plan engine covered. Missing: multi-user
  gateway users (who may issue /goal, /loop, create criteria — auth/role
  unaddressed) and the on-call operator persona (MAJ-004).
- **Success criteria**: The ADR defers measurable acceptance to plan-spec, which is
  legitimate for a ratification ADR — but D7's ratified numbers attach to an
  undefined unit ("round", MIN-001).
- **Failure modes**: The Gaps table (§3) is genuinely good practice — 8 named gaps
  with owners. However three gap *assumptions* function as decisions without
  ratification (Gap #1 data loss, Gap #2 `ask` silence, Gap #3 partial coverage) —
  the CRITICAL/MAJOR findings above land exactly there.
- **Implementation detail**: Seam citations are strong (one fatal exception,
  CRIT-001). Enough for plan-spec to begin only after the D3 re-grounding.
- **Assumptions**: Explicitly labeled with `[FACT]`/`[INFERENCE]` and per-decision
  confidence — commendable; one `[FACT]` is false (CRIT-001).
- **Constraints**: Hard Constraints #1–#8 acknowledged; the Constraint #6
  treatment is incomplete beyond the matrix (MAJ-001) and the SEC-26 interaction
  is missed entirely (CRIT-002).

## Test Coverage Assessment

The ADR gestures at the right artifacts (compaction regression harness, migration
test, BDD per surface) but:

1. The compaction harness is specified around a deleted function (CRIT-001) — it
   must target `summarizeSession` + `windowTrim` invariants instead.
2. No security test is named for the check-execution path (assert: check runs
   inside the assignee's sandbox; `bash: deny` ⇒ criterion fails closed;
   `bash: ask` behavior per CRIT-003c; timeout enforcement).
3. No boot test is named for Constraint #6 with System Agents seeded (fresh
   install: matrix total; upgrade: repair backfills; REST create of a System
   Agent type rejected/allowed per decision).
4. No crash-recovery test for the plan engine (kill mid-plan, restart, assert
   reconciliation from task statuses).
5. No idempotency test requirement for the Milestone migration.
6. Judge fail-closed grammar tests exist to extend (`task_completion_signal.go`,
   ADR-043) — say so explicitly so implementers extend rather than fork the
   parser.

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Machine-check executor | — | Criteria editable via `update_task` post-approval (CRIT-003c) | Criterion author unrecorded (CRIT-003c) | Evidence captures secrets (MAJ-003) | No timeout (CRIT-003d) | Exec path/`ask` undefined; mechanical exec bypasses assignee judgment (CRIT-003a-c) | The epic's primary new attack surface |
| Judge System Agent | Type `system` = privileged identity by string (CRIT-002) | Prose judge foolable by transcript (OBS-003) | Calls outside runTurn may skip usage/audit attribution (MAJ-006) | Rubric/verdict surfaces evidence content | Judge call per attempt, rate-limit exempt (CRIT-002, MAJ-009) | SEC-26 exemption (CRIT-002) |
| Memory Summarizer | Same type concerns as Judge | — | — | Summary of full session content under a different model/provider than the session agent | — | — | Recommend fast-follow (MAJ-008) |
| Plan engine | — | — | State transitions need audit entries | — | Lane cap bounds dispatch, not loop population (MAJ-009); no boot reconciliation (MAJ-004) | — | Server-side, no LLM |
| Evidence store | — | No integrity spec (append-only? HMAC out of scope, fine) | — | Plaintext secrets on disk + UI (MAJ-003) | Unbounded output without caps (Gap #5 acknowledges) | — | |
| /goal, /loop commands | Any channel sender in a bound chat can start loops — role gating unstated | — | — | — | Loop creation itself un-rate-limited (MAJ-009) | — | Multi-user gateway question unasked |
| Milestone migration | — | Name-collision merges distinct groupings (MAJ-002) | No migration log specified | — | — | — | Data loss: due_date (MAJ-002) |

## Unasked Questions

1. Who is authorized to issue `/goal` and `/loop` on a multi-user gateway — any
   authenticated user in a bound chat? Any channel peer routed to an agent?
2. What happens to a running plan/goal when its owner agent is disabled, deleted,
   or its workspace removed mid-loop?
3. Are judge verdicts written into the task session transcript (and in what wire
   shape), so ADR-043's fail-closed marker and the new judge verdict cannot
   disagree about the same run?
4. Is a Plan workspace-scoped (tasks are)? Can a plan reference tasks across
   workspaces, and if not, is that validated?
5. Does the plan-level judge run as the same Judge System Agent with a different
   rubric, or a second seeded agent?
6. Can a delegated sub-turn or a task run issue `/goal`/`/loop` (loops spawning
   loops), and does DelegationDepth bound that?
7. How does the epic reach `main` from `release/v0.1.1`, and who closes the
   issues that the non-main base will not auto-close?
8. Does evidence retention follow the 90-day session default, and is it deleted
   with the task?
9. Is milestone `due_date` migrated into member tasks' `Due`, tag metadata, or
   discarded (operator sign-off required if discarded)?
10. After a config hot-reload, is there exactly one plan engine (overlap guard
    like cron's), or can two engine instances double-dispatch a ready task?

---

## Verdict

**BLOCK.**

Three CRITICAL findings, all inside the exact areas the ADR nominated for stress:
D3's factual premise (CRIT-001), the System-Agent privilege interaction
(CRIT-002), and the machine-check execution model (CRIT-003). The decision
directions D1, D2, D4–D8 are sound and well-evidenced; the blockers are targeted
amendments, not a redesign.

Address the findings above (at minimum CRIT-001–003, MAJ-001–004, and a recorded
decision on MAJ-007/MAJ-008), then re-run:

```
/grill-spec docs/internal/architecture/ADR-049-planning-goals-system-agents.md
```

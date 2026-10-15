# Adversarial Review: Agent scheduled tasks and Suspend / Resume

**Document reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md`
**Input design**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/design/sched-tasks-suspend-resume-design-20261015.md`
**Round-1 review left in place**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review.md`
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-10-15
**Reviewed commit**: `5c62e772794b5475646d9348315d22962612e8a5` on `feat/sched-tasks-20261015`
**Verdict**: BLOCK
**Independence**: Fresh reviewer. This reviewer did not author the specification or the design. The spec was not edited.
**Policy baseline inspected**: `origin/work/omnipus-l1-ui-rooms-wip` at `6d3aa4ce12e71ee6073bac2962d8b0117e2fdab5`. The spec's pinned candidate `ad853522d1a9bbcbf800ea59b76c337cbada3feb` is an ancestor of that tip. L1 is not merged into this worktree.
**Method**: Full spec read, round-1 finding check against the corrected text, and direct reads of the scheduler, executor, cron engine, and L1 policy symbols. GitNexus was not used; symbol locations were verified by search and read. No application build or test was run.

## Executive Summary

Round 1's seven major repairs are in the text, and the identifier links match (36 requirements, 63 scenarios, 36 planned tests). They do not cover a one-time fire that is reset and then refused before it is claimed: the only job is deleted, and the new queue rule refuses to pick the task up. The approval card can hide that a schedule edit stays suspended. "Creator/assigner authority" is not what is already stored on tasks that have retried. A new custom chat agent is told to omit `schedule_task`, which on the L1 model stores Deny.

Frontend coverage (approval card, Calendar stale-save, saved-but-unavailable, catalogue map) was reviewed at the same depth as the scheduler and the policy seed.

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| MAJOR | 4 |
| MINOR | 3 |
| OBSERVATION | 1 |
| **Total** | **9** |

---

## Round-1 closure

Reviewed against the corrected spec at `5c62e7727`. A closed row is not re-opened. Residuals are new findings below, not a second copy of the old one.

| Round-1 finding | Disposition | Where it is closed, or what remains |
|---|---|---|
| MAJ-001 standing future authority | Closed as a forward rule | AUTH-1, FR-014, FR-036, BDD-55, BDD-63. Residual: files already rewritten by today's retry (MAJ-002 below). |
| MAJ-002 goal/task publication | Closed | §5.1.1 goal-first, task last, goal → task lock order, BDD-56. |
| MAJ-003 admitted retry after capacity refusal | Closed for a receipt that already exists | §5.1 continuation receipt, BDD-57, §9.3 named queue-pickup regression. Residual: the first fire, before any receipt (CRIT-001). |
| MAJ-004 Clear must not enqueue | Closed for a plain Clear | Idle unadmitted Next/blocked → Inbox, BDD-58. Residual: combined status in the same PATCH (MIN-002). |
| MAJ-005 stale Calendar save | Closed | `expected_schedule_hash`, 400/409, draft review/reapply, BDD-59. Residual: docs never say old clients break (MIN-003). |
| MAJ-006 saved but unavailable | Closed | Task.schedule `scheduler_state`, HTTP 200 plus GET, both controls, BDD-60. |
| MAJ-007 request-wide occurrence bound | Closed as caps | 100 series / 100,000 work / 10,000 items / 1,048,576 bytes, BDD-61. Residual: the work-unit counter has no independent number (MAJ-003 below). |
| MIN-001 schedule audit | Closed as an audit row | §5.3.1, FR-035, BDD-62. Residual: the row cannot show who a later fire runs as (MIN-001 below). |
| MIN-002 hand-off scaffolding | Closed | §1.2 no new ADR, §5.0 contract inventory before mechanisms, §6.5 named catalogue map. |
| OBS-001 thin browser policy matrix | Adopted | T35 is wiring only; T06/T07/T22 and UAT-07/UAT-08 stay. |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] A one-time fire that fails before claim is deleted and never runs

- **Lens**: Incompleteness
- **Affected section**: §5.1 trusted-dispatch table ("A timed task without either of the first two proofs refuses"); FR-009; BDD-18; BDD-57; SC-002. Capacity, plan-gate, and unmet-dependency refusals are specified only after an admitted attempt has a `pending_attempt` receipt.
- **Failure scenario**: Mia schedules "remind me once at 09:00". At 09:00 every worker slot is busy, or the task still has an open dependency, or the plan gate refuses. The task file is rewritten to `next` or `blocked`, its result and session fields are cleared, and the only job is deleted. When a slot frees or the dependency completes, the queue skips the task because it is timed and has no receipt. The calendar shows a past one-time event with no run. Create already returned armed. A repeating series does not die this way, because that path re-arms the next occurrence and returns success. A one-time reminder has no next occurrence. US-1 exists for that reminder.
- **Evidence**: `TaskTriggerScheduler.RunScheduled` calls `Store.SpawnReset` before dispatch. `SpawnReset` sets status to `next`, clears result/session fields, then `recomputeBlockedStateLocked` may leave the task `blocked`. `executeTask` returns on status not `next`, `requirePlanExecuting`, an unmet `BlockedBy`, or `ErrDispatchCapReached` **before** `ClaimForRun`. No `pending_attempt` exists: §5.1 writes that receipt only from `consumeTaskAttempt` after an admitted attempt has already failed. For a non-repeating trigger, `RunScheduled` returns that dispatch error. Once jobs are `at` jobs (`triggerToCronSchedule`), created with `DeleteAfterRun` when the schedule kind is `at`. `CronService.scheduleNextRunUnsafe` removes an `at` job with `DeleteAfterRun` **before** the transient-retry branch, on the error path as well as the success path. `RunRecoverySweep` continues past any trigger that is not a recurring rule, so it will not put the once job back. §5.1 says suspension and staleness refuse before reset. It does not say capacity, the plan gate, or an unmet dependency refuse before reset, and it does not say what the cron engine must do with the once job. FR-009 and BDD-18 then forbid the queue, dependency-unblock, and follow-up paths from starting a timed task with no receipt. BDD-57 starts only after a receipt already exists.
- **Recommendation**: State one rule for a one-time fire and a different rule for a repeating fire. Do not remove a one-time `at` job, and do not keep `SpawnReset`'s status, result, or session writes, unless `ClaimForRun` has succeeded. On a pre-claim refusal (capacity, unmet dependency, plan gate), restore the pre-reset task file and leave that same job retryable; when the blocker clears, exactly one run starts. A repeating fire may still skip to the next planned position, and that skip must not also leave a `next` timed task that the queue will run. Add BDD coverage whose oracle is the task file and the job list after a barrier: once-fire plus cap error; once-fire plus unmet `blocked_by`, then the dependency completes; repeating-fire plus cap error must not double-run. Do not treat BDD-57 as covering this. The queue refusal for a never-admitted twin stays.

---

### MAJOR Findings

#### [MAJ-001] Approving a schedule edit does not say that firing stays off

- **Lens**: Ambiguity; UI states & journey gaps
- **Affected section**: US-4.AC1 ("all five actions have readable … consequence details"); §5.1 Set/replace ("Preserve suspension"); §6.4 Set row; BDD-27; FR-020; FR-029.
- **Failure scenario**: The person asked the agent to move a suspended weekly report to Monday. The card shows the new Monday time and a count restart. They approve, believing the report will run. It stays suspended, `next_fire` is null, and no run appears. That is the informed-consent failure US-4 calls out, in the other direction from approving the wrong instant.
- **Evidence**: Set/replace keeps the task suspended, including recurring→once (§5.1). The approval card is shown before the tool result exists. §6.4's Set row requires identity, old → new schedule, anchor, zone, and count-restart. It does not require the words that automatic firing stays off, or a Suspended label. Resume has an explicit missed-occurrence sentence; Set does not. `scheduler_state=disarmed` on the later tool result is not on the card. The continuing-authority notice (AUTH-1, BDD-63) says the schedule keeps running until suspended or removed. On an already-suspended Set, that sentence is true only if the reader already knows it is suspended. The card is not required to say so.
- **Recommendation**: For Set/replace where the preserved result is suspended, the frozen preview and the registered card must include a fixed sentence before the decision controls, on the live card and after reconnect: "Automatic firing stays off. This change does not resume the schedule." Missing or altered text blocks Approve / Always Allow, same as the continuing-authority notice. Add a Set row (or a sibling scenario) for a suspended recurring→once proposal, and assert an unsuspended Set does not show that sentence. Count-restart text stays; it is not a substitute.

#### [MAJ-002] Stored "creator/assigner" authority is already the last retry

- **Lens**: Inconsistency & ADR/AS-IS contradiction
- **Affected section**: AUTH-1 ("Use the existing `Task.Initiator` creator/assigner carrier"); FR-014; FR-036; the notice string in §5.3; BDD-55; §9.3; DOC-01/02/03.
- **Failure scenario**: A person created the task (nil initiator). Mia used Run now, the run failed, and the file now stores Mia with `Direct` set. After this feature, the next timed fire re-authorizes Mia, not the person. If Mia's delegation edge is gone, a schedule the person believes is theirs never starts. If Mia still has a broader budget than the person, the fire runs as Mia. The approval card states the creator/assigner sentence, which is false for that file. Fresh tasks never hit it.
- **Evidence**: AUTH-1 defines future timed runs as the task's original creator or assigner, and says a nil `Initiator` is a human start. `consumeTaskAttempt` sets `Patch.Initiator` from `retryAuthority` whenever the failed run's authority is non-nil, in the same write that sets `next`. `retryAuthority` sets `Direct: true` on that carried value. `TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup` requires the retry to persist the failed run's authority on the task. A human nil is not written over (`if carried != nil`). §9.3 moves that write off the field for **new** retries and changes one test. It does not say what already-written values mean. FQ-1 A forbids a new standing-authority field and forbids reconstructing history. The required notice still says the schedule "keeps running under the task's creator/assigner authority."
- **Recommendation**: Do not leave this as an implicit "no migration." The founder has to accept one legacy rule, because it changes what FQ-1 A means for files already on disk. See founder question 1. Add a fixture that loads a pre-upgrade task file with `Direct: true` and asserts the chosen rule. Do not claim BDD-55's "original human" row covers a file the old retry path already rewrote.

#### [MAJ-003] The work-unit cap has no count a test can know in advance

- **Lens**: Infeasibility; Testability & false-green risk
- **Affected section**: §5.6 work-unit row ("each query-zone day evaluated … each candidate/anchor-skip/irregular recurrence position visited, and each stored run record examined"); DS-4 rows 12–14; BDD-61; FR-025; SC-004.
- **Failure scenario**: Two builds with the same calendar can refuse or succeed at different workspace sizes. A long-lived series can make a one-day calendar fail if every past run is charged. A test written against the counter under test cannot catch an under-count, which is the false-green SC-004 is supposed to prevent.
- **Evidence**: Series count, output items, and encoded bytes can be computed from the request and the response shape. The work-unit counter cannot. A unit is "a position the engine visited" and "a run record examined," which is known only by walking the same recurrence engine and store the implementation uses. DS-4 row 12 says "demand 100000 / 100001" but gives no fixture whose visit count is frozen the way DS-2 freezes instants. §5.6 also says not to materialize whole history, while charging "each stored run record examined." The spec never names the existing range read or the window it uses. §9.4 says expected values must not be derived with the implementation under test. Those two sentences cannot both be satisfied for this counter as written.
- **Recommendation**: Replace the visit-based sentence with a formula of inputs only, plus one worked number frozen in DS-4. Shape: one unit per selected task file, plus one unit per series per inclusive query-zone day, plus one unit per in-window run returned by the existing range read, and irregular expansion may add at most the already-specified per-task iteration cap. State that runs outside `[from, to)` are not read and not charged. The +1 test must use that published total, not a counter the production function returns. Keep the series, item, and byte caps as they are.

#### [MAJ-004] Omitting `schedule_task` on a new custom chat agent stores Deny

- **Lens**: Reachability; Inconsistency & ADR/AS-IS contradiction
- **Affected section**: §5.4 registration table, row `seed.go::allStaticToolNames / NewCustomAgentToolsCfg / RestrictForWorkerTier` ("deliberately omit only that new key from Main's constructor result") and the `buildToolConfig` row ("Main's intentionally absent new key must survive REST's final Deny-fill"); policy matrix row "Ask / no entry (… custom Main …)".
- **Failure scenario**: A person creates a new chat agent and asks it to schedule a task. The tool is not offered, or the call is denied, even though the global ceiling is Ask and the matrix says a custom Main with no entry is Ask. Mia and Jim can still schedule. The new agent cannot. That is the reachability failure for every agent created after this feature, which is the population Q1 B calls "new."
- **Evidence**: On `origin/work/omnipus-l1-ui-rooms-wip` (`6d3aa4ce1`), `NewCustomAgentToolsCfg` builds its map with `denyAllThenOverride`. That function writes Deny for every name in `allStaticToolNames`, then applies only the keys present in the override map. An omitted `schedule_task` is therefore Deny in the constructor result, not an absent key that rides the ceiling. `rest_agents_create.go::buildToolConfig` then copies that full map and, for any name still missing from `buildKnownBuiltinToolNames`, writes Deny again. Absence cannot survive that fill. The same constructor is what `pkg/sysagent/tools/agent.go` uses for agent-created agents. The sparse omit path (`adr090SparseRolePolicies`) applies to seeded Mia/Jim/workers, not to this constructor. Silence in `ADR090RolePolicyInventory` is also Deny: the inventory starts every static name at Deny, and the sparse seed keeps a row whenever that Deny differs from an Ask ceiling. "Classification Ask, omitted sparsely" is the right end state for Mia and Jim only if the inventory grants Ask. The constructor sentence does not say that, and its "omit" instruction is the opposite for a new custom Main. Q1 B and the matrix already require Ask for that Main. This is a mechanism contradiction, not a new product decision.
- **Recommendation**: In `NewCustomAgentToolsCfg`'s override map, set `schedule_task` to explicit Ask. Do not omit it. Add `schedule_task` to `workerTierDeniedTools` so `RestrictForWorkerTier` then stores Deny for a new worker, after the Ask seed and before the caller override, in both creation routes. In `ADR090RolePolicyInventory`, grant Ask for Mia and Jim (and any other role the matrix lists as Ask with no entry) so the sparse seed omits the key; leave Worker, Planner, and Researcher at the inventory Deny so their sparse seed retains Deny. Do not change `denyAllThenOverride`, `buildToolConfig`'s final fill, or `resolveEffectivePolicyWith`. Assert the constructor map contains Ask, the worker map contains Deny, and a Mia sparse seed does not contain the key.

---

### MINOR Findings

#### [MIN-001] The audit row shows who edited the schedule, not who the fire will run as

- **Lens**: Security
- **Affected section**: §5.3.1 committed-mutation row; FR-035. The row stores actor, hashes, and `scheduler_state`, and forbids copying "private initiator budgets."
- **Failure scenario**: After FQ-1 A the scheduling actor and the fire principal are different people by design. A later reader of the audit log can see who edited the rule and cannot see who the next fire will run as. A Direct legacy value (MAJ-002) is invisible in the row. The schedule hash is defined as opaque, so it cannot be read back as an agent id or "human."
- **Recommendation**: On create/set/resume, record a non-secret principal label only: `human` or the agent id, plus whether that value is a stored retry carry-forward if founder question 1 chooses option A. Do not record depth, budget, prompt, or criteria. Assert it in BDD-62. Hashes stay for the precondition; they are not the forensic record of who will run.

#### [MIN-002] Clear's "runnable status" is not a closed list

- **Lens**: Ambiguity
- **Affected section**: §5.1 Clear ("Reject a combined trigger-clear plus explicit runnable-status PATCH"); DS-1 row 14.
- **Failure scenario**: A PATCH clears timing and also sends `status: next` in one body. An implementer treats "runnable" as only `in_progress`, applies Clear (inbox), then applies `next` in the same locked update. The task returns to the queue the inbox rule just left.
- **Evidence**: The inbox transition is specified when Clear is the only status change. The combined case is described only as "runnable." DS-1 row 14 says refuse, and does not name `next`, `in_progress`, `done`, or `failed`.
- **Recommendation**: Name the combinations. If the body clears timing and also sets `next` or `in_progress`, the whole PATCH is 400 and writes nothing. Omitted status uses the Clear transition (unadmitted `next`/`blocked` → `inbox`). Setting `inbox` explicitly is the same transition. Setting `done` or `failed` in that body is refused too unless a separate existing terminal path is cited. Add those four bodies to DS-1 row 14.

#### [MIN-003] Existing trigger saves become 400, and the doc TODOs never say so

- **Lens**: Incompleteness
- **Affected section**: §5.5 `expected_schedule_hash` ("required whenever `trigger` is present"); §13 DOC-01–DOC-05.
- **Failure scenario**: Any current client that PATCHes a task with a trigger and no 64-character hash gets `400 schedule_precondition_required`, including an older screen or a saved caller that still sends `type/config`. The in-app editor is updated in the same change. None of the documentation TODOs say this is a breaking request change, or what an external caller must send.
- **Recommendation**: Add one sentence to DOC-05 and DOC-01: a trigger change must send the `schedule_hash` from the last Task read; a hash computed by the client is not valid; metadata saves must omit both `trigger` and the hash. Keep the 400/409 text already in §5.5.

---

### Observations

#### [OBS-001] The link checker does not cover these holes

- **Lens**: Testability & false-green risk
- **Affected section**: §17 E-VERIFY; SC-001.
- **Suggestion**: An independent count of this text matches the claimed 36 requirements, 63 scenarios, and 36 planned tests, and every requirement and scenario id appears in the traceability matrix. That check does not execute BDD-57 against `RunScheduled`, and it will stay green if CRIT-001 is left unspecified. Do not cite the link check as evidence that round-1 capacity coverage includes the first fire.

---

## Structural Integrity

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | Draft. No review PASS claimed. |
| ADR linked, or explicit no-ADR determination | PASS | §1.2: no new ADR; existing decisions cited by title. |
| Contract changes stated first, citing `contracts/` | PASS | §5.0 inventory before §5.1 mechanisms. Shapes in §5.5. |
| API and data section | PASS | §5. Closed for round-1 contract holes. CRIT-001 is execution, not a missing schema. |
| UI screens and states (loading/empty/error/partial) | PASS, with MAJ-001 | §6.2 names saving, conflict, saved-unavailable, exhausted, unknown. Set approval omits the preserved-suspension consequence. |
| User journey section | PASS | §2 and §6. |
| Accessibility and keyboard section | PASS | §6.5: Tab, Enter/Space, focus return, announcements, narrow payload. |
| Design-system components, catalogue-first | PASS | §6.5 names Button, Badge, FormError, Skeleton, QueryErrorState, EmptyState, Sheet, Dialog. No new family. |
| Security and user promises section | PASS, with MAJ-002 and MIN-001 | AUTH-1, audit, docs TODOs present. Legacy initiator and audit principal remain. |
| BDD acceptance scenarios, oracle from spec | PASS, with MAJ-003 | DS-2 instants are frozen. DS-4 work units are not. |
| Traceability table: requirement → scenario → test | PASS | 36 / 63 / 36, matrix covers every id. Links are not behavioural proof. |
| Reachability section | PASS as a plan, REVISE on MAJ-004 | §5.4 and §11 name the real symbols. The custom-Main instruction contradicts the L1 fill. |
| User-facing documentation TODOs | PASS, with MIN-003 | DOC-01–DOC-05 name pages. The breaking PATCH is not one of the sentences. |

---

## Twelve-lens coverage

| Lens | Disposition |
|---|---|
| 1 — Ambiguity | MAJ-001, MIN-002. |
| 2 — Incompleteness | CRIT-001, MIN-003. |
| 3 — Inconsistency with ADRs / as-is | MAJ-002 (stored initiator is not original creator). MAJ-004 (omit versus L1 deny-fill). No retired surface reintroduced. |
| 4 — Infeasibility | MAJ-003. MAJ-004's "absent key survives Deny-fill" is also infeasible on the inspected create path. |
| 5 — Contract-first | No new defect. Round-1 conditional PATCH and activation state remain specified as generated types. |
| 6 — Security | MAJ-002 (fire runs as the last retry agent). MIN-001 (audit cannot name the principal). Aggregate bounds from round 1 stay. |
| 7 — Reachability | MAJ-004. Tool registration is named; the custom-Main seed would still deny the call. |
| 8 — UI states and journeys | MAJ-001. Saved-unavailable and stale-save from round 1 are specified. No new Schedules screen. |
| 9 — Accessibility and keyboard | No additional defect. §6.5 specifies keyboard operation, focus, and announcements. No rendered result is claimed. |
| 10 — Design-system reuse and brand | No additional defect. Catalogue map is named. No new token or emoji chrome. |
| 11 — Testability and false-green risk | MAJ-003, OBS-001, and the missing first-fire barrier under CRIT-001. |
| 12 — Overcomplexity | No additional defect. The visit counter in MAJ-003 is the extra machinery; the recommendation removes it rather than adding a layer. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Pre-claim fire failure | BDD-57 starts after a receipt exists. No scenario deletes the once `at` job and then releases capacity, or completes a dependency after a refused once fire. | CRIT-001; add beside BDD-18 and BDD-57 |
| Suspended Set consent | BDD-63 checks the authority notice, not "firing stays off." | MAJ-001 |
| Pre-upgrade initiator file | BDD-55 builds a fresh nil or agent initiator. It does not load `Direct: true` written by today's `consumeTaskAttempt`. | MAJ-002 |
| Work-unit oracle | DS-4 row 12 cannot fail a counter that agrees with itself. | MAJ-003 |
| Custom-Main seed value | T06/T07 cover the matrix. They do not say the constructor map contains explicit Ask rather than an omitted key. | MAJ-004 |
| Clear combined status | DS-1 row 14 names the contradiction and not the status enum. | MIN-002 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-5 | Dispatch error after `SpawnReset`, before `ClaimForRun`, once versus repeating | Assert task bytes restored or unchanged, job still present for once, no second run for repeating |
| DS-3 | Task file with `initiator.direct=true` from a failed explicit run, then a timed fire | Assert the founder-chosen principal, not a freshly created nil initiator |
| DS-4 row 12 | Input-derived unit total | Publish the arithmetic and the expected integer in the row |
| DS-3 | `NewCustomAgentToolsCfg` map after adding the tool | Assert Ask for a new Main and Deny after `RestrictForWorkerTier` |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `schedule_task` and REST trigger writes | ok | ok | risk | ok | risk | risk | Caller is authenticated. Stale trigger overwrite is specified. Audit cannot show the fire principal (MIN-001). One-time fire can be dropped with no user-visible fault (CRIT-001). Legacy `Initiator` can run the fire as the last retry agent (MAJ-002). |
| Approval card / reconnect preview | ok | ok | ok | ok | ok | risk | Frozen preview and reconnect are specified. Set omits the suspension consequence (MAJ-001). |
| Occurrence query | ok | ok | ok | ok | risk | ok | Series, item, and byte caps are specified. Work-unit accounting can refuse or allow the wrong calendars (MAJ-003). |
| Policy seed / Omnipus allow-list | ok | ok | ok | ok | ok | risk | Q2 YES (add to `omnipusSupportedTools`, no per-agent entry) matches `omnipusSeedPolicies`. Custom Main "omit" becomes Deny (MAJ-004). |
| Suspend fence / cron callback | ok | ok | ok | ok | risk | ok | Job id plus generation is the right fence for Suspend→Resume. It is not a recovery path for a once job deleted after a pre-claim error. |
| Task/goal publication | ok | ok | ok | ok | ok | ok | Goal-first publication and the goal→task lock order are specified. Not re-opened. |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | Specified, but the custom-Main instruction denies the new agent | §5.4 names `allStaticToolNames`, `buildKnownBuiltinToolNames`, `defaultToolPoliciesGeneral`, `ADR090RolePolicyInventory`, `omnipusSupportedTools`, `autoApproveClasses`, `AUTO_APPROVE_ASK_GROUPS`, `EXPLICIT_LABELS`. Those symbols exist on the inspected L1 tip. MAJ-004: omit plus `denyAllThenOverride` / `buildToolConfig` stores Deny. |
| User-facing: named screen/component renders it? | Yes, as a plan | `CalendarEventSlideOver` and `TaskDetailPanel` in §6.2. No new Schedules screen. |
| Test plan describes execution, not just authorship? | Yes | §9.4 RED/GREEN/CHECK, §11 reachability, §12 exact-commit UAT. Not yet executed. This review does not claim delivery. |

---

## Unasked Questions

1. The one-time pre-claim failure in CRIT-001 does not need a new product meaning: US-1 already says the fire happens. The open point is only which repair (restore the task and keep the job, versus a first-fire receipt the queue may run once). Pick one in the spec; do not leave both possible. Founder question 2 is only about whether that wait is visible.
2. Should a human Calendar edit that keeps an agent-created task's authority show the same continuing-authority sentence in the editor, or is the public doc enough? FR-036 requires it only on the agent approval card.

---

## Questions for the founder

These are decisions. They are not extra findings. Team-lead should interview on this list before the correction pass freezes the notice text.

1. **Who does a timed fire run as, on a task file today's retry has already rewritten?** Context: FQ-1 A said "original creator/assigner" and forbade a new stored fact. `consumeTaskAttempt` already replaces `Task.Initiator` with the failed run's agent, and marks `Direct`. A person-created task that Mia ran and that then failed is stored as Mia. The card text in AUTH-1 would call Mia the creator.
   - **A.** Keep the stored `Initiator` bytes, including `Direct`. Change the notice, the public docs, and the audit label from "original creator/assigner" to "stored execution authority," and show when that value is a retry carry-forward. No new field. Some existing schedules will keep running as the last retry agent.
   - **B.** Reopen FQ-1 A and store the creator/assigner separately from retry authority, so a `Direct` value stops being the fire principal. This adds a stored fact FQ-1 A forbade, and it needs a rule for files that no longer contain the original creator.
   - **Recommendation: A.** Do not invent history the file no longer has. Say what the file actually holds. B is the right product meaning only if the founder now wants a new field.

2. **When a one-time reminder cannot start because no worker slot is free, what should the person see?** Context: CRIT-001 must not delete the only job. The spec already forbids a synthetic "skipped" history row. The remaining choice is whether the wait is silent.
   - **A.** Silent retry. The same job stays. The task file is unchanged until a run is actually claimed. The calendar does not gain a missed-run record. Looking at the task still shows the future, or now past, schedule as armed.
   - **B.** Visible wait. Same retention rule as A, plus a warning on the task and in the calendar chip: the scheduled start has not begun and will retry. No skipped-run record.
   - **Recommendation: A.** US-1 says the fire happens. A warning is a new surface. The lost job is the defect; do not add a chip to fix it.

---

## Verdict Rationale

BLOCK. CRIT-001 is a lost one-time run under ordinary load: the spec's queue refusal removes the only recovery for a fire that `SpawnReset` already committed and the cron engine already deletes. MAJ-001 and MAJ-002 will ship a card and a notice that describe a different outcome from the one the file produces. MAJ-003 makes the new calendar cap untestable at the boundary the spec itself added. MAJ-004 denies scheduling for every new custom chat agent if the registration paragraph is implemented as written.

Round 1's goal-first publication, Clear-to-inbox for a plain Clear, conditional trigger hash, saved-but-unavailable `scheduler_state`, and series/item/byte caps are in the text and are not repeated here. The link counts match. None of that covers the first fire before claim, an `Initiator` value the retry path already replaced, or the L1 deny-fill.

### Escalation to the founder

This is the last grill round. The correction pass still has to happen. If CRIT-001 is still open after that correction, it escalates. Do not run a third grill.

| Finding ID | Why it would still be open | Founder decision needed |
|---|---|---|
| CRIT-001 | The correction must keep the one-time job and the pre-reset task file until claim succeeds, and must add the two barrier scenarios. A wording change that only cites BDD-57 does not close it. | If the correction cannot close it, choose founder question 2 and accept or reject a lost one-time reminder. Default recommendation remains: the reminder must still run. |

### Recommended Next Actions

- [ ] Rewrite the one-time pre-claim failure so the job and the task file survive until claim, and add the two barrier scenarios (CRIT-001).
- [ ] Put the "firing stays off" sentence on suspended Set previews and block approval without it (MAJ-001).
- [ ] Take founder question 1 before another correction pass freezes the notice text (MAJ-002).
- [ ] Replace visit-based work units with a published input formula and a frozen expected integer (MAJ-003).
- [ ] Set explicit Ask on the custom-Main constructor, then Deny only through `workerTierDeniedTools`; grant Ask in the Mia/Jim inventory (MAJ-004).
- [ ] Apply MIN-001–MIN-003 in the same correction.

### Next step in the process

Verdict: BLOCK

Review written to: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review-round2.md`

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-2 findings. Any CRITICAL finding still open after that fix is listed under "Escalation to the founder" above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

Address the findings with:

`/plan-spec --revise /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec-review-round2.md`

---

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Round-1 MAJ-001–007 and MIN-001–002 are present as forward rules in the corrected spec | Read of §1.3, §5.1, §5.1.1, §5.3, §5.3.1, §5.5, §5.6, §6.2, §6.5, BDD-55–BDD-63 | Verified |
| A once fire calls SpawnReset before claim, and pre-claim refusals return before ClaimForRun | Read `pkg/agent/task_trigger.go::RunScheduled`, `pkg/task/store.go::SpawnReset`, `pkg/agent/task_executor.go::executeTask` | Verified |
| The cron engine deletes an `at` job with DeleteAfterRun on the error path, before transient retry | Read `pkg/cron/service.go::scheduleNextRunUnsafe`; once mapping `pkg/agent/task_trigger.go::triggerToCronSchedule`; `DeleteAfterRun: spec.Schedule.Kind == "at"` in `pkg/cron/service.go` | Verified |
| Recovery sweep does not re-arm a once trigger | Read `pkg/agent/task_trigger.go::RunRecoverySweep` (continues unless recurring with a rule) | Verified |
| §6.4 Set row does not require a "firing stays off" sentence; §5.1 Set preserves suspension | Read §5.1 operation table and §6.4 action table | Verified |
| Today's retry overwrites Task.Initiator with the failed run, Direct set | Read `pkg/agent/task_run_loop.go::consumeTaskAttempt` and `retryAuthority`; test name `TestAutoInitiator_F5_RetryAuthoritySurvivesQueuePickup` | Verified |
| Work-unit cap is defined as positions visited and records examined, with no frozen fixture integer | Read §5.6 and DS-4 row 12 | Verified |
| Omitting a new tool from NewCustomAgentToolsCfg stores Deny, and REST create deny-fills a missing known name | `git show origin/work/omnipus-l1-ui-rooms-wip` of `pkg/coreagent/seed.go::denyAllThenOverride`, `NewCustomAgentToolsCfg`, `RestrictForWorkerTier`; `pkg/gateway/rest_agents_create.go::buildToolConfig` final missing-name loop. Tip `6d3aa4ce1`. Ancestor check of `ad853522d` exited 0. | Verified |
| Mia/Jim sparse omit requires inventory Ask; inventory default is Deny | Read `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory` and `adr090SparseRolePolicies` on the same L1 tip | Verified |
| Identifier counts are 36 FR, 63 BDD, 36 tests, all matrix-linked | Python count over the spec file: unique FR 36, BDD 63, tests 36, matrix missing none | Verified |
| **Self-check** | Re-read this review against grill-spec round-2 required sections (closure, twelve lenses, founder questions with options, escalation, verdict BLOCK because of CRIT-001). Re-checked that the spec file was not modified (`git status` shows only this review). No third-round review was written. | Verified |

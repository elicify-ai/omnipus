# Adversarial Review — Round 3: Calendar Recurrence Redesign (Recurring Tasks are Calendar Events)

**Spec reviewed**: docs/internal/specs/calendar-recurrence-redesign-spec.md (Revision 3)
**Review date**: 2026-07-19
**Review mode**: plan-spec (full structural checks)
**Round 1 review**: docs/internal/specs/calendar-recurrence-redesign-spec-review.md (BLOCK — 3 CRIT / 14 MAJ / 7 MIN / 4 OBS)
**Round 2 review**: docs/internal/specs/calendar-recurrence-redesign-spec-review-round2.md (REVISE — 0 CRIT / 8 MAJ / 9 MIN / 4 OBS)
**Verdict**: REVISE

## Executive Summary

Revision 3 resolves all 21 round-2 findings with real normative text, and — a first across three rounds — **every new codebase claim it makes verified accurate against source**: `newAPIRateLimiter(240, 1*time.Minute)` exists verbatim (`pkg/gateway/rest_auth.go:121,190`), the task routes are plain `withAuth` at exactly `pkg/gateway/rest.go:4695-4696`, `OnTaskUpserted`'s map-only locking and `RunScheduled`'s five exits are as cited, the `at`-branch-before-backoff and `rescheduleSkippedUnsafe` mechanics are as described, and the auth-gated `server_tz` is implementable (`withOptionalAuth` sets `UserContextKey`). However, three of the eight round-2 remedies carry new defects — the sweep's "armed" predicate cannot distinguish a dead entry from an **in-flight fire** (the engine clears `NextRunAtMS` before dispatch and exposes `State.Running`, which the predicate ignores), the iteration-budget prose contradicts its own dataset row 17 on whether a valid `FREQ=MINUTELY` rule truncates Month view, and FR-024's byte-identical rule collides with US-5.3's any-edit-converts rule for legacy triggers — and three fresh MAJORs were found: the occurrences endpoint's task-selection predicate is unspecified (terminal and heartbeat-surface tasks would render occurrences that will never fire), FR-013's "all `every_ms` values reverse-map" is unimplementable for wire-legal sub-minute and non-whole-minute intervals (the floor is 1000 ms, verified), and expansion/re-arm cost from an aged DTSTART is unbounded or permanently-truncating for dense rules.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 6 |
| OBSERVATION | 3 |
| **Total** | **15** |

---

## Round-2 Resolution Audit

Each round-2 finding was checked against the Revision 3 text; every codebase claim introduced by a fix was re-verified against source. "Residue" = the finding's core is fixed but the fix itself has a defect, tracked as a new round-3 finding.

| R2 Finding | Status | Evidence in R3 / Verification / Residue |
|-----------|--------|------------------------------------------|
| MAJ-201 (fictional limiter bucket) | **RESOLVED, claim verified** | New `taskReadLimiter = newAPIRateLimiter(240, 1*time.Minute)` on the occurrences endpoint only; CRUD stays unlimited. Verified: `newAPIRateLimiter(limit int, window time.Duration)` exists (`pkg/gateway/rest_auth.go:121`); `configLimiter` is `newAPIRateLimiter(240, 1*time.Minute)` (`:190`); `/api/v1/tasks` + `/api/v1/tasks/` registered with plain `a.withAuth(a.HandleTasks)` at `pkg/gateway/rest.go:4695-4696` exactly as cited. Test 13 carries the literal 240/min + 429 assertion. |
| MAJ-202 (bucket day-boundary tz) | **RESOLVED** | Required viewer `tz` query param as the day-boundary authority for all trigger flavors; dataset row 16 (Tokyo rule / Berlin query); test 8 updated; 400 on unloadable tz; FR-008 updated. |
| MAJ-203 (bucket-count work unbounded) | **RESOLVED, remedy self-contradictory** | 10,000/task/request budget counting bucket enumeration, enforced during iteration; arithmetic derivation MAY for provably regular triggers; dataset row 17; test 8. Residue: the prose ("regular rules never hit the caps in overview ranges") and row 17 ("`FREQ=MINUTELY`, 400-day span → `truncated: true`") assert opposite outcomes → MAJ-303. |
| MAJ-204 (fictional sweep mechanics) | **RESOLVED, remedy defective** | Dedicated 5-minute ticker in `TaskTriggerScheduler`; "armed" = job exists ∧ `NextRunAtMS` non-nil ∧ ≥ now; test 9 covers both orphan shapes. Residue: verified `RunDueJobs` clears `NextRunAtMS` before dispatch (`pkg/cron/service.go:515-519`) and marks `State.Running = true` during execution (`:613`) — an **in-flight fire is indistinguishable from a dead entry under the spec's predicate**, and re-arm registration is not replace-by-task → MAJ-301. |
| MAJ-205 (DST 03:00 vs 03:30) | **RESOLVED (one leftover)** | §5, Boundary Conditions, Edge Cases, and BDD DST row 3 all pin 03:30 gap-shift ("NOT 03:00"). Residue: occurrence-expansion dataset row 3 still reads "fires once at first valid post-gap instant" — the exact phrase MAJ-205 ordered replaced → MIN-301. |
| MAJ-206 (non-atomic generation guard) | **RESOLVED** | Rule 4 requires the re-arm's hash-check + registration + map-write and `OnTaskUpserted`'s whole remove→`AddJobFull`→map-write sequence under one mutex; test 10 forces the interleaving via a blocking hook. Verified the current-code claim (`s.mu` held only around map ops, `pkg/agent/task_trigger.go:206-208, 215-228`). Residue: lock ordering (`s.mu` → engine mutex) is achievable — `executeJobByID` releases `cs.mu` before invoking the runner (`pkg/cron/service.go:624`) — but the spec never states the ordering constraint → MIN-304. |
| MAJ-207 (edit-mode anchor semantics) | **RESOLVED, conflicts with US-5.3** | FR-024 (byte-identical on non-recurrence edits; re-anchor + COUNT restart on rule changes, disclosed), "Title-only edit" BDD scenario, editor dataset row 13, test 20. Residue: FR-024's unqualified MUST contradicts US-5.3/US-5.6/test 21 for **legacy** triggers → MAJ-304. |
| MAJ-208 (cross-zone save unverifiable) | **RESOLVED** | Test 21 now asserts the untouched-save payload carries `tz = server_tz` + same time-of-day AND the browser-zone flip after editing the time; matrix FR-021 row repointed at test 21. The phantom-citation class is closed. |
| MIN-201 (span units) | **RESOLVED** | 8×24 h boundary in ms; dataset row 18 (169 h fall-back week). |
| MIN-202 (missing exit paths) | **RESOLVED** | Rule 2 enumerates five exits (a)–(e); task-unreadable → ERROR + sweep as designated recovery; test 9 includes it. Verified against `pkg/agent/task_trigger.go:243-293` — accurate (the additional empty-payload and `dispatch == nil` corner exits are covered by the cleanup carve-out and the blanket readable-task rule respectively). |
| MIN-203 (§3d self-contradiction) | **RESOLVED** | Rewritten exactly as recommended (browser zone re-anchors; label flips at that moment). |
| MIN-204 (every_ms past ranges) | **RESOLVED** | Forward-only projection stated in the endpoint section + FR-008a; dataset row 19. |
| MIN-205 (type after conversion) | **RESOLVED** | Validation §1 + FR-013: conversion writes `type: recurring`; `rrule` keys rejected on any other type. |
| MIN-206 (route collision) | **RESOLVED, claim verified** | Routing note present; FR-008 + test 13 assert the literal sub-path wins over ID parsing. Prefix registration verified at `rest.go:4696`. |
| MIN-207 (wrong truncation counter) | **RESOLVED** | Dedicated client counter per `truncated: true` task-set + server-side debug log (Operations & Rollback). |
| MIN-208 (range inclusivity) | **RESOLVED** | Half-open `[from_ms, to_ms)`; client passes `activeStart`/`activeEnd` directly. Residual nit: FR-008 says "from ≥ to → 400" while the endpoint section says "`from_ms > to_ms` → 400" → MIN-303. |
| MIN-209 (DST-collision dedup) | **RESOLVED** | §5 dedup sentence (identical normalized instants collapse to one fire; makes scan-window misses harmless); dataset row 20. |
| OBS-201 (`server_tz` exposure) | **ADDRESSED, implementable** | Authenticated-callers-only on `/api/v1/state`. Verified `withOptionalAuth` sets `UserContextKey{}` on successful bearer resolution (`pkg/gateway/rest_auth.go:304-338`) — the handler can distinguish. |
| OBS-202 (`first_ms` no consumer) | **ADDRESSED** | Consumer = aggregated-chip tooltip ("first at {time}"), FR-009 + shape comment. |
| OBS-203 (cross-split dependency chains) | **ADDRESSED** | Edge-case bullet: blocker names resolve, rollups ignore trigger type. |
| OBS-204 (defaults only in Givens) | **ADDRESSED** | New scenario "Event slide-over opens with asserted defaults" with real Then clauses; test 20 asserts the default state. |

Other Revision 3 citations spot-verified accurate: `CreateTaskSlideOver.tsx` trigger section 448–504 with the raw cron `<Input>` at ~492–500; `TaskDetailPanel.tsx` recurring section ~690–740; `BoardView.tsx:134` and `ListView.tsx:33` surface exclusions; `CalendarEventExtProps` kinds at `src/components/calendar/types.ts:69-72`; `eventMapping.ts` F-10 skip (lines 110, 145); `CalendarToolbar.tsx` has no filters; `ScheduleFormSheet.tsx`/`ScheduleTrigger.yaml`/`cronUtils.ts` exist; `TaskTrigger.yaml` `config` is `additionalProperties: true` (outer object closed — new keys land under `config`, as the spec plans); `isTaskValidationErr` exists; cron expansion runs in the server's local zone (`computeNextRun` → `time.UnixMilli` → `gronx.NextTickAfter`, `pkg/cron/service.go:848-855`); no `rrule` dependency exists yet in `go.mod`/`package.json`.

---

## Findings (Round 3)

### MAJOR Findings

#### [MAJ-301] The sweep's "armed" predicate cannot distinguish a dead entry from an in-flight fire — a sweep tick during any fire double-arms the series, and duplicates self-perpetuate

- **Lens**: Incorrectness / Inoperability (availability)
- **Affected section**: Scheduler rule 6 ("armed" predicate); rule 2 ("compute and register the next occurrence's job"); rule 4 (critical-section contents); FR-014; test 9.
- **Description**: Verified engine mechanics: `RunDueJobs` clears `NextRunAtMS` for every due job **before** dispatch (`pkg/cron/service.go:515-519`), and `executeJobByID` sets `State.Running = true` for the duration of the run (`:613`, reset at `:770-777`). So during every in-flight RRULE fire — from tick until the handler's own exit-path re-arm — the task's tracked job **exists with `NextRunAtMS = nil`**: exactly the shape the sweep predicate ("job exists ∧ `NextRunAtMS` non-nil ∧ ≥ now") classifies as a dead orphan. A 5-minute sweep tick landing in that window re-arms the next occurrence; the fire's exit-path re-arm then also registers (the generation hash matches — the trigger didn't change, so rule 4's guard does not block it). Because `AddJobFull` always creates a new job entry (`:1063-1074`) and neither rule 2 nor rule 4 requires removing the previously tracked job during re-arm (only `OnTaskUpserted` gets remove-then-register), the post-state is **two armed at-jobs for the same next occurrence**. Both fire at that instant; one wins `SpawnReset`, the other overlap-skips — but **each** exit re-arms again, so the duplicate pair propagates to every subsequent occurrence for the life of the series. The mutex requirement of rule 4 serializes the two registrations but does not deduplicate them. Notably, the engine exposes the exact distinguishing signal the predicate needs — `State.Running` — and the spec's predicate ignores it.
- **Impact**: A probabilistic race (fire duration × 5-min sweep cadence, accumulating over months for frequent rules) converts one recurring task into a permanently duplicated series — double dispatches, double agent runs, double side effects — with nothing in logs but a single innocuous sweep WARN at the moment of infection.
- **Recommendation**: Two changes to rule 6/rule 2: (1) the armed predicate MUST treat a job with `State.Running == true` (fire in flight, exit-path re-arm pending) as armed — i.e. "armed = job exists ∧ (`NextRunAtMS` non-nil ∧ ≥ now ∨ `State.Running`)"; (2) every re-arm registration (exit-path AND sweep) MUST be replace-by-task — remove the task's tracked job (if any) before `AddJobFull`, inside the same rule-4 critical section — so any residual double-arm heals idempotently instead of persisting. Extend test 9 with the in-flight case: sweep tick while a fire is mid-handler (blocking hook) → no second job registered; post-fire exactly one armed job.

#### [MAJ-302] The occurrences endpoint's task-selection predicate is unspecified — terminal and heartbeat-surface tasks would render occurrences that will never fire

- **Lens**: Incompleteness / Incorrectness
- **Affected section**: Occurrence expansion endpoint ("Expands **all** recurring-capable triggers in the workspace"); FR-008; Explicit Non-Behaviors (heartbeats "stay cron-driven and hidden as today"); Assumptions (heartbeat exclusion "already excluded via `surface !== 'user'`").
- **Description**: The scheduler never arms terminal tasks and removes their jobs (`OnTaskUpserted` skips/removes on `task.IsTerminal`, `pkg/agent/task_trigger.go:163-169`) and never registers heartbeat-surface tasks (`:158-161`). The endpoint, however, is specified to expand "**all** recurring-capable triggers in the workspace" — a stateless expansion from trigger config with no status or surface filter anywhere in the endpoint section or FR-008. Consequences: (1) a `failed`/`done` recurring task renders future occurrences the scheduler will never fire — precisely the "chips lie" failure class D2/US-2 exist to prevent; (2) workspace heartbeat tasks (`surface: heartbeat`, `recurring` cron — the spec's own HIGH-risk callout confirms they ride this trigger type) get expanded and shipped to the client, where their rendering depends on an **unstated** join behavior — the flow table says occurrences are "joined by task_id" against the fetched task list, but never says what happens to occurrence sets whose task_id has no match. The existing `surface !== 'user'` exclusions the Assumptions bullet leans on are Board/List code (`BoardView.tsx:134`, `ListView.tsx:33`), not calendar code.
- **Impact**: Dead tasks and hidden infrastructure tasks appear as future scheduled work on the calendar (or don't, depending on which join behavior an implementer happens to pick) — an operator-visible correctness bug and a heartbeat-visibility regression against an explicit Non-Behavior.
- **Recommendation**: One sentence in the endpoint section + FR-008: the endpoint expands only tasks that are non-terminal AND not `surface: heartbeat` — the same predicate `OnTaskUpserted` applies before registering a job (cite `task_trigger.go:158-169`) — and the client drops occurrence sets whose `task_id` is absent from its task list (defense-in-depth). Add dataset rows: terminal recurring task → omitted; heartbeat-surface recurring task → omitted. Assert both in test 13.

#### [MAJ-303] The iteration-budget design contradicts its own dataset: prose says regular rules "never hit the caps in overview ranges", row 17 asserts `FREQ=MINUTELY` truncates — and which is true depends on a MAY

- **Lens**: Inconsistency
- **Affected section**: Occurrence expansion endpoint (caps paragraph); FR-008 ("arithmetic count derivation **permitted**"); dataset row 17; test 8.
- **Description**: Arithmetic count derivation for provably regular triggers is optional ("MAY be derived arithmetically"). If an implementer exercises the MAY, a plain `FREQ=MINUTELY` rule (no BY* modifiers — provably regular) never iterates, so dataset row 17's expected `truncated: true` over a 400-day span **fails**. If the implementer iterates instead, the budget (10,000) exhausts around day 7 of a 42-day Month fetch (1,440/day), so Month view for a perfectly valid every-minute rule is truncated-with-marker — making the prose claim "with bucketing, regular rules never hit the caps in overview ranges — truncation is reachable mainly by dense legacy `every_ms` data in ≤ 8-day ranges" false, and quietly degrading D2's "renders every occurrence" for dense rules. The spec asserts both outcomes; test 8 is told to enforce the budget "during iteration" while row 17 pins a response shape that only the no-arithmetic implementation produces.
- **Impact**: The single test that pins the caps design either fails against a correct implementation or freezes the degraded one; two implementers produce observably different Month views for the same valid rule, each citing the spec.
- **Recommendation**: Make arithmetic derivation **MUST** for provably regular triggers (`every_ms`; fixed-interval rrule with no BY* modifiers) and rewrite row 17 to the matching expectation (buckets with arithmetically-derived counts, `truncated: false`, bounded server time); add a separate row for an *irregular* dense rule (e.g. `FREQ=MINUTELY;BYHOUR=8,9,…` shaped to exceed 10,000 over the span) that legitimately asserts `truncated: true`. Delete or qualify the "never hit the caps" sentence to match.

#### [MAJ-304] FR-024's byte-identical rule contradicts US-5.3's any-edit-converts rule for legacy triggers

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: FR-024 ("Save MUST preserve the trigger **byte-identical** when no recurrence or time field was touched" — unqualified); US-5 AC-3 ("**Given** any edit is saved from the new editor, **Then** the stored trigger becomes RRULE-based (one-way conversion)"); US-5.6 + "Cross-zone save without a time change…" scenario; test 21; FR-013.
- **Description**: For a **legacy** (`cron_expr`/`every_ms`) task, a title-only save from the calendar editor cannot satisfy both normative statements: US-5.3 says any save converts the trigger to RRULE (and test 21's "untouched save" case asserts the converted payload carries `tz = server_tz` — i.e. conversion happens on a save that touched nothing recurrence-related), while FR-024 says an edit touching no recurrence/time field leaves the trigger byte-identical. An implementer following FR-024 literally skips conversion on title-only legacy edits (test 21 then fails); one following US-5.3 converts (FR-024's MUST is then violated for the legacy class). The intended split — FR-024 governs already-RRULE triggers; legacy triggers convert on any save — is guessable but never stated, and FR-024 is the newer, more specific MUST.
- **Impact**: The feature's crown-jewel tz-conversion path (MAJ-002/MAJ-208 lineage) and its verifying test rest on which of two contradictory MUSTs the implementer picks; a title-only edit of a legacy task either silently converts a trigger the operator didn't touch, or the conversion flow US-5.6 specifies never fires for the most common edit.
- **Recommendation**: Qualify FR-024: "…byte-identical when no recurrence or time field was touched **and the stored trigger is already RRULE-based**; a save of a legacy (`cron_expr`/`every_ms`) trigger always converts one-way per US-5.3/Timezone §3–4 regardless of which fields changed, preserving fire instants when the time was untouched (US-5.6)." Add the legacy-title-only case to test 21 (payload = converted trigger, `tz = server_tz`, same time-of-day).

#### [MAJ-305] FR-013's "all `every_ms` values" reverse-map is unimplementable for wire-legal values — sub-minute intervals have no legal RRULE equivalent at all

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: FR-013 ("reverse-map … **all** `every_ms` values (interval + minutes/hours unit)"); US-5.5; Timezone Semantics §4 ("conversion on save produces `FREQ=MINUTELY/HOURLY;INTERVAL=n`"); "Interval task reverse-maps" scenario; test 16.
- **Description**: Verified: the `every` floor is **1000 ms**, not 60 s — `ValidateTrigger` rejects only `every_ms < 1000` (`pkg/task/store.go:342-344`) and `TaskTrigger.yaml` documents `minimum: 1000`. Legal stored values therefore include sub-minute intervals (1000–59999 ms) and non-whole-minute intervals (e.g. 90000 ms = 90 s). Neither maps to "interval + minutes/hours unit": 90 s is not an integer number of minutes, and a sub-minute interval's RRULE target **does not exist** — `FREQ=SECONDLY` is hard-rejected by Validation §2, `FREQ=MINUTELY` cannot express sub-minute cadence, and the 60 s min-gap floor forbids it anyway. FR-013 mandates the reverse-map for all values, and the read-only + "Replace" fallback (US-5.2) is defined **only for unmappable `cron_expr`** — there is no specified editor behavior for an unmappable interval task. (The UI has only ever produced whole minutes, but the spec itself declares legacy "an open class" — raw API and agent `create_task` payloads are first-class citizens of this back-compat story.)
- **Impact**: Opening a legal `every_ms=90000` task per FR-013 forces the editor into a rounding lie (silently rewriting the cadence to 1 or 2 minutes on save) or an unrepresentable state; a sub-minute task that fires legally today cannot be expressed post-conversion at all, and the spec offers no path for it.
- **Recommendation**: Scope FR-013's mappable set: `every_ms` reverse-maps iff it is a whole-minute multiple (≥ 60000, divisible by 60000; hours unit when divisible by 3600000). All other values take the US-5.2 read-only + "Replace with a new repeat rule" path, generalized to interval triggers (display "every 90 s" + next fire from `NextRunAtMS`). State explicitly that sub-minute intervals are conversion-terminal: they keep firing under legacy invariance but any replacement rule is subject to the 60 s floor. Add dataset rows to test 16: `every_ms=90000` → fallback; `every_ms=30000` → fallback.

#### [MAJ-306] Expansion and re-arm cost from an aged DTSTART is unbounded — or permanently truncates — and grows with every day a dense rule stays alive

- **Lens**: Insecurity (DoS) / Incompleteness
- **Affected section**: Occurrence expansion endpoint (budget paragraph); FR-008; Scheduler rule 1 ("arms as … the next occurrence after `now`"); Integration Boundaries (rrule-go `Between`/`After`).
- **Description**: rrule-go iterates occurrences sequentially from DTSTART (dateutil-style); reaching a queried range — or "the next occurrence after now" at re-arm time — requires walking every occurrence in between unless the implementation fast-forwards. The spec bounds *in-range* work (10,000 budget) but never addresses the **skip-to-range** work: for a `FREQ=MINUTELY` rule created via the editor in 2026 and queried in 2028, that is >1,000,000 iterations per task per request. If the budget counts skip work, every such request exhausts it before reaching the range → the calendar is permanently empty-with-truncation-marker for a rule that renders fine today (and dataset row 17's semantics silently change over time); if it doesn't count it, the MAJ-007/MAJ-203 unbounded-CPU hole reopens on both the read path and — worse — the **fire path**: every re-arm of a dense aged rule re-walks from DTSTART, so a minutely task burns a growing megabyte-scale iteration every minute, forever. COUNT compounds this: COUNT exhaustion is only decidable by counting from DTSTART, and COUNT is unbounded at validation (`COUNT=10000000` is accepted).
- **Impact**: Steady-state CPU burn that worsens with data age on both the occurrences endpoint and the scheduler's hot re-arm path — a self-inflicted, slow-motion DoS requiring no attacker, plus a wide-open crafted-DTSTART amplification for any authenticated caller.
- **Recommendation**: (1) Require (MUST) O(1) arithmetic fast-forward to the range/`now` for provably regular rules (aligns with MAJ-303's fix); (2) for BY*-modified rules without COUNT, require iteration to begin at the FREQ period containing the target instant, not at DTSTART (rrule semantics permit this — occurrence membership is position-independent when COUNT is absent); (3) bound COUNT at validation (e.g. ≤ 100,000, rejected with 400 above it) so COUNT-exhaustion checks are bounded; (4) state which side of the budget skip-work falls on for the residual COUNT-bearing irregular case. Add a dataset row: dense rule, DTSTART 2 years before the queried range → bounded server time, correct in-range results.

---

### MINOR Findings

#### [MIN-301] Dataset row 3 still says "first valid post-gap instant" — the exact phrase MAJ-205 ordered replaced

- **Lens**: Inconsistency
- **Affected section**: Occurrence-expansion dataset row 3 ("daily 02:30 Berlin, spring-forward day → fires once at first valid post-gap instant").
- **Description**: Every other site (Timezone §5, Boundary Conditions, Edge Cases, BDD DST row 3) is pinned to the 03:30 gap-shift, and the BDD row even says "NOT 03:00" — but the dataset row test 4 draws from retains the old 03:00-reading phrase.
- **Recommendation**: Rewrite row 3's expectation: "fires once at the normalized 03:30 (02:30 + gap length)".

#### [MIN-302] After §2's hard bounds, the bounded-window min-gap scan has no reachable rejection — and the adversarial rows credit it with rejections §2 already makes

- **Lens**: Overcomplexity / Incorrectness
- **Affected section**: Validation §4; adversarial scenario rows 1–2; dataset rows 7–8; test 2.
- **Description**: §2 rejects `FREQ=SECONDLY` and any `BYSECOND` not equal to the DTSTART second. Every surviving rule's occurrences therefore share one seconds value → consecutive occurrences are whole minutes apart (≥ 60 s), or 0 s on a DST collision — which §5 de-duplicates. So no rule that reaches §4 can fail it, yet dataset row 7's note says "bounded scan catches the 30 s daily pair" — that input (`BYSECOND=0,30`, dtstart :15) is rejected by §2's foreign-BYSECOND rule before any scan runs, and row 8 likewise. The scan is defensible as defense-in-depth against library quirks, but the spec presents it as the operative mechanism and tests it via inputs it never sees.
- **Recommendation**: Either (a) keep the scan explicitly labeled defense-in-depth (one sentence) and fix rows 7–8's rationale to name §2 as the rejecting rule, or (b) drop §4 and its test rows. If a genuinely scan-only rejection exists (none was found in this review), add it as a dataset row instead.

#### [MIN-303] FR-008 says "from ≥ to → 400"; the endpoint section says "`from_ms > to_ms` → 400" — `from == to` is undefined

- **Lens**: Ambiguity
- **Affected section**: FR-008 vs Occurrence expansion endpoint bullets.
- **Description**: An empty half-open range `[t, t)` is either a 400 (FR-008) or a valid empty response (endpoint section) depending on which sentence the implementer reads.
- **Recommendation**: Pick one (suggest: `from_ms ≥ to_ms` → 400, matching FR-008) and align both sentences plus dataset row 12.

#### [MIN-304] The mandated mutex nesting (`s.mu` → engine mutex) is safe today but the lock-ordering constraint is unstated

- **Lens**: Incompleteness
- **Affected section**: Scheduler rule 4; FR-014.
- **Description**: Rule 4 requires holding the scheduler mutex across `AddJobFull` (which takes the engine's `cs.mu`). This is deadlock-free only while the engine never invokes scheduler code while holding `cs.mu` — true today (`executeJobByID` snapshots and unlocks at `pkg/cron/service.go:624` before calling the runner; `onSkip` fires after unlock at `:592-597`), but nothing in the spec pins that invariant, and a future engine callback added under `cs.mu` would deadlock the fire path against every task upsert.
- **Recommendation**: One sentence in rule 4: lock order is scheduler-mutex → engine-mutex; the engine MUST NOT hold `cs.mu` when invoking `RunScheduled` or any scheduler callback (current behavior, `service.go:624` — regression-guarded by test 10's interleaving hook).

#### [MIN-305] Dataset row 9's note still says "≤7-day span" against the 8×24 h boundary

- **Lens**: Inconsistency
- **Affected section**: Occurrence-expansion dataset row 9 ("`every_ms=1800000`, 1-day range … ≤7-day span").
- **Description**: MIN-201's fix moved the detail/overview boundary to 8×24 h everywhere else; this note (and only this note, outside the historical Clarifications record) still says 7.
- **Recommendation**: Change the note to "≤ 8-day span (detail mode)".

#### [MIN-306] RRULE→RRULE recurrence changes emit no audit entry — only legacy conversions do

- **Lens**: Insecurity (Repudiation) / Inoperability
- **Affected section**: FR-022 (audit scoped to "one-way legacy→RRULE conversion"); FR-024 (re-anchor disclosure is UI-only, pre-save).
- **Description**: Editing an existing RRULE series' rule re-anchors DTSTART and restarts COUNT — rescheduling every future fire — with no persistent record: FR-022's audit covers only the legacy conversion moment, and FR-024's disclosure is a transient panel summary. After the fact, "why did the Monday run move?" has no answer in the audit log, though the equivalent legacy edit would have one.
- **Recommendation**: Extend FR-022 to any save that changes the recurrence trigger (old trigger → new trigger, same entry shape); assert it alongside test 12's existing audit assertion.

---

### Observations

#### [OBS-301] Go does not document nonexistent-local-time normalization as guaranteed

- **Lens**: Incorrectness (grounding)
- **Affected section**: Timezone Semantics §5.
- **Suggestion**: Go's `time.Date` docs say the result for times in a transition gap "is not guaranteed" — in practice it gap-shifts (03:30), and the spec's "the policy is normative, the library is not" hedge already covers a divergence. Record in the RRULE ADR that the expansion layer owns the 03:30 policy even against a future Go/stdlib behavior change, so the normalization layer is built (and tested, test 4) rather than assumed.

#### [OBS-302] The occurrences query cache key should include the `tz` parameter

- **Lens**: Incompleteness (client detail)
- **Affected section**: `CalendarScreen.tsx` ("occurrences query keyed to the visible range").
- **Suggestion**: With `tz` now a semantic input (bucket boundaries), a query keyed only to the range serves stale-zone buckets if the browser zone ever changes mid-session (travel + laptop resume). One line: key = workspace + range + tz.

#### [OBS-303] `taskReadLimiter` is per-IP — behind a reverse proxy without `gateway.trust_xff`, all callers share one 240/min bucket

- **Lens**: Insecurity (DoS, mild)
- **Affected section**: Rate limiting (concrete) paragraph.
- **Suggestion**: Same posture as every existing `apiRateLimiter` bucket (verified `clientIP` honors XFF only under `trust_xff`), so this is inherited, not new — but the calendar polls per navigation, making this the bucket most likely to be *legitimately* hit through a proxy. One sentence noting the shared-bucket behavior and pointing at `gateway.trust_xff` (and `docs/operations/reverse-proxy.md`) would save the future 429 investigation.

---

## Structural Integrity

### Plan-Spec Format checks (Revision 3)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1: 6, US-2: 6, US-3: 5, US-4: 4, US-5: 6 |
| Every acceptance scenario has BDD scenarios | PASS | OBS-204 closed — slide-over defaults now asserted in Then clauses of a dedicated scenario |
| Every BDD scenario has `Traces to:` reference | PASS | All 29 scenarios carry back-references |
| Every BDD scenario has a test in TDD plan | PASS | The R2 FAIL (cross-zone save) is repaired — test 21 is capable of observing the client-side stamping; all scenarios map to real rows in the 24-test plan |
| Every FR appears in traceability matrix | PASS | FR-001…FR-024 incl. FR-006a/FR-008a, verified row-by-row |
| Every BDD scenario in traceability matrix | PASS | Completeness note accounts for the two scenarios folded under FR-001/FR-019/FR-024 |
| Test datasets cover boundaries/edges/errors | PASS (gaps noted) | R2's five dataset gaps all closed (rows 16–20). New gaps: endpoint selection predicate (MAJ-302), in-flight-sweep case (MAJ-301), regular-vs-irregular budget rows (MAJ-303), unmappable `every_ms` rows (MAJ-305), aged-DTSTART row (MAJ-306), legacy-title-only-edit row (MAJ-304) |
| Regression impact addressed | PASS | HIGH-risk scheduler callout + byte-identical legacy translation test retained; board/list regression rows intact |
| Success criteria are measurable | PASS | SC-001–SC-009 all verifiable |

---

## Test Coverage Assessment

Round 2's seven gap rows are all closed by Revision 3 (cross-zone payload → test 21; sweep dead-entry → test 9; race determinism → test 10 hook; edit-anchor invariance → test 20 + editor row 13; expansion budget → test 8 + row 17; cross-tz bucketing → row 16; `server_tz` fallback → test 16). Gaps found this round:

| Category | Gap Description | Finding |
|----------|----------------|---------|
| Sweep vs in-flight fire | Test 9 covers "no job" and "job with nil `NextRunAtMS`" orphans, but not the false-orphan (fire in flight, `Running=true`) that must NOT be re-armed | MAJ-301 |
| Endpoint selection predicate | No test/dataset row for terminal-status or heartbeat-surface recurring tasks being omitted from the occurrences response | MAJ-302 |
| Budget: regular vs irregular | Row 17's expectation is only satisfiable by the implementation the prose forbids; no row exercises an irregular dense rule against the budget | MAJ-303 |
| Legacy title-only edit | No row pins whether a title-only save of a *legacy* task converts (test 20's title-only case is an RRULE series) | MAJ-304 |
| Unmappable `every_ms` | Test 16 has no rows for 90 s / 30 s intervals (reverse-map vs fallback undefined) | MAJ-305 |
| Aged DTSTART | No row bounds server time for a dense rule whose DTSTART long predates the queried range (endpoint AND re-arm path) | MAJ-306 |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `GET /tasks/occurrences` | ok | ok | ok | ok | risk | ok | Limiter now real and concrete (240/min, verified constructor); residual D: aged-DTSTART skip work unbounded or truncating (MAJ-306); budget semantics self-contradictory (MAJ-303) |
| `ValidateTrigger` RRULE path | ok | ok | ok | ok | ok | ok | Bounds sound; §4 scan is dead code after §2 (MIN-302 — availability-neutral); COUNT unbounded feeds MAJ-306 |
| `TaskTriggerScheduler` re-arm + sweep | ok | ok | ok | ok | risk | ok | Availability/correctness: sweep false-orphan double-arm self-perpetuates (MAJ-301); guard atomicity itself now sound (MAJ-206 closed) |
| Legacy conversion (US-5.3) | ok | ok | risk | ok | ok | ok | Conversion audited (FR-022) and now test-verified (test 21); RRULE→RRULE rewrites unaudited (MIN-306); FR-024 conflict decides whether conversion even fires (MAJ-304) |
| `server_tz` on `/api/v1/state` | ok | ok | ok | ok | ok | ok | Auth-gated per OBS-201; verified implementable (`UserContextKey` set by `withOptionalAuth`) |
| RecurrenceEditor (client) | ok | ok | ok | ok | ok | ok | Server remains validation authority; unmappable-interval hole is a UX/correctness gap (MAJ-305), not a trust-boundary one |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Which tasks does the occurrences endpoint expand — does it mirror `OnTaskUpserted`'s non-terminal, non-heartbeat predicate, and what does the client do with occurrence sets whose `task_id` it can't join? (MAJ-302)
2. What does the sweep do when its tick lands during an in-flight fire — a job entry with `NextRunAtMS` nil and `State.Running` true? (MAJ-301)
3. Is a re-arm registration replace-by-task or add-new — and what heals the state if two re-arms for the same rule both register? (MAJ-301)
4. Does a title-only edit of a **legacy** task convert the trigger (US-5.3, test 21) or preserve it byte-identical (FR-024)? (MAJ-304)
5. What does the editor show for `every_ms=90000`, and what RRULE could `every_ms=30000` possibly convert to given `FREQ=SECONDLY` is banned and the floor is 60 s? (MAJ-305)
6. Does a valid plain `FREQ=MINUTELY` rule truncate Month view or not? Row 17 and the caps prose currently answer differently. (MAJ-303)
7. What bounds the DTSTART→range walk for a two-year-old minutely rule — per occurrences request, and per re-arm on the fire path? Is COUNT bounded at validation? (MAJ-306)
8. Is `from_ms == to_ms` a 400 or an empty 200? (MIN-303)
9. Can any rule that survives Validation §2 actually trip the §4 min-gap scan — and if not, is the scan defense-in-depth or dead weight? (MIN-302)
10. Where is the record, after the fact, that an RRULE series' rule was changed and every future fire moved? (MIN-306)

---

## Verdict Rationale

REVISE, not BLOCK: the spec's architecture is sound and its codebase grounding is now excellent — uniquely in this review series, **every** new source claim Revision 3 makes was verified accurate, including the exact limiter constructor and route registrations its round-2 predecessors got wrong. But the pattern round 2 identified — defects inside the remediation text — recurs at reduced intensity: the sweep predicate (MAJ-204's fix) re-opens a duplicate-arming race the engine's own `State.Running` flag exists to prevent (MAJ-301); the budget design (MAJ-203's fix) asserts contradictory truncation outcomes hinged on an optional optimization (MAJ-303); and FR-024 (MAJ-207's fix) collides head-on with US-5.3 for the legacy class (MAJ-304). Three fresh MAJORs (endpoint selection predicate, `every_ms` mappability against the verified 1000 ms floor, aged-DTSTART cost) are consequential but each resolvable with a few normative sentences, dataset rows, and test amendments. Nothing requires re-opening the operator's D1–D7 decisions; nothing is structurally unimplementable.

### Recommended Next Actions

- [ ] Rule 6/2: treat `State.Running` as armed; make every re-arm replace-by-task inside the rule-4 critical section; extend test 9 with the in-flight false-orphan case — MAJ-301
- [ ] Endpoint + FR-008: expand only non-terminal, non-heartbeat tasks (mirror `task_trigger.go:158-169`); client drops unjoinable task_ids; two dataset rows + test 13 assertions — MAJ-302
- [ ] Make arithmetic derivation MUST for regular triggers; rewrite dataset row 17; add an irregular-dense truncation row; fix the "never hit the caps" sentence — MAJ-303
- [ ] Qualify FR-024 to already-RRULE triggers; legacy saves always convert; add the legacy-title-only case to test 21 — MAJ-304
- [ ] Scope FR-013's mappable `every_ms` to whole-minute multiples; generalize the US-5.2 fallback to interval triggers; two test-16 rows — MAJ-305
- [ ] Mandate fast-forward for regular rules and period-anchored iteration for COUNT-free BY* rules; bound COUNT at validation; aged-DTSTART dataset row — MAJ-306
- [ ] Sweep the MINOR batch (dataset row 3 wording, scan rationale/defense-in-depth label, `from==to`, lock-ordering sentence, row 9 note, RRULE-edit audit) — one to two sentences each

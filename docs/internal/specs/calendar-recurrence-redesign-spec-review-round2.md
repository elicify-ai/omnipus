# Adversarial Review — Round 2: Calendar Recurrence Redesign (Recurring Tasks are Calendar Events)

**Spec reviewed**: docs/internal/specs/calendar-recurrence-redesign-spec.md (Revision 2)
**Review date**: 2026-07-19
**Review mode**: plan-spec (full structural checks)
**Round 1 review**: docs/internal/specs/calendar-recurrence-redesign-spec-review.md (verdict BLOCK — 3 CRIT / 14 MAJ / 7 MIN / 4 OBS)
**Verdict**: REVISE

## Executive Summary

Revision 2 is a genuine revision, not a cosmetic one: 24 of 28 round-1 findings are substantively resolved with normative text, scenarios, dataset rows, and real test-plan rows (the phantom-test citations of MAJ-013 are repaired; the timezone semantics, bucketed response, and re-arm rules are now written against the verified engine). However, re-verification against the actual source found that several of the *remedies themselves* contain defects: the spec normatively assigns the new endpoint to a **"task-read limiter bucket" that does not exist anywhere in the gateway** (`/api/v1/tasks` is registered with plain `withAuth`, no limiter), the recovery sweep "piggybacks the existing scheduler maintenance cadence" — **no such cadence exists** (`TaskTriggerScheduler` has no periodic loop; `Reconcile` is boot-only), the new DST policy **contradicts itself** on the spring-forward instant (03:00 vs 03:30), and the edit-during-fire generation guard is specified as a non-atomic compare-then-register that re-opens the race it exists to close. No finding rises to CRITICAL; eight are MAJOR.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 8 |
| MINOR | 9 |
| OBSERVATION | 4 |
| **Total** | **21** |

---

## Round-1 Resolution Audit

Each round-1 finding was checked against the Revision 2 text (and, where the fix makes a codebase claim, against source). "Residue" = the finding's core is fixed but the fix itself has a defect, tracked as a new round-2 finding.

| R1 Finding | Status | Evidence in R2 / Residue |
|-----------|--------|--------------------------|
| CRIT-001 (first-pair min-gap unsound) | **RESOLVED** | Validation §2/§4/§5: bounded-window scan (60 occ / 366 d), hard rejects for `FREQ=SECONDLY` + foreign `BYSECOND`, 5-year liveness; adversarial dataset rows 7–11; tests 2–3; SC-008. Residue: the "whichever is smaller" window can be as short as 60 days and miss DST-collision pairs → MIN-209. |
| CRIT-002 (cap vs D6 contradiction) | **RESOLVED** | Bucketed `TaskOccurrenceSet` (`DayBucket`), >3/day threshold in >7-day spans, raw instants in ≤7-day spans, iteration-time caps, visible truncation marker (FR-008/009), BDD + dataset rows 8–11 rewritten to the bucketed shape. Residues: bucket day-boundary timezone → MAJ-202; bucket-counting work unbounded → MAJ-203; span measurement undefined → MIN-201. |
| CRIT-003 (re-arm vs DeleteAfterRun/retry) | **RESOLVED (design), remedy defective** | Scheduler rules 2–6: re-arm on every exit path, backoff opt-out (verified consistent with `scheduleNextRunUnsafe` — the `at` branch runs before the transient check, so at-jobs never enter backoff), boot `Reconcile` + sweep, test 9. Residues: the sweep's mechanics are fictional/undefined → MAJ-204; the task-unreadable exit path cannot re-arm as specified → MIN-202. |
| MAJ-001 (`every_ms` has no anchor) | **RESOLVED** | FR-008a projection (first = live `State.NextRunAtMS`, verified the field exists and is readable), engine-agreement assertion in test 7. Residue: past-range behavior unstated → MIN-204. |
| MAJ-002 (legacy-cron tz decisions) | **RESOLVED (text)** | Timezone Semantics §2–§3, `server_tz` on state (feasible — `/api/v1/state`/`AppState.yaml` verified), US-5.6 + "Cross-zone save…" scenario, FR-021. Residue: the scenario's only cited test cannot verify it → MAJ-208. |
| MAJ-003 (non-recurring chip routing) | **RESOLVED** | FR-001 routing sentence, US-2.6, "Clicking a due chip keeps today's panel" scenario, regression rows in test 17. |
| MAJ-004 (`every_ms` edit path) | **RESOLVED** | US-5.5, FR-013 (reverse-map + convert), FR-011 ("`every` remains valid on the wire, no longer produced by any form"), "Interval task reverse-maps" scenario, test 16. Residue: stored `type` after conversion unstated → MIN-205. |
| MAJ-005 (DST nonexistent/ambiguous) | **PARTIAL** | Policy added (§5), 02:30 rows, test 4 — but the policy contradicts itself on the spring-forward instant (03:00 vs 03:30) → MAJ-205. |
| MAJ-006 (source of `tz`) | **RESOLVED** | §1 browser-zone default displayed read-only, US-1 AC-1, FR-021. Residue: §3(d) wording self-contradiction → MIN-203. |
| MAJ-007 (pathological RRULE DoS) | **PARTIAL** | Length/FREQ/BYSECOND bounds, liveness bound, ~1 s validation bound, iteration-time caps all present. Residues: the named rate-limit bucket is fictional → MAJ-201; bucket counting re-opens unbounded expansion work → MAJ-203. |
| MAJ-008 (aggregated-chip label) | **RESOLVED** | `interval_ms`/null + "{count}×/day" rule (endpoint section, FR-009), dataset row 11, test 17. |
| MAJ-009 ("Does not repeat" semantics) | **RESOLVED** | US-1 AC-6 (`once` trigger, generic-form defaults), dedicated BDD scenario, test 20. |
| MAJ-010 (Command Center edit loss) | **RESOLVED** | FR-023 read-only summary + calendar link, FR-005 reworded, CC scenario, test 21. |
| MAJ-011 (re-arm vs upsert race) | **PARTIAL** | Generation guard + test 10 added — but specified as non-atomic compare-then-register → MAJ-206. |
| MAJ-012 (clamp summary/round-trip) | **RESOLVED** | FR-007 recognize-on-reopen + special-cased summary, "Saved day-31 rule reopens" scenario, editor dataset row 11. |
| MAJ-013 (phantom test citations) | **RESOLVED** | Tests 21 (fallback UI + CC summary) and 22 (occurrences degrade) exist in the 24-row plan; matrix rows point at them. A **new instance of the same class** exists: MAJ-208. |
| MAJ-014 (no downgrade story) | **RESOLVED** | Operations & Rollback (one-way door, WARN behavior, ADR reproduction requirement), downgrade edge-case row. |
| MIN-001 (UNTIL wire form) | **RESOLVED** | FR-006a; row 6 literal `20271231T225959Z` verified arithmetically correct (23:59:59 CET = 22:59:59 UTC). |
| MIN-002 (<2 occurrences) | **RESOLVED** | Validation §4 trivial-pass sentence, dataset row 13, edge-case bullet. |
| MIN-003 (UNTIL exhaustion untested) | **RESOLVED** | "End conditions" outline gained the UNTIL row; test 6; dataset row 7. |
| MIN-004 (D6 "sub-daily" wording) | **RESOLVED** | D6 restated with the operative >3/day rule and the "every 8 hours renders individually" note. |
| MIN-005 (misfire policy) | **RESOLVED** | FR-014 + Boundary Conditions: skip-not-replay, COUNT consumed by calendar time. |
| MIN-006 (empty shapes) | **RESOLVED** | Omitted-when-zero, `[]` never null; test 13; dataset row 15. |
| MIN-007 (limiter bucket unnamed) | **NOT RESOLVED** | R2 names "the task-read limiter bucket" — that bucket does not exist in the codebase → escalated to MAJ-201. |
| OBS-001 (reverse-map complexity) | **ADDRESSED** | Ambiguity Warning 6: kept as SHOULD-level convenience with a recorded de-scope path. |
| OBS-002 (FR-020 data source) | **RESOLVED** | FR-020 sourced from the occurrences endpoint, never client math. |
| OBS-003 (cron not frozen) | **RESOLVED** | Deliberate-open-class note in the wire section + Assumptions; follow-up issue named. |
| OBS-004 (audit the conversion) | **RESOLVED** | FR-022 + audit assertion in test 12. |

---

## Findings (Round 2)

### MAJOR Findings

#### [MAJ-201] The "task-read limiter bucket" does not exist — the endpoint's DoS mitigation names a fictional mechanism

- **Lens**: Incorrectness / Insecurity (DoS)
- **Affected section**: Integration Boundaries → Omnipus REST API ("the occurrences endpoint joins the **task-read limiter bucket** — explicitly NOT `configLimiter`"); FR-008 ("task-read rate-limit bucket"); test 13 (asserts the bucket).
- **Description**: Verified against source: the gateway's only rate-limit buckets are `globalLoginLimiter`, `validateLimiter` (30/min), `onboardingCompleteLimiter` (3/min), `configLimiter` (240/min), `reauthLimiter` (10/min), and `cliValidateLimiter` (20/min) (`pkg/gateway/rest_auth.go:180-200`). The task routes are registered with plain `withAuth` and **no `withRateLimit` wrapper at all** (`pkg/gateway/rest.go:4695-4696`). "Joins the task-read limiter bucket" therefore has two incompatible readings: (a) "same as task reads today" = **no rate limit** — which silently deletes the rate-limiting leg of the MAJ-007 DoS mitigation the sentence exists to provide; (b) "create a new bucket" — with no limit or window specified, and test 13 asserting an unspecified number. This is the direct descendant of R1 MIN-007, nominally resolved by naming a bucket that turns out to be fictional.
- **Impact**: Either the endpoint ships unlimited (combined with MAJ-203, a repeatable CPU-burn vector for any authenticated caller), or the implementer invents limiter numbers the spec never reviewed — and either way test 13's "task-read limiter bucket" assertion is unimplementable as written.
- **Recommendation**: Specify the bucket concretely: a **new** `taskReadLimiter = newAPIRateLimiter(240, 1*time.Minute)` (matching `configLimiter`'s post-incident ceiling, which the calendar's navigation cadence is known to fit) applied to `GET /api/v1/tasks/occurrences` only; state explicitly that existing task CRUD routes remain unlimited (unchanged behavior). Update FR-008 and test 13 with the literal numbers.

#### [MAJ-202] `DayBucket` day boundaries use the rule's timezone while the grid renders in the browser's — chips land on the wrong day and D6 breaks cross-zone

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: Occurrence expansion endpoint (`day_start_ms: midnight of the day in the RULE's tz`); FR-009; D6; BDD "Sub-daily task aggregates…".
- **Description**: The client renders the calendar grid in the browser's zone (FullCalendar default; raw `occurrences_ms` instants will be placed on browser-local days). Buckets, however, are built per **rule-tz** day. When rule tz ≠ browser tz: (1) a bucket's `day_start_ms` (e.g. midnight Asia/Tokyo = 17:00 previous day Europe/Berlin) places the aggregated chip on the **wrong browser-local day**, and its occurrences genuinely straddle two browser-local days; (2) the >3/day threshold is evaluated on rule-tz days, so a browser-local day can accumulate up to 6 raw instants (3 + 3 from two adjacent rule-tz days) — violating D6's "more than 3 → one chip" as perceived by the viewer; (3) two tasks with different `tz` values bucket on different day boundaries within one response. The spec never states which zone's "day" the rendered grid cell represents for bucketed data.
- **Impact**: An operator in Berlin viewing a Tokyo-anchored sub-daily task sees its aggregated chip one day off, adjacent to raw chips from the same task placed by instant — the calendar contradicts itself about when work happens, precisely the "chips lie" failure class US-5/D2 exist to prevent.
- **Recommendation**: Make the *viewer's* zone the day-boundary authority for aggregation: add a required `tz` query parameter (the browser's IANA zone) to the occurrences endpoint; the server evaluates the >3/day threshold and builds `day_start_ms` in that zone for all trigger flavors. Add a cross-zone dataset row (rule tz Asia/Tokyo, query tz Europe/Berlin → bucket boundaries in Berlin days) and update the endpoint contract + test 8.

#### [MAJ-203] Bucket counting reintroduces the unbounded expansion work the iteration caps were meant to close

- **Lens**: Insecurity (DoS) / Incompleteness
- **Affected section**: Occurrence expansion endpoint ("Caps, enforced during iteration… `occurrences_ms` ≤ 500 per task"; "`day_buckets` bounded by the range span"); FR-008; Integration Boundaries ("a pathological rule cannot stall a core" — scoped to validation only).
- **Description**: The 500 cap bounds **raw instants** only. A `DayBucket`'s `count` field requires enumerating every occurrence on that day (arithmetic shortcuts exist for `every_ms`, but rrule and cron must iterate). A perfectly *valid* rule — `FREQ=MINUTELY` (60 s gaps, dataset row 6 explicitly accepts it) — over the maximum 400-day span is 400 × 1440 = **576,000 computed occurrences per task per request**, none of which are "instants" subject to the 500 cap ("day_buckets bounded by the range span" bounds the number of buckets, not the work to fill their `count`). Legacy dense `cron_expr` data behaves identically. Multiplied by an unbounded per-workspace task count and no real rate limiter (MAJ-201), this is the MAJ-007 expansion-cost concern re-opened through the CRIT-002 fix.
- **Impact**: A single authenticated GET with a wide range against a workspace holding a few dense-but-valid recurring tasks pins a core for seconds, repeatably — the exact class of stall the validation-side bounds were added to prevent, now on the read path.
- **Recommendation**: Add a per-task **total iteration budget** to FR-008 (e.g. 10,000 computed occurrences per task per request, counting bucket enumeration; on exhaustion → `truncated: true`, stop, marker renders as already specified) and permit arithmetic count derivation where the trigger is provably regular (`every_ms`; fixed-interval rrule). Add a dataset row: `FREQ=MINUTELY`, 400-day span → response within the budget, `truncated: true`, bounded server time.

#### [MAJ-204] The recovery sweep's mechanics are fictional or undefined: no "existing maintenance cadence" exists, no interval is given, and "no armed job" is not defined

- **Lens**: Incorrectness / Inoperability
- **Affected section**: Scheduler rule 6 ("a periodic sweep (piggybacking the existing scheduler maintenance cadence)"); FR-014; test 9.
- **Description**: Verified against source: `TaskTriggerScheduler` (`pkg/agent/task_trigger.go`) has **no periodic loop of any kind** — `Reconcile()` is called once at boot; the only recurring machinery in `pkg/cron` is the due-job tick loop and dispatch/drain goroutines. There is no "existing scheduler maintenance cadence" to piggyback; the sweep needs its own ticker (or a hook added inside `pkg/cron`'s tick), and the spec gives it **no interval** — "periodic" is untestable and unbounded (test 9 asserts the sweep fires, against no stated cadence). Separately, "no armed job" is undefined at exactly the point where it matters: the engine's `rescheduleSkippedUnsafe` (`pkg/cron/service.go`) explicitly leaves `at`-jobs "as-is" after an engine-level skip with `NextRunAtMS` already cleared by `RunDueJobs` — i.e. a job **entry** can exist that will never fire. A sweep that checks job existence (the natural reading of "with no armed job") passes that dead entry forever and never resurrects the series.
- **Impact**: The designated safety net for CRIT-003's residual crash window is specified against a mechanism that doesn't exist, with no detection-latency bound, and with a definitional hole that lets the one dead-job shape the engine is known to produce slip through it.
- **Recommendation**: Rewrite rule 6: the sweep runs on a **dedicated ticker inside `TaskTriggerScheduler`** (state the interval — e.g. every 5 minutes); "armed" means a registered job exists **and** its `State.NextRunAtMS` is non-nil and ≥ now; any non-terminal, non-exhausted RRULE task failing that predicate is re-armed with a WARN. Extend test 9 with the dead-entry case (job present, `NextRunAtMS` nil → sweep re-arms).

#### [MAJ-205] The DST spring-forward policy contradicts itself: "first valid instant after the gap" (03:00) vs Go normalization (03:30)

- **Lens**: Inconsistency
- **Affected section**: Timezone Semantics §5 ("resolves to the first valid instant after the gap (Go `time` normalization)"); Boundary Conditions ("fires at the first valid instant after the gap"); Edge Cases ("post-gap instant"); BDD "DST transitions" row 3 ("fires once at the first valid instant after the gap (03:00 CEST → normalized 03:30)"); dataset row 3; test 4; SC-006.
- **Description**: For a 02:30 rule on a 02:00→03:00 spring-forward day, "the first valid instant after the gap" is **03:00**. Go `time` normalization of the nonexistent 02:30 yields **03:30** (the wall-clock time shifted by the gap length). These are different instants, and the spec asserts both — the prose (three places) says the former, the parenthetical in §5 and the BDD example's own annotation ("03:00 CEST → normalized 03:30") say the latter. Test 4 is instructed to pin "the normative policy," which currently names two instants.
- **Impact**: Two engineers implement 03:00 vs 03:30 and both can cite the spec; the pinning test either can't be written or freezes whichever interpretation the first implementer picked — reproducing the exact "no spec ground truth to call it a bug against" failure MAJ-005 was raised for.
- **Recommendation**: Pick Go normalization (03:30 — it preserves the "minutes past the hour" intent and is what the stack does natively) and replace every occurrence of "first valid instant after the gap" with "the nonexistent local time shifted forward by the gap length (Go `time` normalization; 02:30 → 03:30 for a one-hour gap)". Update BDD row 3, dataset row 3, and the Boundary Conditions and Edge Cases bullets to the single instant.

#### [MAJ-206] The generation guard is specified as non-atomic compare-then-register — the edit-during-fire race survives in shrunken form

- **Lens**: Incorrectness
- **Affected section**: Scheduler rule 4 ("captures a trigger generation… `RunScheduled`'s re-arm compares it against the task's current stored trigger and no-ops on mismatch"); FR-014; test 10; BDD "Editing a task while it is firing…".
- **Description**: Verified against source: `OnTaskUpserted` holds `s.mu` only around the `taskToJob` map operations — the remove → `AddJobFull` → map-write sequence is **not** a critical section (`pkg/agent/task_trigger.go:154-212`), and the spec's guard is a read-compare followed by a separate registration. Interleaving: the re-arm reads the stored trigger (hash matches — the PUT hasn't landed), the concurrent `OnTaskUpserted` for the new rule removes the (already-deleted) old job and registers the new rule's job, then the re-arm registers the **old** rule's next occurrence and overwrites the map entry. Post-state: two armed jobs (the new-rule job now orphaned from the map, the old-rule job tracked) — precisely the MAJ-011 outcome the guard was added to prevent, now confined to the window between hash check and registration. The spec's post-condition ("exactly one armed job, for the new rule") is asserted but no serialization mechanism is required to make it true.
- **Impact**: An ordinary edit landing in a millisecond-scale window during a fire yields a duplicate armed job (double future fires) or a lost rule change; test 10 as described (a plain concurrent PUT) will almost never force the interleaving, so it goes green over the surviving race.
- **Recommendation**: Amend rule 4: the hash comparison, job registration, and `taskToJob` write of the re-arm MUST execute atomically under the scheduler's mutex, and `OnTaskUpserted`'s remove+register+map-write MUST hold the same mutex across the whole sequence. Require test 10 to force the interleaving deterministically (e.g. a test hook blocking the re-arm between check and register while a PUT completes).

#### [MAJ-207] Edit-mode anchor semantics are unspecified: what happens to `dtstart_ms` (and COUNT consumption, and biweekly phase) when a series is saved?

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: US-2.5 / US-5.3 (edit mode); Wire & Engine Design (`dtstart_ms` — anchor…); Scheduler rule 5 (COUNT "evaluated statelessly from DTSTART"); FR-013; BDD "Saving an edit converts…".
- **Description**: Create-mode anchors are defined (the clicked date/time). Edit mode is not: when an existing series is opened, what does Date & time display (the original anchor? the next occurrence?), and what `dtstart_ms` does Save write? Because COUNT and interval phase are computed statelessly from DTSTART, this is not cosmetic: (a) if the editor rebuilds the trigger from UI state on every save, a **title-only edit** silently re-anchors `dtstart_ms` → a `COUNT=10` series that has consumed 4 occurrences restarts at 10; (b) an `INTERVAL=2` biweekly rule re-anchored a week later **flips its week parity** — every future occurrence moves by a week; (c) `BYSETPOS`/ordinal-weekday rules can shift month-position. Nothing in the spec, scenarios, or datasets pins any of these.
- **Impact**: The most routine edit (fixing a typo in the title) can silently reschedule every future fire and extend a bounded series — invisible until the wrong Monday, with the audit trail (FR-022) only covering legacy conversions, not RRULE→RRULE rewrites.
- **Recommendation**: Specify: edit mode displays the series' stored anchor; Save preserves `dtstart_ms` (and the whole trigger) **byte-identical when no recurrence/time field was touched**; when recurrence fields change, the rule re-anchors at the next occurrence of the new rule ≥ now and COUNT restarts (state this visibly in the panel's summary). Add a BDD scenario + dataset row: title-only edit → stored trigger unchanged; biweekly edit → documented re-anchor.

#### [MAJ-208] "Cross-zone save preserves fire instants" — the flagship MAJ-002 regression scenario — is cited only to a test that cannot observe it

- **Lens**: Inconsistency (structural — the MAJ-013 phantom-citation class)
- **Affected section**: Traceability Matrix FR-021 row (cites `TestRestTasks_CreateRecurringRrule` (12) for the "Cross-zone save without a time change…" scenario); tests 16/20/21 descriptions.
- **Description**: The tz-stamping decision on an unchanged-time legacy save is made **client-side** — the editor chooses `tz = server_tz` and puts it in the payload (Timezone Semantics §3c). A Go REST test (12) can only assert that whatever payload it hand-crafts is accepted; it cannot verify that the *editor* stamps the server zone rather than the browser zone — the exact silent-fire-shift bug MAJ-002 was about. No TS test in the 24-row plan asserts the save payload of a mappable-legacy edit: test 16 checks reverse-parse picker state and label, test 20 covers create-mode flows, test 21 covers the unmappable fallback UI. The matrix reads green over an unverified behavior, the same defect shape MAJ-013 identified in round 1.
- **Impact**: The one regression this feature's tz design most needs to prevent — open a working legacy task from a differently-zoned browser, save untouched, fires shift permanently — can ship with every listed test green.
- **Recommendation**: Extend test 20 or 21 (or add a row): mappable legacy cron opened with a mocked `server_tz` ≠ browser zone → save without touching the time → asserted request body carries `tz = server_tz` and an equivalent time-of-day; also assert the §3(d) flip (after editing the time, payload carries the browser zone). Point the matrix's FR-021 row at that test.

---

### MINOR Findings

#### [MIN-201] "Span ≤ 7 days" is not defined in units — a DST fall-back week is 169 h and silently flips Week view into bucketed mode

- **Lens**: Ambiguity
- **Affected section**: Bucketing rule ("For range spans > 7 days… For spans ≤ 7 days"); FR-008.
- **Description**: If span = `to_ms − from_ms` compared against 7×24 h (604,800,000 ms), the fall-back week (7 d + 1 h = 608,400,000 ms) exceeds it → the server buckets >3/day tasks in a Week-view fetch once a year, contradicting D6's "Week/Day views show real times" and the "Day view shows individual timed entries" BDD assertion for that week.
- **Recommendation**: Define the measurement: span in ms with the threshold at 8 days (or "≤ 7 calendar days in the query tz per MAJ-202"). One sentence plus a fall-back-week dataset row.

#### [MIN-202] `RunScheduled`'s enumerated re-arm paths omit two real error exits — including one where re-arm is impossible as specified

- **Lens**: Incompleteness
- **Affected section**: Scheduler rule 2 ("success, overlap-guard skip (`ErrAlreadyRunning`), and dispatch error. The only path that does not re-arm is task-deleted cleanup"); test 9.
- **Description**: Verified: `RunScheduled` also exits on a transient `store.Get` error and on a non-overlap `SpawnReset` error (`pkg/agent/task_trigger.go:243-277`), both returning error to the engine — which deletes the at-job (`scheduleNextRunUnsafe`'s `at` branch runs before any backoff consideration). On the Get-error path the task — and therefore the trigger — **cannot be read**, so "compute and register the next occurrence before returning" is unimplementable there; the series is dead until the sweep. The blanket "any path that returns without re-arming is a defect" sentence papers over a path where re-arming is impossible.
- **Recommendation**: Enumerate all five exits; for task-unreadable errors state the design explicitly: no re-arm is possible, log ERROR, the recovery sweep (MAJ-204, once fixed) is the designated recovery. Add the Get-error case to test 9.

#### [MIN-203] Timezone Semantics §3(d) contradicts itself about which zone re-anchors an edited time

- **Lens**: Ambiguity
- **Affected section**: §3(d): "re-anchored in the zone shown on the control at that moment — the browser zone".
- **Description**: Per §3(b), the control at that moment shows the **server's** zone; the appositive asserts it is the browser zone. The intended behavior (editing the time flips the anchor zone to the browser's and the label updates) is guessable but the sentence as written names both zones.
- **Recommendation**: Rewrite: "editing the time field re-anchors the rule in the **browser's** zone; the read-only zone label switches from `server_tz` to the browser zone at that moment."

#### [MIN-204] `every_ms` projection is forward-only — past days in the visible range render the task as absent, unstated

- **Lens**: Incompleteness
- **Affected section**: FR-008a; Occurrence expansion endpoint.
- **Description**: The projection is baselined at the live `NextRunAtMS`; the spec never says what a range (or range portion) before *now* returns for `every_ms` tasks. Backward extrapolation would be fiction (drift re-baselining); omission means the current month's past days show nothing for an `every` task while cron/rrule tasks show past occurrences — an unexplained asymmetry an operator will read as "it wasn't running".
- **Recommendation**: State it: `every_ms` occurrences before now are omitted (projection is forward-only); optionally note the asymmetry in the aggregated-chip tooltip. Add a dataset row (range fully in the past → task omitted).

#### [MIN-205] The stored `type` after a legacy conversion is never stated — `every` + `rrule` is an undefined wire shape

- **Lens**: Ambiguity
- **Affected section**: Wire & Engine Design ("For `type: recurring`, `config` must carry exactly one of…"); FR-013; US-5.5.
- **Description**: The exactly-one-of rule and `rrule` siblings are defined only for `type: recurring`. Converting an `every` task "to RRULE" must also flip `type` `every` → `recurring`, but no text says so, and `ValidateTrigger`'s behavior for `type: every` with `rrule` keys present (reject? ignore?) is unstated.
- **Recommendation**: One sentence in FR-013: conversion writes `type: recurring` (the `every` type never carries `rrule`); `ValidateTrigger` rejects `rrule`/`dtstart_ms`/`tz` on non-`recurring` types.

#### [MIN-206] `GET /api/v1/tasks/occurrences` nests under the tasks prefix route — it will parse as `{id}: "occurrences"` unless matched first

- **Lens**: Incompleteness
- **Affected section**: Occurrence expansion endpoint path.
- **Description**: Verified: the gateway registers `/api/v1/tasks/` as a prefix route into `HandleTasks` (`pkg/gateway/rest.go:4696`), which parses the trailing segment as a task ID. Implementable (special-case the literal segment before ID parsing), but the spec should acknowledge the collision so the implementer doesn't discover it as a mysterious 404-task-not-found.
- **Recommendation**: Add a note to the endpoint section: `HandleTasks` must match the literal `occurrences` sub-path before ID parsing (or register the sub-route explicitly ahead of the prefix).

#### [MIN-207] Truncation "countable client-side (existing zod-drop counter pattern)" names the wrong mechanism — truncated responses are schema-valid

- **Lens**: Inoperability
- **Affected section**: Operations & Rollback (observability commitments).
- **Description**: The zod-drop counter counts schema-validation failures; a `truncated: true` response validates cleanly, so that counter records nothing. As written, the observability commitment for truncation is satisfied by a mechanism that will always read zero.
- **Recommendation**: Name a real mechanism: a dedicated client counter incremented per `truncated: true` task-set, and/or a server-side debug log per truncated expansion.

#### [MIN-208] `from_ms`/`to_ms` inclusivity is unspecified

- **Lens**: Ambiguity
- **Affected section**: Occurrence expansion endpoint.
- **Description**: FullCalendar's `activeEnd` is exclusive; an occurrence exactly at `to_ms` either double-renders across adjacent fetches or drops at the range edge depending on the implementer's choice.
- **Recommendation**: Specify half-open `[from_ms, to_ms)` and note that the client passes `activeStart`/`activeEnd` directly.

#### [MIN-209] Coincident-occurrence semantics are undefined, and the min-gap scan window can be too short to catch DST-collision pairs

- **Lens**: Incompleteness
- **Affected section**: Validation §4 ("first 60 occurrences or 366 days, whichever is smaller"); Timezone Semantics §5 ("every occurrence fires exactly once").
- **Description**: Under the normalization policy, an API-crafted rule with occurrences at 02:30 and 03:30 (e.g. `FREQ=DAILY;BYHOUR=2,3;BYMINUTE=30`) produces **two distinct rule occurrences at the same instant** on the spring-forward day (02:30 normalizes onto the natural 03:30) — a 0 s gap. The scan would reject it *if* the window covers a transition day, but for a daily rule the window is min(60 occurrences, 366 days) = **60 days**; a rule created in April is scanned only to June, passes, and first collides the following March. At runtime, whether the collision fires once or twice depends on the expansion layer's strictly-after semantics — currently unstated.
- **Recommendation**: One sentence in §5: occurrences that normalize to the same instant collapse to a single fire (expansion de-duplicates identical instants); this makes the scan's window miss harmless. Optionally add the collision rule as a validation dataset row with a transition-covering window.

---

### Observations

#### [OBS-201] `server_tz` ships on an optional-auth endpoint

- **Lens**: Insecurity (Information Disclosure)
- **Affected section**: Timezone Semantics §2 (`server_tz` on `GET /api/v1/state`).
- **Suggestion**: Verified: `/api/v1/state` is registered with `withOptionalAuth` (pre-onboarding surface, `pkg/gateway/rest.go:4681`) — the gateway's IANA zone becomes readable unauthenticated. It's mild fingerprinting data (narrows geography), but it's a deliberate-sounding choice the spec never makes: either note the acceptance in one line or return `server_tz` only to authenticated callers.

#### [OBS-202] `DayBucket.first_ms` has no consumer

- **Lens**: Overcomplexity
- **Affected section**: `TaskOccurrenceSet` shape; FR-009.
- **Suggestion**: Labels use `interval_ms`/`count`; placement uses `day_start_ms`. No FR, scenario, or test reads `first_ms`. Give it a consumer (e.g. aggregated-chip tooltip "first at 09:00") or drop the field — unconsumed wire surface is permanent contract weight under Constraint #8.

#### [OBS-203] Dependency chains crossing the Board/List split are unaddressed

- **Lens**: Incompleteness
- **Affected section**: US-3; Explicit Non-Behaviors ("data remains queryable").
- **Suggestion**: A Board-visible `once` task can be `blocked_by` a calendar-only recurring task; the Board then shows a blocked card whose blocker's card exists on no Board/List surface. Presumably blocker *names* still resolve (the store is untouched) — one sentence confirming that, and that the blocked-state rollups ignore trigger type, would close the gap.

#### [OBS-204] US-1 AC-1's slide-over defaults are asserted only in Given clauses

- **Lens**: Structural
- **Affected section**: BDD "Operator creates a biweekly task…" (defaults appear in the Given); round-1 structural note repeated.
- **Suggestion**: The pre-fill/default state ("Does not repeat", browser-zone time label, clicked date) is still never the subject of a Then. Test 20 likely covers it incidentally; adding one Then (or an explicit assertion line in test 20's description) makes the matrix honest.

---

## Structural Integrity

### Plan-Spec Format checks (Revision 2)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1: 6, US-2: 6, US-3: 5, US-4: 4, US-5: 6 |
| Every acceptance scenario has BDD scenarios | PASS (with note) | US-1.1's default-state assertions live in Givens only (OBS-204); US-3.4 covered via the regression table (accepted in R1) |
| Every BDD scenario has `Traces to:` reference | PASS | All 27 scenarios carry back-references |
| Every BDD scenario has a test in TDD plan | FAIL (one instance) | "Cross-zone save without a time change…" maps only to a Go test that cannot observe the client-side stamping it asserts (MAJ-208); all other scenarios map to real, capable rows in the 24-test plan |
| Every FR appears in traceability matrix | PASS | FR-001…FR-023 incl. FR-006a/FR-008a |
| Every BDD scenario in traceability matrix | PASS | Verified locatable |
| Test datasets cover boundaries/edges/errors | PASS (gaps noted) | Round-1 adversarial gaps all closed; new gaps: cross-tz bucket row (MAJ-202), iteration-budget row (MAJ-203), title-only-edit row (MAJ-207), fall-back-week span row (MIN-201), DST-collision row (MIN-209) |
| Regression impact addressed | PASS | HIGH-risk `TaskTriggerScheduler` callout + byte-identical legacy translation test retained |
| Success criteria are measurable | PASS | SC-001–SC-009 all verifiable; SC-008/SC-009 added in R2 are concrete |

---

## Test Coverage Assessment

Round 1's seven missing categories are all now covered by real plan rows (adversarial validation → tests 2–3; concurrency → test 10; failure-path scheduler → test 9; UI degrade → test 22; fallback UI → test 21; engine agreement → test 7; DST boundaries → test 4). Remaining gaps found in this round:

| Category | Gap Description | Finding |
|----------|----------------|---------|
| Cross-zone save payload | No TS test asserts `tz = server_tz` in the save body of an unchanged-time legacy edit (or the browser-zone flip after editing the time) | MAJ-208 |
| Sweep dead-entry case | Test 9's sweep case covers "no job"; not "job present with nil `NextRunAtMS`" | MAJ-204 |
| Race determinism | Test 10 (plain concurrent PUT) will rarely force the check-vs-register window; needs a blocking hook | MAJ-206 |
| Edit anchor invariance | No test asserts a title-only edit leaves the trigger byte-identical / documents re-anchor on rule change | MAJ-207 |
| Expansion work bound | No test bounds server work for a valid dense rule over the max span | MAJ-203 |
| Cross-tz bucketing | No dataset row exercises rule tz ≠ viewer tz bucket placement | MAJ-202 |
| `server_tz` fallback | No test for the "server time" label when `server_tz` is absent/unparseable (Timezone §2 fallback) | — (fold into test 16 or 21) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `GET /tasks/occurrences` | ok | ok | ok | ok | risk | ok | Named rate-limit bucket is fictional (MAJ-201); bucket-count enumeration unbounded for valid dense rules over max span (MAJ-203) |
| `ValidateTrigger` RRULE path | ok | ok | ok | ok | ok | ok | R1's CRIT-001/MAJ-007 bounds all present and sound; residual scan-window miss is availability-neutral once MIN-209's dedup is stated |
| `TaskTriggerScheduler` re-arm | ok | ok | ok | ok | risk | ok | Availability: sweep mechanics fictional/undefined (MAJ-204); non-atomic generation guard can double-arm (MAJ-206); task-unreadable exit has no possible re-arm (MIN-202) |
| Legacy conversion (US-5.3) | ok | ok | ok | ok | ok | ok | Audit entry now mandatory (FR-022); the tz-shift regression is specified correctly but unverified by any capable test (MAJ-208 — verification gap, not a spec-text threat) |
| `server_tz` on `/api/v1/state` | ok | ok | ok | risk | ok | ok | Optional-auth exposure of the server's zone — mild, undecided (OBS-201) |
| RecurrenceEditor (client) | ok | ok | ok | ok | ok | ok | Server remains validation authority; unchanged from R1 |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. What limit and window does the occurrences endpoint actually get, given no task-read limiter exists to "join"? (MAJ-201)
2. In whose timezone is a `DayBucket`'s "day" when the viewer's browser zone differs from the rule's `tz`? (MAJ-202)
3. What bounds the server's work when counting a valid `FREQ=MINUTELY` rule's bucket occupancy over a 400-day span? (MAJ-203)
4. What ticker runs the recovery sweep, at what interval, and does "armed" mean "job entry exists" or "job exists with a non-nil future `NextRunAtMS`"? (MAJ-204)
5. Does a 02:30 rule on a spring-forward day fire at 03:00 or 03:30? The spec currently says both. (MAJ-205)
6. What serializes the re-arm's trigger-hash check against a concurrent `OnTaskUpserted` registration? (MAJ-206)
7. What `dtstart_ms` does saving an edited series write — and does a title-only edit preserve the trigger byte-identical, COUNT consumption and biweekly phase included? (MAJ-207)
8. Which client-side test observes that an unchanged-time legacy save stamps `tz = server_tz` rather than the browser zone? (MAJ-208)
9. When the task store cannot be read at fire time, where would the re-arm get the rule from — and is the sweep the designated recovery for that path? (MIN-202)
10. Do two rule occurrences that normalize to the same instant (DST collision) fire once or twice? (MIN-209)

---

## Verdict Rationale

REVISE, not BLOCK: no finding leaves the spec structurally unimplementable or specifies a bypassable security guard — the three round-1 CRITICALs are genuinely fixed in design, and the codebase grounding of Revision 2 remains excellent (every carried-over symbol/line citation re-verified accurate). But five of the eight MAJORs are defects **inside the remediation text itself** — a fictional rate-limit bucket (MAJ-201), a fictional maintenance cadence plus an undefined "armed" predicate at the exact engine edge (`rescheduleSkippedUnsafe` leaving cleared at-jobs) where it matters (MAJ-204), a self-contradictory DST instant (MAJ-205), a non-atomic guard for the race it was added to close (MAJ-206), and a new instance of the phantom-test-citation class on the tz-regression crown jewel (MAJ-208) — and the remaining three (bucket timezone, bucket-count work, edit re-anchoring) are consequential gaps in the new bucketed-response and editor designs that will produce wrong calendars or silently rescheduled series if left to implementer discretion. None require re-opening the operator's D1–D7 decisions; all are resolvable with normative sentences, a handful of dataset rows, and two test-plan amendments.

### Recommended Next Actions

- [ ] Replace the fictional "task-read limiter bucket" with a concretely-specified new `taskReadLimiter` (numbers included) — MAJ-201
- [ ] Make the viewer's zone the bucketing day-boundary authority (add a `tz` query param) and add a cross-zone dataset row — MAJ-202
- [ ] Add a per-task total iteration budget covering bucket counting — MAJ-203
- [ ] Rewrite Scheduler rule 6: dedicated ticker + interval + "armed = non-nil future `NextRunAtMS`"; extend test 9 with the dead-entry case — MAJ-204
- [ ] Pin the spring-forward instant to Go normalization (03:30) everywhere — MAJ-205
- [ ] Require mutex-atomic guard-check + registration in both `RunScheduled` re-arm and `OnTaskUpserted`; make test 10 deterministic — MAJ-206
- [ ] Specify edit-mode anchor semantics (byte-identical trigger on non-recurrence edits; documented re-anchor + COUNT restart on rule changes) with a title-only-edit scenario — MAJ-207
- [ ] Add the client-side cross-zone save-payload test and repoint the FR-021 matrix row — MAJ-208
- [ ] Sweep the MINOR batch (span units, exit-path enumeration, §3(d) wording, forward-only projection, type flip, route collision note, truncation counter, range inclusivity, coincident-instant dedup) — one to two sentences each

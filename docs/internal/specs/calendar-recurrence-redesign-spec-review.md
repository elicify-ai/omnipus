# Adversarial Review: Calendar Recurrence Redesign (Recurring Tasks are Calendar Events)

**Spec reviewed**: docs/internal/specs/calendar-recurrence-redesign-spec.md
**Review date**: 2026-07-19
**Review mode**: plan-spec (full structural checks)
**Verdict**: BLOCK

## Executive Summary

The spec is well-grounded in the codebase (every cited symbol/line was verified accurate) and the operator-locked decisions are cleanly encoded, but three CRITICAL defects make it unshippable as written: the specified ≥60s validation algorithm is provably bypassable for RRULE (it is only sound for cron), the 500-occurrence cap arithmetically contradicts the "one aggregated chip per day" promise for the very sub-daily tasks it was designed around, and the RRULE re-arm design ignores the cron engine's `DeleteAfterRun`/retry mechanics, leaving a window where a recurring task silently dies until the next gateway restart. Fourteen MAJOR findings follow, clustered around timezone semantics (three distinct unspecified tz decisions), undefined editing paths (`every_ms` tasks, Command Center, non-recurring chips), and traceability rows citing tests that do not exist in the TDD plan.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 14 |
| MINOR | 7 |
| OBSERVATION | 4 |
| **Total** | **28** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The "first two occurrences" ≥60s check is unsound for RRULE — the self-DoS guard is bypassable

- **Lens**: Incorrectness / Insecurity (DoS)
- **Affected section**: Wire & Engine Design → Validation ("enforce the existing ≥60s minimum between consecutive occurrences (compute the first two occurrences and compare — parity with `validateCronExpr`'s self-DoS guard)"); FR-006; Dataset "RRULE validation" rows 6–7; test `TestValidateTrigger_RruleMinInterval`.
- **Description**: The first-two-fires technique is sound for cron **only** because cron patterns are minute-aligned and periodic — the code comment at `pkg/task/store.go` (validateCronExpr) states it explicitly: "The second field of a 6-field expr is the only way to fire sub-minute; a 5-field expr can never fire more than once per minute." RRULE occurrence gaps are **irregular**. Example bypass: `FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30` with `dtstart_ms` anchored at 09:00:15 — the first two computed occurrences are 09:00:30 (day 1) and 09:00:00 (day 2), ~24h apart, so the check passes; yet the rule fires twice within 30 seconds every day thereafter. Any BYSECOND/BYMINUTE list, or a BYDAY/BYHOUR combination whose smallest gap is not the first gap, defeats the check. Dataset rows 6–7 only test *regular* MINUTELY/SECONDLY patterns, so the TDD plan would go green on a broken guard.
- **Impact**: An operator (or an agent via `create_task`, or a prompt-injected agent) stores a rule that passes validation but fires sub-minute — each fire spawns a fresh agent run with LLM calls. This is exactly the `* * * * * *` self-DoS the guard exists to prevent (cost burn + gateway load), now reachable through the "validated" path.
- **Recommendation**: Rewrite FR-006/the validation paragraph: compute the **minimum gap over a bounded expansion** — e.g. the first 60 occurrences or 366 days from DTSTART, whichever is smaller — and reject if any consecutive pair is <60s. Additionally consider rejecting `FREQ=SECONDLY` and any `BYSECOND` value other than the DTSTART second outright (nothing in the editor can produce them; only hand-crafted API payloads can). Add adversarial dataset rows: the irregular-gap example above (expected 400) and a BYMINUTE-list variant.

---

#### [CRIT-002] The 500-occurrence cap contradicts D6/US-2.3/FR-009 — the sub-daily BDD scenario is unimplementable as written

- **Lens**: Inconsistency / Infeasibility
- **Affected section**: FR-008 (cap 500/task/request) vs. FR-009 + D6 + BDD "Sub-daily task aggregates to one chip per day in Month view" ("**each day** shows exactly one chip") + US-2 AC-3; Behavioral Contract boundary "Month view aggregation (D6) keeps the display sane".
- **Description**: A Month grid fetch spans ~42 days (6 visible weeks). "Every 30 minutes" = 48 occurrences/day → 2,016 occurrences in the range. The 500 cap truncates after ~10.4 days, so days 11–42 of the visible month render **no chips at all** for that task. The BDD scenario asserting "each day shows exactly one chip" cannot pass; the claim that aggregation "keeps the display sane" is backwards — aggregation happens client-side *after* the server has already truncated. Worst legal case (legacy `every_ms=60000`, which `ValidateTrigger` accepts today at the 1000ms/60s floors): 1,440/day × 42 = 60,480 — no flat cap raise fixes this. The spec also never says what the **client does** with `truncated:true` (no warning, no refetch strategy), so the failure is silent.
- **Impact**: The calendar shows a recurring task as *absent* for most of the visible month while looking authoritative — the operator concludes automation is not scheduled when it is. A listed BDD scenario and SC-002 are unachievable, so implementation either fails its own gate or quietly weakens it.
- **Recommendation**: Restructure the response for sub-daily density instead of raising the cap: per task, when a day's occurrence count exceeds the aggregation threshold (>3, matching FR-009), return a **per-day summary bucket** (`{day, count, first_ms, label_interval_ms}`) instead of raw instants; return raw instants only for days at/below the threshold and for Week/Day-range queries (≤7 days, where even every-60s legacy data is 10,080 instants — cap those at 500/task with `truncated`). Then specify client behavior for any remaining `truncated:true` (render a "+N more / truncated" indicator on the last covered day, at minimum). Update the BDD scenario and dataset row 6 to the bucketed shape.

---

#### [CRIT-003] RRULE re-arm ignores `DeleteAfterRun` + retry mechanics — a recurring task can die silently until restart

- **Lens**: Incompleteness / Inoperability
- **Affected section**: Wire & Engine Design → Scheduler ("RRULE triggers register as `kind:"at"` jobs … on each `RunScheduled` completion the scheduler computes the following occurrence … and re-arms"); FR-014; test `TestTriggerScheduler_RruleRearm`.
- **Description**: Verified engine facts the spec's design does not account for: (a) `AddJobFull` hard-codes `DeleteAfterRun: spec.Schedule.Kind == "at"` (`pkg/cron/service.go:1074`) and the engine deletes the job after firing (`service.go:802`) — so after each fire the ONLY thing keeping the series alive is the new re-arm code; (b) `RunScheduled` (`pkg/agent/task_trigger.go`) has three non-success exit paths the spec never mentions — the overlap-guard skip (`ErrAlreadyRunning` → return nil early), dispatch error (returns error → the cron engine's transient-retry backoff at `service.go:812-825` kicks in), and task-deleted cleanup; (c) `computeNextRun` for `at` returns nil for past instants, so a missed fire is dropped, and `Reconcile()` re-registers jobs **only at boot**. The spec says re-arm happens "on each RunScheduled completion" without defining completion, and never specifies the interaction between an at-kind job's retry backoff and its auto-delete, nor the crash window between job deletion and re-arm.
- **Impact**: Concrete 3 AM scenario: a nightly RRULE task's dispatch returns an error (LLM provider down); depending on implementation the at-job is deleted while the retry path expects it to still exist, or the re-arm never runs because RunScheduled returned an error — either way no job remains armed, no further occurrences fire, nothing surfaces in the UI (the calendar happily *renders* future occurrences via the endpoint), and the schedule stays dead until someone restarts the gateway.
- **Recommendation**: Amend FR-014 and the Scheduler section to state: re-arm MUST occur on **every** RunScheduled exit path (success, overlap-skip, dispatch error, retry exhaustion); specify explicitly whether RRULE jobs opt out of the cron retry-backoff (recommended: yes — the next occurrence is the retry, mirroring cron-kind semantics) or how backoff coexists with `DeleteAfterRun`; state that `Reconcile()` at boot is the documented crash-recovery mechanism and add a periodic (or Reconcile-time) WARN when a non-terminal RRULE task has no armed job. Extend `TestTriggerScheduler_RruleRearm` with the overlap-skip and dispatch-error paths (currently it only covers fire → re-arm → exhaustion).

---

### MAJOR Findings

#### [MAJ-001] `every_ms` "expanded arithmetically from its anchor" — the engine has no anchor

- **Lens**: Incorrectness
- **Affected section**: Edge Cases ("Legacy `every_ms` task … expanded arithmetically from its anchor"); Occurrence expansion endpoint ("`every_ms` arithmetically from its anchor"); test 7 `TestOccurrences_LegacyCronAndEveryMs`; SC-002.
- **Description**: `computeNextRun` for kind `every` is `next := nowMS + *schedule.EveryMS` (`pkg/cron/service.go`) — the next fire is anchored to the moment of the last computation, drifts with execution latency, and re-anchors on every service restart (`recomputeNextRuns`). There is no stored anchor instant. Whatever "anchor" the endpoint picks (task CreatedAt? first registration?) will disagree with actual fire times, violating the spec's own core rule ("the server is the single authority on fire times") and SC-002's "no discrepancies".
- **Impact**: An every-4h task shows calendar chips at 00/04/08/… while actually firing at 13:07/17:07/… — the operator plans around times that are fictional.
- **Recommendation**: Define the anchor normatively: the live cron job's `State.NextRunAtMS` is the first displayed occurrence; subsequent displayed occurrences are `+every_ms` from it; document in the spec that `every`-trigger display is a projection that re-baselines after each fire/restart (or render `every` tasks with an "approximate" affordance). Add an endpoint-vs-engine agreement assertion to test 7.

#### [MAJ-002] Three unspecified timezone decisions around legacy cron — conversion can silently shift fire times

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: US-5 (all scenarios); "Mappable legacy cron pre-populates the editor" (asserts "time 09:00"); Occurrence expansion endpoint (`cron_expr` via gronx iteration).
- **Description**: Cron fires evaluate in the **gateway server's local timezone** (`gronx.NextTickAfter(expr, time.UnixMilli(nowMS))`, `pkg/cron/service.go`). The spec never states: (1) which tz the occurrences endpoint uses to expand `cron_expr` — it must be server-local or the chips lie; (2) which tz the editor's reverse-map uses to display "09:00" — the browser's zone is wrong whenever browser ≠ server zone (common: server in UTC container, operator in Europe/Berlin: "0 9 * * MON" fires 09:00 UTC = 10:00/11:00 Berlin, but the editor would show "09:00" as if local); (3) which IANA zone is stamped into the RRULE on one-way conversion (US-5.3) — stamping the browser zone **changes the actual fire instants** of a task the operator only meant to re-express, violating US-5's "without anything breaking".
- **Impact**: An operator opens a working legacy 09:00-UTC task from a Berlin browser, sees "Weekly on Monday at 09:00", saves without changes → the task now fires at 09:00 Berlin, one/two hours off, permanently (one-way conversion).
- **Recommendation**: Specify: cron expansion and reverse-map both use the server's zone; the gateway exposes its IANA zone (e.g. on the existing state/config read the SPA already makes); the editor labels reverse-mapped times with that zone and stamps it (not the browser zone) on conversion of an *unchanged* time; changing the time in the editor may use the browser zone but the tz control must be visible in that flow (see MAJ-006). Add a US-5 acceptance scenario for browser-tz ≠ server-tz.

#### [MAJ-003] Undefined which panel opens for non-recurring chips clicked on the calendar

- **Lens**: Ambiguity
- **Affected section**: FR-001 ("calendar-specific create/edit slide-over … for tasks opened/created from the calendar") vs D3 ("Board/List keep the existing generic form"); Relevant Execution Flows (drag applies to `once` only).
- **Description**: Today, due chips and `once` fire chips open the generic detail panel. FR-001's wording ("tasks opened … from the calendar") sweeps them into the new event-style slide-over — but that panel (Title/Agent/Date & time/Repeat) has no status, todos, description, priority, or blocked_by, which Board tasks need. Two engineers will build different things: one routes all calendar clicks to the new panel (losing fields), the other only occurrence chips (leaving FR-001 as written unsatisfied).
- **Impact**: Either Board tasks become uneditable-in-full from the calendar, or the acceptance of FR-001 is judged failed at review time.
- **Recommendation**: Rewrite FR-001: "occurrence chips and calendar-created recurring series open the calendar slide-over; due chips and `once` fire chips keep today's panel behavior." Add a BDD scenario for clicking a due chip post-feature.

#### [MAJ-004] `every_ms` tasks are calendar-only but have no defined edit path in the calendar

- **Lens**: Incompleteness
- **Affected section**: D3 (`trigger.type ∈ {every, recurring}` calendar-only); US-5 (covers cron only); Edge Cases (`every_ms` renders); US-2.5 (chip click → edit mode).
- **Description**: An `every` task's occurrence chips open the slide-over in edit mode — and then what? The spec defines reverse-mapping and the read-only fallback for `cron_expr` only. `every_ms` is trivially mappable (`FREQ=MINUTELY/HOURLY;INTERVAL=n`), but nothing says whether it maps, falls back read-only, or converts on save. Also unstated: after this feature, is `every` creatable anywhere in the UI at all (the generic form drops it, the editor emits RRULE), leaving it an API/agent-tool-only type?
- **Impact**: The single surface allowed to manage these tasks (the calendar) has undefined behavior for one of the two trigger types it exclusively owns.
- **Recommendation**: Extend US-5 and FR-013: `every_ms` reverse-maps to the Custom editor (interval + minutes/hours unit) and converts one-way on save like cron; state explicitly that `every` remains a valid stored/API type but is no longer produced by any form.

#### [MAJ-005] DST nonexistent and ambiguous local times undefined

- **Lens**: Incompleteness
- **Affected section**: D7 / FR-010; BDD "DST transition keeps wall-clock time"; Dataset "Occurrence expansion" row 2.
- **Description**: All DST coverage uses 09:00 — a time that exists exactly once on every day. A rule at 02:30 Europe/Berlin hits a **nonexistent** time on spring-forward (does it skip, fire at 03:30, or fire at the normalized instant?) and an **ambiguous** time on fall-back (first occurrence, second, or both?). rrule-go's behavior here is an implementation accident unless pinned.
- **Impact**: A 02:30 nightly maintenance task silently skips (or double-fires) twice a year, with no spec ground truth to call it a bug against.
- **Recommendation**: Add a decision to D7/FR-010 (recommended: Go `time` normalization semantics — nonexistent times map forward to the post-transition instant, ambiguous times take the first occurrence, each occurrence fires exactly once) and two dataset rows (02:30 spring-forward, 02:30 fall-back) plus a unit test pinning rrule-go's actual behavior.

#### [MAJ-006] Source of `tz` at rule-creation time unspecified

- **Lens**: Ambiguity
- **Affected section**: Wire & Engine Design (`tz` required sibling); FR-005; the editor UI description (US-1 AC-1 lists Title/Agent/Date & time/Repeat — no tz control).
- **Description**: Nothing states where `tz` comes from when the operator builds a rule: browser zone (`Intl.DateTimeFormat().resolvedOptions().timeZone`)? server zone? a visible picker? Whether it is shown/overridable is also unstated. This interacts with MAJ-002 (server-hosted gateways in UTC) and with operators using multiple devices in different zones.
- **Impact**: Implementations will silently pick the browser zone; an operator configuring "09:00" from a phone while traveling stores a zone they never chose and cannot see.
- **Recommendation**: Specify: default = browser IANA zone, displayed read-only in the editor next to the time (e.g. "09:00 (Europe/Berlin)"), with the reverse-map/conversion flows using the server zone per MAJ-002. Add the tz display to US-1 AC and the compile tests.

#### [MAJ-007] Pathological RRULEs can stall validation and expansion; no input-size bounds

- **Lens**: Insecurity (DoS)
- **Affected section**: Wire & Engine Design → Validation; FR-006; Occurrence expansion endpoint.
- **Description**: A syntactically valid rule that never matches (e.g. `FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31` — Feb 31) forces "compute the first two occurrences" to iterate to the library's max-year horizon. No maximum RRULE string length or BY*-list size is specified (a 100KB BYSECOND list is schema-valid — `config` is `additionalProperties: true` with no length constraint). The occurrences endpoint compounds this per task per request; there is also no statement of which rate-limit bucket the endpoint joins (the calendar already has a 429 history with `configLimiter`).
- **Impact**: A single crafted create request (operator, agent tool, or any API caller) pins a gateway CPU core for seconds-to-minutes inside validation — pre-persistence, so it is repeatable at will.
- **Recommendation**: Add to FR-006: max `rrule` length (e.g. 512 chars); validation must bound its search (reject any rule with no occurrence within N years of DTSTART, e.g. 5); note the expansion endpoint's per-request work bound (cap applies during iteration, not post-hoc) and name its rate-limit bucket. Add dataset rows: never-matching rule → 400 within the bound; oversized rrule string → 400.

#### [MAJ-008] Aggregated-chip label derivation unspecified

- **Lens**: Incompleteness
- **Affected section**: D6 ("Title · every 30 min"); FR-009; BDD "Sub-daily task aggregates…" (asserts the label text); Occurrence expansion endpoint (returns only `{task_id, occurrences_ms, truncated}`).
- **Description**: The endpoint returns bare instants; the client must synthesize "every 30 min" per trigger flavor — rrule string parse (client rrule.js), `every_ms` arithmetic, or cron via `cronUtils`. The spec forbids client occurrence *math* but never says how the label is derived, and for an irregular rule aggregated by the >3/day threshold (e.g. `BYHOUR=9,11,13,15`) no label is defined at all.
- **Impact**: The one string the BDD scenario asserts has no specified producer; irregular aggregates ship with wrong or empty labels.
- **Recommendation**: Have the server include a per-task `frequency_label` (or `interval_ms` when regular, null when irregular → client renders "· N×/day") in the occurrences response — it already owns the trigger and the truth. This also dovetails with the CRIT-002 bucketed response.

#### [MAJ-009] "Does not repeat" save semantics undefined — `once` trigger vs `due` date

- **Lens**: Ambiguity
- **Affected section**: US-1 AC-1 (slide-over defaults to "Does not repeat"); Behavioral Contract (defines only the repeat-rule save); Existing calendar model (distinct `:due` all-day chips vs `:fire` timed chips, `src/components/calendar/types.ts:70-72`).
- **Description**: Saving the slide-over with "Does not repeat" creates… what? A `once` trigger at the picked Date & time (a fire chip that *runs* the agent), or a `due`-dated task (an all-day deadline chip)? These are semantically opposite (execution vs deadline). Also unspecified: the defaults for the task fields the panel doesn't show (status, surface, description, priority) for non-recurring saves — those tasks appear on the Board.
- **Impact**: Two engineers build opposite things; an operator clicking a day to note a deadline instead schedules an autonomous agent run (or vice versa).
- **Recommendation**: Specify: "Does not repeat" + a time ⇒ `once` trigger at `at_ms` (event = something happens at a time, matching the panel's event framing); document the created task's field defaults (`surface: user`, default status, empty description). Add a BDD scenario.

#### [MAJ-010] Command Center can see recurring tasks but loses the ability to edit their trigger

- **Lens**: Inconsistency
- **Affected section**: FR-005 ("the raw-cron input MUST be removed from **all** task forms"); Symbols table (`TaskDetailPanel.tsx:690-740` "recurring section removed for workspace surfaces"); Ambiguity Warning #1 ("Command Center stays an ops surface with full visibility").
- **Description**: These three statements do not compose. If the raw-cron/recurring section is removed from `TaskDetailPanel` (the Command Center's editor) while CC keeps showing recurring tasks, CC becomes view-only for the trigger with no stated replacement — and no stated pointer to the workspace calendar. The Symbols table's "for workspace surfaces" qualifier contradicts FR-005's "all task forms".
- **Impact**: An ops user in CC cannot fix a misfiring recurring task from the surface that shows it; or an implementer keeps raw cron in CC, violating FR-005 — the spec supports both readings.
- **Recommendation**: Pick one and encode it: recommended — TaskDetailPanel shows a read-only plain-English trigger summary plus an "Edit in workspace calendar" link for `every`/`recurring` tasks; FR-005 reworded to "removed from all task forms; CC shows read-only summary".

#### [MAJ-011] Re-arm vs `OnTaskUpserted` race unaddressed, no concurrency test

- **Lens**: Incorrectness
- **Affected section**: Wire & Engine Design → Scheduler; Edge Cases ("Concurrent edit … last-write-wins as today"); TDD plan (no concurrency test).
- **Description**: A PUT during an in-flight fire triggers `OnTaskUpserted` (remove job + add job for the *new* rule) concurrently with the fire's post-completion re-arm (computing the next occurrence of the *old* rule). The `taskToJob` map mutex protects individual operations, not the compose sequence — the stale re-arm can register a second job or overwrite the fresh one. The Edge Cases "last-write-wins" note covers store writes, not job registration.
- **Impact**: Duplicate fires (two armed jobs) or a lost rule change (stale re-arm wins) after the ordinary act of editing a task that happens to be firing.
- **Recommendation**: Specify a generation/rule-hash check: the re-arm no-ops unless the trigger it fired under still matches the stored trigger (and the taskToJob entry it recorded). Add an integration test: edit-during-fire → exactly one armed job, for the new rule.

#### [MAJ-012] Monthly-clamp form breaks the summary and the reverse round-trip

- **Lens**: Incompleteness
- **Affected section**: FR-004 (live plain-English summary, via rrule.js `.toText()`); FR-007 / D5 (compile to `BYMONTHDAY=28,29,30,31;BYSETPOS=-1`); US-5.1 (editor pre-population on reopen).
- **Description**: `.toText()` on the clamp form yields roughly "every month on the 28th, 29th, 30th and 31st" — not "Monthly on day 31". Nothing specifies that the editor must (a) special-case the summary for the clamp pattern and (b) *recognize* the clamp pattern when reopening a saved rule so the picker shows "day 31" rather than a Custom BYMONTHDAY list (or falling into the unmappable path).
- **Impact**: The headline UX promise (plain-English, no cron-tier gibberish) is broken precisely for the D5 flagship case, on both the summary line and every subsequent edit.
- **Recommendation**: Add to FR-007: the editor MUST detect the canonical clamp pattern and render/summarize it as "Monthly on day N (moved to the last day in shorter months)"; add an editor dataset row: compile "day 31" → save → reopen → picker shows "day 31".

#### [MAJ-013] Traceability matrix cites tests that do not exist in the TDD plan

- **Lens**: Inconsistency (structural)
- **Affected section**: Traceability Matrix rows FR-017 ("CalendarEventSlideOver/CalendarScreen vitest; e2e degrade case") and FR-012/FR-013 UI aspects; Test Implementation Order (tests 1–20).
- **Description**: The 20-test plan contains no test for the occurrences-endpoint degrade path (BDD "Occurrences query failure degrades…") — test 18's description covers create/inline-error/end-date only and test 20's e2e sweep lists four happy paths. Likewise, US-5.2's read-only fallback + "Replace with a new repeat rule" UI flow has only the parse-level unit test 14; no test renders the fallback or exercises the Replace action.
- **Impact**: The completeness check at the bottom of the matrix ("every FR has ≥1 scenario + test") is satisfied on paper by phantom tests; FR-017 and the US-5.2 flow can ship unverified while the matrix reads green.
- **Recommendation**: Add to the plan: (21) `CalendarScreen.occurrencesDegrade.test.tsx` — mock 500 → grid renders due/fire chips + toast; (22) extend test 18 or add a case: unmappable cron → read-only display + Replace opens fresh editor. Update the matrix to point at real rows.

#### [MAJ-014] No rollback/downgrade story for a shipping v0.1.x line

- **Lens**: Inoperability
- **Affected section**: Entire Wire & Engine Design; PR Slicing (no flag, no downgrade note); memory: this ships on the maintained `release/v0.1.1` product line.
- **Description**: After an operator saves any RRULE task, downgrading the binary leaves `type: recurring` with `rrule` but no `cron_expr`. The old `triggerToCronSchedule` errors ("missing config.cron_expr") and `OnTaskUpserted` logs a WARN and **skips registration** — the task silently stops firing; the old SPA renders nothing for it. The spec is a one-way door (acceptable) but never says so operationally.
- **Impact**: A routine hotfix rollback silently kills every RRULE task with only a slog WARN as evidence.
- **Recommendation**: Add an Operations note: downgrade below this feature disables RRULE tasks (fires stop, calendar hides them, data is preserved); re-upgrade restores them. State that this is accepted (no feature flag — one-way door) so the decision is on record, and reference it from the ADR that Slice 2 requires.

---

### MINOR Findings

#### [MIN-001] UNTIL wire form and end-date inclusivity unspecified

- **Lens**: Ambiguity
- **Affected section**: Scenario Outline row 6 (`UNTIL=20271231T...` — elided); Wire & Engine validation ("reject UNTIL earlier than dtstart_ms").
- **Description**: RFC 5545 requires UNTIL in UTC when DTSTART is zone-aware; the editor's "Ends on date" inclusivity (through end-of-day in the rule's tz?) is unstated, and the one example elides exactly the contentious part.
- **Recommendation**: Specify: "Ends on date D" compiles to `UNTIL=<D 23:59:59 in rule tz, converted to UTC, Z-suffixed>` (inclusive). Complete row 6 with the literal value.

#### [MIN-002] Min-interval check vs rules with fewer than two occurrences

- **Lens**: Incompleteness
- **Affected section**: Wire & Engine validation; FR-006.
- **Description**: `COUNT=1` (editor dataset row 10 produces it) or a tight UNTIL yields <2 occurrences; "compute the first two and compare" must accept, not error/panic. Unstated.
- **Recommendation**: One sentence in FR-006: rules yielding <2 occurrences trivially satisfy the interval floor. Add a validation dataset row (`COUNT=1` → accept).

#### [MIN-003] US-2.4's UNTIL-exhaustion variant untested

- **Lens**: Incompleteness
- **Affected section**: US-2 AC-4 ("UNTIL date has passed (or COUNT is exhausted)"); BDD/tests cover COUNT only (scenario "Exhausted COUNT…", test 6, dataset row 4).
- **Recommendation**: Add a past-UNTIL row to the expansion dataset and an assertion to `TestRruleExpansion_CountUntilExhaustion`.

#### [MIN-004] D6 "sub-daily" wording conflicts with the >3/day operative rule

- **Lens**: Inconsistency
- **Affected section**: D6 ("Sub-daily repeats → one aggregated chip per day") vs FR-009/Edge Cases (">3 occurrences … exactly at 3 → individual chips").
- **Description**: "Every 8 hours" is sub-daily (3/day) yet renders individually under the >3 rule. Harmless once noticed, but D6 as worded says aggregate.
- **Recommendation**: Reword D6's spec restatement: "tasks with more than 3 occurrences on a day aggregate (D6's intent); ≤3 render individually."

#### [MIN-005] Misfire/catch-up policy on gateway downtime unstated

- **Lens**: Inoperability
- **Affected section**: Scheduler section; FR-014.
- **Description**: `computeNextRun` drops past `at` instants and `Reconcile` arms the next occurrence after *now* — an occurrence spanned by downtime is silently skipped. This matches current cron-kind behavior but is a policy worth one written sentence, especially for COUNT-bearing rules (is the skipped occurrence consumed from COUNT? Stateless computation from DTSTART says yes — state it).
- **Recommendation**: Add to FR-014: missed occurrences during downtime are skipped, not replayed; COUNT positions are consumed by calendar time, not by actual fires.

#### [MIN-006] Occurrences endpoint empty-shape unspecified and untested

- **Lens**: Incompleteness
- **Affected section**: Occurrence expansion endpoint; `TestRestTasks_OccurrencesEndpoint`.
- **Description**: Whether a workspace with zero recurring tasks returns `[]`, and whether a task with zero occurrences-in-range is included with an empty array or omitted, is unstated (zod schema + client join both care).
- **Recommendation**: Specify: tasks with zero occurrences in range are omitted; empty result = `[]` (never null). Add to test 11.

#### [MIN-007] Rate-limit bucket for the new endpoint unnamed

- **Lens**: Inoperability
- **Affected section**: Occurrence expansion endpoint ("auth: same as task reads").
- **Description**: The calendar previously hit `configLimiter` 429s under bucket misassignment ("Failed to load workspace" incident, limiter raised 30→240/min). The new endpoint fires on every range navigation; its limiter bucket is unspecified.
- **Recommendation**: Name the bucket (task-read limiter, not configLimiter) in the endpoint contract paragraph.

---

### Observations

#### [OBS-001] Cron→picker reverse-mapping (US-5.1) is the weakest complexity/value trade in the spec

- **Lens**: Overcomplexity
- **Affected section**: US-5.1; FR-013; `recurrenceEditor.reverseParse.test.ts`.
- **Suggestion**: The read-only fallback + "Replace with a new repeat rule" (US-5.2) already gives every legacy task a complete edit path. Reverse-mapping a subset of cron into picker state adds a mapping table, its tests, and the MAJ-002 tz trap — for a convenience on tasks that are, by definition, being replaced. The operator locked D1–D7 but not US-5.1 specifically. Consider shipping US-5.2-for-all-legacy in this feature and US-5.1 as a follow-up; if kept, MAJ-002's tz rules become mandatory.

#### [OBS-002] FR-020's "next 2–3 fire times" needs a server data source

- **Lens**: Incorrectness (latent)
- **Affected section**: FR-020.
- **Suggestion**: Client-side computation of upcoming fires would violate the "server is the single authority" rule the spec itself sets. If implemented, source it from the occurrences endpoint (small forward range) — worth one sentence so an implementer doesn't reach for rrule.js `.all()`.

#### [OBS-003] "Legacy" cron is not actually frozen

- **Lens**: Incompleteness
- **Affected section**: D1; Non-Behaviors; `ValidateTrigger` (continues accepting `cron_expr`).
- **Suggestion**: Agent tools (`create_task`) and raw API callers can still create new `cron_expr` recurring tasks after this ships — "legacy" is an open class, and the read-only-fallback UI is therefore permanent, not transitional. Either state this deliberately or note a follow-up to steer tool-created recurring tasks toward RRULE (tool description update).

#### [OBS-004] Audit the one-way conversion

- **Lens**: Insecurity (Repudiation)
- **Affected section**: US-5.3 (one-way cron→RRULE conversion); Ambiguity Warning #4.
- **Suggestion**: "Visible in session history" is weak recovery for a destructive rewrite. Emit an audit-log entry (existing audit pipeline) recording task ID, old `cron_expr`, new `rrule/dtstart/tz` on conversion — one line of spec, cheap insurance.

---

## Structural Integrity

### Plan-Spec Format checks

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1:5, US-2:5, US-3:4, US-4:4, US-5:4 |
| Every acceptance scenario has BDD scenarios | FAIL | US-2.4's UNTIL variant has no scenario (COUNT only — MIN-003); US-1.1's "Does not repeat" default state is never asserted in any Then; US-3.4 relies on the regression table, not a scenario (acceptable, but note it) |
| Every BDD scenario has `Traces to:` reference | PASS | All 20 scenarios carry back-references |
| Every BDD scenario has a test in TDD plan | FAIL | "Occurrences query failure degrades…" has no test in the 20-row plan; "Unmappable legacy cron falls back…" has only the parse-level unit (no UI/Replace-action test) — MAJ-013 |
| Every FR appears in traceability matrix | PASS | FR-001…FR-020 all present |
| Every BDD scenario in traceability matrix | PASS | All scenarios locatable in matrix rows |
| Test datasets cover boundaries/edges/errors | FAIL | Strong on regular boundaries, but missing the adversarial rows that matter: irregular-gap RRULE (CRIT-001), never-matching/oversized RRULE (MAJ-007), spring-forward/fall-back times (MAJ-005), clamp round-trip (MAJ-012), `COUNT=1` validation (MIN-002) |
| Regression impact addressed | PASS | Explicit table + HIGH-risk `TaskTriggerScheduler` callout; heartbeat double-fire guard correctly identified |
| Success criteria are measurable | PASS | SC-001–007 all verifiable; SC-002's "spot-checked" is anchored by concrete counts |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Adversarial validation | Irregular-gap, never-matching, and oversized RRULEs absent | Server rejects a sub-minute rule… (CRIT-001, MAJ-007) |
| Concurrency | No edit-during-fire re-arm race test | Saving an edit converts the trigger… (MAJ-011) |
| Failure-path scheduler | Re-arm on overlap-skip / dispatch-error / retry-exhaustion untested | Exhausted COUNT stops… (CRIT-003) |
| Degrade path (UI) | Occurrences 500 → toast + partial grid has no vitest/e2e row | Occurrences query failure degrades… (MAJ-013) |
| Fallback UI flow | Read-only legacy display + Replace action untested beyond parse unit | Unmappable legacy cron falls back… (MAJ-013) |
| Engine agreement | No endpoint-vs-live-scheduler agreement assertion for `every_ms` | Legacy cron task renders… (MAJ-001) |
| DST boundaries | Only 09:00 tested; nonexistent/ambiguous times missing | DST transition keeps wall-clock time (MAJ-005) |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| RRULE validation | Irregular-gap rule (passes first-pair, violates floor later) | `FREQ=DAILY;BYHOUR=9;BYMINUTE=0;BYSECOND=0,30`, anchor 09:00:15 → 400 |
| RRULE validation | Never-matching rule | `FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=31` → 400 within bounded time |
| RRULE validation | Oversized input | 10KB rrule string → 400 |
| RRULE validation | Single-occurrence rule | `COUNT=1` → accept (MIN-002) |
| Occurrence expansion | Spring-forward nonexistent time; fall-back ambiguous time | 02:30 Europe/Berlin rows, pinned expected instants |
| Occurrence expansion | Past-UNTIL exhaustion | UNTIL yesterday, future range → empty (MIN-003) |
| Editor compile | Clamp round-trip | Save "day 31" → reopen → picker shows "day 31" (MAJ-012) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `GET /tasks/occurrences` | ok | ok | ok | ok | risk | ok | Auth inherits task reads (ok); DoS via per-task expansion cost × unbounded task count, pathological rules (MAJ-007), unnamed rate-limit bucket (MIN-007) |
| `ValidateTrigger` RRULE path | ok | ok | ok | ok | risk | ok | Interval-floor bypass (CRIT-001); unbounded iteration on never-matching rules, no input-size cap (MAJ-007) |
| `TaskTriggerScheduler` re-arm | ok | ok | ok | ok | risk | ok | Availability: silent series death on failure paths (CRIT-003); duplicate fires via race (MAJ-011) |
| Legacy conversion (US-5.3) | ok | ok | risk | ok | ok | ok | One-way destructive rewrite with no audit entry (OBS-004); tz shift on conversion (MAJ-002) |
| RecurrenceEditor (client) | ok | ok | ok | ok | ok | ok | Server-side validation is authoritative (FR-006); client is prevention-only (FR-019) — sound |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. What happens to an occurrence missed while the gateway is down — skipped or replayed, and does it consume COUNT? (MIN-005)
2. In which timezone does the server expand `cron_expr`, and is the server's IANA zone exposed to the SPA? (MAJ-002)
3. How does Command Center edit a recurring task's trigger once raw cron is removed from all forms? (MAJ-010)
4. Does "Does not repeat" produce a `once` trigger or a `due` date, and with what defaults for the fields the panel doesn't show? (MAJ-009)
5. What does the client render when `truncated: true` arrives? (CRIT-002)
6. What opens when a due chip or `once` fire chip is clicked after this feature? (MAJ-003)
7. What does edit mode show for an `every_ms` task's chip, and can `every` still be created anywhere? (MAJ-004)
8. Where does `tz` come from in the editor, and can the operator see/change it? (MAJ-006)
9. Do RRULE at-jobs participate in the cron engine's transient-retry backoff, and how does that coexist with `DeleteAfterRun`? (CRIT-003)
10. Is there any log/metric when a re-arm fails or an expansion truncates, for the 3 AM on-call? (CRIT-003, MAJ-014)

---

## Verdict Rationale

BLOCK on three criticals. CRIT-001 specifies a security-guard algorithm that is provably bypassable for the new trigger type it is being extended to — validation parity with cron is asserted where the mathematical property that makes the cron check sound (minute-aligned periodicity) does not hold. CRIT-002 is an internal contradiction: the endpoint cap and the Month-view rendering promise cannot both be true for the sub-daily tasks that motivated D6, and a listed BDD scenario cannot pass as written. CRIT-003 designs the RRULE re-arm loop against an idealized cron engine rather than the real one (`DeleteAfterRun`, retry backoff, overlap-skip, boot-only reconcile), leaving a silent-death failure mode in the core scheduling path of a shipping product.

The MAJOR cluster is dominated by unmade timezone decisions (MAJ-002/005/006 — three separate decisions, each capable of silently shifting fire instants) and undefined edit-path routing (MAJ-003/004/009/010). None require re-litigating the operator's D1–D7 decisions; all are resolvable by adding normative text, scenarios, and dataset rows. The spec's codebase grounding is otherwise excellent — every cited file, line range, and behavior claim checked out against source.

### Recommended Next Actions

- [ ] Rewrite the min-interval validation as a bounded-window minimum-gap check with adversarial dataset rows — CRIT-001
- [ ] Restructure the occurrences response (per-day buckets above the aggregation threshold) and define client `truncated` handling; fix the sub-daily BDD scenario — CRIT-002
- [ ] Specify re-arm on all RunScheduled exit paths, the retry/DeleteAfterRun interaction, and Reconcile-as-recovery with WARN visibility — CRIT-003
- [ ] Add a normative Timezone Semantics subsection resolving MAJ-002/005/006 in one place
- [ ] Resolve the edit-path routing set: MAJ-003 (due/fire chips), MAJ-004 (`every_ms`), MAJ-009 ("Does not repeat"), MAJ-010 (Command Center)
- [ ] Repair the traceability matrix's phantom test citations and add the degrade + fallback-UI tests — MAJ-013
- [ ] Fold MAJ-007's input bounds and MAJ-014's downgrade statement into the RRULE ADR that Slice 2 already requires

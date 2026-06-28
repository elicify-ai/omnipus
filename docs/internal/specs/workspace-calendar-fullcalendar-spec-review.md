# Grill Report — Workspace Calendar (FullCalendar) Spec **v2 re-grill**

**Reviewed file:** `docs/internal/specs/workspace-calendar-fullcalendar-spec.md` (v2, 2026-06-28)
**Supersedes:** the v1 grill verdict (BLOCK) and the UI/UX review verdict (FAIL) in this same file / `…-spec-uiux-review.md`.
**Mode:** `plan-spec` (full structural checks applied).
**Reviewer mindset:** adversarial / read-only. Every prior CRITICAL/MAJOR and UI/UX CRITICAL was re-verified **against the real codebase**, not the revision log.

---

## 1. Executive Summary

v2 is a genuine, evidence-grounded revision. **Every prior CRITICAL (F-01..F-03), every prior MAJOR (F-04..F-10), and every UI/UX CRITICAL (C-1..C-5) is materially resolved** — verified against the actual contract types, component source, route seam, API signatures, toast store, and npm dist-tags (not merely asserted in the revision log). The version correction is right: v7.0.0 is on the `next` dist-tag, `latest` is `6.1.21`, and 6.1.21's React peer includes `^19` (all confirmed via `npm view`).

The re-grill surfaced **one new MAJOR defect introduced by the v2 colour-contrast work**: the spec now asserts a hard, measurable **≥7.5:1** contrast floor in two places (§6 and **SC-006**), but its own chosen `failed`-status red `#F87171` yields **7.15:1** on near-black text — below the stated floor. The accessibility *intent* is fine (7.15:1 still clears WCAG AAA 7:1), but the spec contradicts its own numeric success criterion, so SC-006 would fail an automated contrast assertion as written. Plus a handful of MINOR loose ends (firstDay not codified as an FR, an unverifiable temporal-polyfill rationale, milestone-write format round-trip not pinned end-to-end).

**Findings:** 1 MAJOR · 5 MINOR · 3 OBSERVATION. No CRITICAL.

**Verdict: PASS WITH NOTES.** Fix N-01 (the ≥7.5:1 vs 7.15:1 contradiction) — a one-line edit — and the spec is ready for `/taskify`. The MINORs are cleanups, not blockers.

---

## 2. Verification of prior findings (the core of this re-grill)

### Prior CRITICALs

| Prior ID | Claim resolved? | Evidence (real codebase) | Verdict |
|---|---|---|---|
| **F-01** `surface:"system"` invalid | **RESOLVED** | `contracts/components/schemas/Task.yaml:148-153` enum = `user\|heartbeat`; `schemas.ts:377,510,534`. Spec US-1/AS-3, the BDD scenario, and DS-1 row 4 now all use `"heartbeat"`. Logic stays `surface !== 'user'`. | ✅ Genuine |
| **F-02** reused create form can't emit date-only `due` | **RESOLVED (correctly re-scoped)** | `CreateTaskSlideOver.tsx` **does** have `due: string // datetime-local` in `FormState` (:71), seeded `''` (:87), and submit path `if (form.due){ const iso = datetimeLocalToIso(form.due); body.due = iso }` (:162-164). `datetimeLocalToIso` (`taskFormFields.ts:55-59`) = `new Date(value).toISOString()` → **always a full datetime**. v2 accepts this: create → local-midnight datetime; drag → date-only `YYYY-MM-DD`; read-map → local date (§7 Non-Behavior, A6). The contradiction is gone — no all-day-mode component change is required. | ✅ Genuine |
| **F-03** jsdom can't render/drag FC | **RESOLVED** | `vite.config.ts:29` `environment:'jsdom'`, `:33` `css:false`. §9 header now states grid/placement/drag/dateClick/focus are **E2E-only** (#22 Playwright); units cover pure mapping + handler wiring with FC mocked. Test pyramid re-levelled. | ✅ Genuine |

### Prior MAJORs

| Prior ID | Claim resolved? | Evidence | Verdict |
|---|---|---|---|
| **F-04** status→colour map inconsistent; `next`=gold? | **RESOLVED** | Existing screen maps `inbox\|next\|planning` → muted (`CalendarScreen.tsx:112-113` `text-[#E2E8F0]/60 bg-white/5`). v2 canonical table (§6) pins `next/inbox/planning = slate #94A3B8 + Circle`, explicitly "overrides v1's stray gold." All 7 statuses + milestone + fire covered. | ✅ Genuine |
| **F-05** PATCH whole `TaskTrigger`, preserve siblings | **RESOLVED** | `TaskUpdateRequest.trigger: TaskTrigger` (`schemas.ts:530`); `TaskTrigger.config` = `Partial<{at_ms,every_ms,cron_expr} & {[k]:any}>` (`:408-419`). FR-008 + US-3/AS-2 + test #11 + DS-2 row 2 now mandate `{...task.trigger, config:{...config, at_ms}}` and test sibling preservation. | ✅ Genuine |
| **F-06** TZ-safe date conversion rule | **RESOLVED** | Edge case + FR-015 pin `new Date(y,m-1,d)` for date-only (local, no UTC shift) and `YYYY-MM-DD` local on drag-writes; test #8 / DS-1 row 9 assert under `TZ=America/Los_Angeles`. | ✅ Genuine |
| **F-07** controlled-open + prefill ownership | **RESOLVED** | Component is controlled (`open,onOpenChange,workspaceId,milestoneId` — `CreateTaskSlideOver.tsx:48-55`). §4 + FR-012 now state `CalendarScreen` owns `open`, adds optional `initialDue?`, seeds via `useEffect([open, initialDue])`, props optional → existing callers unaffected; test #16 guards the regression. | ✅ Genuine |
| **F-08** route seam = named-export adapter, not default | **RESOLVED** | `workspaces.$workspaceId.calendar.tsx:7-9` = `import('@/components/screens/CalendarScreen').then((m)=>({default:m.CalendarScreen}))`. §4 line 66 now describes this `.then(...)` mapping verbatim and says preserve it (not a default export). | ✅ Genuine |
| **F-09** `updateMilestone` 3-arg + format | **RESOLVED** | `api.ts:2807` `updateMilestone(workspaceId, milestoneId, body)` (a PUT; body `MilestoneUpdateRequest`, `.partial().passthrough()`, `due_date: string.nullable()` — `schemas.ts:2174-2181`). FR-008/US-3/AS-3/§7 now thread `(workspaceId, milestone.id, {due_date:'YYYY-MM-DD'})`; event id encodes `milestone:{id}` (F-19) so the handler has the id. | ✅ Genuine (see N-04 for the round-trip note) |
| **F-10** drifting `every` marker | **RESOLVED** | Existing screen computes `every` as `new Date(Date.now()+every_ms)` per render (`CalendarScreen.tsx:171`) — the drift the prior grill flagged. v2 §5/§13-A5/test #3/DS-1 row 3 defer **both** `every` and `recurring` until server `next_fire_at`. | ✅ Genuine |

### Prior MINOR/OBSERVATION carried into v2

- **F-11** (anchor date) → RESOLVED: US-2/AS-1 + §8 now say "the week containing `calendarApi.getDate()`."
- **F-12** (no-telemetry) → RESOLVED: explicit §7 Non-Behavior.
- **F-13** (version rationale) → RESOLVED **and the prior grill was itself wrong**: the v1 grill claimed "v7.0.0 is now GA." `npm view @fullcalendar/core dist-tags` = `{latest: 6.1.21, next: 7.0.0}`. v7 is **not** on `latest`. v2's §1/A9 statement is the accurate one. (See N-02 for the one sub-claim I could not verify.)
- **F-14** (grid loading/error states) → RESOLVED: FR-001/US-1/AS-6 + degrade Non-Behavior + tests #20/#21.
- **F-15** (out-of-range timed events) → RESOLVED: Edge + A7 (06:00–22:00 + `scrollTime`, surfaced in Month/Agenda).
- **F-16** (E2E fault injection) → RESOLVED: Playwright route interception of one PATCH→500 (replaces the prior "kill the gateway").
- **F-17** (wrapper in `ui/`) → RESOLVED: co-located `components/calendar/FullCalendarView.tsx`, "single consumer."
- **F-19** (event-id scheme) → RESOLVED: promoted into §6 (`task:{id}:due`, `task:{id}:fire`, `milestone:{id}`).
- **F-20** (SC-007 too weak) → RESOLVED: SC-007 now asserts FC libs land in the **calendar-route chunk specifically**.
- **F-18** (concurrent-edit flicker) → addressed as accepted silent reconcile (Edge).

### UI/UX CRITICALs

| Prior ID | Resolved? | Evidence | Verdict |
|---|---|---|---|
| **C-1** drag has no keyboard/single-pointer path | **RESOLVED** | FR-009 + US-3/AS-7,8 + tests #17/#18: Enter/Space on task chip → `TaskDetailSlideOver`; on milestone chip → new `MilestoneDatePopover`. | ✅ |
| **C-2** colour-only status | **RESOLVED** | Per-status Phosphor icon in every canonical row (§6); FR-005 cites WCAG 1.4.1; test #9 asserts the icon column. | ✅ |
| **C-3** chip text fails contrast | **RESOLVED (intent)**, but see **N-01** | Chip text pinned `#0A0A0B`. Real contrasts (computed this pass): gold 9.41, emerald 10.29, blue 7.78, amber 11.85, red **7.15**, slate 7.72 — all clear WCAG AA (4.5) and AAA (7:1). | ✅ a11y; ❌ self-stated 7.5 floor → N-01 |
| **C-4** focus management | **RESOLVED** | FR-013 + US-4/AS-4 + US-5/AS-1,2: focus into dialog on open, restore to triggering chip/cell on close; #22 covers it. | ✅ |
| **C-5** milestone click = keyboard dead-end | **RESOLVED** | `MilestoneDatePopover` on click/Enter (no silent no-op); §7 Non-Behavior + test #18. | ✅ |

UI/UX IMPORTANT (I-1..I-9) and MINOR (M-1..M-5) are folded in (loading-vs-empty I-1/#20; milestone-fail toast I-2/#21; click affordance I-3/US-4/AS-5; 44px chips I-4/FR-007; undo I-5 + success I-6/FR-010; themed popover I-7/Edge; aria-live I-8 — **already implemented**, see below; two-row toolbar I-9/FR-007; DST M-1; now-indicator M-2/FR-017; reduced-motion M-5/FR-017). **M-4 (`firstDay`) is the one not codified as an FR** — see N-03.

> **I-8 note:** the spec hedges "Verify [the toast store] sets `aria-live`; if not, that's an added requirement." It already does — `toast-container.tsx:18` `toastRole = variant==='error' ? 'alert' : 'status'`, and the store supports `action` (label+onClick) and `duration`, so the 5-second Undo toast (I-5) is buildable with no store change. The spec can drop the hedge and state it as satisfied.

---

## 3. New findings introduced/left by the v2 edits

| ID | Sev | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| **N-01** | **MAJOR** | Infeasibility / Incorrectness | §6 contrast claim; **SC-006** | The spec asserts a measurable **≥7.5:1** contrast floor twice ("passes WCAG 1.4.3 **≥7.5:1** on every bg", §6; "contrast **≥7.5:1**", SC-006). The `failed` chip bg red **`#F87171`** against `#0A0A0B` text computes to **7.15:1** — **below 7.5**. SC-006 is a success criterion an automated check (or a reviewer) will run; it **fails as written** for one of the seven chips. (Blue 7.78 and slate 7.72 sit just above, so the 7.5 floor is fragile generally.) | Restate the threshold to **≥7:1 (WCAG AAA)** in §6 and SC-006 — all six bgs (min 7.15) clear it — **or** darken the `failed` red (e.g. `#EF4444` → 8.05:1, or `#F05252` → ~7.6:1) so the literal 7.5 holds. Pick one; do not leave a numeric SC the chosen palette violates. |
| **N-02** | MINOR | Incorrectness (rationale) | §1 Dependency choice; A9 | The version *pin* is correct, but the supporting rationale "[v7] pulls a `temporal-polyfill` runtime dependency" is **unverified**. `npm view @fullcalendar/core@7.0.0 dependencies` returned empty here (the polyfill, if present, may live in `@fullcalendar/core/internal` or a peer, not the top-level `dependencies`). The decision survives on the verified facts alone (v7 = `next`-tag only, 6.1.21 = `latest` with a `^19` React peer, matches the template). | Either confirm the polyfill dependency against the actual v7 install tree before citing it, or soften to "v7 is on the `next` dist-tag and carries the Temporal-API migration cost" without the specific `temporal-polyfill` claim. Don't ground a decision on an unchecked dependency assertion. |
| **N-03** | MINOR | Incompleteness | §3 ref table; §10 FRs | `firstDay={1}` (Monday-first) appears **only** in the §3 reference-pattern row — it is **not** an FR or SC. FullCalendar defaults to Sunday; without a codified requirement, an implementer can ship Sunday-first and pass every test. (This was UI/UX M-4 — partially carried, not closed.) | Add to FR-017 (or a small new FR): "Week starts Monday (`firstDay={1}`)." One clause. |
| **N-04** | MINOR | Incompleteness | §6 Edge (TZ); §7; FR-015 | F-06's TZ rule is pinned for **tasks** (`due` date-only via `new Date(y,m-1,d)`; drag emits `YYYY-MM-DD`). The **milestone** read-path round-trip is asserted ("same off-by-one avoided") but not pinned the same way: the spec doesn't state that `milestone.due_date` is parsed with the identical component-construction rule and that the milestone list view (which also renders `due_date`) agrees on the format. A milestone written `YYYY-MM-DD` then read elsewhere with `new Date(str)` would shift a day in `TZ<0`. | Make FR-015 explicitly cover `milestone.due_date` (parse + write) with the same rule, and add a one-line check that the milestone list/board renders the same date for a calendar-rescheduled milestone (or a unit row in DS-1 for `milestone{due_date:"2026-06-25"}` under `TZ=America/Los_Angeles`). |
| **N-05** | MINOR | Ambiguity | §6 fire chip; §9 #9 | The `once`-fire chip row says icon = `Clock` "(overrides)" the per-status icon, but the canonical-map test (#9, "all 7 + milestone + fire") and DS-1 row 10 (per-status icon table) don't state which icon the fire chip carries when the task also has a status (e.g. a `blocked` task with a `once` trigger → does its `:fire` chip show `Clock` or `Prohibit`?). The text implies `Clock` always wins on the `:fire` chip; make the test assert it. | State in §6 and in test #9/#6: "the `:fire` chip always uses `Clock` regardless of status; the `:due` chip uses the status icon." Add the assertion to test #6 (both-chips) so the override is covered, not just described. |
| **N-06** | OBSERVATION | Inoperability | §11 SC-006 | SC-006 bundles two independently-measurable claims (icon presence + `#0A0A0B` text contrast) with "axe serious/critical = 0." axe does **not** check contrast of text on a coloured background it can't compute against a CSS variable reliably, and won't assert the icon cue — so "axe = 0" is necessary but not sufficient for SC-006's contrast/icon parts. | Split SC-006: (a) a contrast assertion with explicit per-status expected ratios (the table computed above), (b) an icon-presence assertion, (c) axe = 0 for the rest. Three crisp checks beat one bundled one. |
| **N-07** | OBSERVATION | Overcomplexity | §4 new files | Four new modules + one CSS file for one screen is justified (mapping is pure/testable; toolbar/popover are distinct concerns). No over-abstraction found — `FullCalendarView` is explicitly single-consumer and co-located, not promoted to `ui/`. Noting for completeness that the lens applies and finds nothing blocking. | None. The decomposition is proportionate. |
| **N-08** | OBSERVATION | Incompleteness | §5; FR-014 | Spec says "milestone creation from the calendar" is out of scope, and milestone *reschedule* is in. With `MilestoneDatePopover` editing `due_date`, confirm the popover cannot also *clear* `due_date` to null (the schema allows `null`) — an accidental clear would drop the milestone off the calendar with no undo path (undo is specced for task/milestone *reschedule*, not for a null). | Optionally state the popover only sets a date (no clear), or extend the undo affordance to a null-set. Non-blocking. |

---

## 4. Structural Integrity Results (plan-spec checks) — v2

| Check | v1 | v2 | Note |
|---|---|---|---|
| Every user story ≥1 acceptance scenario | PASS | PASS | US-1..US-5 intact. |
| Every acceptance scenario ≥1 BDD scenario | PASS | PASS | All trace. |
| Every BDD scenario has `Traces to:` | PASS | PASS | All 28 scenarios trace. |
| Every BDD scenario has a corresponding test | PARTIAL | **PASS (substantively)** | Prior gaps closed: navigate → #19; milestone-click → #18. Phone-two-row/44px (#22) and cancel-create focus (#22) ride the E2E umbrella — the correct level for responsive/touch geometry, not a structural defect. |
| Every FR in traceability matrix | PASS | PASS | FR-001..FR-017 all in §12. |
| Every BDD scenario in matrix | PARTIAL | PASS (FR-indexed + test-mapped) | Matrix is FR-indexed but each FR now lists concrete test #s incl. E2E. |
| Datasets cover boundary/edge/error | PARTIAL (used invalid `system`) | **PASS** | DS-1 fixed to `heartbeat`; TZ row #9 added; every/recurring/null rows present. |
| Regression impact addressed | PASS (conditional) | **PASS** | `CreateTaskSlideOver` optional-prop regression real now that `due`/`initialDue` is additive (test #16). |
| SC measurable / no subjective language | PARTIAL | **PARTIAL** | SC-003 (300 ms), SC-004 (≥99%), SC-007 (chunk graph) are crisp; **SC-006 contains a wrong numeric floor (N-01)** and bundles three checks (N-06). "Sovereign-Deep theming" still has no measured SC, but FR-017 + axe + the contrast table cover the substance. |

---

## 5. Test Coverage Assessment — v2

- **Re-levelled correctly (F-03):** pure mapping (#1–#9, DS-1) as unit; handler wiring with FC mocked (#10–#21); real render/drag/dateClick/focus at E2E (#22). This is the right pyramid given `jsdom + css:false`.
- **New negative/edge coverage added:** sibling-config preservation (#11/DS-2 row 2), TZ off-by-one (#8/DS-1 row 9), milestone 3-arg (#12), degrade (#21), loading-no-flash (#20). The prior "missing negative tests" gaps are closed.
- **Remaining test gap (N-05):** the `:fire`-chip icon override is described but not asserted in #6/#9.
- **Milestone round-trip (N-04):** no dataset row pins milestone `due_date` TZ-safety the way task `due` is pinned.

---

## 6. STRIDE Threat Summary — unchanged (inherited surface)

| Component / flow | Threats | Notes |
|---|---|---|
| `GET /tasks`, `GET /milestones` | Information Disclosure | Reuses existing authz'd reads. No new exposure. |
| `PATCH /tasks`, `POST /tasks`, `PUT …/milestones/{id}` | Tampering, Elevation, Repudiation | All reuse existing authorized, audited endpoints. Repudiation of a drag = whatever `PATCH /tasks` already audits (§7 Non-Behavior, no new telemetry per charter). |
| `CalendarEvent` view model | — | Internal, `not-wire-format`; correctly excluded from Constraint #8. |

No new entry points, secrets, or endpoints. The security-relevant risk remains **write-payload correctness** (F-05/F-06/F-09) — all now pinned. Nothing to block.

---

## 7. Unasked Questions (v2)

1. **Does the chosen palette actually meet the spec's own 7.5:1 floor?** No — red is 7.15:1 (N-01). Restate the floor or recolour red.
2. **Is the `temporal-polyfill` claim true for v7?** Unverified here (N-02) — don't ground the decision on it.
3. **Where is Monday-first codified?** Only in a reference row, not an FR (N-03).
4. **Does the milestone `due_date` round-trip use the same TZ-safe rule as task `due`?** Asserted, not pinned (N-04).
5. **Which icon does a `once`-trigger chip on a non-default-status task show?** `Clock` per the text, but untested (N-05).
6. **Can `MilestoneDatePopover` null the `due_date`, and if so is that undoable?** Unspecified (N-08).

---

## 8. Verdict

**PASS WITH NOTES.**

v2 resolves all three prior CRITICALs, all seven prior MAJORs, and all five UI/UX CRITICALs — verified against the real contract, components, route, API signatures, toast store, and npm registry, not the revision log. The spec is implementable as written with one literal-correctness fix.

- **Must fix before `/taskify`:** **N-01** — the `≥7.5:1` floor (§6 + SC-006) is violated by the spec's own `failed` red (7.15:1). One-line edit: change the floor to `≥7:1 (AAA)` or darken the red. This is the only finding that makes a stated success criterion fail on contact.
- **Should fix (cheap, non-blocking):** N-03 (codify `firstDay={1}` as an FR), N-04 (pin milestone `due_date` TZ round-trip), N-05 (assert the `:fire` icon override), N-02 (verify or soften the polyfill rationale).
- **Optional:** N-06 (split SC-006), N-08 (milestone null-clear undo).

No CRITICAL findings; no architectural change required.

```
Verdict: PASS WITH NOTES

Review written to: docs/internal/specs/workspace-calendar-fullcalendar-spec-review.md

Address N-01 (and ideally the MINORs), then proceed:
  /taskify docs/internal/specs/workspace-calendar-fullcalendar-spec.md
```

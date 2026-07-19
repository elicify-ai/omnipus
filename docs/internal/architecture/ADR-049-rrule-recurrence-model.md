# ADR-049: RRULE (RFC 5545) as the Recurrence Model for Task Triggers

**Status:** Accepted
**Date:** 2026-07-19
**Deciders:** architect (+ backend-lead, frontend-lead, qa-lead for implementation/review)

> **Ratification ADR.** The headline decisions (D1–D10) were made interactively with the
> operator on 2026-07-19 and are recorded in
> `docs/internal/specs/calendar-recurrence-redesign-spec.md` ("Decisions Locked" +
> "Clarifications"). The spec was grilled over three rounds. This ADR ratifies the
> architecture and — as the spec requires for its Slice 2 — records the two policies the
> spec explicitly delegates to the ADR: the **input-bounds policy** (FR-006) and the
> **downgrade statement** (Operations & Rollback). It is a ratification, not a fresh
> exploration; the trade-off analysis below is the justification, not a re-open of the
> decision.

## Context

Recurring task triggers were stored as raw **cron expressions** (`trigger.config.cron_expr`),
entered through a raw text `<Input>` with no client validation. Two problems:

1. **Cron cannot express common human schedules** — "every 2 weeks on Monday" has no cron
   form. Non-technical operators cannot use recurring tasks at all.
2. Recurring tasks were **invisible on the calendar** (a deliberate v1 cut, F-10) and mixed
   into the Board/List where a recurring card can never reach "Done".

The operator asked for an Outlook/Google-style recurring-meeting UI with full power
(including intervals like "every 2 weeks"), every occurrence rendered on the calendar, and
recurring tasks moved out of Board/List into the calendar exclusively.

The scheduler seam is **high-risk**: `TaskTriggerScheduler` (`pkg/agent/task_trigger.go`)
executes *all* time triggers, including workspace **heartbeats**. Any recurrence change must
be purely additive to the legacy `at`/`every`/`cron` translation.

## Decision

**Adopt RRULE (RFC 5545) as the recurrence model for task triggers** (D1), expanded
**server-side** as the single authority on fire times. Concretely:

- `trigger.config` gains three **additive** keys on `type: recurring` — `rrule` (RFC 5545
  body), `dtstart_ms` (anchor, unix ms), `tz` (IANA zone) — carrying exactly one of
  `cron_expr` (legacy) *or* `rrule`. No enum change; the wire shape stays intact
  (Constraint #8, contract-first).
- **Server-side expansion is the sole occurrence authority.** A new pure-Go engine
  (`pkg/task/rrule.go`, built on `github.com/teambition/rrule-go`, MIT, no CGo) expands
  occurrences for the calendar, validation, and the scheduler — *one* implementation the
  endpoint, validator, and scheduler all share, so they can never disagree
  (Timezone Semantics §4). The client `rrule` npm lib (BSD-3) is used **only** for
  building/parsing rule strings and summary text in the editor — **never** for occurrence
  computation or display.
- **Occurrences render on the calendar** via a new read-only endpoint
  `GET /api/v1/tasks/occurrences` returning a bucketed `TaskOccurrenceSet[]` (individual
  instants for ≤3/day, aggregated `DayBucket`s for >3/day in overview ranges).
- **Recurring tasks are calendar-only** (D3): excluded from Board/List (presentation-layer
  predicate; the store/REST still return them), created/edited via a calendar-specific
  event slide-over. The raw-cron input is removed from **every** UI surface; cron survives
  under the hood only (engine, API, heartbeats).
- **No migration / no back-compat for legacy triggers** (D8): existing `cron_expr`/`every_ms`
  tasks keep firing untouched and render on the calendar, but the UI never translates them —
  editing one offers only a *fresh* rule that overwrites to RRULE, with no fire-time
  equivalence guarantee. There is no reverse-mapping machinery.

## Options Considered

| Option | Verdict | Why |
|--------|---------|-----|
| **A. Keep cron, prettier compile UI** | Rejected | Cron structurally cannot express "every N weeks" / "every 2nd Tuesday" — the operator's headline requirement. A nicer UI over cron hits the same wall. |
| **B. RRULE, server-side expansion** *(chosen)* | **Accepted** | Full RFC-5545 power; one server-side authority avoids client/server fire-time drift; pure-Go lib keeps the single-binary/no-CGo constraints; the client lib is confined to editor text. |
| **C. RRULE, client-side expansion (`@fullcalendar/rrule`)** | Rejected | Two occurrence engines (client render vs server fire) *will* diverge on DST/clamp edge cases; the calendar would show times the scheduler doesn't fire. Violates "server is the single authority". |
| **D. Bulk-migrate legacy cron→RRULE on upgrade** | Rejected (D8) | Timezone-preserving cron→RRULE conversion is lossy and risky; the operator explicitly chose "no migration". Legacy tasks keep firing; conversion happens only on an explicit user edit, audit-logged. |

## Consequences

### Input-bounds policy (FR-006 — recorded here per the spec's mandate)

RRULE is a small language; unbounded input is a validation-time DoS surface. `ValidateTrigger`
rejects (400, `ErrValidation`) any rule that violates these bounds, completing within ~1 s:

- Exactly one of `cron_expr` / `rrule`; `rrule` requires `dtstart_ms` + `tz`; `tz` must load
  (embedded `time/tzdata`); the RRULE must parse. The `rrule`/`dtstart_ms`/`tz` keys are legal
  **only** on `type: recurring`.
- `rrule` length ≤ **512** characters.
- Reject `FREQ=SECONDLY`; reject any `BYSECOND` value other than the DTSTART second (the editor
  can produce neither — only hand-crafted API payloads can). **These §2 hard-rejects are the
  operative sub-minute guard.**
- `UNTIL` (when present) ≥ `dtstart_ms`.
- `COUNT` (when present) ≤ **100,000** — this bounds every COUNT-exhaustion check and the
  DTSTART skip-walk.
- **Bounded-window minimum-gap scan (defense-in-depth):** expand the first
  `min(60 occurrences, 366 days)` from DTSTART and reject any consecutive pair < 60 s apart.
  After the §2 rejects this scan is expected to have no reachable rejection; it is retained
  against library quirks and future rule-shape growth, not as the operative mechanism.
- **Liveness bound:** reject rules producing zero occurrences within 5 years of DTSTART
  ("rule never fires") — this also bounds worst-case work on never-matching rules (e.g.
  Feb 31). Validation is O(window).

The validator reuses the **same** parse/expand code path as the expansion engine, so its
interpretation can never drift from what actually fires.

### DST policy is owned by the expansion layer (not the library, not the stdlib)

Occurrences are wall-clock in the rule's `tz`. A spring-forward **nonexistent** local time
resolves to wall-clock **+ gap length** (02:30 → 03:30 across a 1 h gap); a fall-back
**ambiguous** time takes the **first** (pre-transition) occurrence; two rule occurrences that
normalize to the **same instant** collapse to a single fire (dedup). This policy is
**normative and owned by `pkg/task/rrule.go`** — it re-derives the gap-shift explicitly (via
`Time.ZoneBounds`) rather than depending on undocumented Go `time.Date` behavior, and corrects
rrule-go's native fall-back resolution (which picks the second instant). Pinned by
`TestRruleExpansion_WallClockDST`. If a future stdlib or library change alters the underlying
behavior, the policy — and its tests — remain the authority.

### Downgrade is a one-way door (accepted, no feature flag)

After any RRULE task exists, downgrading to a pre-feature binary means the old
`triggerToCronSchedule` fails on the missing `cron_expr`; `OnTaskUpserted` logs WARN and skips
registration — **the RRULE task stops firing**, and the old SPA renders nothing for it. **Data
is preserved verbatim**; re-upgrading restores scheduling and rendering. There is no feature
flag and no gradual rollout. **Operators rolling back a hotfix must check the gateway log for
these WARNs** and understand that RRULE-scheduled work is paused until re-upgrade.

### Other consequences

- **Single binary / minimal systems:** the binary embeds `time/tzdata` so IANA zones resolve
  without a system zoneinfo (Constraint #1).
- **New dependencies:** `github.com/teambition/rrule-go` (Go, MIT, no CGo — Constraint #2) and
  `rrule` (npm, BSD-3, editor-only). Both flagged in the spec's Integration Boundaries.
- **Scheduler additivity:** RRULE triggers arm as `kind:"at"` jobs re-armed on every fire
  (the re-arm is the sole series continuation; no retry-backoff — "the next occurrence is the
  retry"), guarded by a mutex-atomic trigger-generation replace-by-task for the edit-during-fire
  race, with a boot `Reconcile` + a 5-minute recovery sweep. The legacy `at`/`every`/`cron`
  translation is byte-for-byte unchanged and regression-tested — **heartbeats are unaffected**.
- **Open legacy class:** `cron_expr` remains creatable via the raw API and agent tools, so the
  read-only-fallback legacy edit path is permanent, not transitional. A follow-up issue steers
  the `create_task` tool description toward RRULE.

## References

- Spec: `docs/internal/specs/calendar-recurrence-redesign-spec.md` (Rev 6; grilled rounds 1–3).
- Precedent for additive `config` growth: `TaskTrigger.yaml` ("open growth surface").
- Related: ADR-046 (unified filesystem/workspace model) — this ADR is the next in sequence.

//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// task_occurrences.go — the pure, unit-testable expansion + bucketing
// orchestration behind GET /api/v1/tasks/occurrences (Calendar Recurrence
// Redesign, docs/internal/specs/calendar-recurrence-redesign-spec.md,
// "Occurrence expansion endpoint" / FR-008 / FR-008a). The REST handler
// (HandleTaskOccurrences, rest_tasks.go) does query-param parsing, the
// task-selection predicate, and HTTP status mapping; everything below is a
// plain function of (tasks, range, query tz, an every_ms anchor lookup) so
// tests 7/8 (TestOccurrences_LegacyCronAndEveryMs,
// TestOccurrences_BucketingAndCaps) exercise it directly without linking the
// full gateway test binary.
//
// Three trigger flavors, three expansion strategies:
//   - rrule (task.ExpandRRULE / task.IsRegular / task.RegularPeriodMs /
//     task.CountRegularInRange) — provably-regular rules (no BY* modifiers)
//     get O(1) arithmetic bucket derivation; irregular rules iterate and
//     consume the 10,000-occurrence budget.
//   - cron_expr (legacy, expandCronServerZone below) — always expanded by
//     walking gronx.NextTickAfter in the SERVER's local zone (Timezone
//     Semantics §2, D8 display-only), matching pkg/cron/service.go's own
//     computeNextRun exactly (same time.UnixMilli + gronx.NextTickAfter
//     call shape) so the endpoint and the live scheduler can never disagree
//     — always consumes the iteration budget (no arithmetic shortcut for
//     cron).
//   - every_ms (legacy, FR-008a) — always provably regular: countEveryMsInRange
//     below derives per-day counts by pure modular arithmetic, never
//     consuming the iteration budget, projected forward from the live
//     job's NextRunAtMS (the everyAnchor callback) since the engine keeps
//     no stored DTSTART-like anchor for `every` jobs.
//
// Bucketing (D6): span ≤ 8×24h ("detail" mode, Week/Day views) returns raw
// instants for every day, one whole-range call per task. span > 8×24h
// ("overview" mode, e.g. Month) walks every QUERY-TZ calendar day
// intersecting the range and buckets any day with more than 3 occurrences
// — the query tz is the day-boundary authority for ALL flavors regardless
// of each rule's own tz (MAJ-202).

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/adhocore/gronx"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

const (
	// perTaskInstantCap is the hard ceiling on TaskOccurrenceSet.occurrences_ms
	// (raw, non-bucketed instants) per task per request — applies
	// unconditionally, regardless of trigger regularity (the wire cap
	// described on DayBucket.yaml/TaskOccurrenceSet.yaml is unqualified;
	// only the SEPARATE 10,000 iteration budget below carries the
	// regular-rule exemption). Hitting this cap always sets truncated=true
	// so the client never silently renders an incomplete range as
	// authoritative.
	perTaskInstantCap = 500

	// perTaskIterationBudget is the total number of occurrences an
	// IRREGULAR (BY*-modified rrule, or any cron_expr) trigger may walk
	// per task per request in overview (bucketed) mode, counting every
	// occurrence enumerated to build a DayBucket's count as well as any
	// destined for occurrences_ms (spec: "counting bucket enumeration").
	// Provably-regular triggers (rrule with no BY* modifiers, every_ms)
	// NEVER decrement this — their bucket counts are derived in O(1) via
	// task.CountRegularInRange / countEveryMsInRange, never iterated, so a
	// dense regular rule (e.g. plain FREQ=MINUTELY) over a 400-day span
	// renders complete buckets with truncated:false.
	perTaskIterationBudget = 10000

	// detailSpanThresholdMs is the ≤8×24h / >8×24h boundary (D6) between
	// "detail" mode (raw instants for every day — Week/Day views, including
	// the 169h DST fall-back week, which is < 192h and so lands here
	// automatically with no special-casing) and "overview" mode (per-day
	// bucketing — Month and other broad ranges).
	detailSpanThresholdMs = int64(8 * 24 * time.Hour / time.Millisecond)

	// maxOccurrenceRangeSpanMs is the endpoint's hard span ceiling: a
	// requested [from_ms, to_ms) wider than 400 days is rejected with 400
	// (spec "Occurrence expansion endpoint"). Enforced by the REST handler
	// (HandleTaskOccurrences, rest_tasks.go) before buildOccurrenceSets is
	// ever called.
	maxOccurrenceRangeSpanMs = int64(400 * 24 * time.Hour / time.Millisecond)
)

// wireRunCounts mirrors the gen.TaskOccurrenceSet.DayBuckets[].RunCounts
// inline type (ADR-050 RD6, task-run-history-spec.md §3.7) — a per-day run-status
// tally computed by scanning that day's actual TaskRun records (see
// populateBucketRunCounts), NOT by enumerating RRULE members.
// not-wire-format: local alias for the generated inline struct; see
// gen.TaskOccurrenceSet.DayBuckets[].RunCounts / DayBucket.yaml for the
// authoritative shape.
type wireRunCounts = struct {
	Done       int32 `json:"done"`
	Failed     int32 `json:"failed"`
	InProgress int32 `json:"in_progress"`
	Scheduled  int32 `json:"scheduled"`
	Skipped    int32 `json:"skipped"`
}

// wireDayBucket mirrors the element type of gen.TaskOccurrenceSet.DayBuckets
// — oapi-codegen inlines the DayBucket.yaml $ref as an anonymous struct
// there rather than emitting []gen.DayBucket, so a local alias with the
// IDENTICAL field order/names/types/json-tags is required for direct
// assignability (same technique as wireTodo in rest_tasks.go).
// not-wire-format: local alias for the generated inline struct; see
// gen.TaskOccurrenceSet.DayBuckets / contracts/components/schemas/DayBucket.yaml
// for the authoritative shape.
//
// RunCounts (ADR-050 RD6, task-run-history-spec.md §3.7) is an additive field on
// the wire schema, populated by populateBucketRunCounts (called from
// buildOneOccurrenceSet via populateRunOverlay) — nil/absent when no
// runsInRange dependency was supplied or no run fell in that day.
//
// DayEndMs (delta-review HIGH fix, DayBucket.yaml) carries the bucket's own
// exclusive window end — the civil next-midnight boundary populateBucketRunCounts
// already computes via civilDayNext — on the wire, so the client never
// recomputes it with a fixed-24h assumption that diverges from the server's
// DST-aware tally window on transition days. Populated unconditionally in
// buildOverview, unlike RunCounts which is conditionally nil.
type wireDayBucket = struct {
	Count      int32          `json:"count"`
	DayEndMs   int64          `json:"day_end_ms"`
	DayStartMs int64          `json:"day_start_ms"`
	FirstMs    int64          `json:"first_ms"`
	IntervalMs *int64         `json:"interval_ms"`
	RunCounts  *wireRunCounts `json:"run_counts,omitempty"`
}

// wireOccurrenceRun mirrors the element type of
// gen.TaskOccurrenceSet.OccurrenceRuns (ADR-050 RD6, task-run-history-spec.md
// §3.7) — oapi-codegen inlines the object schema there rather than emitting
// a named type, so a local alias with the IDENTICAL field order/names/
// types/json-tags is required for direct assignability (same technique as
// wireDayBucket above). Field order matters for Go struct-type identity:
// HasResult, OccurrenceMs, RunId, SessionId, Status — matching the
// generated code exactly.
// not-wire-format: local alias for the generated inline struct; see
// gen.TaskOccurrenceSet.OccurrenceRuns / TaskOccurrenceSet.yaml for the
// authoritative shape.
type wireOccurrenceRun = struct {
	HasResult    bool                                      `json:"has_result"`
	OccurrenceMs int64                                     `json:"occurrence_ms"`
	RunId        string                                    `json:"run_id"`
	SessionId    string                                    `json:"session_id"`
	Status       gen.TaskOccurrenceSetOccurrenceRunsStatus `json:"status"`
}

// buildOccurrenceSets is the pure orchestration entry point: expands every
// eligible task's trigger within [fromMs, toMs) and returns one
// gen.TaskOccurrenceSet per task with at least one occurrence, bucketed per
// the query tz. tasks is assumed already filtered by the caller to the
// scheduler-arming selection predicate (non-heartbeat, and either
// non-terminal OR terminal-but-repeating with a non-exhausted series — see
// HandleTaskOccurrences / task.Task.SeriesRetired) and to the requested
// workspace; this function only additionally filters by trigger FLAVOR
// (recurring-capable: `recurring` with rrule/cron_expr, or `every`) since
// manual/once triggers render via the existing due/fire-chip path, not this
// endpoint.
//
// everyAnchor resolves an `every`-triggered task's live armed NextRunAtMS
// (FR-008a's projection anchor); returning ok=false omits that task (no
// live anchor to project from).
//
// The only error path is an unloadable tz or an invalid range — both are
// already validated by the REST handler before this is called, so in
// production this only ever returns a nil error; the check is retained here
// so the function is independently correct for tests 7/8 that call it
// directly without going through the REST validation.
// runsInRangeFn is the occurrence-run overlay's store dependency (ADR-050
// RD6, task-run-history-spec.md §3.7): folds a task's runs whose occurrence_ms falls
// in [fromMs, toMs). Satisfied in production by task.Store.RunsInRange.
type runsInRangeFn = func(taskID string, fromMs, toMs int64) ([]task.TaskRun, error)

func buildOccurrenceSets(
	tasks []task.Task,
	fromMs, toMs int64,
	tz string,
	everyAnchor func(taskID string) (int64, bool),
	// runsInRange is a trailing VARIADIC dependency (rather than a plain
	// required parameter) so the many call sites that predate the
	// run-history feature (task_occurrences_test.go) keep compiling
	// unchanged — omitting it is exactly "no overlay dependency available",
	// the same fallback populateRunOverlay applies when the callback itself
	// errors. Only the REST entry point (HandleTaskOccurrences,
	// rest_tasks.go) passes one, wired to a.taskStore.RunsInRange.
	runsInRange ...runsInRangeFn,
) ([]gen.TaskOccurrenceSet, error) {
	if fromMs >= toMs {
		return nil, fmt.Errorf("task_occurrences: from_ms (%d) must be before to_ms (%d)", fromMs, toMs)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("task_occurrences: unknown tz %q: %w", tz, err)
	}

	var runsFn runsInRangeFn
	if len(runsInRange) > 0 {
		runsFn = runsInRange[0]
	}

	detail := (toMs - fromMs) <= detailSpanThresholdMs
	out := make([]gen.TaskOccurrenceSet, 0, len(tasks))
	for i := range tasks {
		set, ok := buildOneOccurrenceSet(&tasks[i], fromMs, toMs, loc, detail, everyAnchor, runsFn)
		if ok {
			out = append(out, set)
		}
	}
	return out, nil
}

// buildOneOccurrenceSet expands a single task's trigger, or reports ok=false
// when the task's trigger is not recurring-capable, its config is malformed
// (defensive — creation-time validation should already prevent this), or it
// produces zero occurrences in range (spec: zero-occurrence tasks are
// omitted from the response entirely). When ok=true and runsInRange is
// non-nil, the returned set is additionally enriched with the ADR-050 RD6,
// task-run-history-spec.md §3.7 occurrence-run overlay (populateRunOverlay).
func buildOneOccurrenceSet(
	t *task.Task,
	fromMs, toMs int64,
	loc *time.Location,
	detail bool,
	everyAnchor func(taskID string) (int64, bool),
	runsInRange runsInRangeFn,
) (gen.TaskOccurrenceSet, bool) {
	if t.Trigger == nil {
		return gen.TaskOccurrenceSet{}, false
	}

	var (
		occMs     []int64
		buckets   []wireDayBucket
		truncated bool
	)

	switch t.Trigger.Type {
	case task.TriggerRecurring:
		cfg := t.Trigger.Config
		switch {
		case cfg.Rrule != nil && *cfg.Rrule != "":
			if cfg.DtstartMs == nil || cfg.Tz == nil || *cfg.Tz == "" {
				slog.Warn("task_occurrences: recurring/rrule task missing required siblings, skipping",
					"task_id", t.ID)
				return gen.TaskOccurrenceSet{}, false
			}
			rruleBody, dtstartMs, ruleTZ := *cfg.Rrule, *cfg.DtstartMs, *cfg.Tz

			if detail {
				instants, trunc, err := task.ExpandRRULE(rruleBody, dtstartMs, ruleTZ, fromMs, toMs, perTaskInstantCap)
				if err != nil {
					slog.Warn("task_occurrences: rrule detail expansion failed, skipping task",
						"task_id", t.ID, "error", err)
					return gen.TaskOccurrenceSet{}, false
				}
				occMs, truncated = instants, trunc
				break
			}

			regular, err := task.IsRegular(rruleBody, dtstartMs, ruleTZ)
			if err != nil {
				slog.Warn("task_occurrences: rrule regularity check failed, skipping task",
					"task_id", t.ID, "error", err)
				return gen.TaskOccurrenceSet{}, false
			}
			var intervalMs *int64
			var dayFn overviewDayFn
			if regular {
				if periodMs, ok, _ := task.RegularPeriodMs(rruleBody, dtstartMs, ruleTZ); ok {
					v := periodMs
					intervalMs = &v
				}
				dayFn = regularRruleDayFn(rruleBody, dtstartMs, ruleTZ)
			} else {
				dayFn = irregularRruleDayFn(rruleBody, dtstartMs, ruleTZ)
			}
			res, err := buildOverview(loc, fromMs, toMs, intervalMs, dayFn)
			if err != nil {
				slog.Warn("task_occurrences: rrule overview expansion failed, skipping task",
					"task_id", t.ID, "error", err)
				return gen.TaskOccurrenceSet{}, false
			}
			occMs, buckets, truncated = res.raw, res.buckets, res.truncated

		case cfg.CronExpr != nil && *cfg.CronExpr != "":
			expr := *cfg.CronExpr

			if detail {
				occMs, truncated = expandCronServerZone(expr, fromMs, toMs, perTaskInstantCap)
				break
			}
			res, err := buildOverview(loc, fromMs, toMs, nil, cronDayFn(expr))
			if err != nil {
				slog.Warn("task_occurrences: cron overview expansion failed, skipping task",
					"task_id", t.ID, "error", err)
				return gen.TaskOccurrenceSet{}, false
			}
			occMs, buckets, truncated = res.raw, res.buckets, res.truncated

		default:
			// Neither rrule nor cron_expr set on a `recurring` trigger:
			// malformed data (creation-time ValidateTrigger should prevent
			// this) — omit defensively rather than erroring the whole
			// request.
			return gen.TaskOccurrenceSet{}, false
		}

	case task.TriggerEvery:
		if t.Trigger.Config.EveryMs == nil {
			return gen.TaskOccurrenceSet{}, false
		}
		everyMs := *t.Trigger.Config.EveryMs
		anchorMs, ok := everyAnchor(t.ID)
		if !ok {
			// FR-008a: no live armed job to project from right now — the
			// engine keeps no stored anchor for `every` jobs, so there is
			// nothing to render (not an error).
			return gen.TaskOccurrenceSet{}, false
		}

		if detail {
			occMs, truncated = task.ProjectEveryMs(anchorMs, everyMs, fromMs, toMs, perTaskInstantCap)
			break
		}
		v := everyMs
		res, err := buildOverview(loc, fromMs, toMs, &v, everyMsDayFn(anchorMs, everyMs))
		if err != nil {
			slog.Warn("task_occurrences: every_ms overview expansion failed, skipping task",
				"task_id", t.ID, "error", err)
			return gen.TaskOccurrenceSet{}, false
		}
		occMs, buckets, truncated = res.raw, res.buckets, res.truncated

	default:
		// manual/once: not recurring-capable, rendered by the existing
		// due/fire-chip path instead of this endpoint.
		return gen.TaskOccurrenceSet{}, false
	}

	if len(occMs) == 0 && len(buckets) == 0 {
		return gen.TaskOccurrenceSet{}, false // zero-occurrence tasks are omitted
	}
	// gen.TaskOccurrenceSet.OccurrencesMs/DayBuckets are both `required`,
	// non-nullable array fields on the wire (TaskOccurrenceSet.yaml); a bare
	// nil Go slice marshals to JSON `null`, which the SPA's zod edge
	// validation rejects (the whole set then gets dropped client-side).
	// Detail mode never touches `buckets` (only occMs is populated), and
	// overview mode with a fully-dense rule can leave `occMs` nil (every day
	// bucketed, see buildOverview) — normalize both to non-nil empty slices
	// here, the single choke point every branch above funnels through,
	// mirroring toWireTodos' make(..., 0, ...) convention in rest_tasks.go.
	if occMs == nil {
		occMs = []int64{}
	}
	if buckets == nil {
		buckets = []wireDayBucket{}
	}
	set := gen.TaskOccurrenceSet{
		TaskId:        t.ID,
		OccurrencesMs: occMs,
		DayBuckets:    buckets,
		Truncated:     truncated,
	}
	populateRunOverlay(&set, t.ID, fromMs, toMs, loc, runsInRange)
	return set, true
}

// --- occurrence-run overlay (ADR-050 RD6, task-run-history-spec.md §3.7) --

// populateRunOverlay enriches an already-fully-built set (occMs/buckets/
// truncated are final) with:
//
//   - per-instant occurrence_runs: one entry per set.OccurrencesMs value
//     that has a matching run (matched by EXACT occurrence_ms equality).
//     Scoped STRICTLY to set.OccurrencesMs — never bucket members — so it
//     inherits the existing ≤500/task instant cap with no new cap
//     (spec HIGH-#H4).
//   - per-bucket run_counts: a {scheduled,in_progress,done,failed} tally
//     computed by scanning the SAME runsInRange result over each bucket's
//     own day window — never by enumerating RRULE members.
//
// Both fields stay nil/absent on the wire (ADR-050 RD11's "purely additive,
// absent until populated") when runsInRange is nil (a caller/test that
// doesn't exercise the overlay), the call errors, or it returns zero runs
// for this task's [fromMs, toMs) window.
func populateRunOverlay(
	set *gen.TaskOccurrenceSet,
	taskID string,
	fromMs, toMs int64,
	loc *time.Location,
	runsInRange runsInRangeFn,
) {
	if runsInRange == nil {
		return
	}
	runs, err := runsInRange(taskID, fromMs, toMs)
	if err != nil {
		slog.Warn("task_occurrences: run overlay lookup failed, omitting overlay for task",
			"task_id", taskID, "error", err)
		return
	}
	if len(runs) == 0 {
		return
	}

	populateOccurrenceRunEntries(set, runs)
	populateBucketRunCounts(set, runs, loc)
}

// latestRunPerOccurrence folds runs into a map keyed by occurrence_ms,
// latest-STARTED-wins per key (runIsNewer) — the SINGLE dedup used by both
// the per-instant occurrence_runs overlay (populateOccurrenceRunEntries) and
// the per-bucket run_counts tally (populateBucketRunCounts), so a re-run
// occurrence (a scheduled fire that failed, later manually re-run to done)
// is counted/rendered exactly ONCE, as its current/definitive attempt — not
// once per raw run record. Runs with a nil OccurrenceMs (ad-hoc/manual,
// non-recurring runs) are excluded — they have no occurrence key to fold on.
func latestRunPerOccurrence(runs []task.TaskRun) map[int64]task.TaskRun {
	byOccurrence := make(map[int64]task.TaskRun, len(runs))
	for _, run := range runs {
		if run.OccurrenceMs == nil {
			continue // defensive: RunsInRange never returns these, but don't trust blindly
		}
		key := *run.OccurrenceMs
		if cur, ok := byOccurrence[key]; !ok || runIsNewer(run, cur) {
			byOccurrence[key] = run
		}
	}
	return byOccurrence
}

// populateOccurrenceRunEntries matches each of set.OccurrencesMs against
// runs by exact occurrence_ms. When more than one run shares an
// occurrence_ms — a scheduled fire followed by a later manual re-run of the
// SAME occurrence; OpenRun's idempotency guard only prevents a duplicate
// concurrently-OPEN run for a key, not a later fresh run opened after the
// first one already closed (ADR-050 RD7) — the most recently started run
// wins (runIsNewer): the current/definitive attempt for that occurrence.
func populateOccurrenceRunEntries(set *gen.TaskOccurrenceSet, runs []task.TaskRun) {
	if len(set.OccurrencesMs) == 0 {
		return
	}
	byOccurrence := latestRunPerOccurrence(runs)
	if len(byOccurrence) == 0 {
		return
	}
	entries := make([]wireOccurrenceRun, 0, len(set.OccurrencesMs))
	for _, ms := range set.OccurrencesMs {
		run, ok := byOccurrence[ms]
		if !ok {
			continue
		}
		// H4: validate the enum on encode rather than trusting a bare Go type
		// conversion — a corrupt/foreign stored status must not become a
		// schema-invalid value on the wire. Mirrors foldRunsLocked's own
		// malformed-record skip (pkg/task/run_store.go): log at Warn and drop
		// just this entry, not the whole overlay.
		status := gen.TaskOccurrenceSetOccurrenceRunsStatus(run.Status)
		if !status.Valid() {
			slog.Warn("task_occurrences: run has invalid status, dropping occurrence-run entry",
				"task_id", run.TaskID, "run_id", run.RunID, "occurrence_ms", ms, "status", run.Status)
			continue
		}
		entries = append(entries, wireOccurrenceRun{
			HasResult:    run.Result != "",
			OccurrenceMs: ms,
			RunId:        run.RunID,
			SessionId:    run.SessionID,
			Status:       status,
		})
	}
	if len(entries) > 0 {
		set.OccurrenceRuns = &entries
	}
}

// populateBucketRunCounts tallies each of set.DayBuckets against the SAME
// latest-wins-per-occurrence fold populateOccurrenceRunEntries uses
// (latestRunPerOccurrence) — H1 fix: this previously scanned the raw `runs`
// slice with no dedup, so a re-run occurrence (e.g. a scheduled fire that
// failed, later manually re-run to done) was tallied into BOTH its stale
// `failed` count and its current `done` count for the same occurrence,
// double-counting it and disagreeing with the per-instant overlay (which
// already deduped to the single current attempt). Folding once here keeps
// the two overlay fields consistent.
//
// Each bucket's window is [day_start_ms, next civil day's midnight in loc)
// via civilDayNext — H2-DST fix: a literal dayFrom+24h fixed-width span
// mis-buckets a boundary-hour run on a 23h/25h DST-transition day, because
// DayStartMs itself is already the DST-aware civilDayStart output (see
// buildOverview) — the window's far edge must be computed the same
// calendar-arithmetic way, not as a fixed-duration offset. (ADR-050's
// separately-accepted single-DST-transition occurrence_ms JOIN imprecision —
// NextOccurrenceAfter vs ExpandRRULE naming different valid minutes inside a
// spring-forward gap — is a different, already-documented edge case and does
// not excuse this bucket WINDOW from being DST-aware too.)
//
// scheduled is derived as bucket.Count minus the four observed statuses
// (in_progress/done/failed/skipped), clamped at 0 — a day whose bucket count
// reflects occurrences whose runs were later pruned (retention) could
// otherwise go negative.
func populateBucketRunCounts(set *gen.TaskOccurrenceSet, runs []task.TaskRun, loc *time.Location) {
	if len(set.DayBuckets) == 0 {
		return
	}
	byOccurrence := latestRunPerOccurrence(runs)
	if len(byOccurrence) == 0 {
		return
	}
	for i := range set.DayBuckets {
		b := &set.DayBuckets[i]
		dayFrom := b.DayStartMs
		dayTo := civilDayNext(dayFrom, loc)
		var inProgress, done, failed, skipped int32
		for ms, run := range byOccurrence {
			if ms < dayFrom || ms >= dayTo {
				continue
			}
			switch run.Status {
			case task.StatusInProgress:
				inProgress++
			case task.StatusDone:
				done++
			case task.StatusFailed:
				failed++
			case task.StatusSkipped:
				skipped++
			}
		}
		if inProgress == 0 && done == 0 && failed == 0 && skipped == 0 {
			continue // no runs recorded for this day — leave run_counts unset (nil)
		}
		scheduled := b.Count - (inProgress + done + failed + skipped)
		if scheduled < 0 {
			scheduled = 0
		}
		b.RunCounts = &wireRunCounts{
			Done:       done,
			Failed:     failed,
			InProgress: inProgress,
			Scheduled:  scheduled,
			Skipped:    skipped,
		}
	}
}

// runIsNewer reports whether a's execution attempt should be preferred over
// b's when both share the same occurrence_ms — the most recently STARTED
// run wins, ties (identical StartedAt) broken by the lexically-greater
// RunID (ULIDs are themselves time-ordered, so this is also "most recent"
// on an exact-timestamp tie). StartedAt is a fixed-width RFC 3339 UTC
// string (task.Store.OpenRun's time.Now().UTC().Format(time.RFC3339)),
// which sorts lexically identically to chronological order.
func runIsNewer(a, b task.TaskRun) bool {
	if a.StartedAt != b.StartedAt {
		return a.StartedAt > b.StartedAt
	}
	return a.RunID > b.RunID
}

// --- overview (bucketed) day-walk driver -----------------------------------

// dayResult is one calendar day's occurrence data as reported by an
// overviewDayFn: either an arithmetic count+first (bucket path, when
// count > 3) or the exact instants (raw path, when 0 < count <= 3).
type dayResult struct {
	count     int
	firstMs   int64
	instants  []int64 // populated only when 0 < count <= 3
	consumed  int     // iteration-budget occurrences consumed (0 for arithmetic/regular flavors)
	truncated bool    // true when this day's own computation was cut short by budget
}

// overviewDayFn computes one query-tz calendar day's occurrences, clipped to
// [dayFromMs, dayToMs). budget is the iteration budget still available
// (ignored by arithmetic/regular implementations, which never consume it).
type overviewDayFn func(dayFromMs, dayToMs int64, budget int) (dayResult, error)

// overviewResult accumulates buildOverview's output across the whole
// query-tz day walk.
type overviewResult struct {
	raw       []int64
	buckets   []wireDayBucket
	truncated bool
}

// buildOverview walks every query-tz calendar day intersecting
// [fromMs, toMs), asking dayFn for each day's occurrence data, and applies
// the D6 bucketing rule (>3/day -> DayBucket, else raw) plus both caps:
// perTaskInstantCap always (regardless of regularity — see its doc comment)
// and perTaskIterationBudget only for days that report a non-zero
// `consumed` (irregular flavors; regular/arithmetic dayFns always report 0,
// so they can never exhaust it and never truncate for that reason).
//
// On budget exhaustion, or a dayFn reporting its OWN result was truncated
// (its per-day iteration call was itself capped by the remaining budget),
// that day's partial/unknown data is dropped entirely and the walk stops —
// never rendering a day as complete when it is actually unknown, per the
// spec's "stop, and the client renders a truncation marker on the last
// covered day" rule.
func buildOverview(
	loc *time.Location,
	fromMs, toMs int64,
	intervalMs *int64,
	dayFn overviewDayFn,
) (overviewResult, error) {
	var res overviewResult
	budgetLeft := perTaskIterationBudget

	day := civilDayStart(fromMs, loc)
dayLoop:
	for day < toMs {
		next := civilDayNext(day, loc)
		clipFrom, clipTo := max64(day, fromMs), min64(next, toMs)
		if clipFrom < clipTo {
			if budgetLeft <= 0 {
				res.truncated = true
				break dayLoop
			}
			dr, err := dayFn(clipFrom, clipTo, budgetLeft)
			if err != nil {
				return overviewResult{}, err
			}
			budgetLeft -= dr.consumed
			switch {
			case dr.truncated || budgetLeft < 0:
				res.truncated = true
				break dayLoop
			case dr.count > 3:
				res.buckets = append(res.buckets, wireDayBucket{
					DayStartMs: day,
					// DayEndMs reuses `next` — already computed above as
					// civilDayNext(day, loc) — the SAME DST-aware civil
					// next-midnight populateBucketRunCounts recomputes from
					// this same DayStartMs for its own [dayFrom, dayTo) tally
					// window (delta-review HIGH fix: the wire value must equal
					// the tally window exactly, not a fixed-24h client guess).
					DayEndMs:   next,
					Count:      int32(dr.count), //nolint:gosec // dr.count is bounded by perTaskIterationBudget (10000), well within int32 range
					FirstMs:    dr.firstMs,
					IntervalMs: intervalMs,
				})
			case dr.count > 0:
				if len(res.raw)+len(dr.instants) > perTaskInstantCap {
					room := perTaskInstantCap - len(res.raw)
					if room > 0 {
						res.raw = append(res.raw, dr.instants[:room]...)
					}
					res.truncated = true
					break dayLoop
				}
				res.raw = append(res.raw, dr.instants...)
			}
		}
		day = next
	}
	// Normalize at the source too (defense-in-depth alongside the
	// buildOneOccurrenceSet choke point): res.raw/res.buckets are only ever
	// appended to above, so a day-walk that never appends to one of them
	// (e.g. a fully-dense range leaves res.raw nil; a range with no day
	// exceeding the >3 bucketing threshold leaves res.buckets nil) would
	// otherwise hand back a bare nil slice.
	if res.raw == nil {
		res.raw = []int64{}
	}
	if res.buckets == nil {
		res.buckets = []wireDayBucket{}
	}
	return res, nil
}

// civilDayStart returns the Unix-ms instant of midnight, in loc, of the
// calendar day containing ms.
func civilDayStart(ms int64, loc *time.Location) int64 {
	t := time.UnixMilli(ms).In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).UnixMilli()
}

// civilDayNext returns the Unix-ms instant of the NEXT calendar day's
// midnight after dayStartMs, in loc — correctly DST-normalized (a 23/25-hour
// civil day on a transition day) since it advances via time.AddDate's
// calendar arithmetic, not a fixed 24h duration.
func civilDayNext(dayStartMs int64, loc *time.Location) int64 {
	t := time.UnixMilli(dayStartMs).In(loc)
	return t.AddDate(0, 0, 1).UnixMilli()
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// --- per-flavor overviewDayFn implementations -------------------------------

// regularRruleDayFn derives a day's rrule occurrences arithmetically
// (task.CountRegularInRange for the count/first; task.ExpandRRULE — which
// also takes the O(1) arithmetic path for a provably-regular rule, see
// pkg/task/rrule.go — only when the exact instants are needed for the raw,
// <=3-per-day case). Never consumes the iteration budget.
func regularRruleDayFn(rruleBody string, dtstartMs int64, ruleTZ string) overviewDayFn {
	return func(dayFromMs, dayToMs int64, _ int) (dayResult, error) {
		count, first, hasAny, err := task.CountRegularInRange(rruleBody, dtstartMs, ruleTZ, dayFromMs, dayToMs)
		if err != nil {
			return dayResult{}, err
		}
		if !hasAny {
			return dayResult{}, nil
		}
		dr := dayResult{count: count, firstMs: first}
		if count <= 3 {
			instants, _, err := task.ExpandRRULE(rruleBody, dtstartMs, ruleTZ, dayFromMs, dayToMs, count)
			if err != nil {
				return dayResult{}, err
			}
			dr.instants = instants
		}
		return dr, nil
	}
}

// irregularRruleDayFn expands a day's rrule occurrences by iteration
// (task.ExpandRRULE's BY*-modified / general path), consuming `budget`
// occurrences from the caller-supplied cap. Reports truncated=true (and no
// count/instants — the caller drops the partial day) when the day's true
// count could not be determined within the remaining budget.
func irregularRruleDayFn(rruleBody string, dtstartMs int64, ruleTZ string) overviewDayFn {
	return func(dayFromMs, dayToMs int64, budget int) (dayResult, error) {
		instants, truncated, err := task.ExpandRRULE(rruleBody, dtstartMs, ruleTZ, dayFromMs, dayToMs, budget)
		if err != nil {
			return dayResult{}, err
		}
		if truncated {
			return dayResult{truncated: true}, nil
		}
		dr := dayResult{count: len(instants), consumed: len(instants)}
		if len(instants) > 0 {
			dr.firstMs = instants[0]
			if len(instants) <= 3 {
				dr.instants = instants
			}
		}
		return dr, nil
	}
}

// cronDayFn expands a day's legacy cron_expr occurrences by iterating
// expandCronServerZone (gronx, server zone), consuming `budget` occurrences.
// Always the irregular/budgeted path — cron has no arithmetic shortcut.
func cronDayFn(expr string) overviewDayFn {
	return func(dayFromMs, dayToMs int64, budget int) (dayResult, error) {
		instants, truncated := expandCronServerZone(expr, dayFromMs, dayToMs, budget)
		if truncated {
			return dayResult{truncated: true}, nil
		}
		dr := dayResult{count: len(instants), consumed: len(instants)}
		if len(instants) > 0 {
			dr.firstMs = instants[0]
			if len(instants) <= 3 {
				dr.instants = instants
			}
		}
		return dr, nil
	}
}

// everyMsDayFn derives a day's every_ms occurrences arithmetically
// (countEveryMsInRange), never consuming the iteration budget — every_ms is
// always provably regular (FR-008a projects from a fixed anchor at a fixed
// interval, no wall-clock/DST reasoning at all).
func everyMsDayFn(anchorMs, everyMs int64) overviewDayFn {
	return func(dayFromMs, dayToMs int64, _ int) (dayResult, error) {
		count, first, hasAny := countEveryMsInRange(anchorMs, everyMs, dayFromMs, dayToMs)
		if !hasAny {
			return dayResult{}, nil
		}
		dr := dayResult{count: count, firstMs: first}
		if count <= 3 {
			instants := make([]int64, count)
			for i := 0; i < count; i++ {
				instants[i] = first + int64(i)*everyMs
			}
			dr.instants = instants
		}
		return dr, nil
	}
}

// --- flavor-specific expansion primitives -----------------------------------

// expandCronServerZone expands a legacy cron_expr trigger's occurrences
// within [fromMs, toMs) by walking gronx.NextTickAfter in the SERVER's local
// zone (time.UnixMilli's implicit time.Local) — the identical mechanism
// pkg/cron/service.go's computeNextRun uses for a "cron"-kind job
// (`gronx.NextTickAfter(schedule.Expr, time.UnixMilli(nowMS), false)`), so
// this endpoint's expansion and the live scheduler's own fire computation
// can never disagree (Timezone Semantics §2, D8: display-only, no reverse
// zone-mapping). The first call is inclusive (captures a tick exactly at
// fromMs, honoring the half-open [from, to) contract); subsequent calls are
// exclusive (advance strictly past the last found tick).
func expandCronServerZone(expr string, fromMs, toMs int64, limit int) ([]int64, bool) {
	if limit <= 0 || fromMs >= toMs {
		return nil, false
	}
	cur := time.UnixMilli(fromMs)
	inclusive := true
	var out []int64
	truncated := false
	for {
		next, err := gronx.NextTickAfter(expr, cur, inclusive)
		if err != nil {
			break // unparseable/exhausted — treat as "no further ticks"
		}
		ms := next.UnixMilli()
		if ms < fromMs || ms >= toMs {
			break
		}
		out = append(out, ms)
		if len(out) >= limit {
			truncated = true
			break
		}
		cur = next
		inclusive = false
	}
	return out, truncated
}

// countEveryMsInRange derives, by pure modular arithmetic (no iteration),
// the count and first occurrence of a fixed-interval every_ms projection
// (anchorMs, anchorMs+everyMs, anchorMs+2*everyMs, …) within the half-open
// [fromMs, toMs) range. hasAny is false when the range contains none.
// Mirrors task.ProjectEveryMs's anchor+step semantics (FR-008a) but reports
// only the count/first an overview day needs, in O(1), instead of
// enumerating instants — the arithmetic-derivation counterpart to
// task.CountRegularInRange for the every_ms flavor.
func countEveryMsInRange(anchorMs, everyMs, fromMs, toMs int64) (count int, first int64, hasAny bool) {
	if everyMs <= 0 || fromMs >= toMs {
		return 0, 0, false
	}
	var k int64
	if anchorMs < fromMs {
		k = (fromMs - anchorMs + everyMs - 1) / everyMs
	}
	first = anchorMs + k*everyMs
	if first >= toMs {
		return 0, 0, false
	}
	n := (toMs-1-first)/everyMs + 1
	return int(n), first, true
}

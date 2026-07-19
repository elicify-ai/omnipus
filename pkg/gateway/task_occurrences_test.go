//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// task_occurrences_test.go — tests 7 and 8 of the Calendar Recurrence
// Redesign TDD plan (docs/internal/specs/calendar-recurrence-redesign-spec.md,
// "Test Implementation Order"): TestOccurrences_LegacyCronAndEveryMs and
// TestOccurrences_BucketingAndCaps. Both exercise buildOccurrenceSets
// directly — a pure function of ([]task.Task, range, tz, everyAnchor) — so
// they never link the full gateway test binary (project rule: never run the
// full pkg/gateway suite locally, OOM risk in the devpod).

import (
	"testing"
	"time"

	"github.com/adhocore/gronx"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// noEveryAnchor is the everyAnchor stub for tests that never exercise an
// `every`-triggered task (no live job to project from).
func noEveryAnchor(string) (int64, bool) { return 0, false }

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("time.LoadLocation(%q): %v", name, err)
	}
	return loc
}

// --- Test 7: legacy cron_expr + every_ms -----------------------------------

func TestOccurrences_LegacyCronAndEveryMs(t *testing.T) {
	t.Run("cron_expr detail-mode expansion matches the scheduler's own fire computation", func(t *testing.T) {
		// pkg/cron/service.go's computeNextRun computes a "cron"-kind job's
		// next fire via gronx.NextTickAfter(schedule.Expr,
		// time.UnixMilli(nowMS), false) — the exact same primitive
		// expandCronServerZone (task_occurrences.go) walks. This test
		// independently re-derives the expected instant sequence with that
		// same primitive and asserts buildOccurrenceSets agrees exactly —
		// "the engines can never disagree" (Timezone Semantics §2/§4).
		const expr = "0 9 * * MON" // every Monday 09:00, server-local zone
		from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
		to := from.AddDate(0, 0, 7) // 7-day span: DETAIL mode (<= 8*24h)
		fromMs, toMs := from.UnixMilli(), to.UnixMilli()

		tasks := []task.Task{{
			ID: "cron-task",
			Trigger: &task.Trigger{
				Type:   task.TriggerRecurring,
				Config: task.TriggerConfig{CronExpr: ptr(expr)},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if sets[0].Truncated {
			t.Errorf("truncated = true, want false (well under the 500 cap)")
		}
		if len(sets[0].DayBuckets) != 0 {
			t.Errorf("DayBuckets = %v, want none (detail mode)", sets[0].DayBuckets)
		}

		var want []int64
		cur := time.UnixMilli(fromMs - 1)
		for {
			next, tickErr := gronx.NextTickAfter(expr, cur, false)
			if tickErr != nil {
				break
			}
			ms := next.UnixMilli()
			if ms >= toMs {
				break
			}
			want = append(want, ms)
			cur = next
		}
		if len(want) == 0 {
			t.Fatalf("test setup produced no reference instants — widen the range")
		}
		if !int64SlicesEqual(sets[0].OccurrencesMs, want) {
			t.Errorf("cron expansion mismatch:\n got  %v\n want %v", sets[0].OccurrencesMs, want)
		}
	})

	t.Run("every_ms projection: first entry equals the armed NextRunAtMS, then +k*interval", func(t *testing.T) {
		const everyMs = int64(60 * 60 * 1000) // 1 hour
		armedNextRun := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC).UnixMilli()
		anchor := func(taskID string) (int64, bool) {
			if taskID == "every-task" {
				return armedNextRun, true
			}
			return 0, false
		}
		tasks := []task.Task{{
			ID: "every-task",
			Trigger: &task.Trigger{
				Type:   task.TriggerEvery,
				Config: task.TriggerConfig{EveryMs: ptr(everyMs)},
			},
		}}
		// Range straddles the armed instant (starts before it) to prove the
		// projection never extrapolates backward from armedNextRun.
		fromMs := armedNextRun - 3*60*60*1000
		toMs := armedNextRun + 5*60*60*1000

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", anchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		got := sets[0].OccurrencesMs
		if len(got) == 0 {
			t.Fatalf("got no occurrences")
		}
		if got[0] != armedNextRun {
			t.Errorf("first occurrence = %d, want the armed NextRunAtMS %d (engine-agreement assertion)",
				got[0], armedNextRun)
		}
		for i, ms := range got {
			want := armedNextRun + int64(i)*everyMs
			if ms != want {
				t.Errorf("occurrence[%d] = %d, want %d (armedNextRun + %d*everyMs)", i, ms, want, i)
			}
		}
	})

	t.Run("every_ms is forward-only: a range fully in the past omits the task", func(t *testing.T) {
		armedNextRun := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC).UnixMilli()
		anchor := func(string) (int64, bool) { return armedNextRun, true }
		tasks := []task.Task{{
			ID: "every-task",
			Trigger: &task.Trigger{
				Type:   task.TriggerEvery,
				Config: task.TriggerConfig{EveryMs: ptr(int64(60000))},
			},
		}}
		fromMs := armedNextRun - 10*60*60*1000
		toMs := armedNextRun - 5*60*60*1000 // entirely before the armed instant

		sets, err := buildOccurrenceSets(tasks, fromMs, toMs, "UTC", anchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 0 {
			t.Errorf("got %d occurrence sets for a range fully in the past, want 0 (task omitted, no backward extrapolation)",
				len(sets))
		}
	})

	t.Run("every_ms with no live armed job (everyAnchor ok=false) omits the task", func(t *testing.T) {
		tasks := []task.Task{{
			ID: "orphan-every-task",
			Trigger: &task.Trigger{
				Type:   task.TriggerEvery,
				Config: task.TriggerConfig{EveryMs: ptr(int64(60000))},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, 0, 60*60*1000, "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 0 {
			t.Errorf("got %d occurrence sets with no armed anchor, want 0", len(sets))
		}
	})
}

// --- Test 8: bucketing, caps, and the iteration budget ----------------------

func TestOccurrences_BucketingAndCaps(t *testing.T) {
	t.Run("row 16: >3/day bucketed on QUERY-tz days when rule tz differs from query tz", func(t *testing.T) {
		berlin := mustLoc(t, "Europe/Berlin")
		tokyo := mustLoc(t, "Asia/Tokyo")

		// January: neither Berlin nor Tokyo observes DST, so both zones
		// have a fixed UTC offset for the whole test — isolates "which tz
		// bucketing uses" from DST-normalization concerns (covered
		// separately by pkg/task/rrule_test.go).
		dtstart := time.Date(2026, 1, 5, 0, 0, 0, 0, tokyo)
		rangeStart := time.Date(2026, 1, 10, 0, 0, 0, 0, berlin)
		rangeEnd := rangeStart.AddDate(0, 0, 10) // 10 days: OVERVIEW mode (> 8*24h)

		tasks := []task.Task{{
			ID: "hourly-tokyo-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=HOURLY"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("Asia/Tokyo"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, rangeStart.UnixMilli(), rangeEnd.UnixMilli(), "Europe/Berlin", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		set := sets[0]
		if set.Truncated {
			t.Errorf("truncated = true, want false (provably regular)")
		}
		if len(set.OccurrencesMs) != 0 {
			t.Errorf("OccurrencesMs = %v, want none (every day is dense, all bucketed)", set.OccurrencesMs)
		}
		if len(set.DayBuckets) != 10 {
			t.Fatalf("got %d day buckets, want 10 (one per Berlin day in range)", len(set.DayBuckets))
		}
		for i, b := range set.DayBuckets {
			wantDayStart := time.Date(2026, 1, 10+i, 0, 0, 0, 0, berlin).UnixMilli()
			if b.DayStartMs != wantDayStart {
				t.Errorf("bucket[%d].DayStartMs = %d, want %d (Berlin midnight, the QUERY tz — not Tokyo's)",
					i, b.DayStartMs, wantDayStart)
			}
			if b.Count != 24 {
				t.Errorf("bucket[%d].Count = %d, want 24 (hourly over a full non-DST day)", i, b.Count)
			}
			if b.IntervalMs == nil || *b.IntervalMs != 3600000 {
				t.Errorf("bucket[%d].IntervalMs = %v, want 3600000 (regular HOURLY rule)", i, b.IntervalMs)
			}
			// Both zones are fixed-offset in January and HOURLY fires on
			// the top of every UTC hour relative to its Tokyo dtstart, so
			// a Berlin midnight (also exactly on the UTC hour in January)
			// is itself an occurrence.
			if b.FirstMs != wantDayStart {
				t.Errorf("bucket[%d].FirstMs = %d, want %d (day start itself)", i, b.FirstMs, wantDayStart)
			}
		}
	})

	t.Run("row 18: a 169h fall-back-week span stays in detail (raw) mode, never bucketed", func(t *testing.T) {
		// 169h = 7 days + 1h < 8*24h = 192h -> detail mode regardless of
		// occurrence density. Uses a dense (24/day) rule so bucketing WOULD
		// have applied were this misclassified as overview mode.
		from := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
		to := from.Add(169 * time.Hour)

		tasks := []task.Task{{
			ID: "hourly-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=HOURLY"),
					DtstartMs: ptr(from.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if len(sets[0].DayBuckets) != 0 {
			t.Errorf("DayBuckets = %v, want none (169h span must stay in detail/raw mode)", sets[0].DayBuckets)
		}
		if got, want := len(sets[0].OccurrencesMs), 169; got != want {
			t.Errorf("len(OccurrencesMs) = %d, want %d (169 hourly instants, one per hour)", got, want)
		}
	})

	t.Run("500-instant cap: legacy every_ms=60000 over a 7-day detail-mode range truncates at 500", func(t *testing.T) {
		// Dataset row 10: every_ms=60000 (1/min), 7-day range (10,080
		// potential instants) -> per-task cap of 500 stops iteration.
		armedNextRun := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
		anchor := func(string) (int64, bool) { return armedNextRun, true }
		tasks := []task.Task{{
			ID: "fast-every-task",
			Trigger: &task.Trigger{
				Type:   task.TriggerEvery,
				Config: task.TriggerConfig{EveryMs: ptr(int64(60000))},
			},
		}}
		toMs := armedNextRun + 7*24*60*60*1000
		sets, err := buildOccurrenceSets(tasks, armedNextRun, toMs, "UTC", anchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if !sets[0].Truncated {
			t.Errorf("truncated = false, want true (10,080 potential instants > 500 cap)")
		}
		if len(sets[0].OccurrencesMs) != perTaskInstantCap {
			t.Errorf("len(OccurrencesMs) = %d, want the cap (%d)", len(sets[0].OccurrencesMs), perTaskInstantCap)
		}
	})

	t.Run("500-instant raw cap in overview mode: many sparse (<=3/day) days truncate", func(t *testing.T) {
		// FREQ=DAILY;BYHOUR=9,15 fires 2/day (irregular — BYHOUR present —
		// but always <=3/day, so every day stays RAW, never bucketed).
		// Over 300 days that is 600 potential raw instants, exceeding the
		// 500-per-task cap.
		dtstart := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
		from := dtstart
		to := from.AddDate(0, 0, 300)
		tasks := []task.Task{{
			ID: "sparse-irregular-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;BYHOUR=9,15;BYMINUTE=0;BYSECOND=0"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if len(sets[0].DayBuckets) != 0 {
			t.Errorf("DayBuckets = %v, want none (2/day never exceeds the >3 bucketing threshold)", sets[0].DayBuckets)
		}
		if !sets[0].Truncated {
			t.Errorf("truncated = false, want true (600 potential raw instants > the 500 cap)")
		}
		if len(sets[0].OccurrencesMs) != perTaskInstantCap {
			t.Errorf("len(OccurrencesMs) = %d, want the cap (%d)", len(sets[0].OccurrencesMs), perTaskInstantCap)
		}
	})

	t.Run("10k iteration budget: a dense irregular rule truncates buckets mid-range", func(t *testing.T) {
		// FREQ=MINUTELY;BYHOUR=0..23 is irregular (BYHOUR present) despite
		// being as dense as plain MINUTELY (1440/day) -- exercises the
		// budgeted iteration path, not the O(1) arithmetic path. Over 10
		// days that's 14,400 potential occurrences > the 10,000 budget.
		dtstart := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
		from := dtstart
		to := from.AddDate(0, 0, 10)
		tasks := []task.Task{{
			ID: "dense-irregular-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule: ptr(
						"FREQ=MINUTELY;BYHOUR=0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23",
					),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if !sets[0].Truncated {
			t.Errorf("truncated = false, want true (14,400 potential occurrences > the 10,000 budget)")
		}
		// 10000 / 1440 per day = 6 whole days fully affordable before the
		// budget is exhausted mid-day-7; the partial day-7 is dropped
		// entirely rather than rendered as an incomplete bucket.
		if len(sets[0].DayBuckets) != 6 {
			t.Errorf("got %d day buckets, want 6 (budget covers 6 full dense days before truncating)",
				len(sets[0].DayBuckets))
		}
		for _, b := range sets[0].DayBuckets {
			if b.IntervalMs != nil {
				t.Errorf("bucket IntervalMs = %v, want nil (irregular BYHOUR-modified rule)", *b.IntervalMs)
			}
		}
	})

	t.Run("row 11: irregular rule bucket has interval_ms = nil", func(t *testing.T) {
		// FREQ=DAILY;BYHOUR=9,11,13,15 fires 4/day (> 3 -> bucketed), and is
		// irregular (BYHOUR present) so its bucket's spacing is NOT fixed
		// (9->11 is 2h, but 15->next day's 9 is 18h) -- interval_ms MUST be
		// null; the client falls back to "4x/day".
		dtstart := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
		from := dtstart
		to := from.AddDate(0, 0, 9) // > 8 days -> overview mode
		tasks := []task.Task{{
			ID: "four-per-day-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;BYHOUR=9,11,13,15;BYMINUTE=0;BYSECOND=0"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if len(sets[0].DayBuckets) == 0 {
			t.Fatalf("got no day buckets, want at least one (4/day > 3 threshold)")
		}
		for i, b := range sets[0].DayBuckets {
			if b.Count != 4 {
				t.Errorf("bucket[%d].Count = %d, want 4", i, b.Count)
			}
			if b.IntervalMs != nil {
				t.Errorf("bucket[%d].IntervalMs = %v, want nil (irregular rule)", i, *b.IntervalMs)
			}
		}
	})

	t.Run("row 17: a plain regular FREQ=MINUTELY over a 400-day span renders complete buckets, truncated:false", func(t *testing.T) {
		dtstart := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
		from := dtstart
		to := from.AddDate(0, 0, 400) // the endpoint's max legal span
		tasks := []task.Task{{
			ID: "minutely-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=MINUTELY"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		start := time.Now()
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if elapsed > 3*time.Second {
			t.Errorf("buildOccurrenceSets took %s for a regular rule — arithmetic derivation should be near-instant, "+
				"not proportional to the 400-day*1440/day occurrence count (a linear walk would be far slower)", elapsed)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if sets[0].Truncated {
			t.Errorf("truncated = true, want false (provably regular -> arithmetic, never iterated, never truncated)")
		}
		if len(sets[0].OccurrencesMs) != 0 {
			t.Errorf("OccurrencesMs = %v, want none (every day is dense, all bucketed)", sets[0].OccurrencesMs)
		}
		if len(sets[0].DayBuckets) != 400 {
			t.Fatalf("got %d day buckets, want 400 (one per day in the span)", len(sets[0].DayBuckets))
		}
		for i, b := range sets[0].DayBuckets {
			if b.Count != 1440 {
				t.Errorf("bucket[%d].Count = %d, want 1440 (a full UTC day at 1/min)", i, b.Count)
			}
			if b.IntervalMs == nil || *b.IntervalMs != 60000 {
				t.Errorf("bucket[%d].IntervalMs = %v, want 60000", i, b.IntervalMs)
			}
		}
	})

	t.Run("row 17d: a regular dense rule with a DTSTART 2 years before the range is still O(1)-bounded", func(t *testing.T) {
		dtstart := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) // ~2 years before the range below
		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 9) // > 8 days -> overview mode
		tasks := []task.Task{{
			ID: "aged-dtstart-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=MINUTELY"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		start := time.Now()
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if elapsed > 2*time.Second {
			t.Errorf("buildOccurrenceSets took %s with a 2-year-aged DTSTART — must fast-forward arithmetically "+
				"(seekK), never walk from DTSTART", elapsed)
		}
		if len(sets) != 1 {
			t.Fatalf("got %d occurrence sets, want 1", len(sets))
		}
		if sets[0].Truncated {
			t.Errorf("truncated = true, want false")
		}
		if len(sets[0].DayBuckets) != 9 {
			t.Fatalf("got %d day buckets, want 9", len(sets[0].DayBuckets))
		}
		for i, b := range sets[0].DayBuckets {
			if b.Count != 1440 {
				t.Errorf("bucket[%d].Count = %d, want 1440", i, b.Count)
			}
			wantDayStart := from.AddDate(0, 0, i).UnixMilli()
			if b.DayStartMs != wantDayStart {
				t.Errorf("bucket[%d].DayStartMs = %d, want %d", i, b.DayStartMs, wantDayStart)
			}
		}
	})

	t.Run("zero-occurrence task is omitted; empty result is [] never nil", func(t *testing.T) {
		// COUNT=3 exhausted well before the queried range: an exhausted
		// series has no occurrences in a future range.
		dtstart := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
		from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10)
		tasks := []task.Task{{
			ID: "exhausted-task",
			Trigger: &task.Trigger{
				Type: task.TriggerRecurring,
				Config: task.TriggerConfig{
					Rrule:     ptr("FREQ=DAILY;COUNT=3"),
					DtstartMs: ptr(dtstart.UnixMilli()),
					Tz:        ptr("UTC"),
				},
			},
		}}
		sets, err := buildOccurrenceSets(tasks, from.UnixMilli(), to.UnixMilli(), "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if sets == nil {
			t.Fatalf("got nil, want a non-nil empty slice ([] on the wire, never null)")
		}
		if len(sets) != 0 {
			t.Errorf("got %d occurrence sets, want 0 (exhausted series has none in a future range)", len(sets))
		}
	})

	t.Run("empty task list returns a non-nil empty slice", func(t *testing.T) {
		sets, err := buildOccurrenceSets(nil, 0, 60*60*1000, "UTC", noEveryAnchor)
		if err != nil {
			t.Fatalf("buildOccurrenceSets: %v", err)
		}
		if sets == nil {
			t.Fatalf("got nil, want a non-nil empty slice")
		}
		if len(sets) != 0 {
			t.Errorf("got %d occurrence sets, want 0", len(sets))
		}
	})
}

func int64SlicesEqual(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

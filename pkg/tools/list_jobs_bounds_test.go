package tools

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// These tests assert the properties truncation must preserve — "the blocked
// rows are still there", "the counts add up" — never that a particular
// mechanism ran. A test that asserted "the round-robin loop executed" would
// pass against an implementation that allocated every slot to one group.

// makeRows builds n rows of one status with distinct ids and increasing
// started_at, so ordering is observable.
func makeRows(kind, status string, n int) []jobRow {
	rows := make([]jobRow, 0, n)
	for i := 0; i < n; i++ {
		rows = append(rows, jobRow{
			Kind:      kind,
			ID:        fmt.Sprintf("%s-%s-%03d", kind, status, i),
			Status:    status,
			StartedAt: fmt.Sprintf("2026-07-%02dT00:00:%02dZ", 1+(i/60)%28, i%60),
		})
	}
	return rows
}

func countByStatus(rows []jobRow, status string) int {
	n := 0
	for _, r := range rows {
		if r.Status == status {
			n++
		}
	}
	return n
}

// TestBounds_PerStatusSubBoundsProtectBlocked is the default-call half of the
// anti-starvation property: an overwhelming queued+running population must not
// cost the caller a single blocked row.
func TestBounds_PerStatusSubBoundsProtectBlocked(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusQueued, 400), makeRows(jobKindTask, jobStatusRunning, 400)...)
	all = append(all, makeRows(jobKindSubagent, jobStatusBlocked, 3)...)
	sortJobRows(all)

	got := applyBounds(all, maxRows)

	if n := countByStatus(got.selected, jobStatusBlocked); n != 3 {
		t.Fatalf("all 3 blocked rows must survive a 400/400/3 default call, got %d", n)
	}
	if n := countByStatus(got.selected, jobStatusQueued); n != subBoundQueued {
		t.Errorf("queued rows: want the sub-bound %d, got %d", subBoundQueued, n)
	}
	if n := countByStatus(got.selected, jobStatusRunning); n != subBoundRunning {
		t.Errorf("running rows: want the sub-bound %d, got %d", subBoundRunning, n)
	}

	if got.omittedByStatus[jobStatusQueued] != 375 {
		t.Errorf("omitted queued: want 375, got %d", got.omittedByStatus[jobStatusQueued])
	}
	if got.omittedByStatus[jobStatusRunning] != 375 {
		t.Errorf("omitted running: want 375, got %d", got.omittedByStatus[jobStatusRunning])
	}
	// Zero is ABSENT, not present-and-zero: a `blocked: 0` entry would tell
	// the caller blocked rows were dropped when none were.
	if _, present := got.omittedByStatus[jobStatusBlocked]; present {
		t.Errorf("omitted must carry no blocked entry when none were dropped, got %v", got.omittedByStatus)
	}
}

// TestBounds_SmallLimitDoesNotEvictAWholeGroup is the case a tail-truncating
// total cap deletes entirely.
//
// With 25 queued + 25 running + 3 blocked and limit=30, a cap applied AFTER
// the sub-bounds over a list sorted queued -> running -> blocked hands back 25
// queued + 5 running and ZERO blocked rows: the reservation puts them in and
// the cap takes them straight back out, because blocked sorts last. A small
// limit is exactly what a context-conscious agent passes, and a dropped
// blocked row may be a subagent waiting on an answer only this caller can
// give.
func TestBounds_SmallLimitDoesNotEvictAWholeGroup(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusQueued, 25), makeRows(jobKindTask, jobStatusRunning, 25)...)
	all = append(all, makeRows(jobKindSubagent, jobStatusBlocked, 3)...)
	sortJobRows(all)

	got := applyBounds(all, 30)

	if len(got.selected) != 30 {
		t.Fatalf("limit=30 must return 30 rows when 53 are available, got %d", len(got.selected))
	}
	if n := countByStatus(got.selected, jobStatusBlocked); n != 3 {
		t.Fatalf("all 3 blocked rows must survive limit=30; got %d — a whole status group was evicted", n)
	}
	// Every other group must still be represented: the property is "no group
	// starves", not "blocked wins".
	if countByStatus(got.selected, jobStatusQueued) == 0 {
		t.Error("queued rows were starved by the allocation")
	}
	if countByStatus(got.selected, jobStatusRunning) == 0 {
		t.Error("running rows were starved by the allocation")
	}
}

// TestBounds_TerminalBudgetIsSharedNotStarved: `failed` and `completed` share
// one reservation, so a large failed population must not evict every
// completed row.
func TestBounds_TerminalBudgetIsSharedNotStarved(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusFailed, 100), makeRows(jobKindTask, jobStatusCompleted, 100)...)
	sortJobRows(all)

	got := applyBounds(all, maxRows)

	if total := len(got.selected); total != subBoundTerminal {
		t.Fatalf("terminal rows: want the shared bound %d, got %d", subBoundTerminal, total)
	}
	if n := countByStatus(got.selected, jobStatusCompleted); n == 0 {
		t.Fatalf("completed rows were starved out by failed ones")
	}
	if n := countByStatus(got.selected, jobStatusFailed); n == 0 {
		t.Fatalf("failed rows were starved out by completed ones")
	}
}

// TestBounds_EveryOmissionIsReported: truncation must never be silent, and
// both key spaces must add up to the same total. Deriving them from one
// dropped-row set is what makes the arithmetic true by construction rather
// than by two counters agreeing by luck.
func TestBounds_EveryOmissionIsReported(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusQueued, 40), makeRows(jobKindTask, jobStatusRunning, 40)...)
	all = append(all, makeRows(jobKindSubagent, jobStatusBlocked, 40)...)
	all = append(all, makeRows(jobKindPlan, jobStatusFailed, 40)...)
	sortJobRows(all)

	got := applyBounds(all, maxRows)

	wantOmitted := len(all) - len(got.selected)
	if got.totalOmitted != wantOmitted {
		t.Fatalf("total_omitted: want %d, got %d", wantOmitted, got.totalOmitted)
	}
	if got.totalOmitted == 0 {
		t.Fatal("fixture bug: nothing was truncated, so nothing is being tested")
	}

	sum := func(m map[string]int) int {
		n := 0
		for _, v := range m {
			n += v
		}
		return n
	}
	if s := sum(got.omittedByKind); s != got.totalOmitted {
		t.Errorf("omitted.by_kind sums to %d, want total_omitted %d", s, got.totalOmitted)
	}
	if s := sum(got.omittedByStatus); s != got.totalOmitted {
		t.Errorf("omitted.by_status sums to %d, want total_omitted %d", s, got.totalOmitted)
	}
}

// TestBounds_NothingOmittedWhenEverythingFits guards the other direction: a
// small roster must report no omissions at all, so `notes` can stay null.
func TestBounds_NothingOmittedWhenEverythingFits(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusQueued, 2), makeRows(jobKindTask, jobStatusRunning, 2)...)
	sortJobRows(all)

	got := applyBounds(all, maxRows)

	if len(got.selected) != 4 {
		t.Fatalf("want all 4 rows, got %d", len(got.selected))
	}
	if got.totalOmitted != 0 || len(got.omittedByKind) != 0 || len(got.omittedByStatus) != 0 {
		t.Errorf("nothing was truncated but omissions were reported: %+v", got)
	}
}

// TestSortOrder_TotalOnEmptyStartedAt is the flake-proofing property.
//
// Every queued row has an EMPTY started_at — an approved plan and an inbox
// task have never started — so the whole group ties on the timestamp, and
// sort.Slice is not stable. Without the (kind, id) tail the permutation would
// vary per call and the failure would appear as CI flake rather than as a
// review finding.
func TestSortOrder_TotalOnEmptyStartedAt(t *testing.T) {
	base := make([]jobRow, 0, 40)
	for i := 0; i < 40; i++ {
		base = append(base, jobRow{
			Kind:      []string{jobKindPlan, jobKindSubagent, jobKindTask}[i%3],
			ID:        fmt.Sprintf("id-%03d", i),
			Status:    jobStatusQueued,
			StartedAt: "",
		})
	}

	reference := append([]jobRow(nil), base...)
	sortJobRows(reference)

	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic shuffle, not crypto
	for trial := 0; trial < 50; trial++ {
		shuffled := append([]jobRow(nil), base...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		sortJobRows(shuffled)
		if !reflect.DeepEqual(shuffled, reference) {
			t.Fatalf("trial %d produced a different permutation — the comparator is not total", trial)
		}
	}
}

// TestSortOrder_LiveAscendingTerminalDescending pins the per-group direction.
//
// Truncation takes the head, and the whole premise of the tool is recovering a
// handle for work that has been running long enough to fall out of context.
// Descending live groups would systematically hide exactly those jobs and show
// the ones the agent just started and still has ids for.
func TestSortOrder_LiveAscendingTerminalDescending(t *testing.T) {
	rows := []jobRow{
		{Kind: jobKindPlan, ID: "new-run", Status: jobStatusRunning, StartedAt: "2026-07-27T00:00:00Z"},
		{Kind: jobKindPlan, ID: "old-run", Status: jobStatusRunning, StartedAt: "2026-07-01T00:00:00Z"},
		{Kind: jobKindPlan, ID: "new-fail", Status: jobStatusFailed, StartedAt: "2026-07-27T00:00:00Z"},
		{Kind: jobKindPlan, ID: "old-fail", Status: jobStatusFailed, StartedAt: "2026-07-01T00:00:00Z"},
	}
	sortJobRows(rows)

	got := []string{rows[0].ID, rows[1].ID, rows[2].ID, rows[3].ID}
	want := []string{"old-run", "new-run", "new-fail", "old-fail"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("want live oldest-first then terminal newest-first %v, got %v", want, got)
	}
}

// TestSortOrder_GroupOrderIsQueuedRunningBlockedFailedCompleted pins the group
// order itself.
func TestSortOrder_GroupOrderIsQueuedRunningBlockedFailedCompleted(t *testing.T) {
	rows := []jobRow{
		{Kind: jobKindPlan, ID: "e", Status: jobStatusCompleted},
		{Kind: jobKindPlan, ID: "d", Status: jobStatusFailed},
		{Kind: jobKindPlan, ID: "c", Status: jobStatusBlocked},
		{Kind: jobKindPlan, ID: "b", Status: jobStatusRunning},
		{Kind: jobKindPlan, ID: "a", Status: jobStatusQueued},
	}
	sortJobRows(rows)

	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Status)
	}
	if !reflect.DeepEqual(got, jobStatusOrder) {
		t.Errorf("want group order %v, got %v", jobStatusOrder, got)
	}
}

// TestSortOrder_DraftsRankBelowApprovedWithinQueued: abandoned drafts
// accumulate monotonically and nothing sweeps them, so without this rank 26
// stale drafts would evict every real cap-waiting plan from the roster.
func TestSortOrder_DraftsRankBelowApprovedWithinQueued(t *testing.T) {
	rows := []jobRow{
		{Kind: jobKindPlan, ID: "draft-a", Status: jobStatusQueued, draftRank: 1},
		{Kind: jobKindPlan, ID: "approved-z", Status: jobStatusQueued, draftRank: 0},
	}
	sortJobRows(rows)

	if rows[0].ID != "approved-z" {
		t.Errorf("approved plans must rank above drafts inside queued, got %q first", rows[0].ID)
	}
}

// TestSortOrder_SelectionOrderIsNotEmissionOrder: the round-robin spends the
// budget across groups, but the response must still come back in group order,
// so the shape never depends on how the budget happened to be spent.
func TestSortOrder_SelectionOrderIsNotEmissionOrder(t *testing.T) {
	all := append(makeRows(jobKindPlan, jobStatusQueued, 5), makeRows(jobKindTask, jobStatusRunning, 5)...)
	all = append(all, makeRows(jobKindSubagent, jobStatusBlocked, 5)...)
	sortJobRows(all)

	got := applyBounds(all, 9)

	if len(got.selected) != 9 {
		t.Fatalf("want 9 rows, got %d", len(got.selected))
	}
	for i := 1; i < len(got.selected); i++ {
		prev, cur := got.selected[i-1], got.selected[i]
		if statusGroupRank(cur.Status) < statusGroupRank(prev.Status) {
			t.Fatalf("emission order is not sorted: %q(%s) came after %q(%s)",
				cur.ID, cur.Status, prev.ID, prev.Status)
		}
	}
}

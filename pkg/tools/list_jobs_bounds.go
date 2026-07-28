package tools

import "sort"

// This file holds `list_jobs`' ordering and boundedness rules: the total row
// comparator (FR-007, FR-020), the per-status-group sub-bounds and the
// round-robin allocation of a caller-supplied `limit` (FR-016), and the exact
// omission accounting that reports every dropped row (FR-017).

// Per-status-group sub-bounds (FR-016). Each live group has its OWN reserved
// budget: a group's unused budget is never reallocated to another group,
// because a reservation that can be borrowed is not a reservation.
//
// These sub-bounds are the ONLY anti-starvation mechanism in the design — the
// alternative (reordering the groups to put `blocked` first) was withdrawn by
// the operator, so they carry the whole load and are stated as numbers rather
// than as a principle.
const (
	subBoundQueued  = 25
	subBoundRunning = 25
	subBoundBlocked = 25
	// subBoundTerminal is a SHARED budget across `failed` and `completed`,
	// not 20 each: FR-030's response-size identity is
	// 75 live + 20 terminal = 95 rows. It is allocated between the two
	// terminal groups by the same round-robin rule used for `limit`, so a
	// large `failed` population cannot evict every `completed` row.
	subBoundTerminal = 20

	// maxRows is the hard ceiling on rows in any response and is the row count
	// FR-030's maxResponseBytes identity is derived from.
	maxRows = subBoundQueued + subBoundRunning + subBoundBlocked + subBoundTerminal

	// hardLimitMax is the largest accepted `limit`. A larger value is CLAMPED
	// and the clamp reported, never rejected — clamping is the one disposition
	// that never costs the caller a turn. Note that the range above maxRows is
	// only reachable at all with include_terminal=true.
	hardLimitMax = 200
)

// jobStatusOrder is FR-007's group order, unchanged from ADR-056 D3. It is
// both the emission order and the round-robin visiting order.
var jobStatusOrder = []string{ //nolint:gochecknoglobals // fixed vocabulary, never mutated
	jobStatusQueued,
	jobStatusRunning,
	jobStatusBlocked,
	jobStatusFailed,
	jobStatusCompleted,
}

// statusGroupRank returns the sort rank of a normalized status. An unknown
// value ranks last rather than panicking; normalizeStatus never produces one,
// so this is a defensive total-ordering guarantee, not a live path.
func statusGroupRank(status string) int {
	for i, s := range jobStatusOrder {
		if s == status {
			return i
		}
	}
	return len(jobStatusOrder)
}

// subBoundFor returns the reserved budget of a live status group. Terminal
// groups share subBoundTerminal and are handled separately by
// allocateTerminalBudget.
func subBoundFor(status string) int {
	switch status {
	case jobStatusQueued:
		return subBoundQueued
	case jobStatusRunning:
		return subBoundRunning
	case jobStatusBlocked:
		return subBoundBlocked
	default:
		return 0
	}
}

// lessJobRow is a TOTAL comparator over any fixed row set (FR-020).
//
// Totality is not a nicety here. `started_at` is empty for every `queued` row
// — an approved plan and an inbox task have never started — so the whole
// group ties on "", and sort.Slice is NOT stable: an ordering that stopped at
// the timestamp would produce a different permutation per call and flake in CI
// rather than fail in review. The (kind, id) tail makes the key unique.
//
// Intra-group direction is per group, not inherited (FR-007):
//   - live groups ascend by started_at — OLDEST FIRST. Truncation takes the
//     head, and the whole premise of the tool is recovering a handle for work
//     that has been running long enough to fall out of context. Descending
//     would systematically hide exactly those jobs.
//   - terminal groups descend — most recently finished first, which is what
//     the group is for.
func lessJobRow(a, b jobRow) bool {
	ra, rb := statusGroupRank(a.Status), statusGroupRank(b.Status)
	if ra != rb {
		return ra < rb
	}
	// Within `queued`, approved plans rank above drafts. Abandoned drafts
	// accumulate monotonically, and without this 26 of them would evict every
	// real cap-waiting plan from the roster.
	if a.Status == jobStatusQueued && a.draftRank != b.draftRank {
		return a.draftRank < b.draftRank
	}
	if a.StartedAt != b.StartedAt {
		if terminalStatus(a.Status) {
			return a.StartedAt > b.StartedAt
		}
		return a.StartedAt < b.StartedAt
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.ID < b.ID
}

// sortJobRows orders rows in place under lessJobRow.
func sortJobRows(rows []jobRow) {
	sort.Slice(rows, func(i, j int) bool { return lessJobRow(rows[i], rows[j]) })
}

// boundsResult is the outcome of applying FR-016's bounds to a sorted roster.
type boundsResult struct {
	// selected are the rows the response carries, in FR-007 emission order.
	selected []jobRow
	// omittedByKind and omittedByStatus each sum to totalOmitted, by
	// construction: both are derived from the SAME dropped-row set rather
	// than counted independently (FR-017).
	omittedByKind   map[string]int
	omittedByStatus map[string]int
	totalOmitted    int
}

// applyBounds reduces a sorted roster to the response's rows.
//
// Two stages, in order:
//
//  1. Per-group sub-bounds. Each live group is cut to its own reserved budget;
//     the two terminal groups share subBoundTerminal.
//
//  2. Round-robin allocation of `limit` ACROSS the surviving groups — never a
//     tail-truncating total cap. A total cap over a list sorted
//     queued -> running -> blocked hands a caller passing limit=30 against
//     25 queued + 25 running + 3 blocked exactly 25 queued + 5 running and
//     ZERO blocked rows: the sub-bound reserves them and the total cap removes
//     them again, because `blocked` sorts last. A small `limit` is precisely
//     what a context-conscious agent passes, and a dropped `blocked` row may
//     be a subagent waiting on an answer only this caller can give.
//
// SELECTION ORDER IS NOT EMISSION ORDER: the selected rows are re-sorted into
// FR-007 order before returning, so the response's shape never depends on how
// the budget was spent.
func applyBounds(sorted []jobRow, limit int) boundsResult {
	groups := bucketByStatus(sorted)
	kept := make(map[string][]jobRow, len(jobStatusOrder))
	dropped := make([]jobRow, 0)

	// Stage 1a: the three live groups, each against its own reservation.
	for _, status := range []string{jobStatusQueued, jobStatusRunning, jobStatusBlocked} {
		rows := groups[status]
		bound := subBoundFor(status)
		if len(rows) > bound {
			kept[status] = rows[:bound]
			dropped = append(dropped, rows[bound:]...)
			continue
		}
		kept[status] = rows
	}

	// Stage 1b: the two terminal groups against their shared reservation.
	terminalKept, terminalDropped := allocateTerminalBudget(
		groups[jobStatusFailed], groups[jobStatusCompleted], subBoundTerminal)
	kept[jobStatusFailed] = terminalKept[jobStatusFailed]
	kept[jobStatusCompleted] = terminalKept[jobStatusCompleted]
	dropped = append(dropped, terminalDropped...)

	// Stage 2: spend `limit` round-robin across every surviving group.
	selected, limitDropped := roundRobinSelect(kept, limit)
	dropped = append(dropped, limitDropped...)

	sortJobRows(selected)

	res := boundsResult{
		selected:        selected,
		omittedByKind:   map[string]int{},
		omittedByStatus: map[string]int{},
		totalOmitted:    len(dropped),
	}
	for _, row := range dropped {
		res.omittedByKind[row.Kind]++
		res.omittedByStatus[row.Status]++
	}
	return res
}

// bucketByStatus splits a sorted roster into per-status slices, preserving the
// input order within each bucket (which is that group's own FR-007 order).
func bucketByStatus(sorted []jobRow) map[string][]jobRow {
	groups := make(map[string][]jobRow, len(jobStatusOrder))
	for _, row := range sorted {
		groups[row.Status] = append(groups[row.Status], row)
	}
	return groups
}

// allocateTerminalBudget splits the shared terminal reservation between
// `failed` and `completed` by round-robin, so neither can starve the other.
func allocateTerminalBudget(failed, completed []jobRow, budget int) (map[string][]jobRow, []jobRow) {
	kept := map[string][]jobRow{
		jobStatusFailed:    {},
		jobStatusCompleted: {},
	}
	taken := map[string]int{}
	remaining := budget
	order := []string{jobStatusFailed, jobStatusCompleted}
	pools := map[string][]jobRow{jobStatusFailed: failed, jobStatusCompleted: completed}

	for remaining > 0 {
		progressed := false
		for _, status := range order {
			if remaining == 0 {
				break
			}
			if taken[status] >= len(pools[status]) {
				continue
			}
			kept[status] = append(kept[status], pools[status][taken[status]])
			taken[status]++
			remaining--
			progressed = true
		}
		if !progressed {
			break
		}
	}

	// max(0, …): the budget routinely exceeds the population, and a negative
	// capacity panics.
	dropped := make([]jobRow, 0, max(0, len(failed)+len(completed)-budget))
	for _, status := range order {
		dropped = append(dropped, pools[status][taken[status]:]...)
	}
	return kept, dropped
}

// roundRobinSelect spends `limit` one row at a time across the groups in
// FR-007 order, repeating until the budget is exhausted or every group is.
// Within a group rows are taken in that group's own order, so the rows that
// survive a tight budget are the ones that group's ordering ranks highest.
func roundRobinSelect(kept map[string][]jobRow, limit int) (selected, dropped []jobRow) {
	taken := make(map[string]int, len(kept))
	selected = make([]jobRow, 0, limit)
	remaining := limit

	for remaining > 0 {
		progressed := false
		for _, status := range jobStatusOrder {
			if remaining == 0 {
				break
			}
			pool := kept[status]
			if taken[status] >= len(pool) {
				continue
			}
			selected = append(selected, pool[taken[status]])
			taken[status]++
			remaining--
			progressed = true
		}
		if !progressed {
			break
		}
	}

	total := 0
	for _, status := range jobStatusOrder {
		total += len(kept[status])
	}
	dropped = make([]jobRow, 0, max(0, total-len(selected)))
	for _, status := range jobStatusOrder {
		pool := kept[status]
		dropped = append(dropped, pool[taken[status]:]...)
	}
	return selected, dropped
}

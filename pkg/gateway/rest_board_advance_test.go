//go:build !cgo

// BDD: unified task blocked_by DAG advance on completion (FR-6.5).
//
// Sprint 2 changes:
//   - "waiting" status replaced by "blocked" (unified 7-state vocabulary).
//   - PUT /board/tasks/{id} replaced by PATCH /api/v1/tasks/{id}.
//   - GET /board/tasks/{id} replaced by GET /api/v1/tasks/{id}.
//   - createBoardTaskViaAPI replaced by createTaskViaAPI.
//   - putBoardTask helper replaced by patchTask.
//   - getBoardTaskStatus helper replaced by getTaskStatus.
//   - TestHandleBoardTaskStart_AutonomousCompletionAdvancesDependent is DELETED
//     because the /start endpoint is REMOVED in Sprint 2.
//
// Completing a task un-gates its blocked dependents (blocked→next) once
// all of their blocked_by deps are done. Unified model — no auto-dispatch.
//
// Traces to: FR-6.5 (auto-advance blocked→next on dependency completion)

package gateway

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
)

// TestHandleTasks_CompleteAdvancesBlockedDependent verifies that PATCH A→done
// un-gates B (blocked, blocked_by [A]) to next (FR-6.5).
//
// Sprint 2: "waiting" → "blocked" in the 7-state vocabulary.
// The auto-advance path (AdvanceBlockedDependents) is triggered by the PATCH
// handler when status transitions to "done".
//
// BDD:
//
//	Given task A in status=next and task B in status=blocked with blocked_by=[A],
//	When PATCH /api/v1/tasks/{a.id} {"status":"done"},
//	Then GET /api/v1/tasks/{b.id} returns status=next.
//
// Traces to: FR-6.5 — blocked→next on dependency done
func TestHandleTasks_CompleteAdvancesBlockedDependent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Create A (the blocker) and advance it to next.
	a := createTaskViaAPI(t, api, "Blocker A", wsID)
	wA := patchTask(t, api, a.Id, `{"status":"next","description":"blocker task ready"}`)
	require.Equal(t, 200, wA.Code, "PATCH A→next must return 200; body=%s", wA.Body.String())

	// Gate B: seed directly to disk in `blocked` status with blocked_by=[A].
	// status=blocked is a derived side-state rejected at the gateway seam;
	// use seedBlockedTask for test setup.
	const bID = "test-fr65-complete-advance-b-01"
	seedBlockedTask(t, api, bID, "Dependent B", wsID, []string{a.Id})

	// Precondition: B must be blocked before A completes.
	require.Equal(t, gen.TaskStatus("blocked"), getTaskStatus(t, api, bID),
		"precondition: B must be blocked before A completes")

	// Advance A through in_progress → done.
	wIP := patchTask(t, api, a.Id, `{"status":"in_progress"}`)
	require.Equal(t, 200, wIP.Code, "PATCH A→in_progress; body=%s", wIP.Body.String())
	wDone := patchTask(t, api, a.Id, `{"status":"done"}`)
	require.Equal(t, 200, wDone.Code, "PATCH A→done must return 200; body=%s", wDone.Body.String())

	// B must now be advanced to next (FR-6.5 auto-advance).
	require.Equal(t, gen.TaskStatus("next"), getTaskStatus(t, api, bID),
		"B must be advanced blocked→next after A is done (FR-6.5)")
}

// TestHandleTasks_CompleteWithUnsatisfiedDepStaysBlocked verifies a dependent
// gated on two blockers stays blocked until BOTH are done.
//
// BDD:
//
//	Given task C blocked_by [A, X], both A and X in status=next,
//	When PATCH A→done,
//	Then C stays blocked (X not done yet).
//	When PATCH X→done,
//	Then C advances to next.
//
// Traces to: FR-6.5 — blocked stays blocked until all deps done
func TestHandleTasks_CompleteWithUnsatisfiedDepStaysBlocked(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	a := createTaskViaAPI(t, api, "Blocker A", wsID)
	x := createTaskViaAPI(t, api, "Blocker X", wsID)

	// Advance both blockers to next (description required by the partial-task guard).
	wA := patchTask(t, api, a.Id, `{"status":"next","description":"blocker A ready"}`)
	require.Equal(t, 200, wA.Code, "PATCH A→next; body=%s", wA.Body.String())
	wX := patchTask(t, api, x.Id, `{"status":"next","description":"blocker X ready"}`)
	require.Equal(t, 200, wX.Code, "PATCH X→next; body=%s", wX.Body.String())

	// Gate C: seed directly to disk in `blocked` status with blocked_by=[A, X].
	const cID = "test-fr65-unsatisfied-c-00001"
	seedBlockedTask(t, api, cID, "Dependent C", wsID, []string{a.Id, x.Id})

	// Complete only A (next→in_progress→done). X is still next — C must stay blocked.
	wIPa := patchTask(t, api, a.Id, `{"status":"in_progress"}`)
	require.Equal(t, 200, wIPa.Code, "PATCH A→in_progress; body=%s", wIPa.Body.String())
	wDoneA := patchTask(t, api, a.Id, `{"status":"done"}`)
	require.Equal(t, 200, wDoneA.Code, "PATCH A→done; body=%s", wDoneA.Body.String())

	require.Equal(t, gen.TaskStatus("blocked"), getTaskStatus(t, api, cID),
		"C must stay blocked while X is not done (only one dep satisfied)")

	// Complete X — C must advance to next (all deps done).
	wIPx := patchTask(t, api, x.Id, `{"status":"in_progress"}`)
	require.Equal(t, 200, wIPx.Code, "PATCH X→in_progress; body=%s", wIPx.Body.String())
	wDoneX := patchTask(t, api, x.Id, `{"status":"done"}`)
	require.Equal(t, 200, wDoneX.Code, "PATCH X→done; body=%s", wDoneX.Body.String())

	require.Equal(t, gen.TaskStatus("next"), getTaskStatus(t, api, cID),
		"C must advance blocked→next once BOTH A and X are done (FR-6.5)")
}

// TestHandleTasks_BlockedByDAG_CycleRejected verifies that the DAG validator
// rejects cycles: if B is blocked_by [A] and we try to set A blocked_by [B],
// the cycle must be rejected with 400.
//
// BDD:
//
//	Given B blocked_by [A],
//	When PATCH A with blocked_by [B] (creating a 2-node cycle A→B→A),
//	Then 400 (cycle rejection).
//
// Traces to: FR-6.5 — DAG cycle rejection
func TestHandleTasks_BlockedByDAG_CycleRejected(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	a := createTaskViaAPI(t, api, "A (would-be cycle root)", wsID)
	b := createTaskViaAPI(t, api, "B depends on A", wsID)

	// Gate B: set blocked_by=[A] (status stays inbox — only the edge matters for cycle detection).
	wGate := patchTask(t, api, b.Id, fmt.Sprintf(`{"blocked_by":[%q]}`, a.Id))
	require.Equal(t, 200, wGate.Code, "gate B blocked_by=[A]; body=%s", wGate.Body.String())

	// Try to make A depend on B — creates 2-node cycle A→B→A → 400.
	wCycle := patchTask(t, api, a.Id, fmt.Sprintf(`{"blocked_by":[%q]}`, b.Id))
	require.Equal(t, 400, wCycle.Code,
		"PATCH A blocked_by=[B] must return 400 (cycle A→B→A); body=%s", wCycle.Body.String())

	// Differentiation: adding a non-cycle dependency → 200.
	c := createTaskViaAPI(t, api, "C (no cycle)", wsID)
	wNoCycle := patchTask(t, api, c.Id, fmt.Sprintf(`{"blocked_by":[%q]}`, a.Id))
	require.Equal(t, 200, wNoCycle.Code,
		"PATCH C blocked_by=[A] must return 200 (no cycle); body=%s", wNoCycle.Body.String())

	// The two PATCH calls return different codes — proves cycle detection is real.
	assert.NotEqual(t, wCycle.Code, wNoCycle.Code,
		"cycle PATCH and non-cycle PATCH must return different status codes (not hardcoded)")
}

// TestHandleTasks_DeleteAdvancesBlockedDependent verifies that deleting the last
// blocker of a blocked task causes that task to advance to next
// (cascade-delete edges → status advance in handleTaskDelete).
//
// BDD:
//
//	Given B blocked_by [A] (status=blocked),
//	When DELETE /api/v1/tasks/{a.id},
//	Then B status advances to next (the only blocker is gone).
//
// Previously BLOCKED (production gap now fixed): handleTaskDelete in
// pkg/gateway/rest_tasks.go now iterates the unblocked IDs returned by
// store.Delete, reads each dependent whose status is still "blocked", and
// advances it to "next" via taskStore.Update — same cascade logic as
// handleTaskUpdate / AdvanceBlockedDependents.
//
// Traces to: FR-6.5 — cascade edge delete + auto-advance
func TestHandleTasks_DeleteAdvancesBlockedDependent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	// Create A (the blocker).
	a := createTaskViaAPI(t, api, "Blocker A", wsID)

	// Gate B: seed directly to disk in `blocked` status with blocked_by=[A].
	// status=blocked is a derived side-state rejected at the gateway seam;
	// use seedBlockedTask for test setup.
	const bID = "test-fr65-delete-advance-b-001"
	seedBlockedTask(t, api, bID, "Dependent B", wsID, []string{a.Id})

	// Precondition: B must be blocked before A is deleted.
	require.Equal(t, gen.TaskStatus("blocked"), getTaskStatus(t, api, bID),
		"precondition: B must be blocked before A is deleted")

	// DELETE A — this should cascade-clear B.blocked_by and advance B to next.
	wDel := httptest.NewRecorder()
	rDel := httptest.NewRequest("DELETE", "/api/v1/tasks/"+a.Id, nil)
	rDel.URL.Path = "/api/v1/tasks/" + a.Id
	api.HandleTasks(wDel, rDel)
	require.Equal(t, 204, wDel.Code,
		"DELETE A must return 204; body=%s", wDel.Body.String())

	// Differentiation / content assertion: B must now be next, not still blocked.
	require.Equal(t, gen.TaskStatus("next"), getTaskStatus(t, api, bID),
		"B must be advanced blocked→next after its only blocker A is deleted (FR-6.5 cascade delete advance)")
}

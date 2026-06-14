//go:build !cgo

// BDD: board-task blocked_by DAG advance on completion (FR-6.5).
// Completing a board task un-gates its waiting dependents (waiting→next) once
// all of their blocked_by deps are done. GTD semantics — no auto-dispatch.

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gen "github.com/dapicom-ai/omnipus/pkg/api/generated"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// putBoardTask is a small helper that PUTs a JSON body to the board task and
// returns the recorder.
func putBoardTask(t *testing.T, api *restAPI, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/board/tasks/"+id, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.URL.Path = "/api/v1/board/tasks/" + id
	api.HandleBoardTasks(w, r)
	return w
}

// getBoardTaskStatus reads back a board task and returns its status.
func getBoardTaskStatus(t *testing.T, api *restAPI, id string) gen.BoardTaskStatus {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/board/tasks/"+id, nil)
	r.URL.Path = "/api/v1/board/tasks/" + id
	api.HandleBoardTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET board task; body=%s", w.Body.String())
	var task gen.BoardTask
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &task))
	return task.Status
}

// TestHandleBoardTasks_CompleteAdvancesWaitingDependent verifies that PUT
// A→done un-gates B (waiting, blocked_by [A]) to next (FR-6.5).
func TestHandleBoardTasks_CompleteAdvancesWaitingDependent(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	a := createBoardTaskViaAPI(t, api, "Blocker A", "next")
	b := createBoardTaskViaAPI(t, api, "Dependent B", "inbox")

	// Gate B: blocked_by [A], status waiting.
	wGate := putBoardTask(t, api, b.Id, fmt.Sprintf(`{"status":"waiting","blocked_by":[%q]}`, a.Id))
	require.Equal(t, http.StatusOK, wGate.Code, "gate B; body=%s", wGate.Body.String())

	// Complete A.
	wDone := putBoardTask(t, api, a.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDone.Code, "complete A; body=%s", wDone.Body.String())

	// B must now be advanced to next.
	require.Equal(t, gen.BoardTaskStatus("next"), getBoardTaskStatus(t, api, b.Id),
		"B should be advanced waiting→next after A completes")
}

// TestHandleBoardTasks_CompleteWithUnsatisfiedDepStaysWaiting verifies a
// dependent gated on two blockers stays waiting until BOTH are done.
func TestHandleBoardTasks_CompleteWithUnsatisfiedDepStaysWaiting(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	a := createBoardTaskViaAPI(t, api, "Blocker A", "next")
	x := createBoardTaskViaAPI(t, api, "Blocker X", "next")
	c := createBoardTaskViaAPI(t, api, "Dependent C", "inbox")

	// Gate C: blocked_by [A, X], waiting.
	wGate := putBoardTask(t, api, c.Id, fmt.Sprintf(`{"status":"waiting","blocked_by":[%q,%q]}`, a.Id, x.Id))
	require.Equal(t, http.StatusOK, wGate.Code, "gate C; body=%s", wGate.Body.String())

	// Complete only A. X is still next.
	wDone := putBoardTask(t, api, a.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDone.Code, "complete A; body=%s", wDone.Body.String())

	require.Equal(t, gen.BoardTaskStatus("waiting"), getBoardTaskStatus(t, api, c.Id),
		"C should stay waiting while X is not done")

	// Now complete X — C must advance.
	wDoneX := putBoardTask(t, api, x.Id, `{"status":"done"}`)
	require.Equal(t, http.StatusOK, wDoneX.Code, "complete X; body=%s", wDoneX.Body.String())

	require.Equal(t, gen.BoardTaskStatus("next"), getBoardTaskStatus(t, api, c.Id),
		"C should advance waiting→next once both A and X are done")
}

// TestHandleBoardTaskStart_AutonomousCompletionAdvancesDependent verifies the
// fix for the /start completion-callback gap: when task A is run autonomously via
// POST /start and its onComplete callback writes status=done, any task waiting on
// A (waiting, blocked_by [A]) must be un-gated to next — exactly as the PUT path
// does. Before the fix the callback never called AdvanceBlockedDependents, so
// dependents of an autonomously-completed task were stranded in "waiting" forever.
func TestHandleBoardTaskStart_AutonomousCompletionAdvancesDependent(t *testing.T) {
	api := newTestRestAPIWithProvider(t, &restMockProvider{})

	// A is a startable task with a prompt; B waits on A.
	a := createBoardTaskWithPromptViaAPI(t, api, "Autostart Blocker A", "inbox", "Do the blocking work")
	b := createBoardTaskViaAPI(t, api, "Dependent B", "inbox")

	// Gate B: blocked_by [A], status waiting.
	wGate := putBoardTask(t, api, b.Id, fmt.Sprintf(`{"status":"waiting","blocked_by":[%q]}`, a.Id))
	require.Equal(t, http.StatusOK, wGate.Code, "gate B; body=%s", wGate.Body.String())
	require.Equal(t, gen.BoardTaskStatus("waiting"), getBoardTaskStatus(t, api, b.Id),
		"precondition: B must be waiting before A starts")

	// Start A via the real agent path.
	wStart := postBoardTaskStart(t, api, a.Id)
	require.Equal(t, http.StatusAccepted, wStart.Code, "/start A must return 202; body=%s", wStart.Body.String())

	// Drain the dispatched goroutine so the onComplete callback runs.
	api.agentLoop.WaitForActiveRequests()

	// A must be terminal (done) and B must have advanced waiting→next.
	require.Equal(t, gen.BoardTaskStatus("done"), getBoardTaskStatus(t, api, a.Id),
		"A must be done after the autonomous run drains")
	require.Equal(t, gen.BoardTaskStatus("next"), getBoardTaskStatus(t, api, b.Id),
		"B must be advanced waiting→next after A completes autonomously via /start")
}

// _ keeps the providers import referenced even if the mock provider type lives in
// another test file; restMockProvider satisfies providers.LLMProvider.
var _ providers.LLMProvider = (*restMockProvider)(nil)

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// Oracle: #1026 caller follow-up, CHECK F1. After a persistent binding failure,
// the task MUST remain Failed with its cause visible after the actual HTTP
// Start caller returns. A store/helper-only test misses launchIfStarted's
// rollback. The real PATCH/GET handlers, inline executor, session creation,
// store validations and atomic status writes stay in the boundary; only the
// target task's session-binding filesystem writes fail. No error sentinel or
// implementation shape is prescribed. Admission-failure rollback is a distinct
// class, retained unmodified in rest_tasks_start_test.go. Launcher dispatch and
// compound Failed-write failures remain outside this caller regression.
func TestHandleTaskPatch_SessionBindingFailureRemainsFailed(t *testing.T) {
	fault := installRESTBindingWriteFault(t)
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	tsk := createTaskViaAPI(t, api, "Caller binding failure", wsID)
	advanceTaskToNext(t, api, tsk.Id)
	assigned := patchTask(t, api, tsk.Id, `{"agent_id":"mia"}`)
	if assigned.Code != http.StatusOK {
		t.Fatalf("assign real registered worker: HTTP %d, want 200; body=%s", assigned.Code, assigned.Body.String())
	}
	before := getTaskFull(t, api, tsk.Id)
	if before.Status != gen.TaskStatusNext || before.SessionId != nil {
		t.Fatalf("caller precondition: status/session = %q/%v, want next/nil", before.Status, before.SessionId)
	}
	if api.taskExecutor == nil {
		t.Fatal("BLOCKED: real task executor unavailable — required by #1026 REST caller regression")
	}
	fault.target(filepath.Join(api.taskStore.Dir(), tsk.Id+".json"))

	response := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("REST caller fixture needs a test deadline to drain all executor work")
	}
	api.taskExecutor.Drain(time.Until(deadline) + time.Minute)
	if response.Code != http.StatusInternalServerError {
		t.Errorf("binding-failure PATCH: HTTP %d, want 500; body=%s", response.Code, response.Body.String())
	}
	var rejected gen.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode real PATCH error response: %v; body=%s", err, response.Body.String())
	}
	// Leak round 6 (T1-3): the visible error is FIXED text; the raw
	// binding-write cause must never reach the client.
	if !strings.Contains(rejected.Error, "the task could not be started. Check the agent and try again; details are in the server log.") {
		t.Errorf("PATCH error = %q, want the fixed start-failure text", rejected.Error)
	}
	if strings.Contains(rejected.Error, fault.cause.Error()) {
		t.Errorf("PATCH error = %q, must not surface the raw binding-write cause %q", rejected.Error, fault.cause.Error())
	}

	bindings, persistedFails := fault.snapshot()
	if len(bindings) != 2 { // #1026: initial binding write plus one retry.
		t.Errorf("binding write attempts = %d, want 2 (both persistently rejected)", len(bindings))
	}
	for i, sessionID := range bindings {
		if sessionID == "" || sessionID != bindings[0] {
			t.Errorf("binding attempt %d session = %q, want same nonempty session %q", i+1, sessionID, bindings[0])
		}
	}
	if len(persistedFails) == 0 {
		t.Fatal("fixture did not persist the executor's Failed disposition; cannot test caller preservation")
	}
	for _, failed := range persistedFails {
		if !strings.Contains(failed.Result, fault.cause.Error()) {
			t.Errorf("executor's persisted Failed result = %q, want binding cause %q", failed.Result, fault.cause.Error())
		}
	}

	// Both independent disk and public GET observations happen AFTER the
	// handler, including launchIfStarted's error/rollback branch, returned.
	stored, err := task.New(api.taskStore.Dir()).Get(tsk.Id)
	if err != nil {
		t.Fatalf("reopen task after real PATCH caller returned: %v", err)
	}
	if stored.Status != task.StatusFailed {
		t.Errorf("after real PATCH caller returned: durable status = %q, want %q (must not revert to prior %q after executor persisted Failed); result=%q", stored.Status, task.StatusFailed, before.Status, stored.Result)
	}
	if !strings.Contains(stored.Result, fault.cause.Error()) {
		t.Errorf("durable result = %q, want complete binding-write cause %q", stored.Result, fault.cause.Error())
	}
	if stored.SessionID != "" {
		t.Errorf("durable session = %q, want empty because both binding writes failed", stored.SessionID)
	}
	after := getTaskFull(t, api, tsk.Id)
	if after.Status != gen.TaskStatusFailed {
		t.Errorf("GET after real PATCH caller returned: status = %q, want %q (not prior %q)", after.Status, gen.TaskStatusFailed, before.Status)
	}
	if after.Result == nil || !strings.Contains(*after.Result, fault.cause.Error()) {
		t.Errorf("GET result = %v, want visible binding-write cause %q", after.Result, fault.cause.Error())
	}
}

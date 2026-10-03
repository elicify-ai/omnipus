// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// restUnrelatedFailedCauseFault reuses the EXISTING gatewayTaskAtomicWriter
// seam declared in rest_task_session_binding_fault_test.go (the same alias,
// via go:linkname, to task.writeFileAtomicFn) for a distinct scenario from
// that file's restBindingWriteFault: here the session-binding write for the
// target task persistently fails with bindingCause, exactly as
// installRESTBindingWriteFault does, so the real executor's own
// failTaskBeforeDispatch (pkg/agent/task_executor_session_binding.go)
// attempts to persist a Failed disposition carrying bindingCause. This
// fixture then swaps ONLY the bytes actually committed to disk for that one
// Failed write to an unrelated cause — simulating an out-of-band writer
// (e.g. a differently-caused concurrent attempt) that persisted its own
// Failed disposition for this task. failTaskBeforeDispatch returns its input
// `cause` directly, independent of what the store write actually persists
// (it never re-reads after writing), so the real caller's returned dispatch
// error is unaffected by this substitution while the durable Result differs
// from it — the exact shape CHECK round 5 F2 requires to exercise
// launchIfStarted's cause-equality comparison at rest_tasks.go:1575. All
// unrelated writes delegate to the original real atomic writer. Serial
// tests only (package-global writer seam); never call t.Parallel.
type restUnrelatedFailedCauseFault struct {
	mu             sync.Mutex
	path           string
	bindingCause   error
	unrelatedCause string
	bindings       []string
	persistedFails []task.Task
}

func installRESTUnrelatedFailedCauseFault(t *testing.T) *restUnrelatedFailedCauseFault {
	t.Helper()
	f := &restUnrelatedFailedCauseFault{
		bindingCause:   errors.New("RED1026-F2: REST session binding persistence failed"),
		unrelatedCause: "RED1026-F2: unrelated out-of-band Failed disposition",
	}
	original := gatewayTaskAtomicWriter
	if original == nil {
		t.Fatal("BLOCKED: existing task atomic-writer seam unavailable — required by #1026 CHECK round 5 F2 differing-cause regression")
	}
	gatewayTaskAtomicWriter = func(path string, data []byte, perm os.FileMode) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		if path != f.path {
			return original(path, data, perm)
		}
		var attempted task.Task
		if err := json.Unmarshal(data, &attempted); err != nil {
			return fmt.Errorf("F2 fault fixture: decode attempted task: %w", err)
		}
		if attempted.Status == task.StatusFailed {
			// Commit an UNRELATED Result to disk instead of the attempted
			// bytes. The executor does not re-read after this write to
			// validate what was persisted (failTaskBeforeDispatch returns
			// its input `cause` unconditionally), so this substitution is
			// invisible to the executor and to the returned dispatch error —
			// only the durable Result differs.
			mutated := attempted
			mutated.Result = f.unrelatedCause
			mutatedData, encErr := json.Marshal(mutated)
			if encErr != nil {
				return fmt.Errorf("F2 fault fixture: encode mutated task: %w", encErr)
			}
			if err := original(path, mutatedData, perm); err != nil {
				return err
			}
			f.persistedFails = append(f.persistedFails, mutated)
			return nil
		}
		if attempted.SessionID != "" {
			priorData, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("F2 fault fixture: read durable task: %w", err)
			}
			var prior task.Task
			if err := json.Unmarshal(priorData, &prior); err != nil {
				return fmt.Errorf("F2 fault fixture: decode durable task: %w", err)
			}
			if attempted.SessionID != prior.SessionID {
				f.bindings = append(f.bindings, attempted.SessionID)
				return f.bindingCause
			}
		}
		return original(path, data, perm)
	}
	// Install BEFORE constructing the loop: LIFO cleanup closes/drains it
	// before restoring this package-global writer, just like the existing
	// binding fixture.
	t.Cleanup(func() { gatewayTaskAtomicWriter = original })
	return f
}

func (f *restUnrelatedFailedCauseFault) target(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.path = path
}

func (f *restUnrelatedFailedCauseFault) snapshot() []task.Task {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]task.Task(nil), f.persistedFails...)
}

// TestHandleTaskPatch_DispatchFailure_UnrelatedStoredFailure_StillReverts
// proves launchIfStarted's Failed-disposition preservation guard
// (rest_tasks.go:1575, "A successful executor failure write stores the same
// cause it returns. Preserve only that disposition, not an unrelated or
// stale failure.") is scoped to THIS dispatch attempt's own cause, through
// the REAL PATCH handler and the REAL executor. When the store's Failed
// status was persisted for a DIFFERENT, unrelated reason than the dispatch
// error this handler call actually receives, the handler must still perform
// its ordinary rollback: restore the prior status, clear StartedAt, and
// return the visible dispatch error it actually got — not the unrelated
// stored cause.
//
// Oracle: #1026 CHECK round 5 F2 (independent audit,
// coordination/logs/fix890-opus/1026-check5/REPORT.md). The mutant that
// replaces the cause-equality comparison at rest_tasks.go:1575
// (`fresh.Result == startErr.Error()`) with `true` survives
// TestHandleTaskPatch_SessionBindingFailureRemainsFailed (that fixture's
// store only ever reaches Failed with the SAME cause the executor returns —
// the real executor's failTaskBeforeDispatch always persists exactly the
// error it also returns) and TestHandleTaskPatch_RunTask_DispatchCapReturns409
// (its fresh status never reaches Failed at all — the dispatch-cap guard
// trips before any store write). Only a differing stored cause exercises the
// equality itself. This fixture reuses the SAME gatewayTaskAtomicWriter seam
// installRESTBindingWriteFault already established (no new mock shape),
// substituting only the Failed write's committed bytes.
func TestHandleTaskPatch_DispatchFailure_UnrelatedStoredFailure_StillReverts(t *testing.T) {
	fault := installRESTUnrelatedFailedCauseFault(t)
	api := newTestRestAPIAlignedStores(t)
	wsID := ensureTestWorkspace(t, api)
	setWorkspaceCoreTeam(t, api, wsID, []string{"mia"})
	tsk := createTaskViaAPI(t, api, "Unrelated stored Failed cause", wsID)
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
		t.Fatal("BLOCKED: real task executor unavailable — required by #1026 CHECK round 5 F2 REST caller regression")
	}
	fault.target(filepath.Join(api.taskStore.Dir(), tsk.Id+".json"))

	response := patchTask(t, api, tsk.Id, `{"status":"in_progress"}`)
	deadline, ok := t.Deadline()
	if !ok {
		t.Fatal("REST caller fixture needs a test deadline to drain all executor work")
	}
	api.taskExecutor.Drain(time.Until(deadline) + time.Minute)

	if response.Code != http.StatusInternalServerError {
		t.Errorf("differing-cause PATCH: HTTP %d, want 500; body=%s", response.Code, response.Body.String())
	}
	var rejected gen.ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode real PATCH error response: %v; body=%s", err, response.Body.String())
	}
	// The visible error must be THIS dispatch attempt's own cause — the
	// binding-write failure the handler actually observed — not the
	// unrelated cause substituted on disk.
	if !strings.Contains(rejected.Error, fault.bindingCause.Error()) {
		t.Errorf("PATCH error = %q, want this dispatch attempt's own binding-write cause %q", rejected.Error, fault.bindingCause.Error())
	}
	if strings.Contains(rejected.Error, fault.unrelatedCause) {
		t.Errorf("PATCH error = %q, must not surface the unrelated stored cause %q", rejected.Error, fault.unrelatedCause)
	}

	persisted := fault.snapshot()
	if len(persisted) == 0 {
		t.Fatal("fixture did not reach the executor's Failed disposition write; cannot test the caller's differing-cause comparison")
	}

	// Both independent disk and public GET observations happen AFTER the
	// handler, including launchIfStarted's error/rollback branch, returned.
	stored, err := task.New(api.taskStore.Dir()).Get(tsk.Id)
	if err != nil {
		t.Fatalf("reopen task after real PATCH caller returned: %v", err)
	}
	if stored.Status != task.StatusNext {
		t.Errorf("after real PATCH caller returned: durable status = %q, want reverted to %q (must not preserve the unrelated stale Failed disposition); result=%q",
			stored.Status, task.StatusNext, stored.Result)
	}
	if stored.StartedAt != "" {
		t.Errorf("durable started_at = %q after rollback, want cleared (not a phantom timestamp from the reverted in_progress transition)", stored.StartedAt)
	}
	if stored.Result != fault.unrelatedCause {
		t.Errorf("durable result = %q after rollback, want the unrelated cause left untouched (%q) — the revert patch must not overwrite Result", stored.Result, fault.unrelatedCause)
	}
	after := getTaskFull(t, api, tsk.Id)
	if after.Status != gen.TaskStatusNext {
		t.Errorf("GET after real PATCH caller returned: status = %q, want reverted to %q (not the stale unrelated %q)", after.Status, gen.TaskStatusNext, gen.TaskStatusFailed)
	}
}

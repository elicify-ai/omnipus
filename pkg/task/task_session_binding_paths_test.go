// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task_test

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// #1026's retry-once ruling applies to EVERY launcher entry that binds a task's
// session: the main-run path and the dispatch-time binding, not only
// StartTaskNow's isolated path. Same fault seam and assertions as the
// StartTaskNow pack.

type fixedMainResolver struct{ id string }

func (r fixedMainResolver) EligibleMain(_, _ string) (string, bool, error) { return r.id, true, nil }

func installBindingMain(t *testing.T, f *bindingRunFixture) {
	t.Helper()
	meta, err := f.loop.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", bindingWorkerID)
	if err != nil {
		t.Fatalf("binding fixture: mint the assignee's main stand-in: %v", err)
	}
	f.executor.SetMainSessionResolver(fixedMainResolver{id: meta.ID})
}

func TestStartTaskNow_MainRun_SessionBindingRetriesOnceThenFails(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, false)
	installBindingMain(t, f)
	sessionID, stored, err := startBindingTaskAndDrain(t, f)
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if sessionID != "" {
		t.Errorf("returned session = %q, want empty after binding failure", sessionID)
	}
	if stored.Status != task.StatusFailed {
		t.Errorf("final status = %q, want %q; result=%q", stored.Status, task.StatusFailed, stored.Result)
	}
	assertBindingFailureReason(t, stored.Result, f.fault.bindingCause)
	assertBindingErrorCause(t, err, f.fault.bindingCause)
}

func TestStartTaskNow_MainRun_SessionBindingRecoversOnTheRetry(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailFirst, false)
	installBindingMain(t, f)
	sessionID, _, err := startBindingTaskAndDrain(t, f)
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, nil})
	if err != nil {
		t.Errorf("a recoverable binding failure returned %v, want nil", err)
	}
	if sessionID == "" {
		t.Error("returned session is empty, want the durable binding after the retry")
	}
}

func launchTaskOriginSession(t *testing.T, f *bindingRunFixture) (steer.LaunchResult, *agent.SteerLauncher) {
	t.Helper()
	l := agent.NewSteerLauncher(f.loop)
	res, err := l.Launch(context.Background(), steer.LaunchRequest{
		TargetAgentID: bindingWorkerID,
		Label:         "delegated report",
		Task:          "Write report.md.",
		Origin:        steer.Origin{Kind: steer.OriginKindTask, TaskID: f.taskID},
		WorkspaceID:   "default",
	})
	if err != nil {
		t.Fatalf("binding fixture: launch the task-origin session: %v", err)
	}
	return res, l
}

func TestDispatchLaunchedTask_SessionBindingRetriesOnceThenFails(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailAlways, false)
	res, l := launchTaskOriginSession(t, f)
	_, err := l.Dispatch(context.Background(), res.SessionID, res.Generation)
	f.executor.Drain(time.Minute)
	stored, readErr := f.store.Get(f.taskID)
	if readErr != nil {
		t.Fatalf("read task: %v", readErr)
	}
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, f.fault.bindingCause})
	assertNoBindingWorkerExecution(t, f)
	if stored.Status != task.StatusFailed {
		t.Errorf("final status = %q, want %q; result=%q", stored.Status, task.StatusFailed, stored.Result)
	}
	assertBindingFailureReason(t, stored.Result, f.fault.bindingCause)
	assertBindingErrorCause(t, err, f.fault.bindingCause)
}

func TestDispatchLaunchedTask_SessionBindingRecoversOnTheRetry(t *testing.T) {
	f := newBindingRunFixture(t, bindingFailFirst, false)
	res, l := launchTaskOriginSession(t, f)
	_, err := l.Dispatch(context.Background(), res.SessionID, res.Generation)
	f.executor.Drain(time.Minute)
	assertBindingWriteAttempts(t, f.fault.snapshot(), []error{f.fault.bindingCause, nil})
	if err != nil {
		t.Errorf("a recoverable binding failure returned %v, want nil", err)
	}
}

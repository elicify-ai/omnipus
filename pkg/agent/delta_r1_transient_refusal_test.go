// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// BDD (delta review R1 #2; architect ruling): Given a start whose launcher
// refuses, BEFORE the task is moved to in-progress, for a reason that says
// nothing about the task (the caller gave up, the executor is draining), When
// StartTaskNow returns that error, Then the task is NOT Failed: it stays
// startable and a later start can run it. The dispatch-cap case is NOT here: on
// the launcher path the cap never reaches failTaskBeforeDispatch (the launcher's
// own admission replaces the dispatch semaphore), so it is covered at the
// predicate level only (TestIsTransientLaunchRefusal).
func TestStartTaskNow_TransientRefusalLeavesTheTaskStartable(t *testing.T) {
	for name, cause := range map[string]error{
		"caller cancelled":  fmt.Errorf("launch: %w", context.Canceled),
		"caller timed out":  fmt.Errorf("launch: %w", context.DeadlineExceeded),
		"executor draining": fmt.Errorf("launch: %w", ErrExecutorDraining),
	} {
		t.Run(name, func(t *testing.T) {
			for stage, set := range map[string]func(*m9cLauncher){
				"launch":   func(l *m9cLauncher) { l.launchErr = cause },
				"dispatch": func(l *m9cLauncher) { l.dispatchErr = cause },
			} {
				t.Run(stage, func(t *testing.T) {
					al, _ := newRunTaskPolicyLoop(t, nil)
					tk := createRunnableTask(t, al, "t-r1-transient", rtAssignee)
					wl := &m9cLauncher{inner: NewSteerLauncher(al)}
					set(wl)
					al.taskExecutor.SetSessionLauncher(wl)

					_, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
					require.Error(t, err, "the refusal is still reported to the caller")
					require.ErrorIs(t, err, errCause(cause), "the cause stays identifiable")
					got, gerr := GetTaskStore(al).Get(tk.ID)
					require.NoError(t, gerr)
					require.NotEqual(t, task.StatusFailed, got.Status,
						"a transient refusal must not kill the task (status %q, result %q)", got.Status, got.Result)

					// And it is startable: once the launcher recovers the same task runs.
					wl.launchErr, wl.dispatchErr = nil, nil
					id, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
					require.NoError(t, err, "a later start of the same task must succeed")
					require.NotEmpty(t, id)
					joinGoalFixtureRuns(t, al)
				})
			}
		})
	}
}

// errCause unwraps the sentinel the table wrapped, for errors.Is.
func errCause(wrapped error) error {
	for {
		next := unwrapOnce(wrapped)
		if next == nil {
			return wrapped
		}
		wrapped = next
	}
}

func unwrapOnce(err error) error {
	u, ok := err.(interface{ Unwrap() error })
	if !ok {
		return nil
	}
	return u.Unwrap()
}

// Permanent control: a refusal that is about the session, not the caller, is
// permanent — a retry would hit the same refusal — so it must still fail the
// task.
//
// SPEC CHANGE — ADR-093 amendment 2026-10-23, Decision 2: the launch-time
// lifecycle sentinels (steer.ErrSteeringStopped / ErrSteeringRevivalFailed)
// and the D5 sentence wrapper (newSteeringRefusalError) are DELETED. OLD:
// the launch stage injected ErrSteeringStopped and the task's stored failure
// reason was the D5 sentence. NEW: the launch stage uses a keep-list
// permanent refusal (ErrDepthExceeded — ADR-093 amendment refusal keep-list:
// "depth exceeded ... KEEP — returns its short reason"), and a refused
// bound-session dispatch keeps failing the task with the RAW SENTINEL TEXT
// as its stored reason (Decision 2: "now with the sentinel text as its
// stored failure reason"), never the deleted sentence.
func TestStartTaskNow_LifecycleRefusalStillFailsTheTask(t *testing.T) {
	t.Run("launch", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, nil)
		tk := createRunnableTask(t, al, "t-r1-permanent", rtAssignee)
		wl := &m9cLauncher{inner: NewSteerLauncher(al)}
		wl.launchErr = steer.ErrDepthExceeded
		al.taskExecutor.SetSessionLauncher(wl)

		_, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
		require.Error(t, err)
		got, gerr := GetTaskStore(al).Get(tk.ID)
		require.NoError(t, gerr)
		require.Equal(t, task.StatusFailed, got.Status, "a permanent launch refusal is about the task's admission: the task fails")
	})
	t.Run("dispatch", func(t *testing.T) {
		al, _ := newRunTaskPolicyLoop(t, nil)
		tk := createRunnableTask(t, al, "t-r1-permanent-dispatch", rtAssignee)
		wl := &m9cLauncher{inner: NewSteerLauncher(al)}
		wl.dispatchErr = steer.ErrDispatchCancelled
		al.taskExecutor.SetSessionLauncher(wl)

		_, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
		require.Error(t, err)
		got, gerr := GetTaskStore(al).Get(tk.ID)
		require.NoError(t, gerr)
		require.Equal(t, task.StatusFailed, got.Status, "a dispatch refusal is permanent: the task fails")
		// Decision 2: the stored failure reason is the typed sentinel text —
		// short, machinery-honest — never the deleted D5 sentence.
		require.Contains(t, got.Result, steer.ErrDispatchCancelled.Error(),
			"the stored failure reason must surface steer.ErrDispatchCancelled's own text (amendment 2026-10-23 Decision 2), got %q", got.Result)
		require.NotContains(t, got.Result, "Delegation is unavailable",
			"the stored failure reason must not carry the deleted D5 sentence (amendment 2026-10-23 Decision 2), got %q", got.Result)
	})
}

// The predicate itself, including the cap, which no launcher-path test reaches.
//
// SPEC CHANGE — ADR-093 amendment 2026-10-23: the permanent list is re-pinned
// to the three DISPATCH sentinels only — the launch-time lifecycle sentinels
// (steer.ErrSteeringStopped / ErrSteeringRevivalFailed) and the D5 sentence
// wrapper (newSteeringRefusalError) are deleted with the backstop (Decision 2
// lists this file for exactly this re-pin).
func TestIsTransientLaunchRefusal(t *testing.T) {
	transient := []error{
		context.Canceled, context.DeadlineExceeded, ErrExecutorDraining, ErrDispatchCapReached,
		fmt.Errorf("task_executor: main run launch: %w", context.Canceled),
	}
	for _, err := range transient {
		require.Truef(t, isTransientLaunchRefusal(err), "%v must be transient", err)
	}
	permanent := []error{
		errors.New("session binding persistence failed"),
		steer.ErrDispatchCancelled,
		steer.ErrTerminal, steer.ErrStaleGeneration,
	}
	for _, err := range permanent {
		require.Falsef(t, isTransientLaunchRefusal(err), "%v must be permanent", err)
	}
}

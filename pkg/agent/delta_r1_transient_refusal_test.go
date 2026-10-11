// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/task"
)

// BDD (delta review R1 #2): Given a start whose launcher refuses for a reason
// that says nothing about the task (the caller gave up, the executor is
// draining, the dispatch cap is full), When StartTaskNow returns that error,
// Then the task is NOT Failed: it stays startable and a later start can run it.
// A permanent refusal still fails the task (TestStartTaskNow_RefusedLaunchFailsTheTask_RealLauncher).
func TestStartTaskNow_TransientRefusalLeavesTheTaskStartable(t *testing.T) {
	for name, cause := range map[string]error{
		"caller cancelled":  fmt.Errorf("launch: %w", context.Canceled),
		"caller timed out":  fmt.Errorf("launch: %w", context.DeadlineExceeded),
		"executor draining": fmt.Errorf("launch: %w", ErrExecutorDraining),
		"dispatch cap full": fmt.Errorf("launch: %w", ErrDispatchCapReached),
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

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// session-core U6 — FR-020: the terminal notice to each captured recipient.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-020 + BDD-06.3/06.4 and the architect's U6 seam decision D-U6.6
// (ARCHITECT-ANSWER-U6-U7.md), read from the spec/decision text, never from the
// implementation.
//
// FR-020: "Actual done/failed/stopped runs MUST notify authoritative
// reason/full stored result plus brief engine header ... Eligible idle wake
// once/batch, live safe consumption, stopped report retention."
//
// D-U6.6:
//
//	func (te *TaskExecutor) deliverTaskCompletionUpward(ctx, t *task.Task, run *activeRun)
//	steer.UpwardEvent.RecipientSessionID string  // one new field
//	For each captured recipient: re-check visibility (SplitMainSessionID +
//	EligibleMain); if hidden or removed: skip, log, no wake, no substitute.
//	Text starts with the engine header `Task run <status>: "<title>" (run <run_id>)`
//	where <status> is done, failed or stopped (stopped = CancelReason == stopped_by_user).
//
// THIS FILE COMPILES ONLY AFTER THE pkg/agent IMPLEMENTATION (D-U6.7 step 6):
// deliverTaskCompletionUpward gains a *activeRun argument, activeRun gains
// `recipients`, and steer.UpwardEvent gains RecipientSessionID — none exist yet.
// It is expected not to compile on the pre-change tree — that compile error IS
// its RED, naming exactly the missing seams.
//
// NOT covered here (needs the completeTaskWithResult / task_run_loop harness):
// the "Stop wins over a late success closes the run as stopped, one stopped
// notice" race, and the "skipped fire / goal retry delivers none" cases. Those
// call sites are named in D-U6.6's call-site table; see the RED report.

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// u6SeedChildAndRecipients seeds a steered child lifecycle record plus the two
// captured recipient mains, and returns (childID, []recipientMains).
func u6SeedChildAndRecipients(t *testing.T, al *AgentLoop, lifecycle *session.LifecycleStore, ws, parentMain string) (string, []string) {
	t.Helper()
	childID := "u6-notice-child"
	must := func(err error) {
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	// The child is steered under the real parent main, so deliverOwnerKey works
	// even though the recipients are addressed explicitly.
	must(lifecycle.Persist(&session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    ws,
		AgentID:        testDefaultAgentID,
		ParentAgentID:  testDefaultAgentID,
		Origin:         &session.Origin{Kind: steer.OriginKindTask, TaskID: "u6-notice-task"},
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parentMain, RootSessionID: parentMain},
	}))
	r1, err := session.MainSessionID(ws, testDefaultAgentID)
	require.NoError(t, err)
	return childID, []string{r1}
}

// TestU6_FR020_DeliversHeaderAndResultToEachRecipient asserts an actual
// terminal run notifies each captured recipient once, with the engine header
// and the full stored result.
func TestU6_FR020_DeliversHeaderAndResultToEachRecipient(t *testing.T) {
	al, lifecycle, inbox, _ := newDeliverTestLoop(t)
	te := al.taskExecutor
	const ws = "ws-1"

	parentMain, err := session.MainSessionID(ws, testDefaultAgentID)
	require.NoError(t, err)
	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{ws, testDefaultAgentID}: parentMain,
	}})

	childID, recipients := u6SeedChildAndRecipients(t, al, lifecycle, ws, parentMain)
	require.Len(t, recipients, 1)

	tk := &task.Task{
		ID: "u6-notice-task", AgentID: testDefaultAgentID, WorkspaceID: ws,
		Title: "u6 noticed task", Status: task.StatusDone, SessionID: childID,
		Result: "the full stored result",
	}
	run := &activeRun{runID: "run-u6-1", recipients: recipients}

	te.deliverTaskCompletionUpward(context.Background(), tk, run)

	for _, r := range recipients {
		msgs, _, _, derr := inbox.Drain(r, "", "", 10)
		require.NoError(t, derr)
		require.Len(t, msgs, 1, "FR-020: each captured recipient gets exactly one terminal notice")
		raw, _ := json.Marshal(msgs[0])
		body := string(raw)
		assert.Contains(t, body, `Task run done: "u6 noticed task" (run run-u6-1)`,
			"FR-020: the notice carries the engine header naming status, title and run id")
		assert.Contains(t, body, "the full stored result",
			"FR-020: the notice carries the full stored result")
	}
}

// TestU6_FR020_HiddenRecipientSkipped asserts a recipient that is no longer
// visible is skipped with no wake and no substitute (BDD-01.3 / 06.4).
func TestU6_FR020_HiddenRecipientSkipped(t *testing.T) {
	al, lifecycle, inbox, _ := newDeliverTestLoop(t)
	te := al.taskExecutor
	const ws = "ws-1"

	parentMain, err := session.MainSessionID(ws, testDefaultAgentID)
	require.NoError(t, err)
	// Empty resolver: no address is eligible → the recipient is hidden.
	te.SetMainSessionResolver(&u6FakeMainResolver{})

	childID, recipients := u6SeedChildAndRecipients(t, al, lifecycle, ws, parentMain)

	tk := &task.Task{
		ID: "u6-notice-task", AgentID: testDefaultAgentID, WorkspaceID: ws,
		Title: "u6 noticed task", Status: task.StatusDone, SessionID: childID,
		Result: "the full stored result",
	}
	run := &activeRun{runID: "run-u6-2", recipients: recipients}

	te.deliverTaskCompletionUpward(context.Background(), tk, run)

	for _, r := range recipients {
		msgs, _, _, derr := inbox.Drain(r, "", "", 10)
		require.NoError(t, derr)
		assert.Empty(t, msgs,
			"FR-020/BDD-06.4: a hidden recipient gets no notice and no substitute")
	}
}

// TestU6_FR020_StoppedRunHeaderSaysStopped asserts the status word in the
// notice header is "stopped" for a stopped run (failed + CancelReason
// stopped_by_user), not "failed".
func TestU6_FR020_StoppedRunHeaderSaysStopped(t *testing.T) {
	al, lifecycle, inbox, _ := newDeliverTestLoop(t)
	te := al.taskExecutor
	const ws = "ws-1"

	parentMain, err := session.MainSessionID(ws, testDefaultAgentID)
	require.NoError(t, err)
	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{ws, testDefaultAgentID}: parentMain,
	}})

	childID, recipients := u6SeedChildAndRecipients(t, al, lifecycle, ws, parentMain)

	tk := &task.Task{
		ID: "u6-notice-task", AgentID: testDefaultAgentID, WorkspaceID: ws,
		Title: "u6 stopped task", Status: task.StatusFailed, SessionID: childID,
		CancelReason: task.CancelReasonStoppedByUser, Result: "stopped mid-run",
	}
	run := &activeRun{runID: "run-u6-3", recipients: recipients}

	te.deliverTaskCompletionUpward(context.Background(), tk, run)

	for _, r := range recipients {
		msgs, _, _, derr := inbox.Drain(r, "", "", 10)
		require.NoError(t, derr)
		require.Len(t, msgs, 1)
		raw, _ := json.Marshal(msgs[0])
		assert.True(t, strings.Contains(string(raw), `Task run stopped:`),
			"FR-020: a stopped run's notice header says 'stopped'; got %s", string(raw))
	}
}

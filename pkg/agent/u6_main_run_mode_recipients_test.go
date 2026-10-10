// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// session-core U6 — FR-017 (MAIN half), FR-019 and FR-034: the derived run
// mode, the run's captured recipients, the MAIN child launched under the
// assignee's main, and the resolver's read-error rule.
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-017/FR-019/FR-034 and the architect's U6 seam decision D-U6.1/D-U6.2/D-U6.3/
// D-U6.5 (ARCHITECT-ANSWER-U6-U7.md), read from the spec/decision text, never
// from the implementation.
//
//	D-U6.3: func deriveTaskRunMode(t *task.Task, assigneeMainOK bool) taskRunMode
//	        → taskRunMain | taskRunIsolated | taskRunContinue.
//	        func (te *TaskExecutor) launchMainTaskRun(ctx, t, occurrenceMs, kind) (string, error)
//	        the MAIN child is a real child of the ASSIGNEE's main (steering session).
//	D-U6.2: func (te *TaskExecutor) captureRunRecipients(ctx, t) []string
//	        starter main + assignee main, deduped, creator never used.
//	D-U6.1: type MainSessionResolver interface { EligibleMain(ws, agent) (id, ok, err) };
//	        func (te *TaskExecutor) SetMainSessionResolver(r MainSessionResolver)
//	        a read error is NEVER "not eligible": for the assignee the run is refused.
//	D-U6.5: grants come from the assignee's MAIN via inheritDelegatePermissions —
//	        the launch's parent IS the main.
//
// THIS FILE COMPILES ONLY AFTER THE pkg/agent IMPLEMENTATION (D-U6.7 steps 3–4):
// deriveTaskRunMode, taskRunMode, launchMainTaskRun, captureRunRecipients,
// MainSessionResolver and SetMainSessionResolver do not exist yet. It is
// expected not to compile on the pre-change tree — that compile error IS its
// RED, naming exactly the missing seams (D-U6.7 anchor rows).

package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// u6FakeMainResolver is a deterministic MainSessionResolver (D-U6.1).
type u6FakeMainResolver struct {
	elig map[[2]string]string
	errs map[[2]string]error
}

func (r *u6FakeMainResolver) EligibleMain(workspaceID, agentID string) (string, bool, error) {
	k := [2]string{workspaceID, agentID}
	if r.errs != nil {
		if e := r.errs[k]; e != nil {
			return "", false, e
		}
	}
	if r.elig != nil {
		if id, ok := r.elig[k]; ok {
			return id, true, nil
		}
	}
	return "", false, nil
}

// u6CapturingLauncher records every LaunchRequest so a test can assert the
// parent the MAIN child was steered under.
type u6CapturingLauncher struct {
	mu   sync.Mutex
	reqs []steer.LaunchRequest
}

func (l *u6CapturingLauncher) Launch(_ context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	l.mu.Lock()
	l.reqs = append(l.reqs, req)
	n := len(l.reqs)
	l.mu.Unlock()
	return steer.LaunchResult{SessionID: fmt.Sprintf("u6-child-%d", n), Generation: 1}, nil
}

func (l *u6CapturingLauncher) Dispatch(_ context.Context, _ string, gen int) (steer.DispatchResult, error) {
	return steer.DispatchResult{State: steer.DispatchRunning, Generation: gen}, nil
}

func (l *u6CapturingLauncher) last() (steer.LaunchRequest, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.reqs) == 0 {
		return steer.LaunchRequest{}, false
	}
	return l.reqs[len(l.reqs)-1], true
}

// TestU6_FR017_DeriveTaskRunMode pins the derivation table (D-U6.3).
func TestU6_FR017_DeriveTaskRunMode(t *testing.T) {
	mainEligible := true
	mainMissing := false

	t.Run("eligible_main_not_isolated_is_MAIN", func(t *testing.T) {
		tk := &task.Task{AgentID: "native-agent", Status: task.StatusNext}
		assert.Equal(t, taskRunMain, deriveTaskRunMode(tk, mainEligible),
			"FR-017: an eligible assignee main with run_isolated off runs as a MAIN child")
	})

	t.Run("run_isolated_forces_ISOLATED_even_with_eligible_main", func(t *testing.T) {
		tk := &task.Task{AgentID: "native-agent", Status: task.StatusNext, RunIsolated: true}
		assert.Equal(t, taskRunIsolated, deriveTaskRunMode(tk, mainEligible),
			"FR-017: the scheduled isolation checkbox forces a fresh independent chat for either role")
	})

	t.Run("no_eligible_main_is_ISOLATED", func(t *testing.T) {
		tk := &task.Task{AgentID: "native-agent", Status: task.StatusNext}
		assert.Equal(t, taskRunIsolated, deriveTaskRunMode(tk, mainMissing),
			"FR-017: without an eligible main the run is a fresh independent ISOLATED run")
	})
}

// TestU6_FR019_CaptureRunRecipients pins the role rule (D-U6.2): the starter
// agent's main plus the assignee's main, deduped, never the creator.
func TestU6_FR019_CaptureRunRecipients(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	te := al.taskExecutor

	const ws, starter, assignee, creator = "ws-1", "starter-agent", "native-agent", "creator-agent"
	starterMain, err := session.MainSessionID(ws, starter)
	require.NoError(t, err)
	assigneeMain, err := session.MainSessionID(ws, assignee)
	require.NoError(t, err)
	creatorMain, err := session.MainSessionID(ws, creator)
	require.NoError(t, err)

	res := &u6FakeMainResolver{elig: map[[2]string]string{
		{ws, starter}:  starterMain,
		{ws, assignee}: assigneeMain,
		{ws, creator}:  creatorMain, // present, to prove it is never consulted
	}}
	te.SetMainSessionResolver(res)

	tk := &task.Task{
		ID: "u6-capture-1", AgentID: assignee, WorkspaceID: ws,
		Status: task.StatusNext, CreatedByAgentID: creator,
	}
	// The starter source is the run_task caller's ctx (D-U6.2).
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), starter), ws)

	got := te.captureRunRecipients(ctx, tk)
	assert.Equal(t, []string{starterMain, assigneeMain}, got,
		"FR-019: recipients are [starter main, assignee main], deduped, and never the creator")

	// Equal starter and assignee collapse to one entry (dedupe).
	res.elig[[2]string{ws, starter}] = assigneeMain
	deduped := te.captureRunRecipients(ctx, tk)
	assert.Equal(t, []string{assigneeMain}, deduped,
		"FR-019: a starter and assignee that share a main must be deduplicated")

	// A person/scheduler-started run has no starter agent in ctx → only the
	// assignee's main.
	res.elig[[2]string{ws, starter}] = starterMain
	bare := te.captureRunRecipients(context.Background(), tk)
	assert.Equal(t, []string{assigneeMain}, bare,
		"FR-019: a run with no starter agent in ctx captures only the assignee's main")
}

// TestU6_FR017_LaunchMainTaskRunSteersUnderAssigneeMain pins D-U6.3's core:
// the MAIN child is a real child of the ASSIGNEE's main — the assignee main is
// the steering session, not the starter chat (which is what carries FR-034's
// grant inheritance, D-U6.5).
func TestU6_FR017_LaunchMainTaskRunSteersUnderAssigneeMain(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	te := al.taskExecutor

	const ws, starter, assignee = "ws-1", "starter-agent", "native-agent"
	assigneeMain, err := session.MainSessionID(ws, assignee)
	require.NoError(t, err)
	starterMain, err := session.MainSessionID(ws, starter)
	require.NoError(t, err)

	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{
		{ws, assignee}: assigneeMain,
		{ws, starter}:  starterMain,
	}})
	launcher := &u6CapturingLauncher{}
	te.SetSessionLauncher(launcher)

	tk := &task.Task{
		ID: "u6-main-run-1", AgentID: assignee, WorkspaceID: ws,
		Title: "run under main", Status: task.StatusNext,
	}
	require.NoError(t, GetTaskStore(al).Create(tk))

	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), starter), ws)
	_, err = te.launchMainTaskRun(ctx, tk, nil, task.RunKindManual)
	require.NoError(t, err)

	req, ok := launcher.last()
	require.True(t, ok, "launchMainTaskRun must launch a steered child")
	assert.Equal(t, assigneeMain, req.SteeringSessionID,
		"FR-017/FR-034: the MAIN child must be steered under the ASSIGNEE's main (its grants inherit "+
			"from the main, never from the starter's extra chat)")
	assert.NotEqual(t, starterMain, req.SteeringSessionID,
		"FR-017: the MAIN child is never parented on the starter's chat")
	assert.Equal(t, assignee, req.TargetAgentID)
	assert.Equal(t, steer.OriginKindTask, req.Origin.Kind,
		"FR-017: the MAIN child launch is task-origin")
}

// TestU6_FR019_MainResolverReadErrorRefusesRun pins D-U6.1's rule for the
// assignee: a read error from EligibleMain is NEVER "not eligible" — the run is
// refused (truthful pre-dispatch failure), not silently degraded to ISOLATED.
func TestU6_FR019_MainResolverReadErrorRefusesRun(t *testing.T) {
	al, _ := newGoalLoopTestLoop(t, &mockProvider{}, nil)
	te := al.taskExecutor

	const ws, starter, assignee = "ws-1", "starter-agent", "native-agent"
	te.SetMainSessionResolver(&u6FakeMainResolver{
		errs: map[[2]string]error{{ws, assignee}: errors.New("lifecycle store unreadable")},
	})
	te.SetSessionLauncher(&u6CapturingLauncher{})

	tk := &task.Task{
		ID: "u6-main-run-err", AgentID: assignee, WorkspaceID: ws,
		Title: "run with unreadable main", Status: task.StatusNext,
	}
	require.NoError(t, GetTaskStore(al).Create(tk))

	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), starter), ws)
	_, err := te.launchMainTaskRun(ctx, tk, nil, task.RunKindManual)
	assert.Error(t, err,
		"D-U6.1: a read error from EligibleMain for the ASSIGNEE must refuse the run, never be treated "+
			"as 'not eligible' (which would silently run ISOLATED)")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// N7 (security re-verification of the U5 stack): after a gateway restart a
// human message to a stopped or finished external helper's chat revived it with
// a fresh holder, so `started` was false and a NEW CLI conversation began inside
// the old chat without its CLI identity. The "this session already ran" fact is
// now durable (LifecycleRecord.ExternalRunStarted), so the restarted process
// resumes-or-refuses exactly like the in-process one.
package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func n7WireLifecycle(t *testing.T, al *AgentLoop) *session.LifecycleStore {
	t.Helper()
	lc := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lc)
	return lc
}

func n7SeedExternalChild(t *testing.T, lc *session.LifecycleStore, ts *turnState, origin *session.Origin) {
	t.Helper()
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID:      ts.transcriptSessionID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		Origin:         origin,
		WorkspaceID:    "ws-1",
		AgentID:        ts.agentID,
		Is3P:           true,
	}))
}

// Oracle: FR-043 / N7 — a later entry of a steered external session after a
// restart (fresh holder, durable record says the CLI conversation started) must
// refuse with errExternalResumeUnavailable and start NO fresh Run.
// Real: runExternalCLISubTurn -> beginExternalRun + markExternalRunStarted.
// Instrument: recording driver run counter; the "restart" is the holder being
// dropped (ForgetExternalRunSession), the only state a restart loses.
func TestN7_RestartedRevivalOfStartedExternalSessionRefusesInsteadOfFreshRun(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	lc := n7WireLifecycle(t, al)
	n7SeedExternalChild(t, lc, ts, nil)
	drv, restore := withRecordingDriver(t)
	defer restore()

	_, err := runExternalCLISubTurn(context.Background(), al, ts, "launch instruction", 30*time.Second)
	require.NoError(t, err, "the genuine first launch Runs fresh")
	runs, _ := drv.snapshot()
	require.Equal(t, 1, runs)
	rec, err := lc.Load(ts.transcriptSessionID)
	require.NoError(t, err)
	require.True(t, rec.ExternalRunStarted, "the first CLI run must be durably recorded before it starts")

	// Gateway restart: the in-memory holder (and its `started` flag) is gone.
	al.ForgetExternalRunSession(ts.transcriptSessionID)

	// The human message revives the helper: a plain entry, ExternalCLIResume false.
	ts.opts.ExternalCLIResume = false
	res, err := runExternalCLISubTurn(context.Background(), al, ts, "message after restart", 30*time.Second)
	require.ErrorIs(t, err, errExternalResumeUnavailable)
	require.Nil(t, res, "a refused entry starts no process")
	runs, resumes := drv.snapshot()
	require.Equal(t, 1, runs, "no fresh CLI Run may start in the old chat after a restart")
	require.Empty(t, resumes)
}

// Oracle: the N7 rule is scoped to steered delegated sessions — a task-origin
// session keeps its explicit fresh Run per turn and is never marked.
func TestN7_TaskOriginExternalSessionStaysFreshAndUnmarked(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	lc := n7WireLifecycle(t, al)
	n7SeedExternalChild(t, lc, ts, &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"})
	drv, restore := withRecordingDriver(t)
	defer restore()

	_, err := runExternalCLISubTurn(context.Background(), al, ts, "run one", 30*time.Second)
	require.NoError(t, err)
	al.ForgetExternalRunSession(ts.transcriptSessionID)
	_, err = runExternalCLISubTurn(context.Background(), al, ts, "run two", 30*time.Second)
	require.NoError(t, err, "a task-origin session starts a fresh Run per turn")
	runs, _ := drv.snapshot()
	require.Equal(t, 2, runs)
	rec, err := lc.Load(ts.transcriptSessionID)
	require.NoError(t, err)
	require.False(t, rec.ExternalRunStarted, "a task-origin session is exempt from the resume-only mark")
}

func n7SeedStoppedExternal(t *testing.T, lc *session.LifecycleStore, id string, started bool) {
	t.Helper()
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleStopped,
		OwnerScopeKind: session.OwnerScopeHuman,
		SteeredBy:      &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
		WorkspaceID:    "ws-1", AgentID: "claude-code", Is3P: true, ExternalRunStarted: started,
		StopNote: &session.StopNote{At: time.Now().UTC(), By: "human:dan", Seq: 1, Cause: session.StopCauseStop},
	}))
}

// Oracle: FR-043 — reviving a stopped external helper whose CLI conversation is
// gone refuses visibly BEFORE the generation moves or the instruction is stored.
// Real: AgentLoop.ReviveStoppedSession (the entry every revive path funnels
// through: human message, delegate resume, steer, redirect).
func TestN7_ReviveStoppedExternalWithoutConversationRefusesAndLeavesRecordUntouched(t *testing.T) {
	al := u5bLoop(t)
	lc := al.GetSessionLifecycleStore()
	n7SeedStoppedExternal(t, lc, "n7-stopped", true)
	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "dan"}

	revived, err := al.ReviveStoppedSession(context.Background(), "n7-stopped", by, "carry on")
	require.Error(t, err)
	require.False(t, revived)
	require.True(t, strings.Contains(err.Error(), errExternalResumeUnavailable.Error()),
		"the refusal must be the visible resume-unavailable one, got: %v", err)
	rec, lerr := lc.Load("n7-stopped")
	require.NoError(t, lerr)
	require.Equal(t, session.LifecycleStopped, rec.State, "a refused revive leaves the record stopped")
	require.Equal(t, 1, rec.Generation)
}

// Oracle: the refusal is exactly "the CLI conversation started AND no driver is
// retained". An unstarted external helper (stopped before its first run) and a
// helper whose driver is still retained are NOT refused by this gate.
func TestN7_ConversationAvailabilityGate(t *testing.T) {
	al := u5bLoop(t)
	lc := al.GetSessionLifecycleStore()
	n7SeedStoppedExternal(t, lc, "n7-never-ran", false)
	n7SeedStoppedExternal(t, lc, "n7-retained", true)
	n7SeedStoppedExternal(t, lc, "n7-gone", true)

	load := func(id string) *session.LifecycleRecord {
		rec, err := lc.Load(id)
		require.NoError(t, err)
		return rec
	}
	require.True(t, al.externalConversationAvailable("n7-never-ran", load("n7-never-ran")),
		"a helper that never ran has no conversation to lose; its first run may be fresh")

	sess := al.externalRunSession("n7-retained")
	sess.mu.Lock()
	sess.started = true
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()
	require.True(t, al.externalConversationAvailable("n7-retained", load("n7-retained")),
		"a retained driver means the conversation can be resumed")

	require.False(t, al.externalConversationAvailable("n7-gone", load("n7-gone")),
		"started with no driver and no holder is the post-restart state: refuse")
}

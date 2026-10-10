// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Re-verification round 2 of the U5 security batch: NEW-3 (a failed lifecycle
// read must refuse, never fall through to an unmarked fresh run), NEW-4 (a
// revival's availability decision holds the driver until the revived turn
// begins) and the N7 ordering the first round's test could not see (the start
// mark is durable BEFORE the CLI runs).
package agent

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// n3MakeUnreadable makes the session's lifecycle journal unreadable (open
// fails with a permission error — not "not found") and returns the restore.
func n3MakeUnreadable(t *testing.T, lc *session.LifecycleStore, sessionID string) func() {
	t.Helper()
	found := filepath.Join(lc.Dir(), sessionID+".jsonl")
	_, err := os.Stat(found)
	require.NoError(t, err, "instrument check: the seeded journal must exist before it is made unreadable")
	require.NoError(t, os.Chmod(found, 0o000))
	f, openErr := os.Open(found)
	require.Error(t, openErr, "instrument check: the journal must really be unreadable (tests must not run as root)")
	if f != nil {
		f.Close()
	}
	return func() { _ = os.Chmod(found, 0o600) }
}

// Oracle: NEW-3 — a lifecycle read failure for a real delegated session is not
// "no record". The dispatch refuses BEFORE any CLI process starts, and nothing
// is marked started.
// Real: runExternalCLISubTurn -> externalRunResumeOnly.
func TestNEW3_UnreadableLifecycleRecordRefusesBeforeAnyRun(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	lc := n7WireLifecycle(t, al)
	n7SeedExternalChild(t, lc, ts, nil)
	drv, restore := withRecordingDriver(t)
	defer restore()
	unreadable := n3MakeUnreadable(t, lc, ts.transcriptSessionID)

	res, err := runExternalCLISubTurn(context.Background(), al, ts, "launch instruction", 30*time.Second)
	require.Error(t, err, "an unreadable session record must refuse visibly")
	require.Nil(t, res)
	runs, resumes := drv.snapshot()
	require.Equal(t, 0, runs, "no CLI conversation may start when the record cannot be read")
	require.Empty(t, resumes)

	unreadable()
	rec, lerr := lc.Load(ts.transcriptSessionID)
	require.NoError(t, lerr)
	require.False(t, rec.ExternalRunStarted, "a refused entry must not mark the conversation started")
}

// Oracle: NEW-3 — the durable prior-start read is also fail-closed: when the
// mark cannot be read on re-entry, beginExternalRun refuses instead of treating
// the session as never started.
func TestNEW3_PriorStartReadFailureRefuses(t *testing.T) {
	al := u5bLoop(t)
	u5bSeedSteered(t, al, "n3-prior", "claude-code", true)
	lc := al.GetSessionLifecycleStore()
	unreadable := n3MakeUnreadable(t, lc, "n3-prior")
	defer unreadable()

	ts := &turnState{turnID: "n3-turn", transcriptSessionID: "n3-prior"}
	_, resume, err := al.beginExternalRun("n3-prior", ts, func() {}, true)
	require.Error(t, err, "an unreadable start mark must refuse, not read as 'never started'")
	require.False(t, resume)
}

// Oracle: a record that is genuinely absent keeps the record-less fixture
// behaviour (not a steered session, fresh Run, no error) — the fail-closed rule
// is for read FAILURES, not for "no such session".
func TestNEW3_AbsentRecordIsNotAnError(t *testing.T) {
	al := u5bLoop(t)
	resumeOnly, err := al.externalRunResumeOnly("n3-no-such-session")
	require.NoError(t, err)
	require.False(t, resumeOnly)
}

// markObservingDriver records what the durable lifecycle record said at the
// instant the CLI's Run began.
type markObservingDriver struct {
	recordingExternalDriver
	lc        *session.LifecycleStore
	sessionID string
	mu2       sync.Mutex
	markAtRun *bool
}

func (d *markObservingDriver) Run(ctx context.Context, o runner.RunOptions) (<-chan runner.RunEvent, error) {
	rec, err := d.lc.Load(d.sessionID)
	d.mu2.Lock()
	v := err == nil && rec != nil && rec.ExternalRunStarted
	d.markAtRun = &v
	d.mu2.Unlock()
	return d.recordingExternalDriver.Run(ctx, o)
}

// Oracle: N7 — the start mark is durable BEFORE the CLI runs (the first-round
// test read it only afterwards, so moving the write after Run went unnoticed).
func TestN7_StartMarkIsDurableBeforeTheCLIRuns(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	al, ts := newExternalTestLoop(t, "claude-code", "")
	ts.agent.MaxIterations = extTestEffectiveLimit
	lc := n7WireLifecycle(t, al)
	n7SeedExternalChild(t, lc, ts, nil)
	drv := &markObservingDriver{lc: lc, sessionID: ts.transcriptSessionID}
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return drv, nil }
	defer func() { newExternalDriver = prev }()

	_, err := runExternalCLISubTurn(context.Background(), al, ts, "launch instruction", 30*time.Second)
	require.NoError(t, err)
	drv.mu2.Lock()
	defer drv.mu2.Unlock()
	require.NotNil(t, drv.markAtRun, "instrument check: the driver's Run must have been observed")
	require.True(t, *drv.markAtRun, "ExternalRunStarted must already be durable when the CLI starts")
}

// Oracle: NEW-4 — a completion releasing the driver in the window between a
// revival's availability decision and its dispatch must not strand the revival:
// when the revival goes ahead, the retained driver it relied on is still there.
// Real: AgentLoop.ReviveStoppedSession on a stopped external helper with a real
// session store; the seam fires exactly in the window.
func TestNEW4_ReleaseBetweenAvailabilityCheckAndRevivalKeepsTheDriver(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	steererID := newTestSteeringSession(t, al, adr093Workspace)
	adr093Persist(t, al, adr093Record(steererID, 1, session.LifecycleRunning))
	childID := newTestSteeringSession(t, al, adr093Workspace)
	parentID := steererID
	require.NoError(t, al.GetSessionStore().SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}))
	adr093Persist(t, al, &session.LifecycleRecord{
		SessionID:          childID,
		Generation:         1,
		State:              session.LifecycleStopped,
		OwnerScopeKind:     session.OwnerScopeHuman,
		SteeredBy:          &session.SteeredBy{SteeringSessionID: steererID, RootSessionID: steererID},
		WorkspaceID:        adr093Workspace,
		AgentID:            testDefaultAgentID,
		Origin:             &session.Origin{Kind: session.OriginKindDelegate, CallID: "call-n4"},
		Is3P:               true,
		ExternalRunStarted: true,
		StopNote:           &session.StopNote{At: time.Now(), By: "human:tester", Seq: 1, Cause: session.StopCauseStop},
	})
	installParkedProvider(t, al)
	sess := al.externalRunSession(childID)
	sess.mu.Lock()
	sess.started = true
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()

	fired := false
	reviveAfterAvailabilityTestHook = func(id string) {
		fired = true
		// The old episode's deferred release, landing in the window.
		al.releaseExternalRunIfIdle(id)
	}
	defer func() { reviveAfterAvailabilityTestHook = nil }()

	by := steer.Principal{Kind: steer.PrincipalKindHuman, ID: "tester"}
	revived, reviveErr := al.ReviveStoppedSession(context.Background(), childID, by, "carry on")
	t.Logf("revived=%v err=%v", revived, reviveErr)
	require.True(t, fired, "instrument check: the availability decision must have passed and reached the window")

	require.NoError(t, reviveErr)
	require.True(t, revived, "instrument check: the revival must have gone ahead, or this test could not see the stranding")
	sess.mu.Lock()
	retained := sess.driver != nil
	sess.mu.Unlock()
	require.True(t, retained,
		"the revival went ahead but the driver it relied on was released underneath it")
}

// Oracle: the reservation is a hold, not a leak — a revival that fails before
// dispatch (here: refused) leaves the holder releasable, and Stop clears a
// reservation whose revived turn never began.
func TestNEW4_ReservationEndsWithTheRevivalOrStop(t *testing.T) {
	al := u5bLoop(t)
	lc := al.GetSessionLifecycleStore()
	n7SeedStoppedExternal(t, lc, "n4-hold", true)
	sess := al.externalRunSession("n4-hold")
	sess.mu.Lock()
	sess.started = true
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()
	rec, err := lc.Load("n4-hold")
	require.NoError(t, err)

	hold, err := al.reserveExternalConversation("n4-hold", rec)
	require.NoError(t, err)
	require.NotNil(t, hold)
	al.releaseExternalRunIfIdle("n4-hold")
	sess.mu.Lock()
	require.NotNil(t, sess.driver, "a reserved driver survives an idle release")
	sess.mu.Unlock()

	hold.cancel()
	al.releaseExternalRunIfIdle("n4-hold")
	sess.mu.Lock()
	require.Nil(t, sess.driver, "once the hold ends the idle release drops the driver again (N6)")
	sess.driver = runner.NewFakeRunner()
	sess.mu.Unlock()

	// Round 5 (NEW-7): Stop no longer clears every hold of the session. The hold of
	// an execution whose admission a Stop removed ends with that execution, and
	// only that one's.
	stopped, err := al.reserveExternalConversation("n4-hold", rec)
	require.NoError(t, err)
	stopped.bind(2, "run-stopped")
	al.retireExternalReservations("n4-hold", executionClaim{SessionID: "n4-hold", Generation: 2, RunID: "run-other"})
	sess.mu.Lock()
	require.NotNil(t, sess.driver, "a Stop selecting another execution must not drop this hold")
	sess.mu.Unlock()
	al.retireExternalReservations("n4-hold", executionClaim{SessionID: "n4-hold", Generation: 2, RunID: "run-stopped"})
	sess.mu.Lock()
	require.Nil(t, sess.driver, "the selected execution ended: its hold is dropped with the idle driver")
	sess.mu.Unlock()
}

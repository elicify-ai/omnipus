// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// steer_autodeny_inheritance_891_test.go: issue #891 — a delegated child of an
// unattended (headless/task/trigger) parent must inherit the parent's
// AutoDenyAsk posture. The child's turn is reconstructed from its lifecycle
// record on a detached context, so before this fix the stamp on the parent's
// tool ctx never reached the child: its ask-policy call raised an approval
// request nobody could answer and pended for the full approval timeout.
//
// Every test drives a REAL delegated child through SteerLauncher.Launch +
// Dispatch, so a regression in the wiring (not only in the store) fails here.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// launchWorkerDelegateCtx launches and dispatches a worker delegate under
// launchCtx (the stand-in for the calling tool's ctx) and waits until the
// child's lifecycle record is terminal, i.e. the child's one scripted turn
// finished whether or not its tool call ran.
func launchWorkerDelegateCtx(
	t *testing.T, al *AgentLoop, launcher *SteerLauncher, launchCtx context.Context,
	parentSessionID, callID string,
) string {
	t.Helper()
	launch, err := launcher.Launch(launchCtx, steer.LaunchRequest{
		SteeringSessionID: parentSessionID,
		TargetAgentID:     "worker",
		Task:              "edit the knowledge base",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "Launch(worker delegate)")
	_, err = launcher.Dispatch(context.Background(), launch.SessionID, launch.Generation)
	require.NoError(t, err, "Dispatch(worker delegate)")
	lifecycle := al.GetSessionLifecycleStore()
	waitFor(t, 10*time.Second, func() bool {
		rec, loadErr := lifecycle.Load(launch.SessionID)
		return loadErr == nil && session.IsTerminalLifecycleState(rec.State)
	})
	return launch.SessionID
}

// TestSteerLauncher_AutoDenyAskInheritance_UnattendedParentDeniesWithoutPrompt
// is the #891 failure: the parent's tool ctx carries AutoDenyAsk, the child's
// Ask-policy call must be auto-denied — the approver is never asked and the
// tool never runs.
func TestSteerLauncher_AutoDenyAskInheritance_UnattendedParentDeniesWithoutPrompt(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, false)

	launchCtx := tools.WithAutoDenyAsk(context.Background(), true)
	launchWorkerDelegateCtx(t, al, launcher, launchCtx, parentSessionID, "call-891-unattended")

	require.Zero(t, approver.countFor("knowledge_edit"),
		"an unattended parent's child must auto-deny its ask-policy call, never raise an approval request nobody can answer")
	require.Zero(t, stub.calls.Load(), "the auto-denied tool must not run")
}

// TestSteerLauncher_AutoDenyAskInheritance_AttendedParentStillPrompts is the
// negative control: with no AutoDenyAsk on the launch ctx the child prompts
// exactly as before, so the inheritance never changes a watched session.
func TestSteerLauncher_AutoDenyAskInheritance_AttendedParentStillPrompts(t *testing.T) {
	al, launcher, parentSessionID, approver, stub := steerInheritanceFixture(t, false)

	launchWorkerDelegateCtx(t, al, launcher, context.Background(), parentSessionID, "call-891-attended")

	require.Equal(t, 1, approver.countFor("knowledge_edit"),
		"an attended parent's child must still raise its approval request")
	require.Zero(t, stub.calls.Load(), "the tool must not run before the (denied) prompt resolves")
}

// launchOnly launches (does not dispatch) a worker delegate under launchCtx and
// returns the child's session id.
func launchOnly(t *testing.T, launcher *SteerLauncher, launchCtx context.Context, parentSessionID, callID string) string {
	t.Helper()
	launch, err := launcher.Launch(launchCtx, steer.LaunchRequest{
		SteeringSessionID: parentSessionID,
		TargetAgentID:     "worker",
		Task:              "edit the knowledge base",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "Launch(worker delegate)")
	return launch.SessionID
}

// TestSteerLauncher_AutoDenyAskInheritance_SurvivesRestartAndSessionEnd pins
// that the unattended posture is DURABLE: it rides the child's lifecycle record,
// so dropping every in-memory per-session store (what a restart or an idle
// close does) must not lose it. A child rebuilt later for a wake would
// otherwise ask a question nobody can answer and pend for the full approval
// timeout.
func TestSteerLauncher_AutoDenyAskInheritance_SurvivesRestartAndSessionEnd(t *testing.T) {
	al, launcher, parentSessionID, _, _ := steerInheritanceFixture(t, false)
	childID := launchOnly(t, launcher, tools.WithAutoDenyAsk(context.Background(), true), parentSessionID, "call-891-restart")

	// Simulate the restart / session end: nothing in memory remembers the child.
	al.SessionModes().ClearSession(childID)
	al.SessionModes().ClearSession(parentSessionID)

	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	ts, err := al.reconstructSteeredTurn(rec, nil)
	require.NoError(t, err)
	require.True(t, ts.opts.AutoDenyAsk,
		"a rebuilt child of an unattended parent must still auto-deny ask-policy calls after in-memory state is gone")
}

// TestSteerLauncher_AutoDenyAskInheritance_AttendedChildIsNotUnattended is the
// negative control for the durable path: an attended launch must rebuild with
// AutoDenyAsk off.
func TestSteerLauncher_AutoDenyAskInheritance_AttendedChildIsNotUnattended(t *testing.T) {
	al, launcher, parentSessionID, _, _ := steerInheritanceFixture(t, false)
	childID := launchOnly(t, launcher, context.Background(), parentSessionID, "call-891-attended-rebuild")

	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	ts, err := al.reconstructSteeredTurn(rec, nil)
	require.NoError(t, err)
	require.False(t, ts.opts.AutoDenyAsk, "an attended child must not be marked unattended")
}

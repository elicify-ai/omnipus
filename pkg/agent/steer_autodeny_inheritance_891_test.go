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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// reviveFixture is steerInheritanceFixture with a provider scripted for TWO
// runs: tool call then answer, twice — the first run of the child, then the run
// after a revive. The tool is an Ask-policy stub, so a run that is NOT
// unattended raises exactly one approval request per run.
func reviveFixture(t *testing.T) (al *AgentLoop, launcher *SteerLauncher, parentSessionID string, approver *autoRecordingApprover) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	provider := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{autoToolCall("rev-1", "knowledge_edit", `{}`)}).WithText("first done").
		WithToolCalls([]providers.ToolCall{autoToolCall("rev-2", "knowledge_edit", `{}`)}).WithText("second done")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens: 4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: testDefaultAgentID, Home: home}, {ID: "worker", Home: home}},
		},
	}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	mintGenuineBootEpochForLoop(t, al)
	t.Cleanup(func() { al.Close() })
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	installAutoStubs(t, al, "worker", []string{"knowledge_edit"})
	approver = &autoRecordingApprover{approve: false}
	al.SetToolApprover(approver)
	parentSessionID = newTestSteeringSession(t, al, "")
	return al, NewSteerLauncher(al), parentSessionID, approver
}

// reviveAndRun revives childID under by, dispatches the new run and waits for
// the child to finish it, returning the generation that ran.
func reviveAndRun(t *testing.T, al *AgentLoop, launcher *SteerLauncher, childID string, by steer.Principal) int {
	t.Helper()
	gen, err := al.steerCanceller().Revive(context.Background(), childID, by)
	require.NoError(t, err, "Revive")
	_, err = launcher.Dispatch(context.Background(), childID, gen)
	require.NoError(t, err, "Dispatch after revive")
	lifecycle := al.GetSessionLifecycleStore()
	waitFor(t, 15*time.Second, func() bool {
		rec, loadErr := lifecycle.Load(childID)
		return loadErr == nil && rec.Generation == gen && session.IsTerminalLifecycleState(rec.State)
	})
	return gen
}

// TestSteerLauncher_AutoDenyAskInheritance_HumanReviveClearsUnattended: a person
// who revives an unattended child is the audience now, so the revived run must
// PROMPT for its ask-policy call (founder ruling R2). The principal rule is the
// gateway-identity one used by principalAuthorizedForTarget
// (session_messaging_wire.go): Kind == human with a non-empty ID.
func TestSteerLauncher_AutoDenyAskInheritance_HumanReviveClearsUnattended(t *testing.T) {
	al, launcher, parentSessionID, approver := reviveFixture(t)
	childID := launchOnly(t, launcher, tools.WithAutoDenyAsk(context.Background(), true), parentSessionID, "call-891-human-revive")
	launch, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), childID, launch.Generation)
	require.NoError(t, err)
	waitFor(t, 15*time.Second, func() bool {
		rec, loadErr := al.GetSessionLifecycleStore().Load(childID)
		return loadErr == nil && session.IsTerminalLifecycleState(rec.State)
	})
	require.Zero(t, approver.countFor("knowledge_edit"), "control: the unattended first run must auto-deny without a prompt")

	reviveAndRun(t, al, launcher, childID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "alice"})

	require.Equal(t, 1, approver.countFor("knowledge_edit"),
		"after a human revive the run must prompt normally, not auto-deny")
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.False(t, rec.Unattended, "the human revive must clear Unattended in the revive's own record write")
}

// TestSteerLauncher_AutoDenyAskInheritance_AgentReviveKeepsUnattended: an agent
// or system revive adds no audience, so the posture stays and the run still
// auto-denies.
func TestSteerLauncher_AutoDenyAskInheritance_AgentReviveKeepsUnattended(t *testing.T) {
	al, launcher, parentSessionID, approver := reviveFixture(t)
	childID := launchOnly(t, launcher, tools.WithAutoDenyAsk(context.Background(), true), parentSessionID, "call-891-agent-revive")
	launch, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), childID, launch.Generation)
	require.NoError(t, err)
	waitFor(t, 15*time.Second, func() bool {
		rec, loadErr := al.GetSessionLifecycleStore().Load(childID)
		return loadErr == nil && session.IsTerminalLifecycleState(rec.State)
	})

	reviveAndRun(t, al, launcher, childID, steer.Principal{Kind: steer.PrincipalKindAgent, ID: "scheduler"})

	require.Zero(t, approver.countFor("knowledge_edit"), "an agent/system revive must keep auto-denying")
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	require.True(t, rec.Unattended, "an agent revive must not clear Unattended")
}

// finishUnattendedChild launches a child under an unattended parent, runs it to
// its terminal state and returns its (terminal) record — the state in which a
// post-finish steer revives it.
func finishUnattendedChild(t *testing.T, al *AgentLoop, launcher *SteerLauncher, parentSessionID, callID string) *session.LifecycleRecord {
	t.Helper()
	childID := launchOnly(t, launcher, tools.WithAutoDenyAsk(context.Background(), true), parentSessionID, callID)
	rec, err := al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), childID, rec.Generation)
	require.NoError(t, err)
	waitFor(t, 15*time.Second, func() bool {
		cur, loadErr := al.GetSessionLifecycleStore().Load(childID)
		return loadErr == nil && session.IsTerminalLifecycleState(cur.State)
	})
	rec, err = al.GetSessionLifecycleStore().Load(childID)
	require.NoError(t, err)
	return rec
}

func waitForGeneration(t *testing.T, al *AgentLoop, childID string, gen int) *session.LifecycleRecord {
	t.Helper()
	var out *session.LifecycleRecord
	waitFor(t, 20*time.Second, func() bool {
		cur, err := al.GetSessionLifecycleStore().Load(childID)
		if err != nil || cur.Generation < gen || !session.IsTerminalLifecycleState(cur.State) {
			return false
		}
		out = cur
		return true
	})
	return out
}

// TestPostFinishSteer_AgentSteerKeepsUnattended: the parent AGENT steers a
// finished unattended child (a delegate steer carries a D4 control receipt).
// That adds no audience, so the revived run must still auto-deny.
func TestPostFinishSteer_AgentSteerKeepsUnattended(t *testing.T) {
	al, launcher, parentSessionID, approver := reviveFixture(t)
	rec := finishUnattendedChild(t, al, launcher, parentSessionID, "call-891-postfinish-agent")
	require.Zero(t, approver.countFor("knowledge_edit"), "control: the first run auto-denies")

	grant, err := al.GetSessionLifecycleStore().AcceptSteerControl(rec.SessionID, "do it again", session.StopActorAgent("parent-agent"), "corr-agent")
	require.NoError(t, err)
	items := []steeringQueueItem{{
		message: providers.Message{Role: "user", Content: "do it again"}, correlationID: "corr-agent", steerControlID: grant.ControlID,
	}}
	require.NoError(t, al.processFinishingItems(context.Background(), rec, nil, items, true))

	final := waitForGeneration(t, al, rec.SessionID, rec.Generation+1)
	require.Zero(t, approver.countFor("knowledge_edit"), "an agent's post-finish steer must keep the child auto-denying")
	require.True(t, final.Unattended, "an agent's post-finish steer must not clear Unattended")
}

// TestPostFinishSteer_PersonSteerClearsUnattended: a person steers the finished
// child (chat input carries no ledger receipt): they are the audience, so the
// revived run prompts.
func TestPostFinishSteer_PersonSteerClearsUnattended(t *testing.T) {
	al, launcher, parentSessionID, approver := reviveFixture(t)
	rec := finishUnattendedChild(t, al, launcher, parentSessionID, "call-891-postfinish-person")
	require.Zero(t, approver.countFor("knowledge_edit"), "control: the first run auto-denies")

	items := []steeringQueueItem{{message: providers.Message{Role: "user", Content: "please redo it"}, correlationID: "corr-person"}}
	require.NoError(t, al.processFinishingItems(context.Background(), rec, nil, items, true))

	final := waitForGeneration(t, al, rec.SessionID, rec.Generation+1)
	require.Equal(t, 1, approver.countFor("knowledge_edit"), "a person's post-finish steer must prompt normally")
	require.False(t, final.Unattended, "a person's post-finish steer must clear Unattended")
}

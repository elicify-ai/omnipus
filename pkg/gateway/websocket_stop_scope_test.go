// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// These real web-Stop tests use the frozen ADR-20260928 control-plane
// contract: D2/Vocabulary make stopped non-terminal and resumable; D4
// reports the transition through subagent_state; D6 persists the direct
// parent's nonfatal stop notice. A never-ran queued child must not publish
// the retired subagent_end(interrupted) or a terminal final. The live-turn
// positive control below still protects its unchanged active goal.
package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward pins
// D2 CRIT-001, D4 and D6, not the retired interrupted-final mechanism.
func TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, adr093IdleProvider{})
	lifecycle := session.NewLifecycleStore(t.TempDir())
	inbox := session.NewMessageInboxStore(t.TempDir())
	al.SetSessionMessagingStores(inbox, lifecycle)

	// The production canceller AND the production upward-delivery wiring
	// (gateway_boot.go::wireSteerDeps) — without the real deliverer, a
	// passing assertion on the parent's transcript would prove nothing.
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	const workspaceID = "ws-stop-scope-never-ran"
	require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: strPtr(workspaceID)}))
	parent := &session.LifecycleRecord{
		SessionID:      meta.ID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    workspaceID,
		AgentID:        "mia",
		Origin:         &session.Origin{Kind: session.OriginKindChat},
	}
	require.NoError(t, lifecycle.Persist(parent))

	const callID = "call-never-ran"
	launched, err := agent.NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: meta.ID,
		TargetAgentID:     "mia",
		Task:              "stopped while queued, never runs",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: callID},
	})
	require.NoError(t, err, "launch a real steered child (the parent must have a child to stop)")
	childID := launched.SessionID

	before, err := lifecycle.Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleQueued, before.State, "test setup invalid: the child must be queued, never dispatched")

	h := makeMinimalHandler()
	h.agentLoop = al
	h.msgBus = msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "user-stop-scope"

	// The real web Stop button, scoped to the child directly (TurnOnly, the
	// "this_turn"/"session" default) — the exact call chain handleCancel ->
	// handleCancelWithScope -> requestScopedStop -> StopTurns -> this
	// file's gateway-local stopTurn closure, not al.SteerGenerationCancel.
	h.handleCancel(wc, childID)

	after, err := lifecycle.Load(childID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, after.State, "D2: a never-ran queued child lands stopped, not a terminal failure")
	assert.False(t, after.Terminal(), "D2/Vocabulary: stopped is alive and resumable")
	assert.Equal(t, before.Generation, after.Generation, "a Stop never mints a replacement generation")
	assert.Empty(t, after.FailedReason, "a Stop is not a real failure")
	assert.Nil(t, after.FinalDelivery, "D2 CRIT-001: a Stop winner creates no terminal final outbox")
	require.NotNil(t, after.StopNote, "D2: the landed stop keeps its durable reason")
	assert.Equal(t, session.StopCauseStop, after.StopNote.Cause, "the child is the direct Stop target, not a cascaded descendant")
	assert.Equal(t, "human:"+wc.userID, after.StopNote.By, "the authenticated owner is the stop actor")
	assert.Equal(t, uint64(1), after.StopNote.Seq, "D4: this child's first stop takes its first monotonic stop sequence")
	assert.Nil(t, after.Stop, "D2: the stopped landing atomically spends the in-flight fence")

	entries, err := al.GetSessionStore().ReadTranscript(meta.ID)
	require.NoError(t, err)
	wantSpanID := agent.SubagentSpanID(callID, before.Generation)
	stoppedFrames, endFrames := 0, 0
	for i := range entries {
		if frame := entries[i].SubagentState; frame != nil && frame.SpanId == wantSpanID && frame.State == string(session.LifecycleStopped) {
			stoppedFrames++
			assert.Equal(t, string(generated.WsFrameTypeSubagentState), frame.Type, "D4: report a state transition, never a terminal end")
			assert.Equal(t, meta.ID, frame.SessionId, "the state frame belongs to the direct parent's transcript")
			require.NotNil(t, frame.ChildSessionId, "the stopped frame identifies the actual child")
			assert.Equal(t, childID, *frame.ChildSessionId, "the stopped frame must name this child")
		}
		if entries[i].SubagentEnd != nil && entries[i].SubagentEnd.SpanId == wantSpanID {
			endFrames++
		}
	}
	assert.Equal(t, 1, stoppedFrames, "D4: one durable subagent_state(stopped) reports the never-ran child's Stop")
	assert.Zero(t, endFrames, "D2: a resumable Stop must emit no subagent_end, including the retired interrupted verdict")

	// D6: one notice to the DIRECT parent, with this stopped transition's
	// tuple, cause/actor/time and all decide-offers. No terminal final id.
	inboxEntries, err := inbox.Entries(meta.ID)
	require.NoError(t, err)
	var notices []generated.SessionMessage
	for _, entry := range inboxEntries {
		if entry.Kind == session.InboxEntryMessage && entry.Message != nil {
			notices = append(notices, *entry.Message)
		}
	}
	require.Len(t, notices, 1, "D6: exactly one nonfatal stopped-child notice; no extra interrupted final")
	notice, err := notices[0].AsSessionMessageError()
	require.NoError(t, err, "the D6 notice uses the generated nonfatal error envelope")
	wantNoticeID := fmt.Sprintf("stopped-notice:%s:%s:%d:%d", meta.ID, childID, before.Generation, after.StopNote.Seq)
	assert.Equal(t, wantNoticeID, notice.MessageId, "D6's direct-parent/child/generation/stop-sequence dedup identity")
	assert.NotEqual(t, fmt.Sprintf("%s:%d:final", childID, before.Generation), notice.MessageId, "D2: never reserve a terminal final id for a Stop")
	assert.Equal(t, childID, notice.SessionId)
	require.NotNil(t, notice.ParentSessionId)
	assert.Equal(t, meta.ID, *notice.ParentSessionId)
	require.NotNil(t, notice.Generation)
	assert.Equal(t, before.Generation, *notice.Generation)
	assert.Equal(t, generated.SessionMessageErrorKindError, notice.Kind)
	assert.Equal(t, generated.SessionMessageErrorDirectionChildToParent, notice.Direction)
	assert.False(t, notice.Fatal, "D6: this is an alive helper's stop notice, not a fatal final")
	assert.True(t, notice.CreatedAt.Equal(after.StopNote.At), "the notice retains the landed stop's original instant")
	assert.Contains(t, notice.Text, "cause: "+string(session.StopCauseStop))
	assert.Contains(t, notice.Text, "actor: "+after.StopNote.By)
	assert.Contains(t, notice.Text, after.StopNote.At.UTC().Format(time.RFC3339Nano))
	for _, offer := range []string{"resume", "redirect", "do the work", "report it open", "clear that helper's goal", "consider asking the owner first"} {
		assert.Contains(t, strings.ToLower(notice.Text), offer, "D6's notice must offer the parent's decision: %s", offer)
	}
}

// newLiveTurnOnlyScopeFixture is u2ScopeFixture's own setup
// (adr093_web_stop_record_test.go::newU2ScopeFixture), trimmed to exactly
// one root session with a live in-flight turn — no descendants. The
// positive control below needs only that: an ordinary TurnOnly Stop on a
// session that is actually running a turn right now.
func newLiveTurnOnlyScopeFixture(t *testing.T) *u2ScopeFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "u2-scope-provider"}},
			List:     []config.AgentConfig{{ID: "mia", Home: home}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 3, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	ensureTestWorkspaceMembership(t, cfg)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	p := &u2ScopeProvider{entered: make(chan context.Context, 8), release: make(chan struct{})}
	al, err := agent.NewAgentLoop(cfg, msgBus, p)
	require.NoError(t, err)
	var closeOnce sync.Once
	closeLoop := func() { closeOnce.Do(al.Close) }
	t.Cleanup(closeLoop)
	t.Cleanup(func() { close(p.release) })
	// Production always mints and registers the boot epoch before the first
	// admission (gateway_boot.go::setupAndStartServices -> mintBootEpoch); the
	// ordinary-root admission refuses epoch 0 ("no minted boot epoch"). Mint a
	// genuine store exactly as the sibling newU2ScopeFixture does — never a
	// hard-coded epoch (boot-epoch ruling: an epoch-0 positive harness is invalid).
	boot := session.NewBootEpochStore(home)
	epoch, mintErr := boot.Mint()
	require.NoError(t, mintErr, "SETUP Mint genuine boot epoch")
	require.NotZero(t, epoch, "SETUP requires one genuine nonzero boot epoch")
	require.Equal(t, epoch, boot.Current(), "SETUP must wire the epoch minted by this store")
	al.SetBootEpochStore(boot)
	lifecycle := session.NewLifecycleStore(t.TempDir())
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), lifecycle)
	setGatewaySteerCanceller(al, agent.NewSteerCanceller(lifecycle, al.SteerGenerationCancel))
	t.Cleanup(func() { gatewaySteerCancellers.Delete(al) })
	classifier := agent.NewSteerRecordClassifier(lifecycle, al.GetSessionStore())
	al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())

	h := makeMinimalHandler()
	h.agentLoop, h.msgBus = al, msgBus
	wc, _ := makeForwarderTestConn(32)
	wc.userID = "u2-scope-user"
	f := &u2ScopeFixture{
		al: al, lifecycle: lifecycle, provider: p, closeLoop: closeLoop,
		reader:   &wsHandlerReadLoop{h: h, wc: wc, ctx: context.Background(), chatID: "live-turn-only-chat"},
		contexts: make(map[string]context.Context), before: make(map[string]*session.LifecycleRecord),
	}
	f.parent = f.startRoot(t)
	f.before[f.parent], err = lifecycle.Load(f.parent)
	require.NoError(t, err)
	return f
}

// TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal is the
// positive control. See the file-header comment (case 2) for the oracle.
func TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal(t *testing.T) {
	f := newLiveTurnOnlyScopeFixture(t)
	gstore := goal.NewStore(config.OmnipusHomeDir())
	before := f.seedActiveGoal(t, gstore, f.parent)

	f.cancelFrame(t, nil) // omitted scope => "this_turn": TurnOnly, no subtree.

	require.Equal(t, context.Canceled, f.contexts[f.parent].Err(), "the live turn must actually be cancelled by the Stop")

	for _, join := range f.joinScheduled {
		join()
	}
	f.closeLoop()

	after, err := gstore.Get(before.GoalID)
	require.NoError(t, err, "an ordinary TurnOnly web Stop must not remove the session-owned goal")
	assert.Equal(t, generated.GoalStateActive, after.State,
		"c6bc40804: an ordinary TurnOnly web Stop ends the turn, not the goal — the goal must stay Active")
	assert.Equal(t, before, after, "the entire active goal record must be unchanged by an ordinary TurnOnly Stop")

	rec, err := f.lifecycle.Load(f.parent)
	require.NoError(t, err)
	// ADR-20260928 (sub-agent control plane) D2: `stopped` REPLACES the old
	// paused/cancelled states as one alive, resumable, NON-terminal state — a
	// Stop of a live turn legitimately lands the record `stopped` (the sibling
	// requireStopped oracle: "stopped is resumable/nonterminal"). The property
	// c6bc40804 protects is therefore "never terminal" (done/failed), not
	// "never stopped": the superseded oracle pinned the pre-D2 vocabulary in
	// which Stopped was the terminal landing.
	assert.False(t, rec.Terminal(),
		"c6bc40804 + D2: an ordinary TurnOnly Stop on a live turn must not land the record terminal (state %q); `stopped` is non-terminal", rec.State)
}

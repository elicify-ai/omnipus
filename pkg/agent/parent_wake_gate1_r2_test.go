package agent

// Gate-1 fix round 2 for PR #1345 (parent wake). One proof test per fix; the
// reviewer IDs are in each comment.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestParkedQuestion_PartialSaveRetryStillReachesTheAnswer — R2-1. The saved
// report's archive append lands but its pending-index write fails. The retried
// delivery must restore the pending entry, so the person's answer continuation
// still carries the held report exactly once.
func TestParkedQuestion_PartialSaveRetryStillReachesTheAnswer(t *testing.T) {
	const heldBody = "HELPER-REPORT-AFTER-A-PARTIAL-SAVE"
	provider := &scriptChatProvider{
		scripted: []*providers.LLMResponse{
			e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
				`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
					`"options":[{"label":"Single","description":"one player"},`+
					`{"label":"Multi","description":"two players"}]}]}`),
			e2eTextResponse("Continuing with the answer and the helper report."),
		},
		fallback: "later turn",
	}
	al, msgBus := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	reg := askuser.NewRegistry(store, &throughBusResumeDispatcher{msgBus: msgBus}, askuser.Options{})
	al.SetAskUserRegistry(reg)
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	go func() { _ = al.Run(runCtx) }()
	pubCtx, pubCancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-r21", SessionID: sid, Content: "Pick a game with me.",
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "mia"},
	}))
	waitForLLMCallCount(t, provider, 1, busWaitBudget)
	pending := waitForPendingCard(t, reg, sid, busWaitBudget)
	deadline := time.Now().Add(busWaitBudget)
	for al.liveTurnForSession(sid) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Nil(t, al.liveTurnForSession(sid), "SETUP: the question turn must have parked")

	session.SetSavedInputIndexWriteFaultForTest(func(string) error { return errors.New("injected index write failure") })
	t.Cleanup(func() { session.SetSavedInputIndexWriteFaultForTest(nil) })
	_, err = al.WakeSession(context.Background(), sid, "finished", "helper-partial:1:final", heldBody)
	require.Error(t, err, "SETUP: the first save fails after its archive append")
	session.SetSavedInputIndexWriteFaultForTest(nil)

	retry, err := al.WakeSession(context.Background(), sid, "finished", "helper-partial:1:final", heldBody)
	require.NoError(t, err)
	require.Equal(t, "held_for_question", retry.Disposition)

	require.NoError(t, reg.Submit(pending.CardID, sid, "daniel",
		[]askuser.SubmittedAnswer{{Header: "Scope", Selected: []string{"Single"}}}))
	waitForLLMCallCount(t, provider, 2, busWaitBudget)
	second, ok := provider.lastMessages(2)
	require.True(t, ok)
	require.Equal(t, 1, gate1CountInMessages(second, heldBody),
		"the answer continuation carries the report exactly once after the partial save was retried")
}

// TestLateOrdinaryJoin_PostTurnContinuationDeliversOnce — R2-2. An outcome
// joined an ordinary parent after its last steering poll, so the post-turn
// drain (Continue) consumes it. That continuation has no transcript binding of
// its own; the saved report must still be placed once and its claim released.
func TestLateOrdinaryJoin_PostTurnContinuationDeliversOnce(t *testing.T) {
	const lateBody = "HELPER-OUTCOME-AFTER-THE-LAST-POLL"
	provider := &scriptChatProvider{fallback: "continued with the late outcome"}
	al, _ := newChatTargetAskUserLoop(t, provider)
	gate1WireSessionMessaging(t, al)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := meta.ID
	scope := "agent:mia:session:" + sid
	// The parent's own earlier turn (it has finished; the outcome arrived after
	// its last steering poll and is drained by the post-turn continuation).
	_, _, err = al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: sid, SessionID: sid, Content: "Delegate the export.",
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true,
		Metadata: map[string]string{"agent_id": "mia"},
	})
	require.NoError(t, err)
	_, err = al.GetSessionStore().SaveChatInput(sid, "saved-input-late-join", "mia", lateBody)
	require.NoError(t, err)
	al.asyncNotifier.sessionWakes.Store(sid+"\x00late-join", true) // the join's publication claim
	require.NoError(t, al.EnqueueSteeringWake(scope, "mia", sid, "late-join",
		providers.Message{Role: "user", Content: lateBody}))

	_, err = al.Continue(context.Background(), scope, "webchat", sid, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	require.Equal(t, 2, provider.callCount(), "the parent's turn, then one continuation model call")
	request, ok := provider.lastMessages(2)
	require.True(t, ok)
	require.Equal(t, 1, gate1CountInMessages(request, lateBody), "the late outcome reaches the model once")
	require.False(t, al.wakePublicationInFlight(sid, "late-join"), "its claim is released")
	left, err := al.GetSessionStore().PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Empty(t, left, "the saved report was consumed, not stranded")
}

// gate1R2ToolThenTextProvider holds its first call, then answers it with one
// tool call; any later call answers with text.
type gate1R2ToolThenTextProvider struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *gate1R2ToolThenTextProvider) Chat(ctx context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.calls++
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		close(p.entered)
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return e2eToolCallResponse("call_missing", "gate1_r2_no_such_tool", `{}`), nil
	}
	return &providers.LLMResponse{Content: "after the tool"}, nil
}

func (*gate1R2ToolThenTextProvider) GetDefaultModel() string { return "gate1-r2-tool-then-text" }
func (p *gate1R2ToolThenTextProvider) open()                 { p.once.Do(func() { close(p.release) }) }

// TestRateLimitedTurn_ReleasesUninjectedSavedWakeClaim — SFH-02 (rest). A
// joined outcome is dequeued at a tool boundary, then the next iteration is
// refused by the per-agent model-call limit before injection. The turn's exit
// must give back that saved input's claim; the report stays pending.
func TestRateLimitedTurn_ReleasesUninjectedSavedWakeClaim(t *testing.T) {
	const body = "HELPER-OUTCOME-DEQUEUED-BEFORE-A-RATE-LIMIT"
	provider := &gate1R2ToolThenTextProvider{entered: make(chan struct{}), release: make(chan struct{})}
	// A custom (non-core) agent: core agents are exempt from the model-call
	// limit (security.IsPrivilegedAgent).
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
		List:     []config.AgentConfig{{ID: "rl-agent", Name: "RL", Type: config.AgentTypeCustom, Home: t.TempDir()}},
	}}
	cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour = 1
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	gate1WireSessionMessaging(t, al)
	t.Cleanup(provider.open)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "rl-agent")
	require.NoError(t, err)
	sid := meta.ID
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	go func() { _ = al.Run(runCtx) }()
	pubCtx, pubCancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-rl", SessionID: sid, Content: "Start working.",
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel",
		Metadata: map[string]string{"agent_id": "rl-agent"},
	}))
	select {
	case <-provider.entered:
	case <-time.After(busWaitBudget):
		t.Fatal("SETUP: the ordinary turn never reached its provider")
	}
	joined, err := al.WakeSession(context.Background(), sid, "finished", "helper-rl:1:final", body)
	require.NoError(t, err)
	require.Equal(t, "joined_live_turn", joined.Disposition)
	provider.open()
	deadline := time.Now().Add(busWaitBudget)
	for al.liveTurnForSession(sid) != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Nil(t, al.liveTurnForSession(sid), "SETUP: the rate-limited turn ended")
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	require.Equal(t, 1, calls, "SETUP: the second model call was refused by the rate limit")

	// The parent's turn and its post-turn continuation are both refused by the
	// limit; once they have given up, nothing may still hold the claim.
	released := false
	for end := time.Now().Add(busWaitBudget); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if !al.wakePublicationInFlight(sid, "helper-rl:1:final") {
			released = true
			break
		}
	}
	require.True(t, released, "the abandoned saved input gives back its claim")
	left, err := al.GetSessionStore().PendingChatInputs(context.Background(), sid)
	require.NoError(t, err)
	require.Len(t, left, 1, "the report stays pending for the next legitimate fence")
}

// TestResume_RefusedWhenRecordedWorkspaceWasDeleted — PW-SEC-01 (deleted
// workspace). The helper's recorded workspace no longer exists: restart
// recovery must not continue its saved work in another workspace.
func TestResume_RefusedWhenRecordedWorkspaceWasDeleted(t *testing.T) {
	h := newPWBootRecoveryHarness(t)
	const wsGone = "gate1-ws-deleted"
	_, statErr := os.Stat(filepath.Join(omnipusHome(), "workspaces", wsGone+".json"))
	require.True(t, errors.Is(statErr, os.ErrNotExist), "SETUP: the recorded workspace must not exist")
	parent := h.rootSession(t)
	child := h.newSession(t, session.SessionTypeDelegate, parent)
	rec := h.steeredRecord(child, parent, session.LifecycleRunning)
	rec.WorkspaceID = wsGone
	h.persist(t, rec)
	h.seedCompletedToolHistory(t, child)

	require.NoError(t, h.recovery().Run(context.Background()))
	after, err := h.lifecycle.Load(child)
	require.NoError(t, err)
	require.NotEqual(t, session.LifecycleRunning, after.State, "no automatic resume into another workspace")
	require.NotEqual(t, session.LifecycleQueued, after.State)
	refused := 0
	entries, err := h.sessions.ReadTranscript(parent)
	require.NoError(t, err)
	for _, e := range entries {
		if e.SystemSubtype == "helper_event" && strings.Contains(e.Content, "Resume did not start.") {
			refused++
		}
	}
	require.Equal(t, 1, refused, "the refusal is visible in the parent chat")
}

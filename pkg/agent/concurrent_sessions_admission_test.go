// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// concurrent_sessions_admission_test.go — founder ruling 2026-10-06: the
// ordinary-execution admission ("waiting room") is per chat SESSION, never per
// agent. Two chats with the same agent run in parallel; one chat's own turns
// still serialise. These tests drive the real AgentLoop through the bus
// (Run -> dispatchSessionWorker -> processMessage -> provider) with a provider
// whose calls block until the test releases them, so the assertions are about
// ordering, not wall-clock time.
package agent

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// gatedProvider blocks every Chat call whose last user message contains a
// registered marker until the test releases that marker. It records the order
// in which provider calls were entered.
type gatedProvider struct {
	entered map[string]chan struct{}
	release map[string]chan struct{}
}

func newGatedProvider(markers ...string) *gatedProvider {
	p := &gatedProvider{entered: map[string]chan struct{}{}, release: map[string]chan struct{}{}}
	for _, m := range markers {
		p.entered[m] = make(chan struct{}, 8)
		p.release[m] = make(chan struct{})
	}
	return p
}

func (p *gatedProvider) Chat(
	ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	last := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			last = msgs[i].Content
			break
		}
	}
	for marker, entered := range p.entered {
		if !strings.Contains(last, marker) {
			continue
		}
		entered <- struct{}{}
		select {
		case <-p.release[marker]:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &providers.LLMResponse{Content: "reply to " + marker}, nil
	}
	return &providers.LLMResponse{Content: "ungated"}, nil
}

func (p *gatedProvider) GetDefaultModel() string { return "gated-mock" }

// waitEntered reports whether marker's provider call was entered within d.
func (p *gatedProvider) waitEntered(marker string, d time.Duration) bool {
	select {
	case <-p.entered[marker]:
		return true
	case <-time.After(d):
		return false
	}
}

// enteredCount is how many provider calls for marker are entered and not yet consumed by waitEntered.
func (p *gatedProvider) enteredCount(marker string) int { return len(p.entered[marker]) }

func (p *gatedProvider) releaseMarker(marker string) { close(p.release[marker]) }

// newAdmissionSessionsLoop builds a loop with one chat-target agent, starts
// Run, and returns the loop, the bus and two fresh chat sessions of that agent.
func newAdmissionSessionsLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *bus.MessageBus, string, string) {
	t.Helper()
	return newAdmissionSessionsLoopCfg(t, provider, nil)
}

// newAdmissionSessionsLoopCfg is newAdmissionSessionsLoop with a hook to adjust
// the config (for example routing bindings) before the loop is built.
func newAdmissionSessionsLoopCfg(t *testing.T, provider providers.LLMProvider, mutate func(*config.Config)) (*AgentLoop, *bus.MessageBus, string, string) {
	t.Helper()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "test-model"}},
			List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCore, Home: t.TempDir()}},
		},
	}
	if mutate != nil {
		mutate(cfg)
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	// Production wiring: a lifecycle store and a minted boot epoch, so every
	// ordinary turn goes through the real admission (prepareOrdinaryExecution).
	al.SetSessionMessagingStores(nil, session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle")))
	mintGenuineBootEpochForLoop(t, al)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	a, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	b, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	runCtx, runCancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()
	t.Cleanup(func() { runCancel(); <-runDone })
	return al, msgBus, a.ID, b.ID
}

func publishChat(t *testing.T, msgBus *bus.MessageBus, sessionID, content string, meta map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), busWaitBudget)
	defer cancel()
	require.NoError(t, msgBus.PublishInbound(ctx, bus.InboundMessage{
		Channel:       "webchat",
		ChatID:        "chat-" + sessionID,
		SessionID:     sessionID,
		Content:       content,
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		GatewayUserID: "daniel",
		UserInitiated: true,
		Metadata:      meta,
	}))
}

const admissionEnterBudget = 10 * time.Second

// Two chats of the same agent, default route (no agent_id — the shape the
// gateway's webchat path sends): B's provider call must be entered while A's is
// still blocked.
func TestOrdinaryAdmission_TwoSessionsSameAgent_DefaultRoute_RunInParallel(t *testing.T) {
	p := newGatedProvider("MARK-A", "MARK-B")
	_, msgBus, sidA, sidB := newAdmissionSessionsLoop(t, p)

	publishChat(t, msgBus, sidA, "MARK-A", nil)
	require.True(t, p.waitEntered("MARK-A", admissionEnterBudget), "session A's provider call was never entered")

	publishChat(t, msgBus, sidB, "MARK-B", nil)
	enteredB := p.waitEntered("MARK-B", admissionEnterBudget)
	// Unblock both so the loop can drain whatever the outcome.
	p.releaseMarker("MARK-A")
	p.releaseMarker("MARK-B")
	require.True(t, enteredB, "session B (same agent, different chat) was held behind session A's blocked turn")
}

// Same, with an explicit agent_id (session-qualified route key).
func TestOrdinaryAdmission_TwoSessionsSameAgent_ExplicitAgent_RunInParallel(t *testing.T) {
	p := newGatedProvider("MARK-A", "MARK-B")
	_, msgBus, sidA, sidB := newAdmissionSessionsLoop(t, p)
	meta := map[string]string{"agent_id": "mia"}

	publishChat(t, msgBus, sidA, "MARK-A", meta)
	require.True(t, p.waitEntered("MARK-A", admissionEnterBudget), "session A's provider call was never entered")

	publishChat(t, msgBus, sidB, "MARK-B", meta)
	enteredB := p.waitEntered("MARK-B", admissionEnterBudget)
	p.releaseMarker("MARK-A")
	p.releaseMarker("MARK-B")
	require.True(t, enteredB, "session B (same agent, explicit agent_id) was held behind session A's blocked turn")
}

// Regression guard: two messages to the SAME session still serialise — the
// second one's provider call is not entered while the first is blocked, and it
// IS entered once the first is released. No wall-clock window: the second
// message is first observed queued behind the running turn (an event), then the
// provider's entered-state is read.
func TestOrdinaryAdmission_SameSession_StillSerialises(t *testing.T) {
	p := newGatedProvider("MARK-A1", "MARK-A2")
	al, msgBus, sidA, _ := newAdmissionSessionsLoop(t, p)

	publishChat(t, msgBus, sidA, "MARK-A1", nil)
	require.True(t, p.waitEntered("MARK-A1", admissionEnterBudget), "first turn's provider call was never entered")

	publishChat(t, msgBus, sidA, "MARK-A2", nil)
	waitMessageQueuedBehindTurn(t, al, sidA)
	require.Zero(t, p.enteredCount("MARK-A2"), "a second message to the same session reached the provider while the first turn was still blocked")

	p.releaseMarker("MARK-A1")
	enteredAfter := p.waitEntered("MARK-A2", admissionEnterBudget)
	p.releaseMarker("MARK-A2")
	require.True(t, enteredAfter, "the queued second message never ran after the first turn was released")
}

// waitMessageQueuedBehindTurn waits (by polling state, not by sleeping a fixed
// window) until a message for the session sits behind its running turn: either
// in the in-turn steering queue or in the session worker's inbox.
func waitMessageQueuedBehindTurn(t *testing.T, al *AgentLoop, sessionID string) {
	t.Helper()
	key := agentSessionKey("mia", bus.InboundMessage{SessionID: sessionID})
	deadline := time.Now().Add(admissionEnterBudget)
	for time.Now().Before(deadline) {
		if al.pendingSteeringCountForScope(key) > 0 {
			return
		}
		queued := false
		al.sessionWorkers.Range(func(_, v any) bool {
			if w, ok := v.(*sessionWorker); ok && len(w.inbox) > 0 {
				queued = true
			}
			return !queued
		})
		if queued {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the second message for session %s was never queued behind the running turn", sessionID)
}

// The admission itself is keyed by the chat session id, not by the routing
// SessionKey: two different sessions that carry the SAME agent-level routing
// key must both be admitted, while a second admission for the same session id
// is still refused while the first execution is pending.
func TestOrdinaryAdmission_KeyedBySessionID_NotRoutingKey(t *testing.T) {
	al, _, sidA, sidB := newAdmissionSessionsLoop(t, newGatedProvider())
	opts := processOptions{SessionKey: "agent:mia:main", TranscriptStore: al.GetSessionStore()} // agent-level key shared by both chats
	human := func(sid string) bus.InboundMessage {
		return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + sid, SessionID: sid,
			Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true}
	}
	ctx := context.Background()

	prepA, err := al.prepareOrdinaryExecution(ctx, human(sidA), opts)
	require.NoError(t, err)
	require.NotNil(t, prepA.execution)

	// A is still pending: a different session of the same agent is admitted at once.
	start := time.Now()
	prepB, err := al.prepareOrdinaryExecution(ctx, human(sidB), opts)
	require.NoError(t, err, "a different session must not be refused because another session of the agent is pending")
	require.NotNil(t, prepB.execution)
	require.Less(t, time.Since(start), previousExecutionSettleBudget/2, "a different session must not wait in the previous-execution waiting room")

	// The SAME session is still held back while its own execution is pending.
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_, err = al.prepareOrdinaryExecution(waitCtx, human(sidA), opts)
	require.ErrorIs(t, err, ErrPreviousExecutionPending,
		"a second admission for the same session must be refused as 'previous execution still pending' — not by a context deadline or any other error")
	require.NotErrorIs(t, err, context.DeadlineExceeded)
}

// Every turn-scoped key derived from the route (turn registry, history,
// steering queue, continuation target) must be per chat session when the
// message carries a session id — on the default route too. If two chats of the
// same agent shared "agent:<id>:main", a follow-up typed into one chat could be
// drained by the other chat's running turn.
func TestResolveMessageRoute_DefaultRoute_SessionKeyIsPerSession(t *testing.T) {
	al, _, sidA, sidB := newAdmissionSessionsLoop(t, newGatedProvider())
	msgFor := func(sid string) bus.InboundMessage {
		return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + sid, SessionID: sid,
			Sender: bus.SenderInfo{CanonicalID: "webchat_user"}}
	}
	routeA, _, err := al.resolveMessageRoute(msgFor(sidA))
	require.NoError(t, err)
	routeB, _, err := al.resolveMessageRoute(msgFor(sidB))
	require.NoError(t, err)
	require.NotEqual(t, routeA.SessionKey, routeB.SessionKey, "two chats of one agent resolved to the same routing session key")
	require.Contains(t, routeA.SessionKey, sidA)
	require.Contains(t, routeB.SessionKey, sidB)

	targetA, err := al.buildContinuationTarget(msgFor(sidA))
	require.NoError(t, err)
	targetB, err := al.buildContinuationTarget(msgFor(sidB))
	require.NoError(t, err)
	require.NotEqual(t, targetA.SessionKey, targetB.SessionKey, "steering-queue key is shared between two chats of one agent")
}

// A hand-back wake of the same conversation arrives while the human turn's
// tail still owns the session's execution. The wake enters admission with
// opts.SessionKey = the session id while the human turn used the routing key;
// both must land in the same (session id) bucket, so the wake WAITS for the
// human execution to settle and then runs — it is neither admitted in parallel
// nor refused.
func TestOrdinaryAdmission_HandbackWakeDuringHumanTail_WaitsThenRuns(t *testing.T) {
	al, _, sidA, _ := newAdmissionSessionsLoop(t, newGatedProvider())
	ctx := context.Background()
	humanOpts := processOptions{SessionKey: "agent:mia:session:" + sidA, TranscriptStore: al.GetSessionStore()}
	wakeOpts := processOptions{SessionKey: sidA, TranscriptStore: al.GetSessionStore(), TranscriptSessionID: sidA}

	human, err := al.prepareOrdinaryExecution(ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-" + sidA, SessionID: sidA,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true,
	}, humanOpts)
	require.NoError(t, err)
	require.NotNil(t, human.execution)

	type result struct {
		prep ordinaryExecutionPreparation
		err  error
	}
	done := make(chan result, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		prep, werr := al.prepareOrdinarySessionExecution(ctx, sidA, wakeOpts, &handbackRevivalPrincipal)
		done <- result{prep, werr}
	}()
	<-started
	// Give the wake goroutine scheduler turns (no timer) and require that it
	// has neither returned nor taken over the session's execution owner.
	for i := 0; i < 200; i++ {
		runtime.Gosched()
		select {
		case r := <-done:
			t.Fatalf("the hand-back wake did not wait for the human turn's tail: prep=%v err=%v", r.prep.execution != nil, r.err)
		default:
		}
	}
	al.admission.mu.Lock()
	owner := al.admission.activeScopes[sidA]
	stillHuman := owner != nil && owner.execution == human.execution
	al.admission.mu.Unlock()
	require.True(t, stillHuman, "the session's execution owner moved to the wake while the human tail was still pending")

	require.NoError(t, al.finishExecutionDisposition(human.execution))
	select {
	case r := <-done:
		require.NoError(t, r.err, "the hand-back wake must run after the human tail settles, not be refused")
		require.NotNil(t, r.prep.execution)
		require.NoError(t, al.finishExecutionDisposition(r.prep.execution))
	case <-time.After(admissionEnterBudget):
		t.Fatal("the hand-back wake never proceeded after the human tail settled")
	}
}

// The channel-binding branch of the routing cascade, with a session id set,
// also yields a per-session route key.
func TestResolveMessageRoute_BindingBranch_SessionKeyIsPerSession(t *testing.T) {
	al, _, sidA, sidB := newAdmissionSessionsLoopCfg(t, newGatedProvider(), func(cfg *config.Config) {
		cfg.Bindings = []config.AgentBinding{{AgentID: "mia", Match: config.BindingMatch{Channel: "webchat"}}}
	})
	msgFor := func(sid string) bus.InboundMessage {
		return bus.InboundMessage{Channel: "webchat", ChatID: "chat-" + sid, SessionID: sid,
			Sender: bus.SenderInfo{CanonicalID: "webchat_user"}}
	}
	routeA, _, err := al.resolveMessageRoute(msgFor(sidA))
	require.NoError(t, err)
	routeB, _, err := al.resolveMessageRoute(msgFor(sidB))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(routeA.MatchedBy, "binding."), "the test must go through a binding branch, got %q", routeA.MatchedBy)
	require.NotEqual(t, routeA.SessionKey, routeB.SessionKey)
	require.Contains(t, routeA.SessionKey, sidA)
	require.Contains(t, routeB.SessionKey, sidB)
}

// An ORDINARY admission refusal reaches the person as the "still finishing"
// sentence, never the generic "can't tell why" copy. A bare
// steer.ErrStaleGeneration — what steered dispatch, reconstruction, cancel and
// the queued wake return — is NOT an admission refusal and keeps its own text.
func TestTranslateTurnError_PreviousExecutionPending_UserMessage(t *testing.T) {
	const want = "Your previous reply is still finishing — send your message again in a moment."
	generic := TranslateLLMError(nil, "something unrecognisable happened").Message
	require.NotEqual(t, want, generic)

	for name, err := range map[string]error{
		"pending sentinel":          ErrPreviousExecutionPending,
		"pending sentinel, wrapped": fmt.Errorf("turn: %w", ErrPreviousExecutionPending),
		"ordinary admission refusal": refuseOrdinaryAdmission(
			fmt.Errorf("ordinary admission: %w: registration was already claimed", steer.ErrStaleGeneration)),
		"ordinary admission refusal, wrapped": fmt.Errorf("turn: %w", refuseOrdinaryAdmission(steer.ErrStaleGeneration)),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, want, TranslateTurnError(err).Message)
			require.Equal(t, want, userVisibleTurnError(err), "the session worker publishes this text")
		})
	}

	// Existing callers still see the stale-generation cause through the refusal.
	require.ErrorIs(t, refuseOrdinaryAdmission(steer.ErrStaleGeneration), steer.ErrStaleGeneration)

	for name, err := range map[string]error{
		"bare stale generation":    steer.ErrStaleGeneration,
		"wrapped stale generation": fmt.Errorf("steer: dispatch: %w: promoted reservation is stale", steer.ErrStaleGeneration),
		"unrelated":                errors.New("unrelated failure"),
	} {
		t.Run("not mapped: "+name, func(t *testing.T) {
			require.NotEqual(t, want, TranslateTurnError(err).Message)
			require.NotEqual(t, want, userVisibleTurnError(err))
		})
	}
}

// Through the real admission: a session that already has a registered turn is
// refused with the ordinary-admission refusal — still a stale-generation error
// for the callers that rely on that, and the "still finishing" sentence for the
// person.
func TestOrdinaryAdmission_RegisteredTurn_RefusalIsUserVisibleAndStillStale(t *testing.T) {
	al, _, sidA, _ := newAdmissionSessionsLoop(t, newGatedProvider())
	ts := newTurnState(&AgentInstance{ID: "mia"},
		processOptions{SessionKey: "agent:mia:session:" + sidA, TranscriptSessionID: sidA},
		turnEventScope{agentID: "mia", sessionKey: "agent:mia:session:" + sidA, turnID: "mia-turn-1"})
	al.registerActiveTurn(ts)
	t.Cleanup(func() { al.clearActiveTurn(ts) })

	_, err := al.prepareOrdinaryExecution(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "chat-" + sidA, SessionID: sidA,
		Sender: bus.SenderInfo{CanonicalID: "webchat_user"}, GatewayUserID: "daniel", UserInitiated: true,
	}, processOptions{SessionKey: "agent:mia:session:" + sidA, TranscriptStore: al.GetSessionStore()})
	require.Error(t, err)
	require.ErrorIs(t, err, steer.ErrStaleGeneration)
	require.Equal(t, previousReplyStillFinishingMessage, userVisibleTurnError(err))
}

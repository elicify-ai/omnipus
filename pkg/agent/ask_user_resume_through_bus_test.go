// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ask_user_resume_through_bus_test.go — SQUAD-R round 2, Phase 1: the
// through-the-bus chat-target AskUserQuestion reproduction for issue #760
// ("Answering the AskUserQuestion card kills the running turn; answers
// only surface on the next prompt.").
//
// Round 1's TestAskUserResumeDispatcher_PublishesToBus (pkg/gateway/
// ws_ask_user_test.go) bounded the search to "the defect, if Go-side, is
// DOWNSTREAM of the publish leg." The session-worker path that consumes
// the bus and routes through processMessage/processTurn/runTurn is the
// remaining untested leg, and it is the one the founder's field defect
// actually exercises: a chat-target agent turn parks on AskUserQuestion,
// the human answers the SPA card, and the answer MUST reach the same
// running session worker's NEXT turn — exactly as a fresh user prompt
// would, but reusing the parked turn's transcript/history context.
//
// That through-the-bus shape is the one `newGoalLoopTestLoop` deliberately
// does NOT exercise: every existing goal-flow test in this package drives
// `runTurn` directly via an `e2eResumeDispatcher` (a test-only synchronous
// shim — pkg/agent/goal_flow_integration_test.go:123-136, the round-1
// "test-harness shortcut" the report flags), because the harness's
// `native-agent` is a worker and a worker is never a chat target
// (pkg/agent/loop_inbound.go::resolveMessageRoute's explicit-agent_id
// fast-path refuses to route to one). The round-2 brief makes closing
// that gap the explicit mission.
//
// The harness registered here (newChatTargetAskUserLoop) does the
// minimum needed to exercise the production path end-to-end:
//
//   - a single chat-target agent ("mia", AgentTypeCore — IsWorker=false,
//     IsChatTarget=true per pkg/agent/instance.go::IsChatTarget);
//   - the production askuser.Registry shape (UnifiedMeta-backed, real
//     registry);
//   - a DispatchResume implementation that publishes onto the real bus
//     the loop consumes (a test-local mirror of pkg/gateway/ws_ask_user.go's
//     production askUserResumeDispatcher — same Channel/ChatID/SessionID
//     keys, same UserInitiated, same Metadata[agent_id]);
//   - the loop's Run goroutine started, so the AskUserQuestion resume
//     message lands through processInbound → resolveSteeringTarget →
//     dispatchSessionWorker → sessionWorker.processTurn → processMessage
//     → runTurn (the exact production code path the field defect hits),
//     not through any test-only synchronous shortcut.
//
// The assertion is RED-shaped: the resume bus message MUST drive a
// SECOND LLM call on the same agent/session within the timeout. A green
// here means the harness has not yet reproduced #760 (per the brief:
// "tighten the harness, not the assertion"). A red — the second LLM
// call never arrives — reproduces the founder's symptom verbatim: the
// running turn's waiter never resolves, the answer is recorded on the
// pending set, and the next user prompt is what surfaces it.
package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// throughBusResumeDispatcher is the test-local mirror of pkg/gateway/
// ws_ask_user.go::askUserResumeDispatcher.DispatchResume: it publishes the
// §0.2 correlated user-role resume message onto the SAME bus.MessageBus
// the AgentLoop consumes in its Run goroutine. Mirrors the production
// dispatcher's Message shape (Channel, ChatID, SessionID, Content,
// UserInitiated, Sender.CanonicalID, GatewayUserID, and Metadata[agent_id])
// field-for-field, so any routing/serialization pathology in the
// production shape will reproduce here too.
//
// Lives in pkg/agent (not pkg/gateway) to avoid an import cycle: the
// loop tests must drive the loop directly. Functionally identical to the
// production dispatcher — a copy kept here under a deliberately distinct
// name so a future refactor that touches one cannot silently desync the
// other without test fallout.
type throughBusResumeDispatcher struct {
	msgBus *bus.MessageBus
}

func (d *throughBusResumeDispatcher) DispatchResume(set *askuser.PendingSet, resumeText string) error {
	msg := bus.InboundMessage{
		Channel:       set.Channel,
		ChatID:        set.ChatID,
		SessionID:     set.TranscriptSessionID,
		Content:       resumeText,
		UserInitiated: true, // human-submitted answer is a user-initiated turn (the field's own definition)
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		GatewayUserID: set.Owner,
	}
	if set.AgentID != "" {
		msg.Metadata = map[string]string{"agent_id": set.AgentID}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.msgBus.PublishInbound(ctx, msg)
}

// scriptChatProvider scripts a fixed sequence of LLM responses by
// 1-based call number — calls beyond the script return a plain-text
// fallback (never an error) so an unexpected extra round degrades to an
// observable assertion failure rather than a panic. Captures each call's
// messages list so the assertion can verify what the resume turn's LLM
// prompt actually carried (the §0.2 correlated user-role message MUST
// reach the model on the second call — that is the live-resolve
// round-trip, end-to-end, the field defect breaks).
type scriptChatProvider struct {
	mu       sync.Mutex
	calls    int
	scripted []*providers.LLMResponse
	captured [][]providers.Message
	fallback string
}

func (p *scriptChatProvider) Chat(
	_ context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	cp := append([]providers.Message(nil), msgs...)
	p.captured = append(p.captured, cp)
	if p.calls-1 < len(p.scripted) && p.scripted[p.calls-1] != nil {
		return p.scripted[p.calls-1], nil
	}
	return &providers.LLMResponse{Content: p.fallback}, nil
}

func (p *scriptChatProvider) GetDefaultModel() string { return "script-chat-target-mock" }

func (p *scriptChatProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// lastMessages returns a copy of the messages list from the N-th (1-based)
// LLM call, with a bool indicating whether the call happened. Used to
// assert what the resume turn actually saw in its prompt — the live-
// resolve symptom: when the resume message never reaches the new turn's
// prompt, the conversation is essentially "starting over" from the
// script's perspective, and the second call's first user message is the
// raw initial prompt rather than the §0.2 resume.
func (p *scriptChatProvider) lastMessages(n int) ([]providers.Message, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.captured) {
		return nil, false
	}
	cp := append([]providers.Message(nil), p.captured[n-1]...)
	return cp, true
}

// newChatTargetAskUserLoop builds an AgentLoop with a SINGLE chat-target
// agent ("mia", AgentTypeCore), no worker, no judge — the minimum
// harness needed to drive the through-the-bus resume path on a real chat
// target. Returns the loop and a freshly-minted session (the store +
// transcript id the AskUserQuestion card is registered against and the
// resume turn runs in). The caller is responsible for al.Close() and
// for starting the loop's Run goroutine with the ctx it returns.
//
// Workspace-membership seeding mirrors pkg/agent/test_helpers_test.go's
// mustNewAgentLoop's contract: the configured agent must resolve under
// routing.NormalizeAgentID at runTurn's ADR-046 P1 gate, or the test
// would silently refuse with only a WARN log. mustNewAgentLoop does
// that seeding internally; we route through it so the harness inherits
// the same guard.
func newChatTargetAskUserLoop(
	t *testing.T, provider providers.LLMProvider,
) (*AgentLoop, *bus.MessageBus) {
	t.Helper()
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         t.TempDir(),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{
					ID:   "mia",
					Name: "Mia",
					Type: config.AgentTypeCore, // chat target — IsWorker()==false
					Home: workspace,
				},
			},
		},
	}
	// Pre-seed the global ceiling so the chat-target agent's tool-policy
	// resolution on the live message has a real entry to inherit (Hard
	// Constraint #6: no default policy fallback, fail-closed deny on
	// missing entries). The per-agent tighten/override below will narrow
	// the actual tool surface the agent sees.
	cfg.Sandbox.ToolPolicies = map[string]string{
		"bash":                        "allow", // baseline; tighten below
		tools.AskUserQuestionToolName: "allow",
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)

	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })
	return al, msgBus
}

// waitForLLMCallCount polls provider.callCount until it reaches at least
// want, or the deadline elapses. Used by the two-step RED assertion:
// (a) the AskUserQuestion turn parked (provider called at least once
// with AskUserQuestion as a tool call), and (b) the resume turn ran
// (provider called at least twice, with the §0.2 correlated user-role
// message in the second call's prompt).
func waitForLLMCallCount(t *testing.T, p *scriptChatProvider, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.callCount() >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("LLM call count = %d, want >= %d within %s — the through-the-bus path did not drive the expected number of LLM calls",
		p.callCount(), want, timeout)
}

// waitForPendingCard polls the registry until a pending card exists for
// sid, or the deadline elapses. The AskUserQuestion tool runs INSIDE the
// turn — so this only fires AFTER the first LLM call returned the
// AskUserQuestion tool call AND the tool's CreatePending registered the
// set. Both legs must complete before this returns; a green here is the
// minimum pre-condition for any submission test.
func waitForPendingCard(t *testing.T, reg *askuser.Registry, sid string, timeout time.Duration) *askuser.PendingSet {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pending, ok := reg.PendingForSession(sid); ok {
			return pending
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no pending AskUserQuestion card for sid=%q within %s — the parked turn never registered a card", sid, timeout)
	return nil
}

// TestAskUserResume_ThroughBus_ChatTargetAgent is SQUAD-R round 2's
// Phase-1 RED reproduction: a chat-target agent (the production
// configuration, NOT a worker — `native-agent` is a worker, and the
// brief's primary suspect hypothesis, H1, requires a real chat target
// to even be reachable via the explicit-agent_id fast-path in
// pkg/agent/loop_inbound.go::resolveMessageRoute) takes an
// AskUserQuestion door on a real running sessionWorker, parks, and the
// human-submitted answer lands on the SAME bus the loop's Run
// goroutine is consuming — the EXACT production shape issue #760 hits
// on the founder's install every time. The assertion is the
// live-resolve round-trip: the SECOND LLM call MUST happen on the
// chat target's running session, with the §0.2 correlated user-role
// message in its prompt.
//
// If this is RED, #760 is reproduced at the Go layer and Phase 2
// (root-cause + fix) opens with a real, file::symbol-anchored defect
// to fix. If this is GREEN, the through-the-bus chat-target path is
// healthy at this layer — the brief explicitly says to TIGHTEN the
// harness, not weaken the assertion: try SPA layer per the round-1
// fallback, or look at the parked-turn-cleanup-vs-resume-message
// arrival race the brief flags as an open hypothesis (the
// sessionWorker steering-queue path in pkg/agent/session_worker.go's
// enqueue/inTurn logic, specifically the "inTurn was still true at
// the moment the resume message arrived" window).
func TestAskUserResume_ThroughBus_ChatTargetAgent(t *testing.T) {
	provider := &scriptChatProvider{
		scripted: []*providers.LLMResponse{
			// Call 1: the AskUserQuestion door — parks the turn.
			e2eToolCallResponse("call_ask", tools.AskUserQuestionToolName,
				`{"questions":[{"header":"Scope","question":"Single or multiplayer?",`+
					`"options":[{"label":"Single","description":"one player"},`+
					`{"label":"Multi","description":"two players"}]}]}`),
			// Call 2 (the resume turn the field defect never reaches):
			// a plain text reply — proves the resumed turn ran and the
			// agent saw the answer, end-to-end through the bus.
			e2eTextResponse("Got the answer, continuing work."),
		},
		fallback: "unexpected extra LLM call after the resume turn completed",
	}

	al, msgBus := newChatTargetAskUserLoop(t, provider)

	// Wire the AskUserQuestion registry with the through-the-bus
	// dispatcher — the production-equivalent path (registry +
	// real-bus PublishInbound), not the test-only synchronous shim the
	// existing goal-flow tests use. The chat-target agent's own
	// AskUserQuestion tool will pick this up via registerSharedTools's
	// live-per-call closure (pkg/agent/loop_wire.go:649-651), which
	// looks up al.getAskUserRegistry() each call.
	miaInst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "chat-target agent 'mia' must be registered")
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store not available")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", miaInst.ID)
	require.NoError(t, err)
	sid := meta.ID

	dispatcher := &throughBusResumeDispatcher{msgBus: msgBus}
	reg := askuser.NewRegistry(store, dispatcher, askuser.Options{})
	al.SetAskUserRegistry(reg)

	// Start the loop's Run goroutine — WITHOUT this, the inbound
	// messages we publish sit in the bus buffer forever and the
	// sessionWorker that drives the turn never spawns.
	runCtx, runCancel := context.WithCancel(context.Background())
	t.Cleanup(runCancel)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = al.Run(runCtx)
	}()

	// Drive the user prompt through the bus — exactly how the gateway
	// websocket handler would: Channel="webchat", SessionID set (the
	// transcript session the parked turn registers against), and an
	// explicit agent_id metadata so resolveMessageRoute's fast-path
	// (loop_inbound.go:323-374) targets "mia" without consulting the
	// routing default. The SPA's first message carries the same shape.
	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()
	require.NoError(t, msgBus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel:       "webchat",
		ChatID:        "chat-1",
		SessionID:     sid,
		Content:       "Help me pick a single-player tetris game.",
		Sender:        bus.SenderInfo{CanonicalID: "webchat_user"},
		GatewayUserID: "daniel",
		Metadata:      map[string]string{"agent_id": "mia"},
	}))

	// (a) The AskUserQuestion turn parked — first LLM call returned
	// the AskUserQuestion tool call, the tool ran CreatePending, the
	// registry now has a pending card for sid. This is the
	// pre-condition for any submission test.
	waitForLLMCallCount(t, provider, 1, 5*time.Second)
	pending := waitForPendingCard(t, reg, sid, 5*time.Second)
	require.NotEmpty(t, pending.CardID, "the parked turn must have a card id the SPA can answer")
	require.NotEmpty(t, pending.TranscriptSessionID, "the parked set must carry the transcript session id so the resume targets the same session")

	// (b) Human submits the card answer — the EXACT shape the SPA's
	// sendAskUserAnswer sends (registry.Submit is the server-side
	// equivalent of handleAskUserAnswer's normal — non-cancel — branch
	// in pkg/gateway/ws_ask_user.go:264). First-valid-wins; this is
	// the only submission in the test, so it must succeed.
	require.NoError(t, reg.Submit(
		pending.CardID, sid, "daniel",
		[]askuser.SubmittedAnswer{
			{Header: "Scope", Selected: []string{"Single"}},
		},
	))

	// (c) THE RED ASSERTION: the resume bus message MUST drive a
	// SECOND LLM call on the SAME chat-target session within the
	// timeout. This is the live-resolve round-trip the field defect
	// breaks — the running turn's waiter never resolves, the parked
	// turn dies, and the answer only surfaces on the NEXT user
	// prompt. If the second call never arrives, #760 is reproduced.
	waitForLLMCallCount(t, provider, 2, 5*time.Second)

	// And the second call's prompt MUST carry the §0.2 correlated
	// user-role resume message — that is the live-resolve content,
	// end-to-end. A resumed turn that ran but did not see the answer
	// (e.g. a pathway that re-prompts with the original "pick a game"
	// message instead of the resume) is still a turn-death: the user
	// answered and the agent did not see it, the exact symptom of
	// #760 minus the "answers only surface on next prompt" half.
	msgs, ok := provider.lastMessages(2)
	require.True(t, ok, "second LLM call must have captured a prompt")
	foundResume := false
	for i := range msgs {
		if cardID, ok := askuser.ParseResumeCardID(msgs[i].Content); ok && cardID == pending.CardID {
			foundResume = true
			break
		}
	}
	assert.True(t, foundResume,
		"the resume turn's LLM prompt must carry the §0.2 correlated user-role message "+
			"for card_id=%s — the answer reached the bus, but the running session's next "+
			"turn did not see it; that is the field defect, end-to-end",
		pending.CardID)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// scheduled_turn_live_view_test.go — RED tests for #1442: the live view of a
// scheduled/heartbeat turn misses both the prompt and the answer.
//
// SPEC (brief #1442 + contracts/asyncapi.yaml components.messages.UserMessageFrame
// "Persisted user message echoed to every bound tab"): when a scheduled
// (recurring task or heartbeat) turn runs on session S, a viewer attached to S
// must receive, live and without a reload:
//   - exactly ONE user_message frame carrying the scheduled prompt, published
//     after the transcript append so its id IS the persisted user entry's id;
//   - then the answer's token frames and a terminal done, in that order, with
//     the same message ids the persisted assistant entry carries.
// Controls: an interactive turn on the same harness is unchanged, and a viewer
// of another session receives nothing (no cross-session leak).
//
// ORACLE PROVENANCE — no expected value below was read off the implementation:
//   - frame types/shapes: generated UserMessageFrame/TokenFrame/DoneFrame
//     (pkg/api/generated/asyncapi_types.gen.go — the only legal wire types).
//   - "exactly one user_message, ids == persisted transcript entries": brief
//     #1442 + the contract's echo semantics (a scheduled prompt is a persisted
//     user message — pkg/agent/loop.go::ProcessScheduled appends it).
//   - "token frames then done, in order": brief #1442.
//   - "same ids": the exactly-once live/persisted identity invariant the
//     interactive path is already held to (since_cursor_continuation_stream_test.go).
//   - "streamed, not the plain fallback": token frames can only originate from
//     the streaming pipeline (pkg/gateway/websocket_streamer.go::wsStreamer.Update).
//
// Harness: REAL AgentLoop (full engine + scheduled admission), REAL WSHandler
// + session hub, REAL UnifiedStore on disk, REAL scheduledRunner fire path.
// The only mock is at a process edge: the LLM provider (scripted ChatStream;
// its plain Chat records and serves the same answer so a silent fallback is
// DETECTED, not fatal). The bus stream delegate is wired exactly the way the
// existing webchat streaming tests wire it (msgBus.SetStreamDelegate(handler);
// the channel Manager is a pass-through for webchat to this same WSHandler —
// see newWSHandler's doc comment).
//
// DEFERRED TO CHECK (per RED rules): the green run and the mutation gate
// (channel "" vs "webchat"; a dropped user_message publish; a publish ordered
// before the transcript append; a publish aimed at another session).

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const (
	// schedLivePrompt is the scheduled job's message. Any non-empty string
	// would do; the oracle is frame content == job payload content.
	schedLivePrompt = "scheduled live-view prompt: summarize the queue"
	// schedLiveAnswer is the scripted answer. The oracle is that the live
	// tokens concatenate to exactly this and the transcript holds it once.
	schedLiveAnswer = "Scheduled answer delivered live."
	// schedLiveHeartbeatWorkspace is the heartbeat job's workspace id; the
	// heartbeat main session is computed from it (heartbeatJobName format).
	schedLiveHeartbeatWorkspace = "ws-live-view"
)

// schedLiveViewProvider scripts the LLM edge. ChatStream serves the answer in
// two visible chunks (so token reassembly is real, not a single frame). Plain
// Chat ALSO serves the answer but is counted — the spec's "the answer's token
// frames" means the turn must stream; a non-streaming fallback serving the
// same text is exactly the silent degrade #1442 must not keep.
type schedLiveViewProvider struct {
	mu       sync.Mutex
	streamed int
	plain    int
}

func (*schedLiveViewProvider) GetDefaultModel() string { return "sched-live-view-model" }

func (p *schedLiveViewProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.plain++
	p.mu.Unlock()
	return &providers.LLMResponse{Content: schedLiveAnswer, FinishReason: "stop"}, nil
}

func (p *schedLiveViewProvider) ChatStream(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any, chunk func(string), _ providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.streamed++
	p.mu.Unlock()
	half := len(schedLiveAnswer) / 2
	chunk(schedLiveAnswer[:half])
	chunk(schedLiveAnswer)
	return &providers.LLMResponse{Content: schedLiveAnswer, FinishReason: "stop"}, nil
}

func (p *schedLiveViewProvider) counts() (streamed, plain int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamed, p.plain
}

// schedLiveViewHarness is the shared fixture: real loop, real hub, real
// runner ingredients. Session creation and viewer binding are per-test.
type schedLiveViewHarness struct {
	loop     *agent.AgentLoop
	handler  *WSHandler
	provider *schedLiveViewProvider
	store    *session.UnifiedStore
	events   agent.EventSubscription
}

func newSchedLiveViewHarness(t *testing.T) *schedLiveViewHarness {
	t.Helper()
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{
			Home:              t.TempDir(),
			DefaultModel:      config.DefaultModel{Model: "sched-live-view-model"},
			MaxTokens:         4096,
			MaxToolIterations: 4,
		},
		List: []config.AgentConfig{{ID: "mia"}},
	}}
	cfg.Sandbox.ToolPolicies = config.DefaultToolPolicyCeiling()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &schedLiveViewProvider{}
	loop := mustAgentLoop(t, cfg, msgBus, provider)
	handler := newWSHandler(msgBus, loop, "")
	handler.webchatCh = newWebchatChannel(handler)
	msgBus.SetStreamDelegate(handler)

	events := loop.SubscribeEvents(256)
	t.Cleanup(func() { loop.UnsubscribeEvents(events.ID) })

	runDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-runDone)
	})
	go func() { runDone <- loop.Run(ctx) }()

	return &schedLiveViewHarness{loop: loop, handler: handler, provider: provider, store: loop.GetSessionStore(), events: events}
}

// bindViewer binds a fresh test connection as a live viewer of sessionID's
// session hub — the same binding the production accept loop plus an attach
// produce. Returns the connection whose sendCh is the viewer's socket edge.
func (h *schedLiveViewHarness) bindViewer(t *testing.T, chatID, sessionID string) *wsConn {
	t.Helper()
	conn := makeTestConn()
	conn.sendCh = make(chan []byte, 512)
	t.Cleanup(conn.close)
	bindTestConnToSession(h.handler, chatID, sessionID, conn)
	return conn
}

// newRunner builds the production scheduled fire path around the real loop.
func (h *schedLiveViewHarness) newRunner(t *testing.T) *scheduledRunner {
	t.Helper()
	return newScheduledRunner(
		h.loop,
		fakeChecker{registered: map[string]bool{"mia": true}},
		h.handler.msgBus,
		notifications.NewStore(t.TempDir()),
		h.loop.GetConfig,
	)
}

// waitTurnEnd blocks until the loop reports TurnEnd for sessionID (the same
// event the production hub tap is driven from). No sleeps.
func (h *schedLiveViewHarness) waitTurnEnd(t *testing.T, sessionID string) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-h.events.C:
			if event.Kind != agent.EventKindTurnEnd {
				continue
			}
			if payload, ok := event.Payload.(agent.TurnEndPayload); ok && payload.SessionID == sessionID {
				return
			}
		case <-timer.C:
			t.Fatalf("no TurnEnd event observed for session %q", sessionID)
		}
	}
}

// schedLiveFrameKind classifies one raw queued frame by its wire type.
func schedLiveFrameKind(t *testing.T, raw []byte) string {
	t.Helper()
	var envelope struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(raw, &envelope), "every hub frame must be valid JSON with a type")
	return envelope.Type
}

// assertScheduledTurnLiveView is the shared oracle for a completed scheduled
// turn on sid: exactly one user_message (the prompt, id == persisted user
// entry), then the answer's tokens (message_id == persisted assistant entry),
// then exactly one done — in that order — with no copy on otherViewer.
func assertScheduledTurnLiveView(t *testing.T, h *schedLiveViewHarness, sid string, viewer, otherViewer *wsConn) {
	t.Helper()
	frames := drainAllFrames(viewer.sendCh)

	// Walk the frames IN ARRIVAL ORDER and record each kind's positions.
	var (
		userIndexes  []int
		tokenIndexes []int
		doneIndexes  []int
		tokenTexts   = map[string]string{} // message_id -> concatenated content
	)
	for i, raw := range frames {
		switch schedLiveFrameKind(t, raw) {
		case "user_message":
			userIndexes = append(userIndexes, i)
		case "token":
			tokenIndexes = append(tokenIndexes, i)
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &token))
			require.NotNil(t, token.MessageId, "every token frame must carry a message_id")
			require.Equal(t, sid, token.SessionId, "token frame must stay on the scheduled session")
			tokenTexts[*token.MessageId] += token.Content
		case "done":
			doneIndexes = append(doneIndexes, i)
		}
	}

	// Exactly one user_message, carrying the scheduled prompt.
	require.Len(t, userIndexes, 1, "expected exactly one live user_message frame for the scheduled prompt, got %d — #1442", len(userIndexes))
	var userFrame generated.UserMessageFrame
	require.NoError(t, json.Unmarshal(frames[userIndexes[0]], &userFrame))
	assert.Equal(t, "user_message", userFrame.Type)
	assert.Equal(t, sid, userFrame.SessionId)
	assert.Equal(t, schedLivePrompt, userFrame.Content, "the live user_message must carry the scheduled prompt verbatim")
	require.NotEmpty(t, userFrame.Id, "user_message must carry the persisted entry id")
	assert.Equal(t, "mia", derefStringForTest(userFrame.AgentId), "user_message must attribute the owning agent")

	// The answer streamed as tokens under ONE message id, before the done.
	require.NotEmpty(t, tokenIndexes, "expected live token frames for the scheduled answer, got none — #1442")
	require.Len(t, tokenTexts, 1, "one scheduled answer must stream under exactly one message_id, got %d distinct ids: %v", len(tokenTexts), tokenTexts)
	var answerMessageID string
	for id, text := range tokenTexts {
		answerMessageID = id
		assert.Equal(t, schedLiveAnswer, text, "live tokens must reassemble to the scripted answer exactly once")
	}

	// Exactly one done, after every token.
	require.Len(t, doneIndexes, 1, "expected exactly one live done frame for the scheduled turn, got %d — #1442", len(doneIndexes))
	var doneFrame generated.DoneFrame
	require.NoError(t, json.Unmarshal(frames[doneIndexes[0]], &doneFrame))
	assert.Equal(t, sid, doneFrame.SessionId)
	require.NotNil(t, doneFrame.MessageId, "done must carry the answer's message_id")
	assert.Equal(t, answerMessageID, *doneFrame.MessageId, "done must close the message the tokens opened")

	// Order: user_message < all tokens < done.
	require.Less(t, userIndexes[0], tokenIndexes[0], "user_message must arrive before the answer's tokens")
	require.Less(t, tokenIndexes[len(tokenIndexes)-1], doneIndexes[0], "the done must arrive after every token")

	// Provider edge: the turn must have streamed, not fallen back to plain Chat.
	streamed, plain := h.provider.counts()
	assert.Equal(t, 1, streamed, "the scheduled turn must stream through the streaming pipeline")
	assert.Zero(t, plain, "the non-streaming Chat fallback must not serve the scheduled answer")

	// The live ids are the persisted transcript entries' ids.
	entries, err := h.store.ReadTranscript(sid)
	require.NoError(t, err, "the scheduled session's transcript must be readable")
	var userEntryIDs, assistantEntryIDs []string
	for _, entry := range entries {
		switch entry.Role {
		case "user":
			if entry.Content == schedLivePrompt {
				userEntryIDs = append(userEntryIDs, entry.ID)
			}
		case "assistant":
			if entry.Content == schedLiveAnswer {
				assistantEntryIDs = append(assistantEntryIDs, entry.ID)
			}
		}
	}
	require.Len(t, userEntryIDs, 1, "the prompt must be persisted as exactly one user entry")
	assert.Equal(t, userEntryIDs[0], userFrame.Id, "the live user_message id must be the persisted user entry's id")
	require.Len(t, assistantEntryIDs, 1, "the answer must be persisted as exactly one assistant entry (no double save)")
	assert.Equal(t, assistantEntryIDs[0], answerMessageID, "the live token/done message_id must be the persisted assistant entry's id")

	// Cross-session control: a viewer of another session receives nothing.
	otherFrames := drainAllFrames(otherViewer.sendCh)
	assert.Empty(t, otherFrames, "a viewer of another session must receive none of the scheduled turn's frames (no cross-session leak)")
}

// derefStringForTest dereferences an optional string field, "" when nil.
func derefStringForTest(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// TestScheduledTurnLiveView_RecurringTask_StreamsPromptAndAnswerToSessionHub is
// the recurring-task half of #1442's oracle: a recurring schedule fires into its
// continue session S while a viewer is attached to S — the viewer must see the
// prompt and the live answer. RED today: no user_message, no tokens, no done.
func TestScheduledTurnLiveView_RecurringTask_StreamsPromptAndAnswerToSessionHub(t *testing.T) {
	h := newSchedLiveViewHarness(t)

	sched, err := h.store.NewScheduledSession("mia")
	require.NoError(t, err)
	viewer := h.bindViewer(t, "sched-live-viewer-chat", sched.ID)
	otherMeta, err := h.store.NewScheduledSession("mia")
	require.NoError(t, err)
	otherViewer := h.bindViewer(t, "sched-other-viewer-chat", otherMeta.ID)

	runner := h.newRunner(t)
	job := &cron.CronJob{
		ID:          "j-live-view-recurring",
		Name:        "live-view recurring task",
		AgentID:     "mia",
		CreatedBy:   "tester",
		SessionMode: cron.SessionModeContinue,
		SessionID:   sched.ID,
		Schedule:    cron.CronSchedule{Kind: "every", EveryMS: i64Ptr(60000)},
		Payload:     cron.CronPayload{Message: schedLivePrompt},
	}
	_, err = runner.RunScheduled(context.Background(), job)
	require.NoError(t, err, "the scheduled run itself must succeed — the bug is the missing live view, not the turn")

	assertScheduledTurnLiveView(t, h, sched.ID, viewer, otherViewer)
}

// TestScheduledTurnLiveView_Heartbeat_StreamsPromptAndAnswerToSessionHub is the
// heartbeat half of #1442's oracle: a heartbeat fires into the pair's standing
// main session while a viewer is attached — same live-view contract.
func TestScheduledTurnLiveView_Heartbeat_StreamsPromptAndAnswerToSessionHub(t *testing.T) {
	h := newSchedLiveViewHarness(t)

	main, err := h.store.GetOrCreateMainSession(schedLiveHeartbeatWorkspace, "mia")
	require.NoError(t, err)
	viewer := h.bindViewer(t, "sched-live-viewer-hb", main.ID)
	other, err := h.store.NewScheduledSession("mia")
	require.NoError(t, err)
	otherViewer := h.bindViewer(t, "sched-other-viewer-hb", other.ID)

	runner := h.newRunner(t)
	job := &cron.CronJob{
		ID:          "j-live-view-heartbeat",
		Name:        heartbeatJobName(schedLiveHeartbeatWorkspace, "mia"),
		AgentID:     "mia",
		CreatedBy:   "tester",
		SessionMode: cron.SessionModeMain,
		Payload:     cron.CronPayload{Kind: heartbeatJobKind, Message: schedLivePrompt},
	}
	_, err = runner.RunScheduled(context.Background(), job)
	require.NoError(t, err, "the heartbeat run itself must succeed — the bug is the missing live view, not the turn")

	assertScheduledTurnLiveView(t, h, main.ID, viewer, otherViewer)
}

// TestScheduledTurnLiveView_InteractiveTurnUnchanged is the control: the SAME
// harness, driven through the interactive chat path, already satisfies the
// whole oracle today. It proves the fixture can observe every frame (an empty
// drain is not evidence otherwise) and pins the interactive path against a
// GREEN fix regressing it. Expected GREEN on the pre-change code.
func TestScheduledTurnLiveView_InteractiveTurnUnchanged(t *testing.T) {
	h := newSchedLiveViewHarness(t)

	chat, err := h.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	viewer := h.bindViewer(t, "sched-live-viewer-interactive", chat.ID)

	h.handler.handleChatMessage(context.Background(), "sched-live-interactive-chat", chat.ID, schedLivePrompt, "mia", nil, "", "", viewer)
	h.waitTurnEnd(t, chat.ID)

	frames := drainAllFrames(viewer.sendCh)
	var userIndex, firstTokenIndex, lastTokenIndex, doneIndex = -1, -1, -1, -1
	tokenText := ""
	var answerMessageID string
	for i, raw := range frames {
		switch schedLiveFrameKind(t, raw) {
		case "user_message":
			if userIndex == -1 {
				userIndex = i
				var userFrame generated.UserMessageFrame
				require.NoError(t, json.Unmarshal(raw, &userFrame))
				assert.Equal(t, schedLivePrompt, userFrame.Content)
				assert.Equal(t, chat.ID, userFrame.SessionId)
			}
		case "token":
			if firstTokenIndex == -1 {
				firstTokenIndex = i
			}
			lastTokenIndex = i
			var token generated.TokenFrame
			require.NoError(t, json.Unmarshal(raw, &token))
			require.NotNil(t, token.MessageId)
			answerMessageID = *token.MessageId
			tokenText += token.Content
		case "done":
			if doneIndex == -1 {
				doneIndex = i
			}
		}
	}
	require.NotEqual(t, -1, userIndex, "the interactive turn's user_message must be live")
	require.NotEqual(t, -1, firstTokenIndex, "the interactive turn's tokens must be live")
	require.NotEqual(t, -1, doneIndex, "the interactive turn's done must be live")
	require.Less(t, userIndex, firstTokenIndex, "user_message must precede the tokens")
	require.Less(t, lastTokenIndex, doneIndex, "done must follow the tokens")
	assert.Equal(t, schedLiveAnswer, strings.TrimSpace(tokenText))
	require.NotEmpty(t, answerMessageID)

	entries, err := h.store.ReadTranscript(chat.ID)
	require.NoError(t, err)
	var assistantIDs []string
	for _, entry := range entries {
		if entry.Role == "assistant" && entry.Content == schedLiveAnswer {
			assistantIDs = append(assistantIDs, entry.ID)
		}
	}
	require.Len(t, assistantIDs, 1, "the interactive answer is persisted exactly once")
	assert.Equal(t, assistantIDs[0], answerMessageID, "interactive live ids remain the persisted entry ids")

	streamed, plain := h.provider.counts()
	assert.Equal(t, 1, streamed, "the interactive turn streams (instrument proof)")
	assert.Zero(t, plain, "no non-streaming fallback may serve the interactive control")
}

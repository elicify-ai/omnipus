// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// A-I4 round 6, Priority 2 regression coverage: two delegate-completion
// notifications for the SAME agent, originating from two DIFFERENT chat
// sessions, must never share the in-memory conversation history (or the
// per-scope sessionWorker) that builds the notification turn's LLM prompt.
//
// Before this fix, processSystemMessage (pkg/agent/loop.go) hard-coded
// sessionKey := routing.BuildAgentMainSessionKey(agent.ID) — "agent:<id>:main"
// — for every reconstructed notify-turn, regardless of which real session's
// background work it was reporting on. Since agent.Sessions.GetHistory/
// SetHistory (the store backing the LLM's own prompt) is keyed ONLY by that
// SessionKey, two delegate completions for the same agent (routine — an
// orchestrator commonly runs several concurrent background delegates) shared
// ONE growing history bucket: the second notify-turn's LLM call saw the
// first, unrelated notify-turn's content in its own context. Live-verified:
// a session that only ever delegated to "Ava" received a persisted assistant
// message narrating a nonexistent "delegation to Ray" pulled from an
// entirely different session's exchange.
//
// The fix scopes the notify-turn's SessionKey to
// "agent:<id>:session:<originatingSessionID>" (msg.AsyncTranscriptSessionID)
// — the exact convention every regular routed chat turn already uses via
// agentSessionKey() — so each originating session gets its own isolated
// history bucket, matching regular chat's isolation guarantee.

package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// recordingProviderSawInLastCall reports whether ANY message in the most
// recent Chat() call recorded by a *recordingProvider (loop_test.go) contains
// substr — used to prove what the LLM prompt actually contained, an
// assertion mockProvider (which ignores its input entirely) cannot make.
func recordingProviderSawInLastCall(t *testing.T, p *recordingProvider, substr string) bool {
	t.Helper()
	require.NotEmpty(t, p.lastMessages, "provider must have been called at least once")
	for _, m := range p.lastMessages {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// TestProcessSystemMessage_AsyncResult_SessionIsolation_NoCrossSessionLeak is
// the direct regression test for the A-I4 round 6 Priority 2 finding: two
// delegate-completion notifications for the SAME agent, from two DIFFERENT
// originating sessions, must not blend their content into a shared LLM
// prompt, and must not persist into each other's transcript.
//
// BDD:
//
//	Given one agent that owns TWO separate chat sessions (sessionOne, sessionTwo),
//	  each with its own background delegate that completed,
//	When AsyncNotifier.Notify delivers sessionOne's result and
//	  processSystemMessage reconstructs and runs that notify-turn,
//	  And THEN AsyncNotifier.Notify delivers sessionTwo's result and
//	  processSystemMessage reconstructs and runs THAT notify-turn,
//	Then sessionTwo's notify-turn's LLM prompt does NOT contain sessionOne's
//	  distinctive marker content,
//	  And sessionOne's persisted transcript does NOT contain sessionTwo's
//	  marker content, and vice versa.
func TestProcessSystemMessage_AsyncResult_SessionIsolation_NoCrossSessionLeak(t *testing.T) {
	provider := &recordingProvider{}
	al, msgBus, delegate, _ := newAsyncResultTestLoop(t, provider)

	store := al.GetSessionStore()
	require.NotNil(t, store, "session store must exist")

	metaOne, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session one")
	t.Cleanup(func() { _ = store.DeleteSession(metaOne.ID) })

	metaTwo, err := store.NewSession(session.SessionTypeChat, "webchat", delegate.ID)
	require.NoError(t, err, "create session two")
	t.Cleanup(func() { _ = store.DeleteSession(metaTwo.ID) })
	require.NotEqual(t, metaOne.ID, metaTwo.ID, "test setup: two distinct sessions required")

	const sessionOneMarker = "SESSION-ONE-EXCLUSIVE-MARKER-e8f1c2"
	const sessionTwoMarker = "SESSION-TWO-EXCLUSIVE-MARKER-b4a97d"

	// First notify-turn: sessionOne's delegate result.
	msgOne := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct-one",
		AgentID:             delegate.ID,
		TranscriptSessionID: metaOne.ID,
		SourceKind:          "delegate",
		Content:             sessionOneMarker,
	})
	_, err = al.processSystemMessage(context.Background(), msgOne)
	require.NoError(t, err)
	require.True(t, recordingProviderSawInLastCall(t, provider, sessionOneMarker),
		"sanity: sessionOne's own notify-turn must see its own marker")

	// Second notify-turn: sessionTwo's UNRELATED delegate result, same agent.
	msgTwo := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:             "webchat",
		ChatID:              "direct-two",
		AgentID:             delegate.ID,
		TranscriptSessionID: metaTwo.ID,
		SourceKind:          "delegate",
		Content:             sessionTwoMarker,
	})
	_, err = al.processSystemMessage(context.Background(), msgTwo)
	require.NoError(t, err)

	assert.True(t, recordingProviderSawInLastCall(t, provider, sessionTwoMarker),
		"sessionTwo's own notify-turn must see its own marker")
	assert.False(t, recordingProviderSawInLastCall(t, provider, sessionOneMarker),
		"REGRESSION (A-I4 round 6 Priority 2): sessionTwo's notify-turn LLM prompt must NOT "+
			"contain sessionOne's content — a shared 'agent:<id>:main' SessionKey used to leak "+
			"cross-session history into the prompt")

	// Persisted-transcript isolation: each session's own transcript must
	// only ever mention its own marker, never the other session's.
	oneEntries := readAssistantTranscript(t, store, metaOne.ID)
	require.NotEmpty(t, oneEntries, "sessionOne must have a persisted assistant entry")
	for _, e := range oneEntries {
		assert.NotContains(t, e.Content, sessionTwoMarker,
			"sessionOne's transcript must never contain sessionTwo's marker")
	}

	twoEntries := readAssistantTranscript(t, store, metaTwo.ID)
	require.NotEmpty(t, twoEntries, "sessionTwo must have a persisted assistant entry")
	for _, e := range twoEntries {
		assert.NotContains(t, e.Content, sessionOneMarker,
			"sessionTwo's transcript must never contain sessionOne's marker")
	}
}

// TestProcessSystemMessage_AsyncResult_NoOriginSession_FallsBackToMainKey
// covers the narrower fallback: when the inbound system message carries no
// AsyncTranscriptSessionID at all (no origin session known), the notify-turn
// still falls back to the agent-wide "main" key exactly as it did before this
// fix — there is no session to scope to, so this is not a regression case.
func TestProcessSystemMessage_AsyncResult_NoOriginSession_FallsBackToMainKey(t *testing.T) {
	al, msgBus, _, defaultAgent := newAsyncResultTestLoop(t, &mockProvider{})

	msg := drainNotify(t, al, msgBus, AsyncNotifyEvent{
		Channel:    "webchat",
		ChatID:     "direct",
		SourceKind: "bash",
		Content:    "no TranscriptSessionID on this event",
		// AgentID and TranscriptSessionID deliberately left unset.
	})
	require.Empty(t, msg.AsyncTranscriptSessionID, "test precondition: no origin session on this message")

	_, err := al.processSystemMessage(context.Background(), msg)
	require.NoError(t, err)

	mainKey := "agent:" + defaultAgent.ID + ":main"
	require.NotNil(t, defaultAgent.Sessions)
	// Give the async turn goroutine (if any) a brief moment; runAgentLoop
	// itself is synchronous here so this is just defensive against future
	// changes, mirroring the existing FallsBackToDefault test's spirit.
	time.Sleep(10 * time.Millisecond)
	mainHistory := defaultAgent.Sessions.GetHistory(mainKey)
	assert.NotEmpty(t, mainHistory,
		"with no origin session known, the notify-turn must still use the agent-wide 'main' key %q "+
			"(unchanged fallback behavior — not a regression)", mainKey)
}

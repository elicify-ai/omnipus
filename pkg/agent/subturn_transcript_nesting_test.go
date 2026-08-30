// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// subturn_transcript_nesting_test.go — A-I4 live/reload parity regression,
// live re-verification 2026-07-12.
//
// THE BUG: a delegated child sub-turn shares its parent's
// transcriptSessionID (spawnSubTurn: TranscriptSessionID:
// parentTS.transcriptSessionID, CoreTeam-scoped workspace design — there is
// no separate per-sub-turn transcript file). Every one of the child's OWN
// assistant-text writes (its own intermediate narration between tool-call
// rounds, AND its own final-turn text) therefore lands in the SAME
// transcript.jsonl as the delegator's real top-level messages, with nothing
// distinguishing them from a genuine top-level chat message. LIVE never
// shows this content as a chat bubble — pkg/gateway/websocket.go's
// wsStreamer.Update silently withholds the live TokenFrame for a child
// sub-turn's own streaming (the shadow-stream ownership gate) while still
// fully persisting it via Finalize; the non-streaming
// appendIntermediateAssistantTranscript/appendAssistantTranscript paths
// never had a live-frame counterpart to begin with. Without a persisted
// signal distinguishing these entries, pkg/gateway/replay.go had no way to
// replicate that suppression on reload: it flattened EVERY entry with
// non-empty Content into a top-level replay_message frame, doubling the
// visible bubble count on a multi-step delegation and leaking the
// delegate's raw internal narration/report (plus a model tag/avatar it
// never carried live) as if each were a separate top-level turn.
//
// THE FIX: session.TranscriptEntry gains ParentSpawnCallID — the spawning
// "delegate"/"spawn" ToolCall.ID in the PARENT's own turn, mirroring
// ToolCall.ParentToolCallID's existing correlation pattern for nested tool
// calls. turnState.appendIntermediateAssistantTranscript and
// appendAssistantTranscript now stamp it from ts.parentSpawnCallID (empty
// for a root turn); pkg/gateway/websocket.go's wsStreamer gets the same
// signal via a new SetParentSpawnCallID setter, mirroring SetTurnID's
// established stamping pattern (see turn_stream_identity_stamp_test.go for
// that half). pkg/gateway/replay.go then skips any entry carrying a
// non-empty ParentSpawnCallID entirely — matching live's silent
// suppression — instead of flattening it into a top-level bubble.
//
// THIS TEST proves the fix through the REAL production path: a real
// spawnSubTurn call whose child runs a REAL, multi-step runTurn loop
// (scripted via a fake LLM provider producing 4 narration+tool-call rounds
// plus a final narration-only round — 5 total LLM turns, satisfying the
// "5+ intermediate steps" gap the earlier A-I4 fix rounds' coverage did not
// close) and asserts every one of the child's own persisted assistant-text
// entries carries ParentSpawnCallID equal to the spawning ToolCall.ID, while
// its own tool-call entries keep carrying the PRE-EXISTING
// ParentToolCallID correlation (proving the new field is additive, not a
// replacement).

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestSpawnSubTurn_MultiStepChild_StampsParentSpawnCallIDOnOwnNarration is
// the core regression test for the A-I4 live/reload parity fix.
func TestSpawnSubTurn_MultiStepChild_StampsParentSpawnCallIDOnOwnNarration(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Provider: "mock", Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, &mockProvider{})
	t.Cleanup(al.Close)

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "test setup: a default agent must be registered")
	require.NotEmpty(t, defaultAgent.Model, "test setup: default agent must have a model")

	// A real transcript store, shared between "parent" and "child" exactly as
	// production wires it (spawnSubTurn: TranscriptSessionID:
	// parentTS.transcriptSessionID). ADR-057 FR-005/FR-096 fixture repair: an
	// independent session.NewUnifiedStore rooted at its own directory is a
	// DIFFERENT store instance than the one spawnSubTurn actually validates
	// the parent against (al.GetSessionStore() — subturn.go's
	// sharedStore.CreateSessionWithID call) — a session minted there is
	// invisible to spawnSubTurn, which fails "resolve parent ...: no such
	// file or directory". Use the AgentLoop's own shared store instead.
	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "web", "main")
	require.NoError(t, err)
	sessionID := meta.ID

	parentTS := &turnState{
		ctx:                 context.Background(),
		turnID:              "parent-turn-1",
		depth:               0,
		childTurnIDs:        []string{},
		pendingResults:      make(chan *tools.ToolResult, 4),
		concurrencySem:      make(chan struct{}, 4),
		session:             &ephemeralSessionStore{},
		agent:               defaultAgent,
		transcriptSessionID: sessionID,
		routingSessionID:    session.RoutingSessionID(sessionID),
		transcriptStore:     store,
		agentID:             defaultAgent.ID,
	}

	// 5-round scripted child: 4 rounds of narration+tool-call (drives
	// appendIntermediateAssistantTranscript once per round), then a final
	// narration-only round (drives appendAssistantTranscript). "ToolSearch" is
	// ScopeCore and allowed by default policy resolution with no extra
	// config — mirrors the established raySeq pattern in
	// subturn_delegate_nesting_test.go.
	loadToolArgs := map[string]any{"names": []string{"search_web"}}
	childProvider := newScriptedProvider(
		&providers.LLMResponse{
			Content: "Step 1: let me check what tools I have available.",
			ToolCalls: []providers.ToolCall{
				{ID: "child-call-1", Name: "ToolSearch", Arguments: loadToolArgs},
			},
		},
		&providers.LLMResponse{
			Content: "Step 2: searching for the first source now.",
			ToolCalls: []providers.ToolCall{
				{ID: "child-call-2", Name: "ToolSearch", Arguments: loadToolArgs},
			},
		},
		&providers.LLMResponse{
			Content: "Step 3: found something, digging deeper.",
			ToolCalls: []providers.ToolCall{
				{ID: "child-call-3", Name: "ToolSearch", Arguments: loadToolArgs},
			},
		},
		&providers.LLMResponse{
			Content: "Step 4: cross-checking a second source.",
			ToolCalls: []providers.ToolCall{
				{ID: "child-call-4", Name: "ToolSearch", Arguments: loadToolArgs},
			},
		},
		&providers.LLMResponse{
			Content: "Executive Summary\n\nKey Findings: ...\n\nFinal answer: the research is complete.",
		},
	)
	defaultAgent.mu.Lock()
	defaultAgent.Provider = childProvider
	defaultAgent.mu.Unlock()

	const spawnCallID = "call_research1"
	// Corollary — distinct ids everywhere: the child's own session id is
	// deliberately DIFFERENT from the parent's, pinned via
	// SubTurnConfig.DelegateSessionID so the test can read the exact file the
	// child's writes land in.
	const childSessionID = "child-transcript-nesting-1"
	require.NotEqual(t, sessionID, childSessionID, "parent and child session ids must be distinct")
	ctx := withSpawnToolCallID(context.Background(), spawnCallID)

	result, err := spawnSubTurn(ctx, al, parentTS, SubTurnConfig{
		SystemPrompt:      "Research topic X across multiple sources and report back.",
		Model:             defaultAgent.Model,
		Async:             false,
		DelegateSessionID: childSessionID,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.ForLLM, "Executive Summary",
		"the child's own final turn text must be returned as the sub-turn result")

	// ADR-057 D1 inversion: a delegated child no longer shares the parent's
	// transcriptSessionID — it owns its OWN real, store-backed session
	// (FR-007/FR-009), so its writes land in a SEPARATE transcript.jsonl.
	// "Nesting survives on the span key, not the transcript key": the
	// pre-ADR-057 version of this test read the PARENT's shared transcript
	// (store.ReadTranscript(sessionID)) to find the child's own entries —
	// that file is now permanently empty of them. Correlation back to the
	// spawning tool call is proven instead by ParentSpawnCallID surviving
	// INSIDE the child's own, separate transcript file.
	entries, err := store.ReadTranscript(childSessionID)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the child's OWN transcript must contain its own writes")

	// THE NEW-INVARIANT PROOF (negative space the pre-ADR-057 assertion could
	// never express): the PARENT's shared session must contain NONE of the
	// child's writes — proving the child's transcript is genuinely its own
	// file, not a shared one the old assertion happened to also read from.
	parentEntries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Empty(t, parentEntries,
		"the child's own narration/tool-calls must NOT land in the parent's shared transcript — "+
			"D1 gives every delegated child its own real session")

	var assistantEntries []session.TranscriptEntry
	var toolCallEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" {
			assistantEntries = append(assistantEntries, e)
		}
		for _, tc := range e.ToolCalls {
			if tc.Tool == "ToolSearch" {
				toolCallEntries = append(toolCallEntries, e)
			}
		}
	}

	// 5 LLM rounds -> 5 assistant-role entries: 4 via
	// appendIntermediateAssistantTranscript (narration preceding a tool-call
	// round) + 1 via appendAssistantTranscript (the final, tool-call-free
	// round). This is the "5+ steps" case the earlier A-I4 fix rounds' own
	// coverage (a 3-segment/2-tool-call case, per the delivery report) did
	// not exercise.
	require.Len(t, assistantEntries, 5,
		"expected 4 intermediate narration entries + 1 final entry from the 5-round scripted "+
			"child turn; got %d: %+v", len(assistantEntries), assistantEntries)

	for i, e := range assistantEntries {
		assert.Equal(t, spawnCallID, e.ParentSpawnCallID,
			"assistant entry %d (%q) must carry ParentSpawnCallID == the spawning ToolCall.ID so "+
				"pkg/gateway/replay.go can withhold it from top-level replay, matching live "+
				"rendering's silent suppression of a child sub-turn's own narration — got %q",
			i, e.Content, e.ParentSpawnCallID)
	}

	// The child's own TOOL CALLS keep the pre-existing ParentToolCallID
	// correlation (unaffected by this fix — proves the new field is
	// additive, not a replacement for the existing nested-tool-call path).
	require.Len(t, toolCallEntries, 4)
	for _, e := range toolCallEntries {
		for _, tc := range e.ToolCalls {
			if tc.Tool != "ToolSearch" {
				continue
			}
			assert.Equal(t, spawnCallID, string(tc.ParentToolCallID),
				"child's own tool call must keep carrying ParentToolCallID == the spawning "+
					"ToolCall.ID (pre-existing FR-H-001 nesting, unaffected by this fix)")
		}
	}
}

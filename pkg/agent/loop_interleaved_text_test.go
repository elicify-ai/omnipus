// Package agent — regression test for bug #416.
//
// When an assistant turn interleaves narration text with multiple tool-call
// rounds (text → tool → text → tool → text), only the FINAL text segment was
// persisted to the session transcript.  The first two sentences were dropped.
//
// Fix: appendIntermediateAssistantTranscript is now called in the tool-call
// branch of runTurn — once per iteration that has both Content and ToolCalls —
// before the tool_call entries for that round, so the transcript faithfully
// reflects the order the user saw live.
//
// This test drives the non-streaming (ProcessScheduled) path because it does
// not require a websocket.  The streaming path shares the same fix point in
// loop.go so the invariant holds for both paths.

package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestInterleavedAssistantText_AllSegmentsPersisted reproduces bug #416.
//
// Scenario: the LLM emits 2 interleaved (text + tool_call) rounds, then a
// final plain-text response:
//
//	Round 1: "Got it, saving fact 1." + tool call remember-0
//	Round 2: "Got it, saving fact 2." + tool call remember-1
//	Final:   "All three facts saved!"
//
// Before the fix, the transcript contained only "All three facts saved!" as the
// sole assistant entry.  After the fix it must contain all three text segments
// in order, each appearing BEFORE the tool_call entries it introduced.
func TestInterleavedAssistantText_AllSegmentsPersisted(t *testing.T) {
	// Cannot use t.Parallel() — this test calls t.Setenv (OMNIPUS_HOME).

	// -----------------------------------------------------------------------
	// Arrange: minimal AgentLoop wired to a scripted provider.
	// -----------------------------------------------------------------------
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	provider := testutil.NewScenario().
		// Iteration 1: narration text + tool call. Carries token usage so the
		// turn accumulates real stats (#411 path).
		WithTextAndToolCall("Got it, saving fact 1.", "remember", `{"key":"name","value":"Alex"}`).
		WithUsage(100, 20).
		// Iteration 2: a NO-TEXT tool round (empty Content) — must NOT produce an
		// empty-content assistant entry (covers the content=="" guard in
		// appendIntermediateAssistantTranscript). Also carries usage.
		WithToolCall("remember", `{"key":"color","value":"blue"}`).
		WithUsage(50, 10).
		// Iteration 3: narration text + tool call again.
		WithTextAndToolCall("Got it, saving fact 2.", "remember", `{"key":"pet","value":"cat"}`).
		WithUsage(40, 5).
		// Iteration 4: final text only (no tool calls) — ends the turn.
		WithText("All three facts saved!").
		WithUsage(30, 15)

	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.Defaults.MaxTokens = 4096
	cfg.Agents.Defaults.MaxToolIterations = 10

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })

	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	// Register the agent that will own the scheduled session.
	mia := registerAgent(t, al, home, "mia", provider, true)
	// No-default-policy model (CLAUDE.md hard constraint 6): "remember" is a
	// real builtin tool with zero policy coverage on this bare test config, so
	// it now fails closed to "deny" unless granted explicitly — this test's
	// whole scenario depends on the scripted "remember" tool calls actually
	// executing so the interleaved-transcript ordering can be observed.
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"remember": "allow"},
	})

	// -----------------------------------------------------------------------
	// Arrange: session that carries a transcript store.
	// ProcessScheduled uses GetAgentStore(ownerAgentID) as the transcript store.
	// NewScheduledSession creates the session under the shared store which
	// ProcessScheduled's ResolveSessionStore can find.
	// -----------------------------------------------------------------------
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err, "NewScheduledSession must succeed")
	sessionID := meta.ID
	require.NotEmpty(t, sessionID)

	// -----------------------------------------------------------------------
	// Act: drive a full turn through ProcessScheduled (non-streaming path).
	// -----------------------------------------------------------------------
	reply, err := al.ProcessScheduled(
		context.Background(),
		"mia", sessionID,
		"Save these 3 facts one at a time, write a sentence after each",
		"scheduled", sessionID,
	)
	require.NoError(t, err, "ProcessScheduled must not error")
	assert.Equal(t, "All three facts saved!", reply)

	// -----------------------------------------------------------------------
	// Assert: the transcript must contain all three assistant text segments.
	// -----------------------------------------------------------------------
	// ResolveSessionStore finds the store that owns sessionID.
	store := al.ResolveSessionStore(sessionID)
	require.NotNil(t, store, "ResolveSessionStore must find the session store")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "ReadTranscript must succeed")
	require.NotEmpty(t, entries, "transcript must be non-empty")

	// Collect assistant text entries in order.
	var assistantTexts []string
	for _, e := range entries {
		if e.Role == "assistant" && e.Content != "" {
			assistantTexts = append(assistantTexts, e.Content)
		}
	}

	// Before the fix: assistantTexts == ["All three facts saved!"]  (len=1)
	// After the fix:  assistantTexts == [segment1, segment2, "All three facts saved!"]  (len=3)
	//
	// Note the NO-TEXT tool round (iteration 2) contributes NO assistant entry —
	// the content=="" guard in appendIntermediateAssistantTranscript suppresses
	// it. So we still expect exactly 3 narrated segments.
	assert.Len(t, assistantTexts, 3,
		"all 3 narrated assistant text segments must be persisted; the no-text "+
			"tool round must NOT add an empty-content entry (bug #416 regression); got: %v",
		assistantTexts)

	if len(assistantTexts) == 3 {
		assert.Equal(t, "Got it, saving fact 1.", assistantTexts[0],
			"first segment must be the round-1 narration")
		assert.Equal(t, "Got it, saving fact 2.", assistantTexts[1],
			"second segment must be the round-3 narration")
		assert.Equal(t, "All three facts saved!", assistantTexts[2],
			"third segment must be the final answer")
	}

	// -----------------------------------------------------------------------
	// Assert: no empty-content assistant entry was written for the no-text
	// tool round. Belt-and-suspenders against a regression where an empty
	// narration leaks an assistant bubble.
	// -----------------------------------------------------------------------
	for _, e := range entries {
		if e.Role == "assistant" {
			assert.NotEmpty(t, e.Content,
				"no empty-content assistant entry may be written (no-text tool round must be silent)")
		}
	}

	// -----------------------------------------------------------------------
	// Assert: token/cost attribution (#411 — no double-counting).
	// The two INTERMEDIATE narrated entries must carry Tokens==0 && Cost==0;
	// the FINAL assistant entry carries the whole turn total (sum of every
	// round's usage). Total tokens = (100+20)+(50+10)+(40+5)+(30+15) = 270.
	// -----------------------------------------------------------------------
	const wantTurnTokens = 270
	var assistantEntries []session.TranscriptEntry
	for _, e := range entries {
		if e.Role == "assistant" && e.Content != "" {
			assistantEntries = append(assistantEntries, e)
		}
	}
	require.Len(t, assistantEntries, 3, "expected exactly 3 narrated assistant entries")

	// Intermediate entries (all but the last) carry no usage.
	for i := 0; i < len(assistantEntries)-1; i++ {
		assert.Equal(t, 0, assistantEntries[i].Tokens,
			"intermediate assistant entry %d must have 0 tokens (no double-counting, #411)", i)
		assert.Zero(t, assistantEntries[i].Cost,
			"intermediate assistant entry %d must have 0 cost (no double-counting, #411)", i)
	}

	// Final entry carries the full turn total.
	final := assistantEntries[len(assistantEntries)-1]
	assert.Equal(t, wantTurnTokens, final.Tokens,
		"final assistant entry must carry the full turn token total (#411)")
	assert.Positive(t, final.Cost,
		"final assistant entry must carry a non-zero turn cost (#411)")

	// -----------------------------------------------------------------------
	// Assert: ordering — each text segment must appear BEFORE the tool_call
	// entries that were triggered in the same round.
	//
	// Expected transcript order:
	//   [user]               — (written by processMessage, not checked here)
	//   [assistant text 1]   ← intermediate, round 1
	//   [tool_call round 1]  — remember call for fact 1
	//   [assistant text 2]   ← intermediate, round 2
	//   [tool_call round 2]  — remember call for fact 2
	//   [assistant text 3]   ← final answer
	// -----------------------------------------------------------------------
	type entryKind int
	const (
		kindAssistant entryKind = iota
		kindToolCall
		kindOther
	)
	kind := func(e session.TranscriptEntry) entryKind {
		if e.Role == "assistant" && e.Content != "" {
			return kindAssistant
		}
		if e.Type == session.EntryTypeToolCall || len(e.ToolCalls) > 0 {
			return kindToolCall
		}
		return kindOther
	}

	var sequence []entryKind
	for _, e := range entries {
		k := kind(e)
		if k != kindOther {
			sequence = append(sequence, k)
		}
	}

	// Expected pattern (user entry is kindOther, excluded above):
	//   assistant(text1) → tool(r1) → tool(r2, no-text round) → assistant(text2)
	//   → tool(r3) → assistant(final)
	// The no-text round 2 contributes only a tool entry, no assistant entry.
	expectedPattern := []entryKind{
		kindAssistant, kindToolCall,
		kindToolCall,
		kindAssistant, kindToolCall,
		kindAssistant,
	}
	assert.Equal(t, expectedPattern, sequence,
		"transcript entries must reflect the interleaved order, with the no-text "+
			"round emitting only a tool entry (bug #416)")
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Boundaries of keeping a completed answer when steering continues the turn
// (loop_run_turn_iterations.go::keepAnswerBeforeSteering; UAT-A on bundle
// #1370). Review round 1 on that fix: the natural-exit site and the
// iteration ceiling (pr-test critical gap), an auto-continued answer (CR-1),
// and a tool-stop notice at the natural exit (CR-2).
package agent

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

const boundaryFollowUp = "follow-up queued as the reply finished"

// steerAtNaturalExit queues boundaryFollowUp at the turn's natural-exit
// steering poll on the first `times` visits.
func steerAtNaturalExit(t *testing.T, al *AgentLoop, times int32) {
	t.Helper()
	var visits atomic.Int32
	naturalExitSteeringPollTestHook = func() {
		if visits.Add(1) <= times {
			if err := al.Steer(providers.Message{Role: "user", Content: boundaryFollowUp}); err != nil {
				t.Errorf("steer at natural exit: %v", err)
			}
		}
	}
	t.Cleanup(func() { naturalExitSteeringPollTestHook = nil })
}

func runBoundaryTurn(t *testing.T, al *AgentLoop, inst *AgentInstance, store *session.UnifiedStore, sessionID string) string {
	t.Helper()
	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:          "steer-boundary-" + sessionID,
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "write the answer; I may add something",
		DefaultResponse:     defaultResponse,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)
	return answer
}

func indexOfRole(msgs []providers.Message, role, content string) int {
	for i, m := range msgs {
		if m.Role == role && m.Content == content {
			return i
		}
	}
	return -1
}

// TestSteeringAtNaturalExit_KeepsTheCompletedAnswer: the follow-up reaches
// the natural-exit poll, not the direct-response one. The completed answer is
// still kept: in the next request ahead of the follow-up, and in the saved
// transcript ahead of the follow-up's answer.
func TestSteeringAtNaturalExit_KeepsTheCompletedAnswer(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "FIRST answer", finishReason: "stop"},
		{content: "SECOND answer", finishReason: "stop"},
	}}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	steerAtNaturalExit(t, al, 1)

	answer := runBoundaryTurn(t, al, inst, store, sessionID)
	assert.Equal(t, "SECOND answer", answer)

	require.Len(t, provider.requests, 2)
	first := indexOfRole(provider.requests[1], "assistant", "FIRST answer")
	follow := indexOfRole(provider.requests[1], "user", boundaryFollowUp)
	require.NotEqual(t, -1, first, "the completed answer must be in the next request")
	require.NotEqual(t, -1, follow, "the follow-up must reach the model")
	assert.Less(t, first, follow)

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Equal(t, []string{"FIRST answer", "SECOND answer"}, nonEmpty(assistantTranscriptContents(entries)))
}

// TestSteeringAtIterationCeiling_KeepsTheLastAnswer: at the hard ceiling the
// turn cannot continue for a late follow-up, so the answer it just completed
// must stay the turn's final answer - not be cleared into the tool-limit
// notice.
func TestSteeringAtIterationCeiling_KeepsTheLastAnswer(t *testing.T) {
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: "FIRST answer", finishReason: "stop"},
		{content: "SECOND answer", finishReason: "stop"},
	}}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 1, 0)
	steerAtNaturalExit(t, al, 2) // the second follow-up arrives at the ceiling

	answer := runBoundaryTurn(t, al, inst, store, sessionID)
	assert.Equal(t, "SECOND answer", answer, "the completed answer, not the tool-limit notice")
	assert.Equal(t, 2, provider.calls, "no third call past the ceiling")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Equal(t, []string{"FIRST answer", "SECOND answer"}, nonEmpty(assistantTranscriptContents(entries)))
}

// TestSteeringAtNaturalExit_AfterAutoContinue_DoesNotRepeatTheAnswer (CR-1):
// A is cut off and auto-continued with B; the follow-up arrives at the
// natural exit and is answered with C. The saved answers must read [AB, C]:
// the kept answer must be retired from the continuation accumulator, or C
// is saved as ABC. The model must also see AB once, not the A-plus-continue
// chain as well.
func TestSteeringAtNaturalExit_AfterAutoContinue_DoesNotRepeatTheAnswer(t *testing.T) {
	const a, b, c = "Part A of the answer. ", "Part B finishes it.", "C answers the follow-up."
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: a, finishReason: "truncated"},
		{content: b, finishReason: "stop"},
		{content: c, finishReason: "stop"},
	}}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 10, 0)
	steerAtNaturalExit(t, al, 1)

	answer := runBoundaryTurn(t, al, inst, store, sessionID)
	assert.Equal(t, c, answer)

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Equal(t, []string{a + b, c}, nonEmpty(assistantTranscriptContents(entries)),
		"the follow-up's answer must not repeat the kept answer")
	require.Len(t, provider.requests, 3)
	assert.Equal(t, 1, countMessagesContaining(provider.requests[2], a),
		"the model sees the kept answer once")
}

// TestSteeringDuringAutoContinuedRound_KeepsTheWholeAnswer (CR-1, the
// direct-response site): the follow-up arrives while B, the continuation of
// cut-off A, is being written. The kept answer is AB, not B alone, and the
// follow-up's answer C is not prefixed with A.
func TestSteeringDuringAutoContinuedRound_KeepsTheWholeAnswer(t *testing.T) {
	const a, b, c = "Part A of the answer. ", "Part B finishes it.", "C answers the follow-up."
	var al *AgentLoop
	provider := &truncationScriptedProvider{steps: []truncationScriptStep{
		{content: a, finishReason: "truncated"},
		{content: b, finishReason: "stop", onCall: func() {
			if err := al.Steer(providers.Message{Role: "user", Content: boundaryFollowUp}); err != nil {
				t.Errorf("steer during B: %v", err)
			}
		}},
		{content: c, finishReason: "stop"},
	}}
	var inst *AgentInstance
	var store *session.UnifiedStore
	var sessionID string
	al, inst, store, sessionID = newTruncationTestHarness(t, provider, 10, 0)

	answer := runBoundaryTurn(t, al, inst, store, sessionID)
	assert.Equal(t, c, answer)

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	assert.Equal(t, []string{a + b, c}, nonEmpty(assistantTranscriptContents(entries)))
	require.Len(t, provider.requests, 3)
	assert.Equal(t, 1, countMessagesContaining(provider.requests[2], a))
}

// TestSteeringAtNaturalExit_AfterToolStop_NoticeNotSavedUnderNarrationID
// (CR-2): a tool round's narration is saved under that round's message id;
// the repeat stop then sets a synthetic notice and the follow-up arrives at
// the natural exit. The queued follow-up supersedes the stop (as it does in
// finishToolIteration), so the notice is not an answer to keep. On a
// streamed turn saving it reused the narration's round id (round ids are
// minted only when streaming, loop_run_turn_attempt.go), and replay drops the
// second entry with that id. This harness does not stream, so the decisive
// check is that the notice is not saved at all; the id check guards the
// streamed shape too.
func TestSteeringAtNaturalExit_AfterToolStop_NoticeNotSavedUnderNarrationID(t *testing.T) {
	steps := make([]truncationScriptStep, 0, 9)
	for i := 0; i < 8; i++ {
		steps = append(steps, truncationScriptStep{
			content: "Checking the file again.", finishReason: "tool_calls",
			toolCalls: []providers.ToolCall{truncationEchoCall("call-"+string(rune('a'+i)), "same")},
		})
	}
	steps = append(steps, truncationScriptStep{content: "Answer to the follow-up.", finishReason: "stop"})
	provider := &truncationScriptedProvider{steps: steps}
	al, inst, store, sessionID := newTruncationTestHarness(t, provider, 20, 0)
	registerTruncationEchoTool(al, inst)
	steerAtNaturalExit(t, al, 1)

	answer := runBoundaryTurn(t, al, inst, store, sessionID)
	assert.Equal(t, "Answer to the follow-up.", answer, "the follow-up supersedes the stop and is answered")

	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err)
	seen := map[string]string{}
	for _, e := range entries {
		if e.Role != "assistant" || e.ID == "" {
			continue
		}
		if prev, dup := seen[e.ID]; dup {
			t.Fatalf("two assistant entries share message id %s (%q and %q): replay keeps only the first", e.ID, prev, e.Content)
		}
		seen[e.ID] = e.Content
		assert.NotContains(t, e.Content, "I stopped this turn", "a superseded stop notice is not saved as an answer")
	}
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

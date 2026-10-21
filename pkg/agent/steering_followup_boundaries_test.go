package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The founder's D1/D2 follow-ups to bundle #1370: keeping a partial answer
// must retain its truncation mark, and a dequeued instruction must either be
// answered or remain queued when the hard iteration budget is exhausted.
// These tests leave the conductor, queue, history and transcript stores real.
func TestSteeringFollowUpBoundaries(t *testing.T) {
	t.Run("D1_truncated_answer", testSteeringTruncatedAnswer)
	t.Run("D2_hard_iteration_limit", testSteeringHardIterationLimit)
}

func testSteeringTruncatedAnswer(t *testing.T) {
	const partial = "The partial original answer."
	const reply = "The answer to the follow-up."
	cases := []struct {
		name       string
		steps      []truncationScriptStep
		max        int
		wantAnswer string
	}{
		{
			name: "terminal_partial", max: 1, wantAnswer: partial,
			steps: []truncationScriptStep{{content: partial, finishReason: "truncated"}},
		},
		{
			name: "empty_response_retry", max: 1, wantAnswer: partial,
			steps: []truncationScriptStep{
				{finishReason: "stop"},
				{content: partial, finishReason: "truncated"},
			},
		},
		{
			// Two auto-continuations are the existing ADR-087 bound. The
			// third cut-off round ends with the combined partial answer.
			name: "exhausted_auto_continuation", max: 10, wantAnswer: "Part A. Part B. Part C.",
			steps: []truncationScriptStep{
				{content: "Part A. ", finishReason: "truncated"},
				{content: "Part B. ", finishReason: "truncated"},
				{content: "Part C.", finishReason: "truncated"},
			},
		},
		{
			name: "empty_truncation", max: 1,
			steps: []truncationScriptStep{{finishReason: "truncated"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]truncationScriptStep(nil), tc.steps...)
			steps = append(steps, truncationScriptStep{content: reply, finishReason: "stop"})
			provider := &truncationScriptedProvider{steps: steps}
			al, inst, store, sessionID := newTruncationTestHarness(t, provider, tc.max, 0)
			steerAtNaturalExit(t, al, 1)

			answer := runBoundaryTurn(t, al, inst, store, sessionID)
			assert.Equal(t, reply, answer, "the follow-up must receive its own answer")
			requests := provider.Requests()
			require.Len(t, requests, len(steps), "no unscripted continuation")
			history := inst.Sessions.GetHistory("steer-boundary-" + sessionID)
			if tc.wantAnswer != "" {
				assert.Equal(t, []string{tc.wantAnswer, reply}, followUpBoundaryAnswers(history))
				next := requests[len(requests)-1]
				assert.Equal(t, []string{tc.wantAnswer}, followUpBoundaryAnswers(next),
					"the next request sees the whole partial once, without a stale continuation chain")
				first := indexOfRole(next, "assistant", tc.wantAnswer)
				follow := indexOfRole(next, "user", boundaryFollowUp)
				require.NotEqual(t, -1, first, "partial answer is not dropped from the request")
				require.NotEqual(t, -1, follow, "follow-up reaches the request")
				assert.Less(t, first, follow, "the partial precedes the follow-up")
			}

			entries, err := store.ReadTranscript(sessionID)
			require.NoError(t, err)
			var answers []session.TranscriptEntry
			for _, entry := range entries {
				if entry.Role == "assistant" && (entry.Content != "" || entry.Truncated) {
					answers = append(answers, entry)
				}
			}
			require.Len(t, answers, 2, "keep the truncated reply and the follow-up answer separately")
			assert.Equal(t, tc.wantAnswer, answers[0].Content)
			assert.True(t, answers[0].Truncated, "the partial keeps its existing truncation mark")
			assert.Equal(t, "max_output_tokens", answers[0].TruncationReason)
			assert.Equal(t, reply, answers[1].Content, "no partial prefix duplicated into the follow-up")
			assert.False(t, answers[1].Truncated, "the next answer must not inherit the mark")
			assert.Empty(t, answers[1].TruncationReason)
			assert.NotEqual(t, answers[0].ID, answers[1].ID, "saved replies have distinct identities")
		})
	}
}

func testSteeringHardIterationLimit(t *testing.T) {
	for _, boundary := range []string{"natural_exit", "direct_response", "tool_result"} {
		t.Run(boundary, func(t *testing.T) {
			const first = "FIRST answer"
			const second = "SECOND answer"
			const reply = "Both deferred follow-ups are answered."
			// MaxIterations=1 means the hard ceiling is exactly two calls.
			provider := &truncationScriptedProvider{steps: []truncationScriptStep{
				{content: first, finishReason: "stop"},
				{content: second, finishReason: "stop"},
				{content: reply, finishReason: "stop"},
			}}
			al, inst, store, sessionID := newTruncationTestHarness(t, provider, 1, 0)
			key := "agent:" + inst.ID + ":session:" + sessionID
			wantQueued := []steeringQueueItem{
				{message: providers.Message{Role: "user", Content: "First deferred instruction."},
					correlationID: "deferred-1", transcriptEntryID: "human-entry-1"},
				{message: providers.Message{Role: "user", Content: "Second deferred instruction."},
					correlationID: "deferred-2", transcriptEntryID: "human-entry-2"},
			}
			queueBatch := func() {
				for _, item := range wantQueued {
					require.NoError(t, al.enqueueSteeringItem(key, inst.ID, item))
				}
			}
			if boundary != "natural_exit" {
				provider.steps[1].onCall = queueBatch
			}
			if boundary == "tool_result" {
				provider.steps[1].finishReason = "tool_calls"
				provider.steps[1].toolCalls = []providers.ToolCall{truncationEchoCall("cap-echo", "last-step")}
				registerTruncationEchoTool(al, inst)
			}
			visits := 0
			naturalExitSteeringPollTestHook = func() {
				visits++
				if visits == 1 {
					require.NoError(t, al.Steer(providers.Message{Role: "user", Content: boundaryFollowUp}))
				} else if visits == 2 && boundary == "natural_exit" {
					queueBatch()
				}
			}
			t.Cleanup(func() { naturalExitSteeringPollTestHook = nil })

			answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
				SessionKey: key, Channel: "web", ChatID: sessionID,
				UserMessage: "write the answer; I may add something", DefaultResponse: defaultResponse,
				TranscriptSessionID: sessionID, TranscriptStore: store,
			})
			require.NoError(t, err)
			assert.Equal(t, 2, provider.CallCount(), "no provider call past the hard ceiling")
			if boundary != "tool_result" {
				assert.Equal(t, second, answer, "a completed answer is not replaced with a limit notice")
			} else {
				assert.Equal(t, toolLimitResponse, answer, "reuse the existing notice when there is no final answer")
			}
			al.steering.mu.Lock()
			queued := append([]steeringQueueItem(nil), al.steering.queues[key]...)
			al.steering.mu.Unlock()
			assert.Equal(t, wantQueued, queued, "restore the unprocessed batch in order with its original receipt and entry ids")

			// Drive the same post-turn continuation used by the session worker,
			// with a new iteration budget but the same conversation and history.
			naturalExitSteeringPollTestHook = nil
			continued, err := al.Continue(context.Background(), key, "web", sessionID, "")
			require.NoError(t, err)
			assert.Equal(t, reply, continued, "dequeued follow-ups must be answered, not silently lost")
			assert.Equal(t, 3, provider.CallCount(), "one new turn answers the restored batch")
			assert.Equal(t, 0, al.pendingSteeringCountForScope(key), "batch consumed once")
			requests := provider.Requests()
			require.Len(t, requests, 3)
			history := inst.Sessions.GetHistory(key)
			previous := -1
			for _, item := range wantQueued {
				position := indexOfRole(history, "user", item.message.Content)
				assert.Greater(t, position, previous, "both original instructions reach history in order")
				previous = position
				assert.Equal(t, 1, countMessagesContaining(requests[2], item.message.Content), "each instruction reaches the next request once")
			}
			assert.Equal(t, 1, countMessagesContaining(history, reply), "follow-up answered once in saved history")
			entries, err := store.ReadTranscript(sessionID)
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(strings.Join(assistantTranscriptContents(entries), "\n"), reply),
				"the next turn's answer is saved in the original conversation")
		})
	}
}

func followUpBoundaryAnswers(messages []providers.Message) []string {
	var answers []string
	for _, message := range messages {
		if message.Role == "assistant" && message.Content != "" {
			answers = append(answers, message.Content)
		}
	}
	return answers
}

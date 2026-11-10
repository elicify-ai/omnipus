package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Each positive exercises the actual provider send/retry path. GREEN and
// mutation proof belong to a fresh CHECK instance, not this RED author.
func TestContextOverflowPlain_LocallyFittingRecoveryAndQ33(t *testing.T) {
	const key, trigger, answer = "agent:mia:plain-only", "continue plain history", "plain history recovered"
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), answer)
	al, agent, msgBus := newPlainHistoryLoop(t, p)
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	seedPlainHistory(t, agent, key, history)
	transcriptStore := al.GetSessionStore()
	require.NotNil(t, transcriptStore)
	transcript, createErr := transcriptStore.NewSession(session.SessionTypeChat, "plain Q33", agent.ID)
	require.NoError(t, createErr, "fixture: provision a real transcript so classified diagnostic persistence is exercised")
	var mu sync.Mutex
	var retries []LLMRetryPayload
	var compressions []ContextCompressPayload
	al.SetEventSyncTap(func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		switch e.Kind {
		case EventKindLLMRetry:
			retry, ok := e.Payload.(LLMRetryPayload)
			require.True(t, ok)
			retries = append(retries, retry)
		case EventKindContextCompress:
			compression, ok := e.Payload.(ContextCompressPayload)
			require.True(t, ok)
			compressions = append(compressions, compression)
		}
	})
	response, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey: key, UserMessage: trigger, Channel: "webchat", ChatID: "plain-q33-chat", SendResponse: true, DefaultResponse: defaultResponse,
		TranscriptSessionID: transcript.ID, TranscriptStore: transcriptStore,
	})
	assertPlainHistoryFirstAttempt(t, agent, p, "USER-SOURCE-1-END", "ASSISTANT-SOURCE-1-END", "ASSISTANT-SOURCE-2-END")
	require.NoError(t, err, "FR-030/032: an older completed plain exchange must authorize a strictly smaller retry")
	require.Equal(t, answer, response)
	captures := p.recorded()
	assertPlainHistoryShrink(t, captures, "USER-SOURCE-1-END", "ASSISTANT-SOURCE-1-END")
	require.Equal(t, append(plainHistoryExchange(2), providers.Message{Role: "user", Content: trigger}), plainHistoryBody(t, captures[1]), "keep the newest assistant and current user exactly")
	require.Contains(t, string(captures[1].body), "## Earlier in this conversation", "required breadcrumb framing must actually be sent")
	mu.Lock()
	recordedRetries := append([]LLMRetryPayload(nil), retries...)
	recordedCompressions := append([]ContextCompressPayload(nil), compressions...)
	mu.Unlock()
	require.Len(t, recordedRetries, 1, "real relief emits exactly one retry")
	require.Equal(t, 1, recordedRetries[0].Attempt)
	require.Equal(t, 2, recordedRetries[0].MaxRetries, "§2: existing ceiling is unchanged")
	require.Equal(t, "context_limit", recordedRetries[0].Reason)
	require.Contains(t, recordedRetries[0].Error, "context_length_exceeded")
	require.Len(t, recordedCompressions, 1)
	require.Equal(t, ContextCompressReasonRetry, recordedCompressions[0].Reason)
	require.Equal(t, 2, recordedCompressions[0].DroppedMessages)
	notice := recordedRetries[0].ContextWindowNotice
	require.NotNil(t, notice, "Q33: real progress retains the classified diagnostic for Verbose chat, not an ordinary message")
	require.Equal(t, "context_window_notice", notice.Type)
	require.Equal(t, "provider_retry", notice.Notice.Kind)
	require.Equal(t, "Context window exceeded. Compressing history and retrying...", notice.Notice.Message)
	require.Equal(t, transcript.ID, notice.SessionId)
	require.Equal(t, agent.ID, notice.AgentId)
	require.NotEmpty(t, notice.EntryId)
	require.NotEmpty(t, notice.TurnId)
	require.NotEmpty(t, notice.Timestamp)
	entries, transcriptErr := transcriptStore.ReadTranscript(transcript.ID)
	require.NoError(t, transcriptErr)
	var storedNotices []session.TranscriptEntry
	for _, entry := range entries {
		if entry.Type == session.EntryTypeContextWindowNotice {
			storedNotices = append(storedNotices, entry)
		}
	}
	require.Len(t, storedNotices, 1, "classified diagnostic is durably retained exactly once, independently of browser preference")
	storedFrame, frameErr := storedNotices[0].ContextWindowNoticeFrame(transcript.ID)
	require.NoError(t, frameErr)
	require.Equal(t, *notice, storedFrame, "live and durable/replay classification and original identity are identical")
	require.NotContains(t, string(captures[1].body), notice.Notice.Message, "classified UI diagnostic must not enter model history")
	answers := 0
drain:
	for {
		select {
		case msg := <-msgBus.OutboundChan():
			require.NotContains(t, msg.Content, "Context window exceeded. Compressing history and retrying...", "Q33: no ordinary context-management bubble")
			if msg.Channel == "webchat" && msg.ChatID == "plain-q33-chat" && msg.Content == answer {
				answers++
			}
		default:
			break drain
		}
	}
	require.Equal(t, 1, answers, "Q33: normal recovered answer delivered exactly once")
}

func TestContextOverflowPlain_MixedHistoryUsesOldestEndpoint(t *testing.T) {
	const key, trigger = "agent:mia:plain-mixed", "continue mixed history"
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), "mixed recovered")
	al, agent, _ := newPlainHistoryLoop(t, p)
	retained := make([]providers.Message, 0, 6)
	retained = append(retained,
		providers.Message{Role: "user", Content: "keep this tool exchange"},
		providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{toolCallFor("kept-b", "second-named-first"), toolCallFor("kept-a", "first-named-second")}},
		providers.Message{Role: "tool", ToolCallID: "kept-b", Content: "exact B result Ω"},
		providers.Message{Role: "tool", ToolCallID: "kept-a", Content: "exact A result 漢"},
		providers.Message{Role: "assistant", Content: "newest assistant must survive"},
	)
	history := append(plainHistoryExchange(1), retained...)
	seedPlainHistory(t, agent, key, history)
	response, err := al.runAgentLoop(context.Background(), agent, processOptions{SessionKey: key, UserMessage: trigger})
	assertPlainHistoryFirstAttempt(t, agent, p, "ASSISTANT-SOURCE-1-END", "kept-b", "kept-a")
	require.NoError(t, err)
	require.Equal(t, "mixed recovered", response)
	captures := p.recorded()
	assertPlainHistoryShrink(t, captures, "USER-SOURCE-1-END", "ASSISTANT-SOURCE-1-END")
	require.Equal(t, append(retained, providers.Message{Role: "user", Content: trigger}), plainHistoryBody(t, captures[1]), "FR-030/031: oldest endpoint was plain; later tool calls, arguments, result text and declared order must all remain exact")
}

func TestContextOverflowPlain_ConsecutiveNarrationKeepsNewest(t *testing.T) {
	const key, trigger = "agent:mia:plain-narration", "continue narration"
	p := plainHistoryRejectOnce(errors.New("context_length_exceeded"), "narration recovered")
	al, agent, _ := newPlainHistoryLoop(t, p)
	const newest = "NEWEST-NARRATION-IS-THE-FLOOR"
	history := []providers.Message{
		{Role: "user", Content: strings.Repeat("old initiating source ", 128) + "NARRATION-USER-END"},
		{Role: "assistant", Content: strings.Repeat("older narration one ", 128) + "NARRATION-ONE-END"},
		{Role: "assistant", Content: strings.Repeat("older narration two ", 128) + "NARRATION-TWO-END"},
		{Role: "assistant", Content: newest},
	}
	seedPlainHistory(t, agent, key, history)
	response, err := al.runAgentLoop(context.Background(), agent, processOptions{SessionKey: key, UserMessage: trigger})
	assertPlainHistoryFirstAttempt(t, agent, p, "NARRATION-ONE-END", "NARRATION-TWO-END", newest)
	require.NoError(t, err, "MAJ-CW-005: completed old narration is eligible without sweeping newest assistant")
	require.Equal(t, "narration recovered", response)
	captures := p.recorded()
	assertPlainHistoryShrink(t, captures, "NARRATION-USER-END", "NARRATION-ONE-END")
	// §1 permits, but does not require, attachment of remaining older
	// narration. Send normalization may join that survivor with newest.
	body := plainHistoryBody(t, captures[1])
	require.Len(t, body, 2)
	require.Equal(t, "assistant", body[0].Role)
	require.Contains(t, body[0].Content, newest)
	require.Equal(t, providers.Message{Role: "user", Content: trigger}, body[1])
	retained := agent.Sessions.GetHistory(key)
	require.GreaterOrEqual(t, len(retained), 3)
	require.Equal(t, history[3], retained[len(retained)-3], "original newest assistant survives exactly, independently of send coalescing")
}

func TestContextOverflowPlain_RepeatedRejectionShrinksAndHonorsCeiling(t *testing.T) {
	const key, trigger = "agent:mia:plain-repeat", "continue until real ceiling"
	// §2 preserves the existing allowance: initial send + two retries. Four
	// exchanges leave mutable history at the ceiling, proving it really stops.
	rejections := []error{errors.New("context_length_exceeded rejection-1"), errors.New("context_length_exceeded rejection-2"), errors.New("context_length_exceeded rejection-3")}
	p := &plainHistoryProvider{reply: func(call int) (*providers.LLMResponse, error) {
		if call <= len(rejections) {
			return nil, rejections[call-1]
		}
		return nil, fmt.Errorf("context_length_exceeded UNAUTHORIZED-ATTEMPT-%d", call)
	}}
	al, agent, _ := newPlainHistoryLoop(t, p)
	var history []providers.Message
	for i := 1; i <= 4; i++ {
		history = append(history, plainHistoryExchange(i)...)
	}
	seedPlainHistory(t, agent, key, history)
	_, err := al.runAgentLoop(context.Background(), agent, processOptions{SessionKey: key, UserMessage: trigger})
	assertPlainHistoryFirstAttempt(t, agent, p, "ASSISTANT-SOURCE-1-END", "ASSISTANT-SOURCE-4-END")
	captures := p.recorded()
	require.Len(t, captures, 3, "FR-032: two legal plain-history retries, then the real ceiling; never stop after no plain relief")
	for i := 1; i < len(captures); i++ {
		require.Less(t, len(captures[i].body), len(captures[i-1].body), "every sent retry must strictly shrink including framing")
		require.NotContains(t, string(captures[i].body), fmt.Sprintf("ASSISTANT-SOURCE-%d-END", i))
		require.Contains(t, string(captures[i].body), "ASSISTANT-SOURCE-4-END", "newest assistant is never evicted")
	}
	require.Contains(t, string(captures[2].body), "ASSISTANT-SOURCE-3-END", "reducible source remains: termination is the ceiling, not exhausted history")
	assertPlainHistoryRejection(t, err, rejections[2])
}

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func TestContextOverflowPlain_RecallFirstDropsSpanOnRejectedReassembly(t *testing.T) {
	// Ruling §2 requires recall-first relief. This independent, provisioned
	// control does NOT weaken the legacy retain-recall test: that test's
	// transcript is absent at this base, before its reported marker assertion.
	const answer = "recall control recovered"
	rejection := errors.New("context_length_exceeded recall-first control")
	p := &plainHistoryProvider{reply: func(call int) (*providers.LLMResponse, error) {
		switch call {
		case 1:
			return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{
				ID: "plain-recall-control", Type: "function", Name: "recall_conversation", Arguments: map[string]any{"turn_range": "1-1"},
				Function: &providers.FunctionCall{Name: "recall_conversation", Arguments: `{"turn_range":"1-1"}`},
			}}}, nil
		case 2:
			return nil, rejection
		default:
			return &providers.LLMResponse{Content: answer}, nil
		}
	}}
	turns := [][]providers.Message{plainHistoryExchange(1)}
	for i := 2; i <= 6; i++ {
		turns = append(turns, plainHistoryExchange(i))
	}
	al, agent := recallInjectionFixture(t, p, 100000, 1000, turns)
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, len(turns)*2-2)
	// Create real transcript identity before task execution. The old fixture
	// passes the nonexistent literal chat-recall-3 and fails strict append.
	store := al.GetSessionStore()
	require.NotNil(t, store)
	transcript, err := store.NewScheduledSession(agent.ID)
	require.NoError(t, err)
	require.NotEmpty(t, transcript.ID)
	response, err := al.processTaskDirect(context.Background(), agent.ID, "recall older source", recallInjectionSessionKey, transcript.ID)
	require.NoError(t, err)
	require.Equal(t, answer, response)
	captures := p.recorded()
	require.Len(t, captures, 3, "one recall request, one rejected reassembly, one strictly reduced retry")
	require.Equal(t, 0, countMarkers(captures[0].request.Messages))
	require.Equal(t, 1, countMarkers(captures[1].request.Messages), "positive control: real recall tool injected exactly one span before rejection")
	require.True(t, requestContains(captures[1].request.Messages, "ASSISTANT-SOURCE-1-END"), "positive control: recalled archive bytes really crossed the provider seam")
	require.Equal(t, 0, countMarkers(captures[2].request.Messages), "§2: pressure drops recalled reference before evicting retained plain source")
	require.False(t, requestContains(captures[2].request.Messages, "ASSISTANT-SOURCE-1-END"), "do not reinject a pressure-dropped span on reassembly")
	require.Less(t, len(captures[2].body), len(captures[1].body), "recall-first retry has real net serialized progress")
	for _, marker := range []string{"ASSISTANT-SOURCE-2-END", "ASSISTANT-SOURCE-6-END"} {
		require.True(t, requestContains(captures[2].request.Messages, marker), "recall removal alone should preserve previously retained plain history")
	}
	require.Nil(t, al.activeRecallSpan(recallInjectionSessionKey), "pressure-dropped recall is absent from the session injection slot")
	t.Logf("instrument: recall markers 0 -> 1 -> 0; rejected/retry bytes %d -> %d; transcript provisioned", len(captures[1].body), len(captures[2].body))
}

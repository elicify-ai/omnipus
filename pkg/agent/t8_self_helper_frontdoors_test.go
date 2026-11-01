package agent

// Oracle: r17 21.6 / 21.15. Real helpers, stores, message_parent,
// delegate.respond, steering delivery and list_jobs. Provider I/O only is held.
import (
	"context"
	"encoding/json"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOmnipusSelfDelegate_QuestionAndResponseReachHelper(t *testing.T) {
	stubMemory(t, false, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	child := t8DelegateCall(t, al, parent, "Ask a question and await my answer")
	first := provider.next(t)
	inst, ok := al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	childCtx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithDelegateSessionID(tools.WithTranscriptSessionID(context.Background(), child.SessionId), child.SessionId), "t8-default"), "omnipus")
	const question = "T8: which report should the helper prepare?"
	const answer = "T8: prepare the revenue report"
	const correlation = "t8-self-question-1"
	result := inst.Tools.Execute(childCtx, "message_parent", map[string]any{"kind": "question", "text": question, "correlation_id": correlation})
	require.False(t, result.IsError, "real wired message_parent: %s", result.ForLLM)
	var reply gen.MessageParentResponse
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &reply))
	require.NotNil(t, reply.CorrelationId)
	assert.Equal(t, correlation, *reply.CorrelationId)
	entries, err := al.GetMessageInboxStore().Entries(parent)
	require.NoError(t, err)
	var questions []gen.SessionMessageQuestion
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		q, err := entry.Message.AsSessionMessageQuestion()
		require.NoError(t, err)
		if q.Kind == "question" && q.CorrelationId == correlation {
			questions = append(questions, q)
		}
	}
	require.Len(t, questions, 1, "one genuine upward question")
	assert.Equal(t, child.SessionId, questions[0].SessionId, "chat question names its helper")
	assert.Equal(t, question, questions[0].Text)
	parentCtx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithTranscriptSessionID(context.Background(), parent), "t8-default"), "omnipus")
	result = inst.Tools.Execute(parentCtx, "delegate", map[string]any{"action": "respond", "session_id": child.SessionId, "correlation_id": correlation, "text": answer})
	require.False(t, result.IsError, "parent's real respond: %s", result.ForLLM)
	first.finish()
	resumed := provider.next(t)
	data, err := json.Marshal(resumed.messages)
	require.NoError(t, err)
	assert.Contains(t, string(data), answer, "helper's actual model request receives the answer")
	// The correlation is asserted on the generated question above. r17 21.6
	// requires the answer to reach the helper, not routing metadata in its prompt.
	resumed.finish()
	al.steerAdmission().turns.Wait()
}

func TestOmnipusSelfDelegate_ListJobsShowsBothHelpers(t *testing.T) {
	stubMemory(t, false, true)
	al, provider, parent := t8SelfDelegateFixture(t)
	first := t8DelegateCall(t, al, parent, "first visible helper")
	one := provider.next(t)
	second := t8DelegateCall(t, al, parent, "second visible helper")
	two := provider.next(t)
	inst, ok := al.GetRegistry().GetAgent("omnipus")
	require.True(t, ok)
	ctx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithTranscriptSessionID(context.Background(), parent), "t8-default"), "omnipus")
	result := inst.Tools.Execute(ctx, "list_jobs", map[string]any{"kind": "subagent"})
	require.False(t, result.IsError, "real wired list_jobs: %s", result.ForLLM)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	rows, ok := payload["rows"].([]any)
	require.True(t, ok, "rows must be an array")
	var ids []string
	for _, row := range rows {
		obj, ok := row.(map[string]any)
		require.True(t, ok)
		id, ok := obj["id"].(string)
		require.True(t, ok)
		ids = append(ids, id)
	}
	assert.ElementsMatch(t, []string{first.SessionId, second.SessionId}, ids, "21.15 lists precisely the two actual delegated helpers")
	one.finish()
	two.finish()
	al.steerAdmission().turns.Wait()
}

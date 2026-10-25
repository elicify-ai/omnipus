package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/stretchr/testify/require"
)

// R6 / CHECK mutant M3: a provider-capability rejection, not a size rejection
// that proactive fitting would remove. This therefore reaches the actual
// strip/retry guard even after all normal image fitting is implemented.
type issue1435R2SequenceProvider struct {
	mediaCounts []int
	contents    []string
}

func (*issue1435R2SequenceProvider) GetDefaultModel() string { return "m" }
func (p *issue1435R2SequenceProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	count := 0
	for _, message := range messages {
		count += len(message.Media)
	}
	p.mediaCounts = append(p.mediaCounts, count)
	p.contents = append(p.contents, messages[0].Content)
	if count > 0 {
		return nil, &common.ProviderError{Status: 400, Body: `{"error":{"message":"Model 'm' is not available for image input"}}`}
	}
	return &providers.LLMResponse{Content: "recovered-sequence"}, nil
}

func TestIssue1435R2_TwoSequencesInOneTurnEachStripRetry(t *testing.T) {
	provider := &issue1435R2SequenceProvider{}
	agent := &AgentInstance{ID: "issue-1435-two-sequences", Model: "m", Candidates: []providers.FallbackCandidate{{Provider: "openrouter", Model: "m"}}}
	agent.StoreProviderPool(map[string]providers.LLMProvider{"openrouter": provider})
	fixture := newRouting1340ReviewFixture(t, nil, agent)
	fixture.selectProvider(t)
	// An earlier PDF recovery remains spent: only the IMAGE budget resets.
	fixture.rt.ts.mediaRetryDone.Store(true)
	imageURL := issue1435DataURL(issue1435PNG(t, 32, 32))
	for sequence := 1; sequence <= 2; sequence++ {
		fixture.rf.callMessages = []providers.Message{{Role: "user", Content: "inspect this", Media: []string{imageURL}}}
		fixture.ri.messages = fixture.rf.callMessages
		flow := fixture.rr.callLLMWithRetries() // SAME turnState, real caller owns the reset.
		require.Equal(t, agentLoopRunTurnResponseNext, flow)
		require.NoError(t, fixture.rf.err,
			"#1435 R6: sequence %d must get its own image strip-retry, even after the prior sequence spent the guard", sequence)
		require.Equal(t, "recovered-sequence", fixture.rf.response.Content)
		require.Len(t, provider.mediaCounts, sequence*2, "each sequence has exactly one rejection and one retry, never an unbounded loop")
		require.True(t, fixture.rt.ts.imageRetryDone.Load(), "the current sequence's image retry is now consumed")
		require.True(t, fixture.rt.ts.mediaRetryDone.Load(), "PDF recovery budget must remain consumed across both sequences")
	}
	require.Equal(t, []int{1, 0, 1, 0}, provider.mediaCounts,
		"both sequences must send their original image, then exactly one image-free retry")
	require.Equal(t, "inspect this", provider.contents[0])
	require.Equal(t, "inspect this", provider.contents[2], "sequence 2 must not inherit sequence 1's mutated request")
	require.Contains(t, provider.contents[1], "inspect this")
	require.Contains(t, provider.contents[3], "inspect this")
}

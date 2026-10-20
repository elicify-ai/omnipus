package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

// appendFinalModelResponse preserves signed blocks only for an unmodified single
// provider answer. Synthesized notices and collapsed continuation text do not
// correspond to that signed answer and must never inherit its blocks.
func (ts *turnState) appendFinalModelResponse(content string, response *providers.LLMResponse) error {
	if response == nil || response.Content != content || len(response.ThinkingBlocks) == 0 {
		return ts.agent.Sessions.AddMessage(ts.sessionKey, "assistant", content)
	}
	return ts.agent.Sessions.AddFullMessage(ts.sessionKey, providers.Message{
		Role: "assistant", Content: content,
		ThinkingBlocks: append([]string(nil), response.ThinkingBlocks...),
	})
}

package agent

import "github.com/elicify-ai/omnipus/pkg/providers"

// appendFinalModelResponse preserves signed blocks only for an unmodified single
// provider answer. Synthesized notices and collapsed continuation text do not
// correspond to that signed answer and must never inherit its blocks.
func (ts *turnState) appendFinalModelResponse(content string, response *providers.LLMResponse) error {
	msg := providers.Message{Role: "assistant", Content: content}
	if response != nil && response.Content == content {
		msg.ThinkingBlocks = append([]string(nil), response.ThinkingBlocks...)
	}
	_, err := ts.appendWindowMessage(msg, windowProducerAssistant)
	return err
}

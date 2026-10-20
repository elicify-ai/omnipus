package anthropicprovider

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// signedThinkingBlock reads accumulated DIRECT fields, not the union's stale
// RawJSON/As* view while streaming. SDK request types preserve the provider's
// signature, thinking text and redacted bytes without inventing a new shape.
func signedThinkingBlock(block anthropic.ContentBlockUnion) (string, error) {
	var replay anthropic.ContentBlockParamUnion
	switch block.Type {
	case "thinking":
		param := (anthropic.ThinkingBlock{Thinking: block.Thinking, Signature: block.Signature}).ToParam()
		replay.OfThinking = &param
	case "redacted_thinking":
		param := (anthropic.RedactedThinkingBlock{Data: block.Data}).ToParam()
		replay.OfRedactedThinking = &param
	default:
		return "", fmt.Errorf("unsupported signed thinking block type %q", block.Type)
	}
	encoded, err := json.Marshal(replay)
	if err != nil {
		return "", fmt.Errorf("encode signed thinking block: %w", err)
	}
	return string(encoded), nil
}

func replayThinkingBlocks(rawBlocks []string) ([]anthropic.ContentBlockParamUnion, error) {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(rawBlocks))
	for i, raw := range rawBlocks {
		var block anthropic.ContentBlockUnion
		if err := json.Unmarshal([]byte(raw), &block); err != nil {
			return nil, fmt.Errorf("decode stored thinking block %d: %w", i, err)
		}
		if block.Type != "thinking" && block.Type != "redacted_thinking" {
			return nil, fmt.Errorf("stored thinking block %d has unsupported type %q", i, block.Type)
		}
		blocks = append(blocks, block.ToParam())
	}
	return blocks, nil
}

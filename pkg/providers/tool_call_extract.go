package providers

import (
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// findMatchingBrace finds the index after the closing brace matching the
// opening brace at pos. Returns pos when no matching brace is found.
func findMatchingBrace(text string, pos int) int {
	depth := 0
	for i := pos; i < len(text); i++ {
		switch text[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return pos
}

// extractToolCallsFromText parses tool call JSON from response text.
// CodexCliProvider uses this to extract tool calls that the model outputs
// in its response text.
//
// Returns common.ErrToolArgumentsUndecodable when a call's arguments payload
// is present but will not parse. Text-embedded calls are the likeliest of all
// the paths to be cut off mid-payload — the CLI providers' whole transport is
// the completion text itself — and this site used to hide that behind a
// `_raw` key, spelled differently from the `raw` every other decode site used.
// That divergence is itself the evidence nothing ever consumed either key.
func extractToolCallsFromText(text string) ([]ToolCall, error) {
	start := strings.Index(text, `{"tool_calls"`)
	if start == -1 {
		return nil, nil
	}

	end := findMatchingBrace(text, start)
	if end == start {
		return nil, nil
	}

	jsonStr := text[start:end]

	var wrapper struct {
		ToolCalls []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &wrapper); err != nil {
		slog.Warn("tool_call_extract: failed to parse tool call JSON", "error", err)
		return nil, nil
	}

	var result []ToolCall
	for _, tc := range wrapper.ToolCalls {
		args, err := common.DecodeToolCallArguments(
			json.RawMessage(tc.Function.Arguments), tc.Function.Name,
		)
		if err != nil {
			return nil, err
		}

		result = append(result, ToolCall{
			ID:        tc.ID,
			Type:      tc.Type,
			Name:      tc.Function.Name,
			Arguments: args,
			Function: &FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		})
	}

	return result, nil
}

// stripToolCallsFromText removes tool call JSON from response text.
func stripToolCallsFromText(text string) string {
	start := strings.Index(text, `{"tool_calls"`)
	if start == -1 {
		return text
	}

	end := findMatchingBrace(text, start)
	if end == start {
		return text
	}

	return strings.TrimSpace(text[:start] + text[end:])
}

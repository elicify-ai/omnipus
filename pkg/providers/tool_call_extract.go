package providers

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// findMatchingBrace finds the index after the closing brace matching the
// opening brace at pos. Returns pos when no matching brace is found.
func findMatchingBrace(text string, pos int) int {
	depth := 0
	for i := pos; i < len(text); i++ {
		if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
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
func extractToolCallsFromText(text string) []ToolCall {
	start := strings.Index(text, `{"tool_calls"`)
	if start == -1 {
		return nil
	}

	end := findMatchingBrace(text, start)
	if end == start {
		return nil
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
		return nil
	}

	var result []ToolCall
	for _, tc := range wrapper.ToolCalls {
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			slog.Warn("providers: tool call arguments not valid JSON; preserving raw",
				"tool", tc.Function.Name,
				"error", err,
				"raw", tc.Function.Arguments,
			)
			args = map[string]any{"_raw": tc.Function.Arguments}
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

	return result
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

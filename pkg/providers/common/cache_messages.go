package common

// SerializeCacheMessages adds one ephemeral mark to the last history content,
// before the volatile tail. It operates on freshly serialized values, never the
// stored history. OpenAI-compatible hosts call this only on an allow-listed row.
func SerializeCacheMessages(messages []Message) []any {
	out := SerializeMessages(messages)
	i := len(out) - 1
	if len(messages) > 0 && messages[len(messages)-1].Volatile {
		i--
	}
	if i < 0 {
		return out
	}
	var message map[string]any
	switch m := out[i].(type) {
	case openaiMessage:
		message = map[string]any{"role": m.Role, "content": m.Content}
		if m.ToolCallID != "" {
			message["tool_call_id"] = m.ToolCallID
		}
		if len(m.ToolCalls) > 0 {
			message["tool_calls"] = m.ToolCalls
		}
		if m.ReasoningContent != "" {
			message["reasoning_content"] = m.ReasoningContent
		}
	case map[string]any:
		message = m
	default:
		return out
	}
	mark := map[string]any{"type": "ephemeral"}
	switch content := message["content"].(type) {
	case string:
		message["content"] = []map[string]any{{"type": "text", "text": content, "cache_control": mark}}
	case []map[string]any:
		if len(content) > 0 {
			content[len(content)-1]["cache_control"] = mark
		}
	}
	out[i] = message
	return out
}

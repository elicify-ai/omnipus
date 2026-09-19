package systools

func skillsParameters(update bool) map[string]any {
	omitted := "Omitted defaults to no assigned skills on create."
	if update {
		omitted = "Omitted leaves assigned skills unchanged."
	}
	return map[string]any{
		"type":        "array",
		"description": omitted + " [] clears all assigned skills. Names must be installed skill slugs; unknown names are rejected. External CLI agents reject this field.",
		"items":       map[string]any{"type": "string"},
	}
}

func mcpServersParameters() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Connector assignments. [] clears all bindings. Omitted tools means all tools of that assigned server; [] means none. Names are live remote MCP tool names or public registry names (mcp_<server>_<tool>). Unknown or unavailable names are rejected.",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []any{"id"},
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "Configured MCP server ID"},
				"tools": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Exact live names: remote or public. Omitted = all tools of this server; [] = none.",
				},
			},
		},
	}
}

func toolPolicyChangesParameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Sparse override patch on top of current (or custom-create default) policies. set maps tool→allow|ask|deny; remove lists override names. {} and [] are no-ops. Unknown tools and invalid policy values are rejected.",
		"properties": map[string]any{
			"set": map[string]any{
				"type":                 "object",
				"additionalProperties": map[string]any{"type": "string", "enum": []any{"allow", "ask", "deny"}},
				"description":          "Tool names to set. Unknown catalog tools and values other than allow/ask/deny are rejected.",
			},
			"remove": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Override names to delete. Missing names are a no-op.",
			},
		},
	}
}

func shellPolicyParameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Per-agent shell deny-pattern override. Unknown keys are rejected.",
		"properties": map[string]any{
			"enable_deny_patterns": map[string]any{"type": "boolean"},
			"custom_deny_patterns": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}

func modelParamsParameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "temperature and max_tokens only; top_p and other keys are rejected",
		"properties": map[string]any{
			"temperature": map[string]any{"type": "number"},
			"max_tokens":  map[string]any{"type": "integer"},
		},
	}
}

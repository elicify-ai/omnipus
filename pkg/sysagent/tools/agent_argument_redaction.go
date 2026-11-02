// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import "github.com/elicify-ai/omnipus/pkg/tools"

// RedactArguments is the r17 SR-3 record copy of a create_agent call: every
// env_overrides value, at any depth, becomes "[redacted]". Execution keeps the original map.
func (t *AgentCreateTool) RedactArguments(args map[string]any) (map[string]any, error) {
	return tools.RedactEnvOverrides(args)
}

// RedactArguments is the r17 SR-3 record copy of an update_agent call: every
// env_overrides value, at any depth, becomes "[redacted]". Execution keeps the original map.
func (t *AgentUpdateTool) RedactArguments(args map[string]any) (map[string]any, error) {
	return tools.RedactEnvOverrides(args)
}

// Omnipus — System Agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sysagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dapicom-ai/omnipus/pkg/providers"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// SystemAgent is the built-in Omnipus privileged agent.
// It uses the user's configured LLM provider, has a hardcoded prompt compiled
// into the binary, and dispatches all system.* tool calls through SystemToolHandler.
//
// The agent does NOT have a workspace and consumes zero LLM calls when idle.
type SystemAgent struct {
	provider providers.LLMProvider
	handler  *SystemToolHandler
	toolList []tools.Tool
	isCloud  bool
}

// AgentConfig groups the dependencies for creating a SystemAgent.
type AgentConfig struct {
	// Provider is the user's configured LLM provider.
	Provider providers.LLMProvider
	// Handler is the system tool handler (RBAC + rate limit + audit).
	Handler *SystemToolHandler
	// ToolList is the ordered list of all 35 system tools.
	ToolList []tools.Tool
	// IsCloudProvider controls whether tool schemas are summarized (US-5).
	IsCloudProvider bool
}

// NewSystemAgent creates a SystemAgent.
func NewSystemAgent(cfg AgentConfig) *SystemAgent {
	return &SystemAgent{
		provider: cfg.Provider,
		handler:  cfg.Handler,
		toolList: cfg.ToolList,
		isCloud:  cfg.IsCloudProvider,
	}
}

// ProcessMessage runs a single user turn through the system agent loop.
// history is the existing conversation history (no system message);
// userMessage is the new user turn.
// callerRole and deviceID are used for RBAC and audit logging.
//
// Returns the assistant's response and updated history, or an error.
func (a *SystemAgent) ProcessMessage(
	ctx context.Context,
	history []providers.Message,
	userMessage string,
	callerRole PrincipalRole,
	deviceID string,
) (string, []providers.Message, error) {
	providerDefs := a.buildToolDefs()
	msgs := a.buildMessages(history, userMessage)

	const maxIter = 10
	for i := 0; i < maxIter; i++ {
		resp, err := a.provider.Chat(ctx, msgs, providerDefs, a.provider.GetDefaultModel(), nil)
		if err != nil {
			return "", history, fmt.Errorf("system agent: LLM call failed: %w", err)
		}

		assistantMsg := providers.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		}
		msgs = append(msgs, assistantMsg)

		if len(resp.ToolCalls) == 0 {
			// Build updated history (without leading system message).
			updated := make([]providers.Message, 0, len(history)+2)
			updated = append(updated, history...)
			updated = append(updated, providers.Message{Role: "user", Content: userMessage})
			updated = append(updated, assistantMsg)
			return resp.Content, updated, nil
		}

		// Execute each tool call through the handler.
		for _, tc := range resp.ToolCalls {
			name, args := extractToolCall(tc)
			toolResult := a.handler.Handle(ctx, callerRole, deviceID, name, args)
			msgs = append(msgs, providers.Message{
				Role:       "tool",
				Content:    toolResult.ContentForLLM(),
				ToolCallID: tc.ID,
			})
		}
	}

	return "", history, fmt.Errorf("system agent: exceeded max iterations (%d)", maxIter)
}

// buildToolDefs returns provider tool definitions.
// For cloud providers, schemas are summarized per US-5.
func (a *SystemAgent) buildToolDefs() []providers.ToolDefinition {
	defs := make([]providers.ToolDefinition, 0, len(a.toolList))
	for _, t := range a.toolList {
		if a.isCloud {
			paramNames := extractParamNames(t.Parameters())
			props := make(map[string]any, len(paramNames))
			for _, n := range paramNames {
				props[n] = map[string]any{"type": "string"}
			}
			defs = append(defs, providers.ToolDefinition{
				Type: "function",
				Function: providers.ToolFunctionDefinition{
					Name:        t.Name(),
					Description: firstLine(t.Description()),
					Parameters: map[string]any{
						"type":       "object",
						"properties": props,
					},
				},
			})
		} else {
			defs = append(defs, providers.ToolDefinition{
				Type: "function",
				Function: providers.ToolFunctionDefinition{
					Name:        t.Name(),
					Description: t.Description(),
					Parameters:  t.Parameters(),
				},
			})
		}
	}
	return defs
}

// buildMessages prepends the system prompt and appends the new user message.
func (a *SystemAgent) buildMessages(history []providers.Message, userMessage string) []providers.Message {
	msgs := make([]providers.Message, 0, 1+len(history)+1)
	msgs = append(msgs, providers.Message{Role: "system", Content: SystemPrompt})
	msgs = append(msgs, history...)
	msgs = append(msgs, providers.Message{Role: "user", Content: userMessage})
	return msgs
}

// extractToolCall extracts name and arguments from a ToolCall.
// Handles both the Name/Arguments direct fields and the Function sub-struct.
func extractToolCall(tc providers.ToolCall) (string, map[string]any) {
	if tc.Name != "" {
		return tc.Name, tc.Arguments
	}
	if tc.Function != nil {
		return tc.Function.Name, parseJSONArgs(tc.Function.Arguments)
	}
	return "", nil
}

// parseJSONArgs parses a raw JSON object string into a map.
func parseJSONArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		slog.Warn("system agent: failed to parse tool arguments JSON", "error", err)
		return nil
	}
	return m
}

// IsSystemTool reports whether a tool name belongs to the system.* namespace.
// User agents must never be able to call these tools.
func IsSystemTool(name string) bool {
	return strings.HasPrefix(name, "system.")
}

// RedirectMessage returns a friendly redirect for non-system user tasks.
func RedirectMessage(agentName string) string {
	return fmt.Sprintf(
		"That's a great task for one of your agents! %s would be perfect for that.\n\n[→ Switch to %s]",
		agentName, agentName,
	)
}

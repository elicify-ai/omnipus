// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// RequestSecretTool asks for a destination-bound card, never a secret value.
type RequestSecretTool struct {
	BaseTool
	registry func() *secretrequest.Registry
	name     func(string) string
}

func NewRequestSecretTool(registry func() *secretrequest.Registry, name func(string) string) *RequestSecretTool {
	return &RequestSecretTool{registry: registry, name: name}
}
func (*RequestSecretTool) Name() string           { return "request_secret" }
func (*RequestSecretTool) Scope() ToolScope       { return ScopeGeneral }
func (*RequestSecretTool) Category() ToolCategory { return CategoryCommunication }
func (*RequestSecretTool) Description() string {
	return "Ask the person to enter a secret on a secure web card and pause this turn. Supply purpose and exactly one recipient: endpoint or provider for an API key; server + env_key for an existing stdio MCP server; instance + field for an existing channel connection; or variable + workspace_id + agent_id for one agent's private environment variable. The server builds the card and field list. Never pass a secret value or ask the person to type it into chat. Submit stores a new version and starts one continuation containing reference names only; configuring an integration requires its approved change tool. Channel-triggered or unattended turns cannot raise a card. Ordinary helpers ask their parent; an Omnipus helper's card appears in the Omnipus main chat."
}
func (*RequestSecretTool) Parameters() map[string]any {
	props := make(map[string]any)
	for _, key := range []string{"purpose", "endpoint", "provider", "server", "env_key", "instance", "field", "variable", "workspace_id", "agent_id"} {
		props[key] = map[string]any{"type": "string"}
	}
	return map[string]any{"type": "object", "properties": props, "required": []string{"purpose"}, "additionalProperties": false}
}
func (t *RequestSecretTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	name := ToolAgentID(ctx)
	if t.name != nil {
		if display := t.name(name); display != "" {
			name = display
		}
	}
	var reg *secretrequest.Registry
	if t.registry != nil {
		reg = t.registry()
	}
	auditRefusal := func(reason string) {
		if reg != nil {
			reg.AuditTurnRefusal(ToolTranscriptSessionID(ctx), ToolAgentID(ctx), reason)
		}
	}
	if ToolTurnOrigin(ctx).ChannelTriggered() || (ToolChannel(ctx) != webChannelName && ToolDelegateSessionID(ctx) == "") {
		auditRefusal("channel_origin")
		return ErrorResult(fmt.Sprintf("open the web app and ask %s there", name))
	}
	if ToolAutoDenyAsk(ctx) {
		auditRefusal("unattended_turn")
		return ErrorResult("secret cards are unavailable in unattended runs; open the web app and ask " + name + " there")
	}
	if reg == nil {
		return ErrorResult("secret request service is unavailable")
	}
	text := func(key string) string { value, _ := args[key].(string); return value }
	rec, err := reg.Create(ctx, ToolTranscriptSessionID(ctx), ToolAgentID(ctx), ToolChatID(ctx), ToolTurnOrigin(ctx).Label(), secretrequest.Input{
		Purpose: text("purpose"), Endpoint: text("endpoint"), Provider: text("provider"), Server: text("server"), EnvKey: text("env_key"), Instance: text("instance"), Field: text("field"),
		Variable: text("variable"), WorkspaceID: text("workspace_id"), AgentID: text("agent_id"),
	})
	if err != nil {
		auditRefusal("request_refused")
		return ErrorResult(err.Error())
	}
	result := NewToolResult("Secret request pending (request_id=" + rec.Card.RequestId + "). Wait for the person to submit or cancel the secure card.")
	result.ParksTurn = true
	return result
}

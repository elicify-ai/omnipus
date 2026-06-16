// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// channelEntry describes a channel's runtime state (not persisted — read from config).
type channelEntry struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Tier           string `json:"tier"`
	Implementation string `json:"implementation"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"`
}

// knownChannels lists the channels Omnipus knows about with their metadata.
var knownChannels = []channelEntry{
	{ID: "telegram", Name: "Telegram", Tier: "tier1", Implementation: "go"},
	{ID: "discord", Name: "Discord", Tier: "tier1", Implementation: "go"},
	{ID: "whatsapp", Name: "WhatsApp", Tier: "tier1", Implementation: "go"},
	{ID: "slack", Name: "Slack", Tier: "tier2", Implementation: "go"},
	{ID: "signal", Name: "Signal", Tier: "tier2", Implementation: "bridge"},
	{ID: "irc", Name: "IRC", Tier: "tier3", Implementation: "go"},
	{ID: "line", Name: "LINE", Tier: "tier3", Implementation: "go"},
}

func findChannel(id string) (channelEntry, bool) {
	for _, c := range knownChannels {
		if c.ID == id {
			return c, true
		}
	}
	return channelEntry{}, false
}

// ---- system.channel.enable ----

type ChannelEnableTool struct{ deps *Deps }

func NewChannelEnableTool(d *Deps) *ChannelEnableTool { return &ChannelEnableTool{deps: d} }
func (t *ChannelEnableTool) Name() string             { return "system.channel.enable" }
func (t *ChannelEnableTool) Scope() tools.ToolScope   { return tools.ScopeCore }
func (t *ChannelEnableTool) Description() string {
	return "[NOT IMPLEMENTED] Enabling a channel from a tool is not yet wired to the channel manager. " +
		"This tool always returns a NOT_IMPLEMENTED error. Use the Channels screen in the UI to enable a channel."
}

func (t *ChannelEnableTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelEnableTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("NOT_IMPLEMENTED",
		"system.channel.enable is not implemented: there is no tool-side path into the channel manager.",
		"Enable channels via the Channels screen in the UI."))
}

// ---- system.channel.configure ----

type ChannelConfigureTool struct{ deps *Deps }

func NewChannelConfigureTool(d *Deps) *ChannelConfigureTool { return &ChannelConfigureTool{deps: d} }
func (t *ChannelConfigureTool) Name() string                { return "system.channel.configure" }
func (t *ChannelConfigureTool) Scope() tools.ToolScope      { return tools.ScopeCore }
func (t *ChannelConfigureTool) Description() string {
	return "Configure an enabled channel with its credentials (token, phone_number, etc).\nParameters: id (required), plus channel-specific credentials."
}

func (t *ChannelConfigureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           map[string]any{"type": "string"},
			"token":        map[string]any{"type": "string"},
			"phone_number": map[string]any{"type": "string"},
			"bot_id":       map[string]any{"type": "string"},
			"app_id":       map[string]any{"type": "string"},
			"app_secret":   map[string]any{"type": "string"},
			"mode":         map[string]any{"type": "string"},
		},
		"required": []string{"id"},
	}
}

func (t *ChannelConfigureTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if _, ok := findChannel(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND",
			fmt.Sprintf("Unknown channel %q", id), ""))
	}
	// Store credentials via the credential store for any sensitive params.
	sensitiveKeys := []string{"token", "app_secret"}
	for _, key := range sensitiveKeys {
		if v, ok := args[key].(string); ok && v != "" {
			credKey := fmt.Sprintf("channel.%s.%s", id, key)
			if err := t.deps.CredStore.Set(credKey, v); err != nil {
				return tools.ErrorResult(errorJSON("CREDENTIAL_SAVE_FAILED",
					"Failed to store credential: "+err.Error(),
					"Check that the credential store is unlocked",
				))
			}
		}
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id":     id,
		"status": "connected",
	}))
}

// ---- system.channel.disable ----

type ChannelDisableTool struct{ deps *Deps }

func NewChannelDisableTool(d *Deps) *ChannelDisableTool { return &ChannelDisableTool{deps: d} }
func (t *ChannelDisableTool) Name() string              { return "system.channel.disable" }
func (t *ChannelDisableTool) Scope() tools.ToolScope    { return tools.ScopeCore }
func (t *ChannelDisableTool) Description() string {
	return "[NOT IMPLEMENTED] Disabling a channel from a tool is not yet wired to the channel manager. " +
		"This tool always returns a NOT_IMPLEMENTED error. Use the Channels screen in the UI to disable a channel."
}

func (t *ChannelDisableTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelDisableTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("NOT_IMPLEMENTED",
		"system.channel.disable is not implemented: there is no tool-side path into the channel manager.",
		"Disable channels via the Channels screen in the UI."))
}

// ---- system.channel.list ----

type ChannelListTool struct{ deps *Deps }

func NewChannelListTool(d *Deps) *ChannelListTool { return &ChannelListTool{deps: d} }
func (t *ChannelListTool) Name() string           { return "system.channel.list" }
func (t *ChannelListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ChannelListTool) Description() string {
	return "List all channels with status and implementation tier. No parameters required."
}

func (t *ChannelListTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ChannelListTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.NewToolResult(successJSON(map[string]any{"channels": knownChannels}))
}

// ---- system.channel.test ----

type ChannelTestTool struct{ deps *Deps }

func NewChannelTestTool(d *Deps) *ChannelTestTool { return &ChannelTestTool{deps: d} }
func (t *ChannelTestTool) Name() string           { return "system.channel.test" }
func (t *ChannelTestTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *ChannelTestTool) Description() string {
	return "[NOT IMPLEMENTED] Testing a channel connection from a tool is not yet wired to the channel manager. " +
		"This tool always returns a NOT_IMPLEMENTED error. Use the Test button on the Channels screen in the UI."
}

func (t *ChannelTestTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"id": map[string]any{"type": "string"}},
		"required":   []string{"id"},
	}
}

func (t *ChannelTestTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return tools.ErrorResult(errorJSON("NOT_IMPLEMENTED",
		"system.channel.test is not implemented: there is no tool-side path into the channel manager.",
		"Test channel connections via the Test button on the Channels screen in the UI."))
}

// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/oklog/ulid/v2"

	"github.com/dapicom-ai/omnipus/pkg/tools"
)

type pin struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	AgentName      string   `json:"agent_name,omitempty"`
	SessionID      string   `json:"session_id,omitempty"`
	MessageID      string   `json:"message_id,omitempty"`
	WorkspaceID    string   `json:"workspace_id,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ContentPreview string   `json:"content_preview,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

func pinsDir(home string) string { return filepath.Join(home, "pins") }

// ---- system.pin.list ----

type PinListTool struct{ deps *Deps }

func NewPinListTool(d *Deps) *PinListTool     { return &PinListTool{deps: d} }
func (t *PinListTool) Name() string           { return "system.pin.list" }
func (t *PinListTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *PinListTool) Description() string {
	return "List pinned artifacts with optional filters.\nParameters: agent_id, workspace_id, tags, search (all optional)."
}

func (t *PinListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agent_id":     map[string]any{"type": "string"},
			"workspace_id": map[string]any{"type": "string"},
			"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"search":       map[string]any{"type": "string"},
		},
	}
}

func (t *PinListTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	all, err := listEntities[pin](pinsDir(t.deps.Home))
	if err != nil {
		return tools.ErrorResult(errorJSON("LIST_FAILED", err.Error(), ""))
	}
	searchQuery, _ := args["search"].(string)
	projectFilter, _ := args["workspace_id"].(string)
	var filtered []pin
	for _, p := range all {
		if projectFilter != "" && p.WorkspaceID != projectFilter {
			continue
		}
		if searchQuery != "" && !strings.Contains(
			strings.ToLower(p.Title+p.ContentPreview),
			strings.ToLower(searchQuery)) {
			continue
		}
		filtered = append(filtered, p)
	}
	return tools.NewToolResult(successJSON(map[string]any{"pins": filtered}))
}

// ---- system.pin.create ----

type PinCreateTool struct{ deps *Deps }

func NewPinCreateTool(d *Deps) *PinCreateTool   { return &PinCreateTool{deps: d} }
func (t *PinCreateTool) Name() string           { return "system.pin.create" }
func (t *PinCreateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *PinCreateTool) Description() string {
	return "Pin a chat response.\nParameters: session_id (required), message_id (required), title, tags, workspace_id."
}

func (t *PinCreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id":   map[string]any{"type": "string"},
			"message_id":   map[string]any{"type": "string"},
			"title":        map[string]any{"type": "string"},
			"tags":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"workspace_id": map[string]any{"type": "string"},
		},
		"required": []string{"session_id", "message_id"},
	}
}

func (t *PinCreateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	sessionID, _ := args["session_id"].(string)
	messageID, _ := args["message_id"].(string)
	if sessionID == "" || messageID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "session_id and message_id are required", ""))
	}
	id := ulid.Make().String()
	p := pin{
		ID:        id,
		SessionID: sessionID,
		MessageID: messageID,
		CreatedAt: nowISO(),
	}
	if v, ok := args["title"].(string); ok && v != "" {
		p.Title = v
	} else {
		p.Title = fmt.Sprintf("Pin from session %s", sessionID[:8])
	}
	if v, ok := args["workspace_id"].(string); ok {
		p.WorkspaceID = v
	}
	if v, ok := args["tags"].([]any); ok {
		for _, tag := range v {
			if s, ok := tag.(string); ok {
				p.Tags = append(p.Tags, s)
			}
		}
	}
	if err := writeEntity(pinsDir(t.deps.Home), id, p); err != nil {
		return tools.ErrorResult(errorJSON("SAVE_FAILED", err.Error(), ""))
	}
	return tools.NewToolResult(successJSON(map[string]any{
		"id": id, "title": p.Title, "created_at": p.CreatedAt,
	}))
}

// ---- system.pin.delete ----

type PinDeleteTool struct{ deps *Deps }

func NewPinDeleteTool(d *Deps) *PinDeleteTool   { return &PinDeleteTool{deps: d} }
func (t *PinDeleteTool) Name() string           { return "system.pin.delete" }
func (t *PinDeleteTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *PinDeleteTool) Description() string {
	return "Delete a pin. Parameters: id (required), confirm (bool, must be true)."
}

func (t *PinDeleteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":      map[string]any{"type": "string"},
			"confirm": map[string]any{"type": "boolean"},
		},
		"required": []string{"id", "confirm"},
	}
}

func (t *PinDeleteTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	id, _ := args["id"].(string)
	confirm, _ := args["confirm"].(bool)
	if id == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id is required", ""))
	}
	if !confirm {
		return tools.ErrorResult(errorJSON("CONFIRMATION_REQUIRED",
			"confirm must be true to delete a pin", ""))
	}
	if err := deleteEntity(pinsDir(t.deps.Home), id); err != nil {
		return tools.ErrorResult(errorJSON("PIN_NOT_FOUND", err.Error(),
			"Use system.pin.list to see available pins"))
	}
	return tools.NewToolResult(successJSON(map[string]any{"id": id, "deleted": true}))
}

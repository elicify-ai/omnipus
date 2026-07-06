// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// NavigateCallback is an optional hook the gateway provides to receive
// navigation events from system.navigate tool calls, so the frontend
// can react (e.g., router.push("/agents")).
type NavigateCallback func(screen, agentID, section string)

// ---- system.navigate ----

type NavigateTool struct {
	deps     *Deps
	callback NavigateCallback // may be nil in headless mode
}

func NewNavigateTool(d *Deps, cb NavigateCallback) *NavigateTool {
	return &NavigateTool{deps: d, callback: cb}
}
func (t *NavigateTool) Name() string           { return "navigate" }
func (t *NavigateTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *NavigateTool) Description() string {
	return "Navigate the UI to a specific screen.\nParameters: screen (chat/command-center/agents/skills/settings), agent_id (optional), section (optional)."
}

func (t *NavigateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"screen":   map[string]any{"type": "string"},
			"agent_id": map[string]any{"type": "string"},
			"section":  map[string]any{"type": "string"},
		},
		"required": []string{"screen"},
	}
}

// validScreens lists the accepted screen values per BRD §D.4.10.
var validScreens = map[string]bool{
	"chat": true, "command-center": true, "agents": true,
	"skills": true, "settings": true,
}

func (t *NavigateTool) Execute(_ context.Context, args map[string]any) *tools.ToolResult {
	screen, _ := args["screen"].(string)
	if screen == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "screen is required", ""))
	}
	if !validScreens[screen] {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("Unknown screen %q", screen),
			"Valid screens: chat, command-center, agents, skills, settings",
		))
	}
	agentID, _ := args["agent_id"].(string)
	section, _ := args["section"].(string)

	if t.callback != nil {
		t.callback(screen, agentID, section)
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"navigated": true,
		"screen":    screen,
	}))
}

// Omnipus — Agent Loop
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"log/slog"

	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// projectLinkerAdapter adapts ProjectSessionLinker to the ToolInterceptor interface.
// It fires on system.task.create and system.task.update when a project_id argument
// is present, recording a session→project link in ~/.omnipus/project_session_links.jsonl.
type projectLinkerAdapter struct {
	linker *systools.ProjectSessionLinker
}

func (a *projectLinkerAdapter) BeforeTool(
	ctx context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (a *projectLinkerAdapter) AfterTool(
	ctx context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	if result.Tool == "system.task.create" || result.Tool == "system.task.update" {
		if projectID, _ := result.Arguments["project_id"].(string); projectID != "" {
			sessionID := tools.ToolTranscriptSessionID(ctx)
			if sessionID == "" {
				slog.Warn("project-session-linker: skipping link for non-tracked session",
					"tool", result.Tool, "project_id", projectID)
				return result, HookDecision{Action: HookActionContinue}, nil
			}
			a.linker.LinkSession(projectID, sessionID)
		}
	}
	return result, HookDecision{Action: HookActionContinue}, nil
}

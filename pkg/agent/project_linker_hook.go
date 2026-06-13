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

// workspaceLinkerAdapter adapts ProjectSessionLinker to the ToolInterceptor interface.
// It fires on system.task.create and system.task.update when a workspace_id argument
// is present, recording a session→workspace link in ~/.omnipus/project_session_links.jsonl.
type workspaceLinkerAdapter struct {
	linker *systools.ProjectSessionLinker
}

func (a *workspaceLinkerAdapter) BeforeTool(
	ctx context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (a *workspaceLinkerAdapter) AfterTool(
	ctx context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	if result.Tool == "system.task.create" || result.Tool == "system.task.update" {
		if workspaceID, _ := result.Arguments["workspace_id"].(string); workspaceID != "" {
			sessionID := tools.ToolTranscriptSessionID(ctx)
			if sessionID == "" {
				slog.Warn("workspace-session-linker: skipping link for non-tracked session",
					"tool", result.Tool, "workspace_id", workspaceID)
				return result, HookDecision{Action: HookActionContinue}, nil
			}
			a.linker.LinkSession(workspaceID, sessionID)
		}
	}
	return result, HookDecision{Action: HookActionContinue}, nil
}

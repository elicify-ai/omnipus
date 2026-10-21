// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Target workspace for setup verbs (omnipus-system-agent FR-040–FR-043;
// argument shape ruled in WU3-TOOL-SHAPE-RULINGS-20261014 Q1).
//
// create_agent, create_skill, edit_skill, remove_skill and list_skills accept
// an optional workspace_id. Omitted, it is the turn's contextual workspace
// (tools.ToolWorkspaceID) — except create_skill, where omitting it creates an
// installation-wide user skill (chief ruling 2026-10-15 on WU-3 Q1: the only
// path to a global skill). Supplied, it is validated BEFORE any write: the id
// grammar, the workspace record's existence, and — for a workspace other than
// the caller's own — the cross-workspace setup authority, which only Omnipus
// holds (FR-006). Resolving a target never changes the caller's session
// workspace: the value is used for this one call's destination only.
const workspaceIDParam = "workspace_id"

// workspaceIDProperty is the shared JSON-schema entry for the argument.
func workspaceIDProperty(what string) map[string]any {
	return map[string]any{
		"type": "string",
		"description": "Optional. The workspace " + what + "; omit it to use the workspace of the current " +
			"conversation. Only Omnipus may name a workspace other than its own.",
	}
}

// targetWorkspace is a resolved destination.
type targetWorkspace struct {
	// ID is the workspace id, "" when there is no explicit target and the turn
	// has no workspace context.
	ID string
	// Explicit is true when the caller supplied workspace_id.
	Explicit bool
}

// resolveTargetWorkspace returns the destination workspace for one setup verb.
// A refusal (bad grammar, unknown or unreadable workspace, missing authority)
// is returned before any side effect, so it renders as "failed: nothing saved".
func resolveTargetWorkspace(d *Deps, ctx context.Context, args map[string]any) (targetWorkspace, *tools.ToolResult) {
	contextual := strings.TrimSpace(tools.ToolWorkspaceID(ctx))
	raw, present := args[workspaceIDParam]
	if !present || raw == nil {
		return targetWorkspace{ID: contextual}, nil
	}
	s, ok := raw.(string)
	if !ok {
		return targetWorkspace{}, tools.ErrorResult(errorJSON("INVALID_INPUT",
			workspaceIDParam+" must be a string", "Use the workspace id from list_workspaces"))
	}
	s = strings.TrimSpace(s)
	if s == "" {
		// An explicit empty target is refused, never read as "omitted"
		// (FR-043): omit the argument to use the conversation's workspace.
		return targetWorkspace{}, tools.ErrorResult(errorJSON("INVALID_INPUT",
			workspaceIDParam+" is empty", "Omit workspace_id to use this conversation's workspace, or name one from list_workspaces"))
	}
	if err := validateID(s); err != nil {
		return targetWorkspace{}, tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("%s %q is not a valid workspace id", workspaceIDParam, s),
			"Use the workspace id from list_workspaces"))
	}
	if s != contextual && !coreagent.HasCrossWorkspaceSetupAuthority(tools.ToolAgentID(ctx)) {
		return targetWorkspace{}, tools.ErrorResult(errorJSON("WORKSPACE_NOT_AUTHORIZED",
			fmt.Sprintf("%s %q is not the workspace of this conversation; only Omnipus can act on another workspace",
				workspaceIDParam, s),
			"Ask Omnipus to make changes in another workspace"))
	}
	if d == nil || strings.TrimSpace(d.Home) == "" {
		return targetWorkspace{}, tools.ErrorResult(errorJSON("NOT_AVAILABLE",
			"the data directory is not configured, so the workspace cannot be checked", ""))
	}
	w, err := readWorkspaceFromDisk(d.Home, s)
	if err != nil {
		if strings.HasPrefix(err.Error(), "NOT_FOUND") {
			return targetWorkspace{}, tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND",
				fmt.Sprintf("workspace %q does not exist", s), "Use list_workspaces to see the workspace ids"))
		}
		return targetWorkspace{}, tools.ErrorResult(errorJSON("WORKSPACE_READ_FAILED",
			fmt.Sprintf("workspace %q could not be read: %v", s, err), "Retry, or check the workspace in the app"))
	}
	if strings.EqualFold(strings.TrimSpace(w.Status), "archived") {
		return targetWorkspace{}, tools.ErrorResult(errorJSON("WORKSPACE_NOT_FOUND",
			fmt.Sprintf("workspace %q is archived", s), "Use an active workspace from list_workspaces"))
	}
	return targetWorkspace{ID: s, Explicit: true}, nil
}

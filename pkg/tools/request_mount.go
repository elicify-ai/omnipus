// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// RequestMountTool lets an agent ask for write access to a real folder on the
// operator's machine (ADR-063 FR-7.2).
//
// # Why this is a TOOL rather than a bespoke consent flow
//
// Omnipus already has one consent mechanism: the tool-approval modal, whose
// defaults were designed for exactly this weight of decision — focus lands on
// Deny, Escape denies, nothing auto-approves, and the request expires rather
// than waiting forever. Making a mount request an ordinary tool call means the
// operator sees WHY the agent wants the folder (the surrounding conversation)
// and approves it in the place they already approve everything else. A second,
// parallel consent path would be a second thing to get subtly wrong.
//
// # Always Allow is offered only when a folder path is present
//
// Grants are keyed on (session, agent, tool, exact arguments). Always Allow
// therefore means "this folder, this session" — not "any folder". The modal
// hides the button when host_path (or the path alias) is empty, because there
// is nothing safe to remember. Approving once still creates a mount that
// persists until the operator revokes it.
//
// # What this tool deliberately cannot do
//
// It cannot browse. An agent enumerating the operator's disk to find something
// worth asking for inverts the relationship: the operator decides what exists
// to be granted. The agent names a path it already has reason to believe in
// (from the conversation, from a file it was given) and the operator judges it.
type RequestMountTool struct {
	BaseTool
	// homePath is $OMNIPUS_HOME, needed to enforce the one hard boundary.
	homePath string
}

// NewRequestMountTool builds the tool. It is registered ONCE per agent, like
// every other filesystem tool, and resolves the target workspace from the turn
// context at execution time.
//
// It deliberately does NOT take a workspace: an agent can belong to several
// workspaces, so a workspace captured at registration would be wrong for every
// turn on a different one. An earlier version took it as a constructor
// argument, which made the tool impossible to register at instance
// construction — so it never was, and no agent could call it at all despite it
// appearing in the catalog and in Settings. Resolving from the turn context is
// the same pattern the email tools use (see registerEmailToolsForAgent), and
// it is what makes unconditional registration possible.
func NewRequestMountTool(homePath string) *RequestMountTool {
	return &RequestMountTool{homePath: homePath}
}

func (t *RequestMountTool) Name() string { return "request_mount" }

func (t *RequestMountTool) Description() string {
	return "Ask the operator for write access to a folder on their computer, " +
		"mounted into this workspace. Requires their explicit approval. Use it when a task " +
		"genuinely needs to work in a specific existing folder — name the exact path and say " +
		"why in your message, because that reason is what they are judging. Everything outside " +
		"your workspace is already readable; ask only when you need to WRITE. The mount name is " +
		"derived automatically from the folder's basename (you do not choose it) and appears at " +
		"work/<name>."
}

func (t *RequestMountTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host_path": map[string]any{
				"type": "string",
				"description": "Absolute path of the folder on the operator's computer. " +
					"Must already exist and be a directory.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Alias for host_path. Use host_path when you can.",
			},
			"reason": map[string]any{
				"type": "string",
				"description": "One sentence on what you need to do there. Shown to the " +
					"operator with the request — it is the main thing they weigh.",
			},
		},
		// host_path is NOT listed here even though it (or its alias, path) is
		// always required in practice: this schema validator's "required" is
		// a flat AND with no way to express "host_path OR path", and path is
		// a genuine, accepted alias (see TestRequestMount_AcceptsPathAlias) —
		// listing host_path alone would reject a path-only call before
		// Execute's own alias-aware presence check ever runs. Execute
		// enforces "one of host_path/path must be present" at runtime
		// instead.
		"required": []string{"reason"},
	}
}

// Scope reports ScopeCore: this is never available to a custom agent unless an
// operator's policy grants it explicitly.
func (t *RequestMountTool) Scope() ToolScope { return ScopeCore }

func (t *RequestMountTool) Category() ToolCategory { return CategoryFilesystem }

// Execute performs the mount, having already been approved.
//
// Reaching here means the operator said yes: the approval gate runs before
// dispatch. This still re-checks the target rather than trusting the approved
// arguments, because the check is cheap and the alternative is a path that was
// valid when shown and is not when used.
func (t *RequestMountTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	hostPath, _ := args["host_path"].(string)
	if strings.TrimSpace(hostPath) == "" {
		hostPath, _ = args["path"].(string)
	}
	hostPath = strings.TrimSpace(hostPath)
	if hostPath == "" {
		return ErrorResult("request_mount: host_path is required")
	}
	if !filepath.IsAbs(hostPath) {
		return ErrorResult("request_mount: host_path must be an absolute path")
	}
	// Resolved from the turn, never from a model-supplied parameter, so the
	// model cannot aim a grant at another workspace.
	workspaceID := ToolWorkspaceID(ctx)
	if workspaceID == "" {
		return ErrorResult("request_mount: this turn has no workspace to mount into")
	}

	// The one hard boundary, re-checked against the SAME function the REST
	// endpoint and the folder picker use. A second copy of this rule is a
	// second chance for the three to disagree.
	resolved, warning, err := workspace.CheckMountTarget(hostPath, t.homePath)
	if err != nil {
		return ErrorResult(fmt.Sprintf("request_mount: %v", err))
	}

	name := mountNameFromHostPath(resolved)
	if name == "" {
		return ErrorResult("request_mount: that path has no folder name to mount it under")
	}

	mount, _, err := workspace.CreateMount(t.homePath, workspaceID, name, resolved)
	if err != nil {
		return ErrorResult(fmt.Sprintf("request_mount: %v", err))
	}

	msg := fmt.Sprintf("Mounted %q at work/%s — you can now read and write there.",
		mount.HostPath, mount.Name)
	if warning != "" {
		// A broad grant is allowed but must never be quiet, including in the
		// agent's own view of what it just received.
		msg += "\n\nNote: " + warning
	}
	return NewToolResult(msg)
}

// mountNameFromHostPath derives the mount's single path segment inside work/
// from the folder's own name.
//
// The agent does not choose this name. Letting it pick one invites a name that
// misrepresents what the folder is ("notes" pointing at a source tree), and the
// operator approved a PATH, not a label.
func mountNameFromHostPath(hostPath string) string {
	base := filepath.Base(filepath.Clean(hostPath))
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

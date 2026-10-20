// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// SelectSecretVersionTool is Omnipus's rollback of an agent-private variable
// to an older stored version (FR-086, BDD-6.23). Integration references are
// selected only by their own configuration tools (FR-064, §6.7), never here.
type SelectSecretVersionTool struct {
	deps *Deps
	secretSelectionAudit
}

func NewSelectSecretVersionTool(d *Deps) *SelectSecretVersionTool {
	return &SelectSecretVersionTool{deps: d}
}

func (t *SelectSecretVersionTool) Name() string                 { return "select_secret_version" }
func (t *SelectSecretVersionTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *SelectSecretVersionTool) Category() tools.ToolCategory { return tools.CategoryPlatform }
func (t *SelectSecretVersionTool) ReportsChangeOutcome()        {}

// Description is the prompt author's text (select_secret_version.md), with
// the integration clauses removed per the chief's Q2 A ruling (FR-086).
func (t *SelectSecretVersionTool) Description() string {
	return "Select a stored version of a secret reference as the version in use; the previously selected version becomes superseded, listed and removable. Call it when the person asks to roll an agent's private variable back to an earlier one — a private replacement is always selected on submit on the owner's card, so a private variable only ever moves backwards through this tool. Supply reference_name and version as list_secrets reports them; for a private variable also supply the target workspace and agent. A reference stored for a different recipient is refused with nothing selected — request a fresh card for the current recipient instead; the same applies to a private selection that does not match the owner pair. Values are never an input or an output: reference names and version numbers only. Only Omnipus holds this tool — an agent that wants its own variable rolled back asks Omnipus. The shipped policy shows an approval card before it runs; Omnipus skips approvals in God Mode."
}

func (t *SelectSecretVersionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reference_name": map[string]any{"type": "string", "description": "Base name of the stored secret reference, as list_secrets reports it without the version suffix"},
			"version":        map[string]any{"type": "integer", "description": "Version number to select, as list_secrets reports it; the stored reference name is this base name plus .v and the number. For a private variable it must be older than the version currently selected"},
			"workspace":      map[string]any{"type": "string", "description": "Workspace of the owning pair for a private variable; required together with agent for a private variable"},
			"agent":          map[string]any{"type": "string", "description": "Agent of the owning pair for a private variable; must match the pair the variable was stored for"},
		},
		"required":             []string{"reference_name", "version"},
		"additionalProperties": false,
	}
}

func (t *SelectSecretVersionTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	base, _ := args["reference_name"].(string)
	ws, _ := args["workspace"].(string)
	agentID, _ := args["agent"].(string)
	version, versionOK := integerArg(args["version"])
	base = strings.TrimSpace(base)
	if base == "" || !versionOK || version < 1 {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "reference_name and a version of 1 or more are required", "Read list_secrets for the stored names and versions"))
	}
	if t.deps == nil || t.deps.CredStore == nil {
		return tools.ErrorResult(errorJSON("STORE_UNAVAILABLE", "the secret store is unavailable; nothing was selected", ""))
	}
	ref := fmt.Sprintf("%s.v%d", base, version)
	inventory, err := t.deps.CredStore.CardInventory()
	if err != nil {
		return tools.ErrorResult(errorJSON("STORE_UNAVAILABLE", "the secret store could not be read; nothing was selected", ""))
	}
	target, current, found := privateSelectionTarget(inventory, ref)
	if !found {
		return tools.ErrorResult(errorJSON("NOT_FOUND", "no stored version "+ref+"; nothing was selected", "Read list_secrets for the stored names and versions"))
	}
	recipient := target.Secret.Recipient
	if recipient.Kind != credentials.KindAgentPrivate {
		return tools.ErrorResult(errorJSON("NOT_PRIVATE", "use the integration's configuration tool with that reference; nothing was selected", ""))
	}
	if ws == "" || agentID == "" || ws != recipient.WorkspaceID || agentID != recipient.AgentID {
		return tools.ErrorResult(errorJSON("INVALID_REFERENCE", "this version does not belong to that workspace and agent; nothing was selected", "Use request_secret for the current recipient"))
	}
	if found, member := workspacepkg.FindForAgentPreferring(t.deps.Home, agentID, ws); !member || found != ws {
		return tools.ErrorResult(errorJSON("INVALID_TARGET", "that agent is not a member of that workspace; nothing was selected", ""))
	}
	if current != nil && version >= current.Secret.Version {
		return tools.ErrorResult(errorJSON("NOT_OLDER", fmt.Sprintf("version %d is not older than the selected version %d; nothing was selected", version, current.Secret.Version), "A newer value comes from a new secret card"))
	}
	if _, err := t.deps.CredStore.SelectPrivateVersion(ref); err != nil {
		if errors.Is(err, credentials.ErrCardSecretNotFound) {
			return tools.ErrorResult(errorJSON("NOT_FOUND", "no stored version "+ref+"; nothing was selected", ""))
		}
		var authErr *credentials.EntryAuthError
		var encErr *credentials.EntryEncodingError
		if errors.As(err, &authErr) || errors.As(err, &encErr) {
			return tools.ErrorResult(errorJSON("UNREADABLE",
				"stored version "+ref+" cannot be read; the current selection was kept",
				"Choose another version, or request a new value with request_secret"))
		}
		return tools.ErrorResult(errorJSON("SAVE_FAILED", "the selection could not be saved; nothing was selected", ""))
	}
	t.selected(ctx, t.Name(), recipient.WorkspaceID+"/"+recipient.AgentID+"/"+recipient.Field, []string{ref})
	return changeSuccess(map[string]any{
		"reference": ref, "variable": recipient.Field, "workspace": ws, "agent": agentID,
		"message": "Selected " + ref + "; the agent's next command in that workspace uses it.",
	}, working("the stored selection; the agent's next command reads it", "a command actually using the value"))
}

// privateSelectionTarget finds ref and, for a private variable, the version
// currently selected for the same owner pair and variable.
func privateSelectionTarget(inventory []credentials.CardInventoryEntry, ref string) (target, current *credentials.CardInventoryEntry, found bool) {
	for i := range inventory {
		if inventory[i].Reference == ref {
			target, found = &inventory[i], true
		}
	}
	if !found || target.Secret.Recipient.Kind != credentials.KindAgentPrivate {
		return target, nil, found
	}
	r := target.Secret.Recipient
	for i := range inventory {
		other := inventory[i].Secret.Recipient
		if inventory[i].Selected && other.Kind == credentials.KindAgentPrivate &&
			other.WorkspaceID == r.WorkspaceID && other.AgentID == r.AgentID && other.Field == r.Field {
			current = &inventory[i]
		}
	}
	return target, current, true
}

// integerArg accepts a JSON number that is a whole number.
func integerArg(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n == math.Trunc(n) && n >= math.MinInt32 && n <= math.MaxInt32 {
			return int(n), true
		}
	}
	return 0, false
}

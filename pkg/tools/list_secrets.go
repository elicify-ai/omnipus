// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"encoding/json"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
)

// ListSecretsDeps are the live sources list_secrets reads. All are resolved
// per call; a nil source makes the tool fail closed with a clear error.
type ListSecretsDeps struct {
	// Store returns the credential store; exists is false on a fresh install.
	Store func() (store *credentials.Store, exists bool, err error)
	// Config returns the live configuration (consumers, display names).
	Config func() *config.Config
	// Home is the data directory workspace records live in.
	Home func() string
	// SeesAll reports whether the agent sees every stored reference (Omnipus).
	SeesAll func(agentID string) bool
	// WorkspaceOf resolves the workspace of the calling session.
	WorkspaceOf func(sessionID string) (string, error)
}

// ListSecretsTool lists stored secret references, never values (FR-073).
type ListSecretsTool struct {
	BaseTool
	deps ListSecretsDeps
}

func NewListSecretsTool(deps ListSecretsDeps) *ListSecretsTool { return &ListSecretsTool{deps: deps} }

func (*ListSecretsTool) Name() string           { return "list_secrets" }
func (*ListSecretsTool) Scope() ToolScope       { return ScopeGeneral }
func (*ListSecretsTool) Category() ToolCategory { return CategoryCommunication }

// Description is prometheus's list_secrets.md text.
func (*ListSecretsTool) Description() string {
	return "List the secret references visible to you, never their values. Each entry carries its reference name, kind, version, recipient display, consumers and whether the reference is currently in use. An ordinary agent sees only its own private variable names in the current workspace; Omnipus sees every stored reference of every kind. Call it to find the reference name and version to pass to other secret tools, to check what is already stored before asking for a card, or to see which references a removal would affect. Values are never returned — configure agents and integrations with reference names only."
}

func (*ListSecretsTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
}

// listedSecret is one result row: the K6 entry plus its in-use flag.
type listedSecret struct { // not-wire-format: tool result for the model, not a gateway/SPA payload
	gen.SecretNameEntry
	InUse bool `json:"in_use"`
}

func (t *ListSecretsTool) Execute(ctx context.Context, _ map[string]any) *ToolResult {
	d := t.deps
	if d.Store == nil || d.Config == nil || d.Home == nil || d.SeesAll == nil || d.WorkspaceOf == nil {
		return ErrorResult("secret listing is unavailable")
	}
	agentID := ToolAgentID(ctx)
	seesAll := d.SeesAll(agentID)
	workspaceID := ""
	if !seesAll {
		ws, err := d.WorkspaceOf(ToolTranscriptSessionID(ctx))
		if err != nil || ws == "" {
			return ErrorResult("secret listing is unavailable: this chat's workspace could not be resolved")
		}
		workspaceID = ws
	}
	store, exists, err := d.Store()
	if err != nil {
		return ErrorResult("secret listing is unavailable: the secret store could not be opened")
	}
	rows := []listedSecret{}
	if exists {
		inventory, invErr := store.CardInventory()
		if invErr != nil {
			return ErrorResult("secret listing is unavailable: the secret store could not be read")
		}
		for _, entry := range secretrequest.NameEntries(d.Config(), d.Home(), inventory) {
			if !seesAll && (entry.Owner == nil || entry.Owner.AgentId != agentID || entry.Owner.WorkspaceId != workspaceID) {
				continue
			}
			rows = append(rows, listedSecret{SecretNameEntry: entry,
				InUse: entry.State == gen.SecretNameEntryStateSelected || len(entry.Consumers) > 0})
		}
	}
	raw, err := json.Marshal(map[string]any{"secrets": rows})
	if err != nil {
		return ErrorResult("secret listing could not be encoded")
	}
	return NewToolResult(string(raw))
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package secretrequest

import (
	"errors"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// privateOwnerPairLocked fixes the owner pair of an agent-private card
// (FR-065, FR-076, CRIT-001). An ordinary agent's pair is its own: the acting
// agent in the session's workspace, from the trusted execution context. Only
// Omnipus may name a target pair, and only one whose agent exists and belongs
// to that workspace (BDD-7.8). Input never carries a pair for other kinds.
func (r *Registry) privateOwnerPairLocked(cfg *config.Config, actingAgentID, sessionWorkspaceID string, in Input) (Input, error) {
	if in.Variable == "" {
		if in.WorkspaceID != "" || in.AgentID != "" {
			return in, errors.New("workspace_id and agent_id name the owner of a private variable; use them only with variable")
		}
		return in, nil
	}
	if in.WorkspaceID == "" && in.AgentID == "" {
		if sessionWorkspaceID == "" {
			return in, errors.New("a private variable belongs to one agent in one workspace; this chat has no workspace")
		}
		in.WorkspaceID, in.AgentID = sessionWorkspaceID, actingAgentID
		return in, nil
	}
	if actingAgentID != string(coreagent.IDOmnipus) {
		if in.WorkspaceID == sessionWorkspaceID && in.AgentID == actingAgentID {
			return in, nil
		}
		return in, errors.New("only Omnipus may request a private variable for another agent or workspace")
	}
	if in.WorkspaceID == "" || in.AgentID == "" {
		return in, errors.New("a private variable target needs both workspace_id and agent_id")
	}
	if agentName(cfg, in.AgentID) == "" {
		return in, errors.New("the target agent does not exist")
	}
	// FindForAgentPreferring falls back to ANY membership when the preferred
	// workspace does not hold the agent, so the result must equal the target.
	if found, member := workspace.FindForAgentPreferring(r.home(), in.AgentID, in.WorkspaceID); !member || found != in.WorkspaceID {
		return in, errors.New("the target agent is not a member of that workspace")
	}
	return in, nil
}

// privateDisplayLines are K2's agent_private lines: "<Agent> in <Workspace>"
// and the variable name. Display names only; the pair ids stay in the owner.
func (r *Registry) privateDisplayLines(cfg *config.Config, in Input) []string {
	return DisplayLines(cfg, r.home(), credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: in.WorkspaceID, AgentID: in.AgentID, Field: in.Variable})
}

// DisplayLines composes K2's display lines for a stored recipient, the same
// lines its card showed. home is the data directory (workspace names).
func DisplayLines(cfg *config.Config, home string, r credentials.CardRecipient) []string {
	if r.Kind != credentials.KindAgentPrivate {
		return recipientLines(r)
	}
	agent := agentName(cfg, r.AgentID)
	if agent == "" {
		agent = r.AgentID
	}
	return []string{agent + " in " + workspaceTitle(home, r.WorkspaceID), "Variable: " + r.Field}
}

// home is the data directory workspace records live in — the same
// OMNIPUS_HOME the agent loop's turn admission resolves membership against.
func (r *Registry) home() string {
	return config.OmnipusHomeDir()
}

// workspaceTitle is the workspace's display name, or its id with an initial
// capital when the workspace record has no name.
func workspaceTitle(home, id string) string {
	if home != "" {
		if name, _, ok := workspace.LoadTitle(home, id); ok && strings.TrimSpace(name) != "" {
			return name
		}
	}
	runes := []rune(id)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}
	return string(runes)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/programs"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// programsForTurn is the tools.ProgramSource for bash and environment_setup
// (connected programs FR-010, FR-011, FR-012, FR-024, FR-027). The agent and
// the provenance come only from the trusted turn context. Every token is
// registered with the gateway's sensitive-value filter before any child sees
// it (defence in depth B1–B6; the source masking is programs.Attach). It never
// fails the command: a failure is a value-free notice.
func (al *AgentLoop) programsForTurn(ctx context.Context, privateNames []string) *programs.TurnPrograms {
	agentID := tools.ToolAgentID(ctx)
	cfg := al.GetConfig()
	if agentID == "" || cfg == nil {
		return nil
	}
	store, storeErr := al.secretRecoveryCredentialStore()
	if errors.Is(storeErr, secretrequest.ErrRecoveryStoreAbsent) {
		store, storeErr = nil, nil // no store yet: nothing can be connected
	}
	var src programs.TokenSource
	if store != nil {
		src = store
	}
	if storeErr != nil {
		logger.WarnCF("agent", "connected programs not loaded: credential store unavailable",
			map[string]any{"agent_id": agentID, "error": storeErr.Error()})
	}
	resolved, err := programs.Resolve(cfg, src, storeErr, programs.TurnRequest{
		AgentID:          agentID,
		Provenance:       tools.ToolTurnProvenance(ctx),
		PrivateVariables: privateNames,
	})
	if err != nil {
		logger.WarnCF("agent", "connected programs not loaded: catalog unreadable",
			map[string]any{"agent_id": agentID, "error": err.Error()})
		return &programs.TurnPrograms{Tokens: resolved.Tokens, Notices: []string{programs.MsgStoreUnavailable}}
	}
	if len(resolved.Tokens) > 0 {
		cfg.EnsureSensitiveValues(resolved.Tokens)
	}
	return resolved
}

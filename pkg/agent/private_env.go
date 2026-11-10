// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// privateEnvForTurn is the tools.PrivateEnvSource for bash and
// environment_setup (FR-076, FR-241). The owner pair comes only from the
// trusted turn context: the acting agent, and the workspace the turn's
// session belongs to (the same ancestry the secret card used). A turn that
// consumed a channel person's input gets only variables marked available in
// channel conversations. Failures never block the command; they return a
// value-free notice naming the variables that were not loaded.
func (al *AgentLoop) privateEnvForTurn(ctx context.Context) ([]tools.PrivateVariable, string) {
	agentID := tools.ToolAgentID(ctx)
	sessionID := tools.ToolTranscriptSessionID(ctx)
	if agentID == "" || sessionID == "" {
		return nil, ""
	}
	store, err := al.secretRecoveryCredentialStore()
	if errors.Is(err, secretrequest.ErrRecoveryStoreAbsent) {
		return nil, "" // no credential store yet: nothing private can exist
	}
	if err != nil || store == nil {
		al.logPrivateEnvFailure(sessionID, agentID, "store_unavailable")
		return nil, "Private variables were not loaded for this command: the credential store is unavailable."
	}
	sessions := al.GetSessionStore()
	if sessions == nil {
		return nil, ""
	}
	ancestry, err := sessions.ResolveInteractionContext(sessionID)
	if err != nil {
		al.logPrivateEnvFailure(sessionID, agentID, "session_context_unavailable")
		return nil, "Private variables were not loaded for this command: the chat's workspace could not be resolved."
	}
	values, err := store.PrivateEnvironment(ancestry.WorkspaceID, agentID, tools.ToolTurnOrigin(ctx).ChannelTriggered())
	vars := make([]tools.PrivateVariable, 0, len(values))
	plain := make([]string, 0, len(values))
	for _, v := range values {
		vars = append(vars, tools.PrivateVariable{Name: v.Name, Value: v.Value})
		plain = append(plain, v.Value)
	}
	// Mask every value BEFORE any child can see it, independently of the
	// card's continuation having registered it (gate r1 M3, T2-S1).
	if len(plain) > 0 {
		al.GetConfig().EnsureSensitiveValues(plain)
	}
	if err == nil {
		return vars, ""
	}
	var damaged *credentials.PrivateVariableError
	if errors.As(err, &damaged) {
		al.logPrivateEnvFailure(sessionID, agentID, "private_variable_unreadable")
		return vars, "Private variables could not be read and were not set for this command: " +
			strings.Join(damaged.Variables, ", ") + ". Replace them on a new secret card."
	}
	al.logPrivateEnvFailure(sessionID, agentID, "store_unreadable")
	return nil, "Private variables were not loaded for this command: the credential store could not be read."
}

func (al *AgentLoop) logPrivateEnvFailure(sessionID, agentID, category string) {
	logger.WarnCF("agent", "private variables not loaded for a command", map[string]any{
		"session_id": sessionID, "agent_id": agentID, "cause_category": category,
	})
}

// CardSecretStore is the encrypted store secret cards commit into, for the
// gateway's value-free secret listing and options routes. exists is false on
// a fresh install with no credential store yet (not an error).
func (al *AgentLoop) CardSecretStore() (store *credentials.Store, exists bool, err error) {
	store, err = al.secretRecoveryCredentialStore()
	if errors.Is(err, secretrequest.ErrRecoveryStoreAbsent) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return store, store != nil, nil
}

// newListSecretsTool wires list_secrets to the live store, configuration and
// session workspaces (FR-073).
func (al *AgentLoop) newListSecretsTool() *tools.ListSecretsTool {
	return tools.NewListSecretsTool(tools.ListSecretsDeps{
		Store:   al.CardSecretStore,
		Config:  al.GetConfig,
		Home:    config.OmnipusHomeDir,
		SeesAll: func(agentID string) bool { return agentID == string(coreagent.IDOmnipus) },
		WorkspaceOf: func(sessionID string) (string, error) {
			sessions := al.GetSessionStore()
			if sessions == nil || sessionID == "" {
				return "", errors.New("no session")
			}
			ancestry, err := sessions.ResolveInteractionContext(sessionID)
			return ancestry.WorkspaceID, err
		},
	})
}

// reloadAndWaitTimeout bounds reloadAndWait's wait for a queued reload.
const reloadAndWaitTimeout = 60 * time.Second

// reloadAndWait triggers a config reload and waits until it has landed (the
// pending flag clears) or the bound elapses.
func (al *AgentLoop) reloadAndWait() error {
	if err := al.TriggerReload(); err != nil && !errors.Is(err, ErrReloadAlreadyInProgress) {
		return err
	}
	deadline := time.Now().Add(reloadAndWaitTimeout)
	for al.IsReloadPending() {
		if time.Now().After(deadline) {
			return errors.New("the live reload did not finish in time")
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}

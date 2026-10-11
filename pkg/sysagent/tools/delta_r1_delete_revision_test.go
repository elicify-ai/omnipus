// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// BDD (delta review R1 #10): the read-only revision check before a delete may
// pass over a record that does not exist (the cascade's idempotent retry), but
// it must NOT pass over a record it merely failed to read: a corrupt or
// unreadable record would otherwise have its sessions and task assignments
// destroyed first and only then fail at DeleteState.
func TestCheckAgentDeleteRevision_OnlyAMissingRecordIsPassedOver(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	require.NoError(t, store.Create("rev-bot", &config.AgentConfig{ID: "rev-bot", Name: "Rev Bot"}))
	state, err := store.ReadState("rev-bot")
	require.NoError(t, err)

	// A missing record: not decided here (idempotent retry).
	require.NoError(t, CheckAgentDeleteRevision(home, "never-existed", "any"))

	// The right revision passes; a stale one is the conflict.
	require.NoError(t, CheckAgentDeleteRevision(home, "rev-bot", state.Revision))
	require.ErrorIs(t, CheckAgentDeleteRevision(home, "rev-bot", "stale"), agentstore.ErrRevisionConflict)

	// A record that exists but cannot be read must be reported, not swallowed.
	record := filepath.Join(home, "entities", "agents", "rev-bot.json")
	_, statErr := os.Stat(record)
	require.NoError(t, statErr, "premise: the agent record is entities/agents/rev-bot.json")
	require.NoError(t, os.WriteFile(record, []byte("{ not json"), 0o600))
	err = CheckAgentDeleteRevision(home, "rev-bot", state.Revision)
	require.Error(t, err, "an unreadable record must stop the delete before anything is destroyed")
	require.NotErrorIs(t, err, agentstore.ErrRevisionConflict, "it is a read failure, not a revision conflict")
}

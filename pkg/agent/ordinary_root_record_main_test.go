// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// A turn in an agent's main chat establishes its ordinary root record. Before
// the fix, the main session type had no origin kind and admission failed with
// `invalid origin kind "main"`.
func TestEnsureOrdinaryRootRecord_MainSessionEstablishesRoot(t *testing.T) {
	home := t.TempDir()
	sessions, err := session.NewUnifiedStore(home + "/sessions")
	require.NoError(t, err)
	meta, err := sessions.GetOrCreateMainSession("ws-main-root", "ava")
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeMain, meta.Type)

	lifecycle := session.NewLifecycleStore(home + "/session_lifecycle")
	var al *AgentLoop // the method reads no loop state
	rec, err := al.ensureOrdinaryRootRecord(lifecycle, meta.ID, sessions)
	require.NoError(t, err)
	require.NotNil(t, rec.Origin)
	require.Equal(t, session.OriginKindMain, rec.Origin.Kind)

	loaded, err := lifecycle.Load(meta.ID)
	require.NoError(t, err)
	require.Equal(t, session.OriginKindMain, loaded.Origin.Kind)
	require.True(t, session.LifecycleRecordIsStandingRoot(loaded), "a main is a standing conversation")
}

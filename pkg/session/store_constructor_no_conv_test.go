// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Architect ruling (ARCHITECT-ANSWER-FINAL-GATE.md item 2; Q10): the saved-chat
// conversion runs in ONE explicit boot step (CutoverSavedChatsAtBoot*), never
// inside a store constructor. A store opened over a pre-cutover directory must
// leave the saved chat exactly as found.

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewUnifiedStoreWithHome_DoesNotRunTheSavedChatConversion(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	const id = "chat-legacy-ctor-1"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	convSeedLegacyChatDir(t, sessionsDir, id, "mia", "jim", "Old saved chat", "", created, created)
	before, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err)

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	after, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "opening a store must not convert a saved chat")

	// Positive control: the explicit boot step does convert it, so the check
	// above could have seen a conversion.
	require.NoError(t, CutoverSavedChatsAtBoot(sessionsDir))
	require.Equal(t, "chat", convReadMetaDoc(t, sessionsDir, id)["type"])
}

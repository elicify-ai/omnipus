// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// DEL-10 step 1 (T35): the CONV cutover takes the retired per-agent stores into
// the shared store under the same ids, with no lost chat, no second copy and a
// visible refusal on a same-id conflict.
package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newPerAgentSession(t *testing.T, agentDir string) string {
	t.Helper()
	us, err := NewUnifiedStore(agentDir)
	require.NoError(t, err)
	meta, err := us.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	require.NoError(t, us.AppendTranscript(meta.ID, TranscriptEntry{ID: "m1", Role: "user", Content: "from the per-agent store", Timestamp: time.Now().UTC()}))
	require.NoError(t, us.Close())
	return meta.ID
}

func TestAgentStoreCutover_FullSessionMovesUnderTheSameID(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "sessions")
	agentDir := filepath.Join(base, "agents", "a", "sessions")
	id := newPerAgentSession(t, agentDir)

	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir}))

	assert.NoDirExists(t, filepath.Join(agentDir, id), "the source is retired after the move")
	us, err := NewUnifiedStore(shared)
	require.NoError(t, err)
	defer us.Close()
	meta, err := us.GetMeta(id)
	require.NoError(t, err, "the session is listable in the shared store under the same id")
	assert.Equal(t, "agent-a", meta.AgentID)
	entries, err := us.ReadTranscript(id)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "from the per-agent store", entries[0].Content)
	require.NoError(t, us.AppendTranscript(id, TranscriptEntry{ID: "m2", Role: "assistant", Content: "continued", Timestamp: time.Now().UTC()}),
		"the moved session is continuable")

	// A second boot finds nothing to do.
	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir}))
}

func TestAgentStoreCutover_SameIDConflictRefusesVisiblyAndKeepsTheSource(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "sessions")
	agentDir := filepath.Join(base, "agents", "a", "sessions")
	id := newPerAgentSession(t, agentDir)
	require.NoError(t, os.MkdirAll(filepath.Join(shared, id), 0o700))
	marker := filepath.Join(shared, id, "keep.txt")
	require.NoError(t, os.WriteFile(marker, []byte("shared copy"), 0o600))

	err := CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir})
	require.Error(t, err)
	assert.Contains(t, err.Error(), id, "the refusal names the session")
	assert.FileExists(t, filepath.Join(agentDir, id, "meta.json"), "the source is retained")
	b, rerr := os.ReadFile(marker)
	require.NoError(t, rerr)
	assert.Equal(t, "shared copy", string(b), "the shared copy is untouched")
}

func TestAgentStoreCutover_PerAgentModelArchiveJoinsTheSharedChat(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "sessions")
	agentDir := filepath.Join(base, "agents", "a", "sessions")
	// The chat lives in the shared store; its model memory is in the agent's
	// legacy `.context` under the routing key.
	su, err := NewUnifiedStore(shared)
	require.NoError(t, err)
	meta, err := su.NewSession(SessionTypeChat, "", "agent-a")
	require.NoError(t, err)
	require.NoError(t, su.AppendTranscript(meta.ID, TranscriptEntry{ID: "u1", Role: "user", Content: "hello", Timestamp: time.Now().UTC()}))
	require.NoError(t, su.Close())
	key := "agent:a:session:" + meta.ID
	raw := `{"role":"user","content":"remember the number 42","ts":5}` + "\n" + `{"role":"assistant","content":"noted","ts":6}` + "\n"
	convSeedContext(t, agentDir, "sanitized-key", raw, convMeta(key))

	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir}))
	// Retry after an interruption (or a second boot) must not add a second copy.
	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir}))

	us, err := NewUnifiedStore(shared)
	require.NoError(t, err)
	defer us.Close()
	view, err := us.WindowView(context.Background(), key)
	require.NoError(t, err)
	msgs, _ := view.History()
	require.Len(t, msgs, 2, "the next provider request carries the prior turns, exactly once")
	assert.Equal(t, "remember the number 42", msgs[0].Content)
	assert.NoFileExists(t, filepath.Join(agentDir, convContextDir, "sanitized-key.jsonl"), "the source is retired")
	chat, err := us.ReadTranscript(meta.ID)
	require.NoError(t, err)
	assert.Len(t, chat, 1, "the chat view is unchanged by the model records")
}

func TestAgentStoreCutover_ArchiveWithNoSessionIsLeftInPlace(t *testing.T) {
	base := t.TempDir()
	shared := filepath.Join(base, "sessions")
	agentDir := filepath.Join(base, "agents", "a", "sessions")
	convSeedContext(t, agentDir, "main-key", `{"role":"user","content":"x","ts":1}`+"\n", convMeta("agent:a:main"))
	require.NoError(t, CutoverSavedChatsAtBootWithAgentStores(shared, []string{agentDir}))
	assert.FileExists(t, filepath.Join(agentDir, convContextDir, "main-key.jsonl"), "an archive that names no session is retained, not lost")
	entries, _ := os.ReadDir(shared)
	for _, e := range entries {
		assert.NotContains(t, e.Name(), ":", "no colon-named directory appears among the shared sessions")
	}
}

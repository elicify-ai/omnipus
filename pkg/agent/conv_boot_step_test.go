// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Boot-level CONV evidence (WC-3 CONV-FIX; architect ANSWERS-WC2 Q10/Q11).
//
// The pkg/session pack proves the cutover's on-disk contract through the store
// constructor. These two tests prove the BOOT-LEVEL properties the constructor
// alone cannot: that the cutover runs as a STANDALONE step over the shared
// archive BEFORE any session store is constructed (no store is constructed
// here — only the boot step runs), and that a conversion failure makes that
// boot step refuse rather than let ordinary serving proceed over an incomplete
// conversion.
//
// The step under test is newAgentLoop.initializeConvertedSessions — the exact
// call NewAgentLoop makes first, ahead of initializeCore's registry and
// per-agent store construction.

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// bootStepAtHome builds a newAgentLoop whose config resolves the shared
// sessions archive to <home>/sessions, WITHOUT constructing any session store.
func bootStepAtHome(t *testing.T) (*newAgentLoop, string) {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{}
	// AgentHomeBasePath() is this path; NewAgentLoop derives homePath as its
	// parent, exactly as initializeCore derives the shared sessions dir.
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	return &newAgentLoop{cfg: cfg}, home
}

// writePreCutoverSavedChat seeds <sessionsDir>/<id>/meta.json as a pre-cutover
// saved chat: the now-required "type" is MISSING and the retired
// "active_agent_id" is present. The immutable owner is "agent_id".
func writePreCutoverSavedChat(t *testing.T, sessionsDir, id string) {
	t.Helper()
	dir := filepath.Join(sessionsDir, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	created := time.Date(2026, 5, 2, 3, 4, 5, 0, time.UTC)
	doc := map[string]any{
		"id":              id,
		"agent_id":        "mia",
		"active_agent_id": "jim",
		"title":           "Old saved chat",
		"status":          "active",
		"created_at":      created,
		"updated_at":      created,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o600))
}

// TestConvBootStep_CutoverRunsBeforeAnyStoreIsConstructed proves Q10: the boot
// step converts the shared archive on its own — no UnifiedStore is constructed
// in this test — so no cache/list/attach can ever observe the pre-cutover shape.
func TestConvBootStep_CutoverRunsBeforeAnyStoreIsConstructed(t *testing.T) {
	nal, home := bootStepAtHome(t)
	sessionsDir := filepath.Join(home, "sessions")
	const id = "saved-chat-1"

	writePreCutoverSavedChat(t, sessionsDir, id)

	// Precondition: the seeded chat really is pre-cutover (no type on disk).
	before := readBootMetaDoc(t, sessionsDir, id)
	_, hadType := before["type"]
	require.False(t, hadType, "precondition: the seeded saved chat must lack 'type'")

	require.NoError(t, nal.initializeConvertedSessions(),
		"the boot step over a pre-cutover saved chat must succeed")

	// Converted by the boot step alone — no store was ever constructed here.
	after := readBootMetaDoc(t, sessionsDir, id)
	assert.Equal(t, "chat", after["type"],
		"Q10: the boot step (not a store) must write the missing type once as chat")
	_, hasActive := after["active_agent_id"]
	assert.False(t, hasActive,
		"Q10: the boot step must drop the retired active_agent_id")
	assert.Equal(t, "mia", after["agent_id"],
		"Q10: the immutable owner stays agent_id")
}

// TestConvBootStep_RefusesOnCorruptSavedChat proves Q11: a corrupt saved chat
// makes the boot step return a visible error naming the chat and retains the
// recoverable source bytes, so NewAgentLoop refuses boot rather than serving
// over an incomplete conversion.
func TestConvBootStep_RefusesOnCorruptSavedChat(t *testing.T) {
	nal, home := bootStepAtHome(t)
	sessionsDir := filepath.Join(home, "sessions")
	const id = "saved-chat-corrupt"

	dir := filepath.Join(sessionsDir, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	corrupt := []byte("{not json")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "meta.json"), corrupt, 0o600))

	err := nal.initializeConvertedSessions()
	require.Error(t, err,
		"Q11: a corrupt saved chat must make the boot step refuse, not skip with success")
	assert.Contains(t, err.Error(), id,
		"Q11: the cutover error must name the affected saved chat")

	after, rerr := os.ReadFile(filepath.Join(dir, "meta.json"))
	require.NoError(t, rerr, "the corrupt source must be retained, not deleted")
	assert.Equal(t, corrupt, after,
		"Q11: the recoverable source bytes must be left byte-for-byte")
}

// readBootMetaDoc parses a session's meta.json into a generic map for the
// on-disk assertions above.
func readBootMetaDoc(t *testing.T, sessionsDir, id string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(data, &doc))
	return doc
}

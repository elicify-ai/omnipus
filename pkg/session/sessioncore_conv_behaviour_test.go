// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core CONV (one-time saved-chat cutover), pkg/session slice
// — the behavioural tests.
//
// Spec: docs/internal/specs/session-core-spec.md, section "CONV — One-time
// saved-chat cutover only"; FR-038 (sole exception); DEL-09/10/11; BDD-12.6;
// T35. Founder Q2=B (narrowed 2026-10-08): greenfield for heartbeats only;
// KEEP the one-time conversion that keeps existing saved chats reachable.
//
// Every test here drives the EXISTING public boot surface
// (NewUnifiedStoreWithHome) and the store's own methods, then asserts the
// observable POST-BOOT state the spec pins. On current production code the
// conversion does not run at all: a pre-cutover saved chat keeps its missing
// "type" and its retired "active_agent_id" on disk, a completed source is never
// retired, and a corrupt saved chat is silently dropped. Each RED test below
// fails on one of those for the right reason; the two CONTROL tests are green
// at baseline and must stay green after GREEN (canonical positive controls).

package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BDD-12.6 / CONV Identity+Destination: a pre-cutover saved chat that lacks the
// now-required "type" and carries the retired "active_agent_id" must, after the
// first cutover boot, stay listable/continuable under its SAME id, with the
// missing type WRITTEN ONCE as "chat" and active_agent_id DROPPED — keeping
// agent_id as the immutable owner. The persisted form is the contract that lets
// DEL-11 delete the runtime missing-type fallback without stranding the chat.
func TestSessionCoreConv_PreCutoverSavedChatIsNormalizedAndContinuable(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	const id = "chat-legacy-0001"
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)

	convSeedLegacyChatDir(t, sessionsDir, id, "mia", "jim", "Old saved chat", "", created, updated)
	convSeedLegacyTranscript(t, sessionsDir, id, "legacy-m1", "old hello", "mia", created)

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err, "boot over a pre-cutover saved chat must succeed")
	t.Cleanup(func() { _ = store.Close() })

	// Reachable: listable under the same id, no re-keying.
	listed := convListIDs(t, store)
	require.Contains(t, listed, id, "the saved chat must stay listable after cutover; listed %v", keysOf(listed))
	assert.Equal(t, UnifiedSessionType("chat"), listed[id].Type,
		"CONV Identity: a missing pre-cutover type is filled as chat")

	meta, err := store.GetMeta(id)
	require.NoError(t, err, "the converted saved chat must be attachable")
	assert.Equal(t, "mia", meta.AgentID,
		"CONV Identity: agent_id stays the immutable owner, never active_agent_id (jim)")
	assert.Equal(t, "", meta.ActiveAgentID,
		"CONV Identity: the retired active_agent_id must be dropped, not adopted as owner")
	assert.Equal(t, "Old saved chat", meta.Title, "title preserved")
	assert.True(t, meta.CreatedAt.Equal(created), "created_at preserved (no retention-age reset)")
	assert.Equal(t, "", meta.WorkspaceID,
		"CONV Identity: a non-main saved chat with no workspace stays workspace-less — do not invent one")

	// Persisted, current-format on disk — the load-bearing part.
	doc := convReadMetaDoc(t, sessionsDir, id)
	assert.Equal(t, "chat", doc["type"],
		"CONV Identity: the missing type must be WRITTEN once as chat; DEL-11 then deletes the runtime fallback")
	_, hasActive := doc["active_agent_id"]
	assert.False(t, hasActive,
		"CONV Identity: active_agent_id must be dropped from the converted metadata; got %v", doc)
	assert.Equal(t, "mia", doc["agent_id"], "the owner on disk stays agent_id")

	// Continuable: the saved history survives and a new message continues it.
	entries, err := store.ReadTranscript(id)
	require.NoError(t, err)
	require.Equal(t, 1, len(entries), "the saved chat's history must be preserved, not lost")
	assert.Equal(t, "old hello", entries[0].Content, "the retained history must be recallable")
	assert.Equal(t, "mia", entries[0].AgentID,
		"CONV Identity: the actual entry author must be preserved even though active_agent_id is dropped")

	newEntry := TranscriptEntry{ID: "new-m1", Role: "user", Content: "new hello", AgentID: "mia",
		Timestamp: time.Date(2026, 1, 4, 3, 4, 5, 0, time.UTC)}
	require.NoError(t, store.AppendTranscript(id, newEntry), "a new message must continue the converted chat")
	after, err := store.ReadTranscript(id)
	require.NoError(t, err)
	require.Equal(t, 2, len(after), "the new message must append to the same saved chat, not start a new one")
	assert.Equal(t, "new hello", after[1].Content, "accepted order preserved")
}

// CONV Publication/retry: running the cutover twice must be idempotent — the
// second boot recognizes the completed conversion, does NOT rewrite the
// already-current content, and mints no second identity or duplicated session.
func TestSessionCoreConv_SecondBootIsIdempotentAndAddsNoSecondIdentity(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	const id = "chat-legacy-0002"
	created := time.Date(2026, 2, 2, 3, 4, 5, 0, time.UTC)

	convSeedLegacyChatDir(t, sessionsDir, id, "mia", "jim", "Idempotent chat", "", created, created)

	s1, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	docAfterFirst := convReadMetaDoc(t, sessionsDir, id)
	require.Equal(t, "chat", docAfterFirst["type"],
		"precondition: the first boot converted the saved chat (this is the RED assertion on current code)")
	rawAfterFirst, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err)
	require.NoError(t, s1.Close())

	s2, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err, "a second boot over a converted install must succeed")
	t.Cleanup(func() { _ = s2.Close() })

	rawAfterSecond, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err)
	assert.Equal(t, string(rawAfterFirst), string(rawAfterSecond),
		"CONV Publication: a completed conversion must not be rewritten on the next boot")

	listed := convListIDs(t, s2)
	assert.Equal(t, 1, len(listed), "exactly one saved chat after two boots; got %v", keysOf(listed))
	assert.Contains(t, listed, id)
	assert.Equal(t, []string{id}, convSessionDirs(t, sessionsDir),
		"no second identity / duplicated session directory may appear")
}

// CONV Publication/retry: an interruption that left the SAME-ID content
// materialized but the old flat source UNRETIRED must be finished by the next
// boot — retire the source without appending a second copy. The dir already
// holds the converted content, so removing the source does not delete the only
// readable copy ("never remove the only readable copy first").
func TestSessionCoreConv_InterruptedBootRetiresTheCompletedSourceWithoutDuplicating(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	const id = "chat-interrupted-1"
	created := time.Date(2026, 3, 2, 3, 4, 5, 0, time.UTC)

	// Interrupted state: the flat legacy source is still present AND the
	// converted session directory (same id, same content) already exists.
	convWriteBytes(t, filepath.Join(sessionsDir, id+".jsonl"), convLegacyJSONL("interrupted hello"))
	convSeedLegacyChatDir(t, sessionsDir, id, "mia", "", "Interrupted chat", "", created, created)
	convWriteBytes(t, filepath.Join(sessionsDir, id, "context.jsonl"), convLegacyJSONL("interrupted hello"))

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	_, statErr := os.Stat(filepath.Join(sessionsDir, id+".jsonl"))
	assert.True(t, os.IsNotExist(statErr),
		"CONV retry: the next boot must finish the cleanup and retire the completed source; it is still present")

	listed := convListIDs(t, store)
	assert.Equal(t, 1, len(listed), "no duplicated session from the retry; got %v", keysOf(listed))
	assert.Equal(t, []string{id}, convSessionDirs(t, sessionsDir),
		"the retry must not create a second copy of the session")
}

// CONV Failure: a corrupt saved chat must surface a VISIBLE cutover error
// naming the affected chat — never be skipped-with-success (silently excluded
// from the listing while boot proceeds). The recoverable source bytes are
// retained and no empty replacement is written.
func TestSessionCoreConv_CorruptSavedChatSurfacesVisibleFailureAndRetainsSource(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	const id = "chat-corrupt-1"

	corrupt := []byte("{not json")
	convWriteBytes(t, filepath.Join(sessionsDir, id, "meta.json"), corrupt)

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	if store != nil {
		_ = store.Close()
	}
	require.Error(t, err,
		"CONV Failure: an unreadable saved chat must surface a visible cutover error, not be skipped-with-success")

	after, rerr := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, rerr, "the corrupt source must be retained, not deleted")
	assert.Equal(t, corrupt, after,
		"CONV Failure: the recoverable source bytes must be left byte-for-byte (no empty replacement)")
}

// CONTROL (green at baseline): a fresh install with no saved chats is a no-op
// with zero conversion writes — CONV Completion/fresh install. This must stay
// green after GREEN; it guards against a conversion that invents sessions.
func TestSessionCoreConv_FreshInstallIsANoOpWithZeroWrites(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	require.NoError(t, os.MkdirAll(sessionsDir, 0o700))

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	assert.Empty(t, convSessionDirs(t, sessionsDir), "a fresh install must write no session directory")
	listed := convListIDs(t, store)
	assert.Equal(t, 0, len(listed), "a fresh install lists no sessions")
}

// CONTROL (green at baseline): the saved-chat conversion must NOT mint a
// computed main and must NOT promote a heartbeat-typed record (founder Q2=B
// narrowed: no heartbeat-to-main conversion, no folder rename, no id alias).
// A saved chat is not promoted to main either — main creation stays FR-002.
func TestSessionCoreConv_ConversionMintsNoMainAndPromotesNoHeartbeat(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, "sessions")
	created := time.Date(2026, 4, 2, 3, 4, 5, 0, time.UTC)

	convSeedLegacyChatDir(t, sessionsDir, "chat-a", "mia", "", "A saved chat", "", created, created)
	// Negative control for the "no heartbeat-to-main conversion" rule: a
	// heartbeat-typed record must be left as-is, never promoted.
	convSeedLegacyChatDir(t, sessionsDir, "hb-1", "mia", "", "Heartbeat record", string(SessionTypeHeartbeat), created, created)

	store, err := NewUnifiedStoreWithHome(sessionsDir, home)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	listed := convListIDs(t, store)
	require.Equal(t, 2, len(listed), "both saved records must remain; got %v", keysOf(listed))
	for id, m := range listed {
		assert.NotEqual(t, UnifiedSessionType("main"), m.Type, "no main may be minted by the conversion (id %s)", id)
		assert.NotContains(t, id, "main-session-", "no computed main id may appear (id %s)", id)
	}
	assert.Equal(t, UnifiedSessionType("heartbeat"), listed["hb-1"].Type,
		"a heartbeat-typed record must not be promoted to main (Q2=B)")
}

// keysOf returns the keys of a map[string]*UnifiedMeta, for failure messages.
func keysOf(m map[string]*UnifiedMeta) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

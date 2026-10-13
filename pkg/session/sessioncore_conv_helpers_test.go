// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core CONV (one-time saved-chat cutover), pkg/session slice
// — fixtures and assertion helpers.
//
// Spec: docs/internal/specs/session-core-spec.md, section "CONV — One-time
// saved-chat cutover only" (Inputs/Identity/Destination/Publication/Failure/
// Heartbeat boundary), FR-038's sole exception, DEL-09/10/11; BDD-12.6;
// T35 SavedChatCutoverAndSameIDContinuation. ADR:
// docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md.
//
// Oracle independence: every expected literal here derives from the spec's CONV
// section, never from the current store's behaviour. The fixtures write the
// PRE-cutover shape directly to disk (bypassing the store) so the store's first
// read of a saved chat is the one under test, exactly as a legacy install would
// present it.
//
// The tests drive only EXISTING public surfaces (NewUnifiedStoreWithHome and the
// store's own list/get/append/read methods) plus raw on-disk reads of the
// converted meta.json. CONV has no named Go entry point in the spec; the tests
// therefore assert the observable POST-BOOT state, which is where the spec pins
// the contract ("Run CONV at first cutover boot before session-cache/list/
// attach", CONV Ordering). No assumed new symbol is referenced.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// convSeedLegacyChatDir writes a PRE-cutover saved-chat session directory
// directly to sessionsDir/<id>/meta.json, bypassing the store, so the store's
// first read of that id (at construction) sees the legacy shape.
//
// The document is the pre-cutover meta.json: it carries the immutable
// "agent_id" owner and a retired "active_agent_id" handover owner, and it has
// NO "type" key — the now-required field the cutover must fill once as "chat"
// (CONV Identity). Pass typeOverride non-empty to seed a session that already
// carries a valid non-chat type.
func convSeedLegacyChatDir(t *testing.T, sessionsDir, id, agentID, activeAgentID, title, typeOverride string, created, updated time.Time) {
	t.Helper()
	doc := map[string]any{
		"id":         id,
		"agent_id":   agentID,
		"title":      title,
		"status":     "active",
		"created_at": created.UTC().Format(time.RFC3339),
		"updated_at": updated.UTC().Format(time.RFC3339),
		"channel":    "webchat",
		"partitions": []string{},
	}
	if activeAgentID != "" {
		doc["active_agent_id"] = activeAgentID
	}
	if typeOverride != "" {
		doc["type"] = typeOverride
	}
	dir := filepath.Join(sessionsDir, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	convWriteJSON(t, filepath.Join(dir, "meta.json"), doc)
}

// convSeedLegacyTranscript writes a PRE-cutover transcript.jsonl for id with a
// single entry, so "continuable" can assert the saved history is preserved and
// a new message continues the same session.
func convSeedLegacyTranscript(t *testing.T, sessionsDir, id, entryID, content, agentID string, ts time.Time) {
	t.Helper()
	entry := map[string]any{
		"id":        entryID,
		"role":      "user",
		"content":   content,
		"agent_id":  agentID,
		"timestamp": ts.UTC().Format(time.RFC3339),
	}
	dir := filepath.Join(sessionsDir, id)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(entry)
	require.NoError(t, err)
	convWriteBytes(t, filepath.Join(dir, "transcript.jsonl"), append(raw, '\n'))
}

// convWriteJSON marshals v and writes it to path.
func convWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

// convWriteBytes writes raw bytes to path, creating parent dirs.
func convWriteBytes(t *testing.T, path string, raw []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, raw, 0o600))
}

// convReadMetaDoc parses the on-disk meta.json of a session directory into a
// generic document, so an absent key ("type") is distinguishable from a
// present-but-empty one.
func convReadMetaDoc(t *testing.T, sessionsDir, id string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sessionsDir, id, "meta.json"))
	require.NoError(t, err, "meta.json must exist for %s", id)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc), "meta.json must be valid JSON: %s", string(raw))
	return doc
}

// convSessionDirs returns the sorted names of the session directories directly
// under sessionsDir, excluding the store's own ".context" backend (seeded by
// NewUnifiedStoreWithHome; the list path skips it explicitly). A converted
// install must hold exactly one directory per saved chat and no second
// identity.
func convSessionDirs(t *testing.T, sessionsDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(sessionsDir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".context" {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// convListIDs returns the ids of every session the store lists, as a set, so a
// test can assert reachability without depending on list ordering.
func convListIDs(t *testing.T, store *UnifiedStore) map[string]*UnifiedMeta {
	t.Helper()
	listed, err := store.ListSessions()
	require.NoError(t, err, "ListSessions must succeed after cutover")
	out := map[string]*UnifiedMeta{}
	for _, m := range listed {
		out[m.ID] = m
	}
	return out
}

// convLegacyJSONL builds the flat JSONL body a pre-cutover install left behind
// for a session (the migrateLegacy input). Content is a single legacy message
// line; the exact body is not asserted, only that it is non-empty input.
func convLegacyJSONL(content string) []byte {
	return []byte(strings.Join([]string{
		`{"role":"user","content":"` + content + `"}`,
	}, "\n") + "\n")
}

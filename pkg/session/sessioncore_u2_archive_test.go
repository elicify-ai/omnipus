// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U2 (one append-only archive), pkg/session slice.
//
// Spec: docs/internal/specs/session-core-spec.md FR-004, FR-005; C-ARCHIVE;
// BDD-02.1, BDD-02.2. ADR:
// docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md.
// The expected shape comes from C-ARCHIVE / FR-005, never from the current code.

package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FR-004 / C-ARCHIVE: "One entry: view_membership=chat/model/both, full existing
// model representation, trusted source and message/turn/tool/result identity."
// FR-004: "one authoritative append-only content entry format for chat/model/both."
//
// Oracle: the C-ARCHIVE shape — every persisted archive entry carries a view
// membership drawn from chat/model/both. It does not exist on the current entry
// (TranscriptEntry has no such field and the JSON has no such key).
func TestSessionCoreU2_ArchiveEntryCarriesViewMembership(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	entry := TranscriptEntry{
		ID:        "m-1",
		Role:      "user",
		Content:   "hello",
		AgentID:   "mia",
		Timestamp: time.Unix(1_700_000_000, 0).UTC(),
	}
	require.NoError(t, store.AppendTranscript(meta.ID, entry))

	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), meta.ID, "transcript.jsonl"))
	require.NoError(t, err, "the archive file must exist after an append")

	first := bytes.SplitN(raw, []byte{'\n'}, 2)[0]
	var rec map[string]any
	require.NoError(t, json.Unmarshal(first, &rec), "persisted line must be valid JSON: %s", string(first))

	// Instrument control: the record is readable and carries its known fields, so
	// an absent key below is a real absence, not a mis-read.
	require.Equal(t, "m-1", rec["id"], "control: the persisted entry id is readable")
	require.Equal(t, "hello", rec["content"], "control: the persisted content is readable")

	vm, ok := rec["view_membership"]
	require.True(t, ok,
		"FR-004/C-ARCHIVE: every archive entry must carry view_membership; got %s", string(first))
	assert.Contains(t, []string{"chat", "model", "both"}, vm,
		"C-ARCHIVE: view_membership is exactly one of chat/model/both, got %#v", vm)
}

// FR-005: "UTC day files/file-byte marks MUST give bounded model reads across
// days ... avoid full-archive reads per append/step."
//
// Oracle: the spec names UTC day files. The archive must be day-partitioned, so a
// window read can be bounded to the days it needs; a single monolithic file
// cannot give a bounded cross-day read. Two entries a day apart must land in
// more than one archive data file.
func TestSessionCoreU2_ArchiveIsDayPartitionedNotOneMonolithicFile(t *testing.T) {
	store := newTestStore(t)
	meta, err := store.NewSession(SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)

	day1 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "a", Role: "user", Content: "day one", AgentID: "mia", Timestamp: day1}))
	require.NoError(t, store.AppendTranscript(meta.ID, TranscriptEntry{
		ID: "b", Role: "assistant", Content: "day two", AgentID: "mia", Timestamp: day2}))

	// Control: both entries are actually in the archive (the append worked).
	msgs, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2, "control: both appended entries are recallable")

	dir := filepath.Join(store.BaseDir(), meta.ID)
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	dataFiles := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".jsonl") && name != "provenance.jsonl" {
			dataFiles++
		}
	}
	require.GreaterOrEqual(t, dataFiles, 2,
		"FR-005: two distinct UTC days must land in day files; found %d archive data file(s) in %s — a single monolithic archive cannot give a bounded cross-day window read",
		dataFiles, dir)
}

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// The chat half of the one-time CONV cutover for the single archive (session-core
// U2 effects design S5): membership on every saved chat line, provenance folded
// into the line, the unfinished-save residue hidden without moving any other
// line, and the legacy transcript line index translated to an exact address.
package session

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func nonEmptyLines(b []byte) int {
	n := 0
	for _, l := range bytes.Split(b, []byte{'\n'}) {
		if len(bytes.TrimSpace(l)) > 0 {
			n++
		}
	}
	return n
}

func TestConvTranscripts_MembershipProvenanceResidueAndIndexTranslation(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	sid := meta.ID
	dir := filepath.Join(s.baseDir, sid)
	legacy := "" +
		`{"id":"u1","role":"user","content":"hello","timestamp":"2026-03-27T10:00:00Z"}` + "\n" +
		`{"id":"u2","role":"user","content":"paired","timestamp":"2026-03-27T10:01:00Z","provenance_pending":true}` + "\n" +
		`{"id":"u3","role":"user","content":"orphan","timestamp":"2026-03-27T10:02:00Z","provenance_pending":true}` + "\n" +
		`{"id":"c1","type":"tool_call","view_membership":"both","timestamp":"2026-03-27T10:03:00Z","tool_calls":[{"id":"call-9","tool":"x","status":"success"}]}` + "\n" +
		"this line does not decode\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(legacy), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.day"), []byte("2026-03-27\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "provenance.jsonl"), []byte(`{"message_id":"u2","principal":"alice"}`+"\n"), 0o600))

	require.NoError(t, convConvergeTranscripts(s.baseDir))

	entries, err := s.ReadTranscript(sid)
	require.NoError(t, err)
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
		assert.Equal(t, ViewMembershipChat, e.ViewMembership, "%s is a chat record", e.ID)
	}
	assert.Equal(t, []string{"u1", "u2", "c1"}, ids, "the unfinished-save residue is never shown")

	got, ok, err := s.LookupMessageProvenance(sid, "u2")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "alice", got.Principal)
	assert.EqualValues(t, 1, got.Ordinal)
	_, ok, err = s.LookupMessageProvenance(sid, "u3")
	require.NoError(t, err)
	assert.False(t, ok, "the orphan marker never had a provenance record")
	assert.NoFileExists(t, filepath.Join(dir, "provenance.jsonl"))

	raw, err := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, 5, nonEmptyLines(raw), "no line was added, dropped or moved")
	assert.NotContains(t, string(raw), "provenance_pending")

	// Idempotent: a completed pass rewrites nothing.
	require.NoError(t, convConvergeTranscripts(s.baseDir))
	again, err := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	require.NoError(t, err)
	assert.Equal(t, raw, again)

	// The legacy line index resolves to the exact converted address, or is dropped.
	addrs, callsAt, err := convTranscriptLineAddrs(dir)
	require.NoError(t, err)
	require.Len(t, addrs, 5)
	assert.Equal(t, "c1", addrs[3].EntryID)
	assert.Empty(t, addrs[4].EntryID, "an undecodable line keeps its slot but has no address")
	three, four, ten := 3, 4, 10
	out := convTranslateMeta(sid, convLegacyMeta{
		Count: 2,
		Projection: []convLegacyProjectionRow{
			{ToolCallID: "call-9", ArchiveLine: 1, State: "capped", TranscriptLine: &three},
			{ToolCallID: "call-9", ArchiveLine: 2, State: "capped", TranscriptLine: &four},
			{ToolCallID: "other", ArchiveLine: 3, State: "capped", TranscriptLine: &three},
			{ToolCallID: "call-9", ArchiveLine: 4, State: "capped", TranscriptLine: &ten},
		},
	}, 2, addrs, callsAt)
	require.Len(t, out.Projection, 4)
	require.NotNil(t, out.Projection[0].TranscriptAddr)
	assert.Equal(t, addrs[3], *out.Projection[0].TranscriptAddr)
	assert.Nil(t, out.Projection[1].TranscriptAddr, "an undecodable line is not a tool_call record")
	assert.Nil(t, out.Projection[2].TranscriptAddr, "the record at that index does not carry the row's tool call")
	assert.Nil(t, out.Projection[3].TranscriptAddr, "an index past the end is dropped, not guessed")

	// The folded source is on the line itself.
	store, err := s.archiveStore(sid)
	require.NoError(t, err)
	rec, err := store.ReadAt(addrs[1])
	require.NoError(t, err)
	require.NotNil(t, rec.Source)
	assert.Equal(t, "user", rec.Source.Kind)
	assert.Equal(t, "alice", rec.Source.Principal)
}

func TestConvTranscripts_UndecodableProvenanceRefusesVisibly(t *testing.T) {
	s := newTestStore(t)
	meta, err := s.NewSession(SessionTypeChat, "", "agent")
	require.NoError(t, err)
	dir := filepath.Join(s.baseDir, meta.ID)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte(`{"id":"u1","role":"user","content":"x","timestamp":"2026-03-27T10:00:00Z"}`+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "provenance.jsonl"), []byte("{torn"), 0o600))
	err = convConvergeTranscripts(s.baseDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), meta.ID, "the refusal names the saved chat")
	raw, rerr := os.ReadFile(filepath.Join(dir, "transcript.jsonl"))
	require.NoError(t, rerr)
	assert.NotContains(t, string(raw), "view_membership", "a refused cutover leaves the chat untouched")
}

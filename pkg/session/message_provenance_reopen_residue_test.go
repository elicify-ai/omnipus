// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Reopen-residue contract for the provenance-paired append (ADR-20261004
// Correction C4, abrupt-exit rule): a web message is shown, echoed, or
// admitted only when both the transcript line and its sender record exist.
// An abrupt process exit between the two writes leaves the paired transcript
// line on disk with its write-phase marker and no provenance record — on
// reopen that line is NOT shown and NOT admitted from any read path, while
// its bytes remain as inert residue. The rule is deliberately narrow: lines
// without the marker (every agent and tool line, and every historical user
// line that was never part of a paired save) stay visible, and a marker line
// whose record DID land (a crash between the record write and the clearing
// rewrite) is an accepted message and stays visible.
//
// The residue is written exactly the way the crash leaves it — the real
// append primitive persisting the real marked entry, no provenance record —
// and the store is genuinely closed and REOPENED before the read is
// asserted: a same-lock assertion is not crash evidence. The real
// UnifiedStore runs unmodified against real files; nothing is mocked.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/stretchr/testify/require"
)

// reopenResidueFixture is the crash state one reopen must adjudicate: a
// marked line with no record (residue), a marked line whose record landed
// after it (accepted, clear never ran), an ordinary agent line and a
// historical user line (both unmarked, both always visible).
type reopenResidueFixture struct {
	residueID        string
	acceptedID       string
	agentID          string
	historicalUserID string
}

// writeReopenResidue writes the fixture to store the way the two crash cuts
// leave it, using the same primitives the paired append uses.
func writeReopenResidue(t *testing.T, store *UnifiedStore, sessionID string, fx reopenResidueFixture) {
	t.Helper()
	transcriptPath := filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl")
	provenancePath := filepath.Join(store.BaseDir(), sessionID, provenanceFileName)
	now := time.Now().UTC()

	// Cut one — crash between the transcript line and the provenance record:
	// the marked line is on disk, no record exists for it.
	require.NoError(t, fileutil.AppendJSONL(transcriptPath, TranscriptEntry{
		ID: fx.residueID, Role: "user", AgentID: "agent-1",
		Content: "crash residue — the pair never completed", Timestamp: now,
		ProvenancePending: true,
	}), "the residue line must land the way the crash left it")

	// Cut two — crash after the provenance record but before the clearing
	// rewrite: marked line AND record, an accepted message.
	require.NoError(t, fileutil.AppendJSONL(transcriptPath, TranscriptEntry{
		ID: fx.acceptedID, Role: "user", AgentID: "agent-1",
		Content: "accepted — the record landed, the clear did not", Timestamp: now,
		ProvenancePending: true,
	}), "the accepted marked line must land")
	require.NoError(t, fileutil.AppendJSONL(provenancePath, MessageProvenance{
		MessageID: fx.acceptedID, SessionID: sessionID,
		Content:   "accepted — the record landed, the clear did not",
		Principal: "alice", Ordinal: 1,
	}), "the accepted line's provenance record must land")

	// Unmarked lines: an ordinary agent line and a historical user line that
	// was never part of a paired save. Neither may ever be hidden.
	require.NoError(t, fileutil.AppendJSONL(transcriptPath, TranscriptEntry{
		ID: fx.agentID, Role: "assistant", AgentID: "agent-1",
		Content: "ordinary agent turn", Timestamp: now,
	}), "the agent line must land")
	require.NoError(t, fileutil.AppendJSONL(transcriptPath, TranscriptEntry{
		ID: fx.historicalUserID, Role: "user", AgentID: "agent-1",
		Content: "historical user line, no paired save", Timestamp: now,
	}), "the historical user line must land")
}

// transcriptLineByID returns the raw transcript.jsonl line carrying entryID.
func transcriptLineByID(t *testing.T, store *UnifiedStore, sessionID, entryID string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"))
	require.NoError(t, err, "the transcript must be readable")
	for _, line := range strings.Split(string(raw), "\n") {
		var entry TranscriptEntry
		if json.Unmarshal([]byte(line), &entry) == nil && entry.ID == entryID {
			return line
		}
	}
	t.Fatalf("no transcript line carries id %q", entryID)
	return ""
}

// TestReadTranscript_ReopenedStoreHidesUnpairedProvenanceResidue proves the
// abrupt-exit rule at the real write cuts against a reopened store: the
// residue line is absent from the read path while every other line — and the
// accepted marked line — stays visible, and a live paired append on the
// reopened store is immediately visible with its marker cleared on disk.
func TestReadTranscript_ReopenedStoreHidesUnpairedProvenanceResidue(t *testing.T) {
	baseDir := t.TempDir()
	store, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "NewUnifiedStore must succeed")
	meta, err := store.NewSession(SessionTypeChat, "webchat", "agent-1")
	require.NoError(t, err, "NewSession must succeed")
	sessionID := meta.ID

	fx := reopenResidueFixture{
		residueID:        "residue-crash-cut-one",
		acceptedID:       "accepted-crash-cut-two",
		agentID:          "agent-line-ordinary",
		historicalUserID: "historical-user-line",
	}
	writeReopenResidue(t, store, sessionID, fx)

	// The residue is really on disk before the crash is simulated — the rule
	// hides reads, never bytes.
	residueLine := transcriptLineByID(t, store, sessionID, fx.residueID)
	require.Contains(t, residueLine, `"provenance_pending":true`,
		"control: the residue line is on disk carrying the write-phase marker")

	// The abrupt exit: close the store and REOPEN it on the same files.
	require.NoError(t, store.Close(), "closing the store must succeed")
	reopened, err := NewUnifiedStore(baseDir)
	require.NoError(t, err, "reopening the store on the same files must succeed")
	t.Cleanup(func() { _ = reopened.Close() })

	entries, err := reopened.ReadTranscript(sessionID)
	require.NoError(t, err, "the reopened store must read the transcript")

	seen := make(map[string]TranscriptEntry, len(entries))
	for _, entry := range entries {
		seen[entry.ID] = entry
	}

	_, present := seen[fx.residueID]
	require.False(t, present,
		"the residue line (marker, no sender record) must be absent from the read path on reopen")

	accepted, present := seen[fx.acceptedID]
	require.True(t, present,
		"the marked line whose record DID land is an accepted message and must stay visible")
	require.True(t, accepted.ProvenancePending,
		"control: the accepted line still carries its marker — the record, not the marker alone, decides visibility")

	_, present = seen[fx.agentID]
	require.True(t, present,
		"an ordinary agent line (never had a sender record) must stay visible")

	_, present = seen[fx.historicalUserID]
	require.True(t, present,
		"a historical user line that was not part of a paired save must stay visible")

	// The residue's own record still does not exist — nothing was admitted.
	_, found, err := reopened.LookupMessageProvenance(sessionID, fx.residueID)
	require.NoError(t, err)
	require.False(t, found, "no sender record may exist for the residue id")

	// The residue bytes remain on disk as inert residue — hiding is a read
	// rule, not a deletion.
	require.Contains(t, transcriptLineByID(t, reopened, sessionID, fx.residueID), `"provenance_pending":true`,
		"the residue bytes must remain on disk — the rule hides the read path, not the bytes")

	// A live paired append on the reopened store is the retry the person
	// makes: it must be visible immediately and its marker must be cleared
	// from disk once the record lands.
	live := TranscriptEntry{
		ID: "live-retry-after-reopen", Role: "user", AgentID: "agent-1",
		Content: "retry after the crash", Timestamp: time.Now().UTC(),
	}
	require.NoError(t, reopened.AppendTranscriptWithProvenance(sessionID, live, "alice"),
		"the live paired append must succeed")
	_, present = seenLive(t, reopened, sessionID, live.ID)
	require.True(t, present, "the live accepted message must be visible on the read path")
	require.NotContains(t, transcriptLineByID(t, reopened, sessionID, live.ID), `"provenance_pending"`,
		"the accepted message's marker must be cleared from disk once its record lands")
	_, found, err = reopened.LookupMessageProvenance(sessionID, live.ID)
	require.NoError(t, err)
	require.True(t, found, "the live message's sender record must exist")
}

// seenLive reads the transcript fresh (the fixture's `seen` map predates the
// live append) and reports whether the entry is present.
func seenLive(t *testing.T, store *UnifiedStore, sessionID, entryID string) (TranscriptEntry, bool) {
	t.Helper()
	entries, err := store.ReadTranscript(sessionID)
	require.NoError(t, err, "the post-append read must succeed")
	for _, entry := range entries {
		if entry.ID == entryID {
			return entry, true
		}
	}
	return TranscriptEntry{}, false
}

package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// pwLegacyRestartLanding reads the real retained landing, not the post-resume
// tail. PW-D6 (founder 2026-10-15): boot resumes native helpers, so the current
// record is no longer the restart stop. The spec's §16 keeps that durable transition.
// This preserves legacy execution/epoch/stop-note assertions without requiring
// the helper to remain stopped after a successful resume.
func pwLegacyRestartLanding(t *testing.T, al *AgentLoop, sessionID string, bootSeq uint64) *session.LifecycleRecord {
	t.Helper()
	path := filepath.Join(al.GetSessionLifecycleStore().Dir(), sessionID+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read real lifecycle journal: %v", err)
	}
	for _, line := range jsonLines(raw) {
		var rec session.LifecycleRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("decode lifecycle journal: %v", err)
		}
		if rec.State == session.LifecycleStopped && rec.StopNote != nil &&
			rec.StopNote.Cause == session.StopCauseRestart && rec.StopNote.BootSeq == bootSeq {
			return &rec
		}
	}
	t.Fatalf("session %s has no durable restart landing in boot %d (PW-D6/§16)", sessionID, bootSeq)
	return nil
}

// pwLegacyRequireSavedMetInput keeps the spec oracle independent of the old
// ack-at-publish mechanism. PW-D16 (spec §22, 2026-10-15): acknowledgment requires
// the exact combined input to exist as chat membership, not early model input.
func pwLegacyRequireSavedMetInput(t *testing.T, al *AgentLoop, parentID, childID string, generation int, pieces ...string) {
	t.Helper()
	id := fmt.Sprintf("saved-input-%s:%d:final", childID, generation)
	entries, err := al.GetSessionStore().ReadTranscript(parentID)
	if err != nil {
		t.Fatalf("ReadTranscript(parent saved input): %v", err)
	}
	var saved []session.TranscriptEntry
	for _, entry := range entries {
		if entry.ID == id {
			saved = append(saved, entry)
		}
	}
	if len(saved) != 1 {
		t.Fatalf("saved input %s count=%d, want exactly 1 before verdict ack (PW-D16)", id, len(saved))
	}
	if saved[0].ViewMembership != session.ViewMembershipChat {
		t.Errorf("saved input membership=%q, want chat (PW-D16)", saved[0].ViewMembership)
	}
	for _, piece := range pieces {
		if !strings.Contains(saved[0].Content, piece) {
			t.Errorf("combined saved input=%q, missing %q (PW-D16)", saved[0].Content, piece)
		}
	}
}

// ADR-057-U18-inverted: this file pinned the PRE-ADR-057 contract — REST
// cold-load session reads withheld delegation-child transcript entries
// (ParentSpawnCallID != "") exactly like the live-reconnect replay path did.
// D1/W11 (FR-034/FR-035/FR-038) retires that filter outright: a delegated
// child now owns its own real store-backed session (FR-005), so under the
// post-cutover design a child's own entries never land in another session's
// transcript to begin with — but for a PRE-cutover transcript (or any
// transcript carrying a legacy ParentSpawnCallID-tagged entry), no read
// boundary may reintroduce a visibility filter (FR-038, BDD-40: "a
// pre-cutover session shows previously-hidden delegate narration" is the
// accepted outcome, not a bug). This file is inverted, not deleted
// (FR-072/US-17 AS-1) — the assertions below now require BOTH entries to be
// returned unfiltered, matching BDD-37's "each read boundary returns the
// child's transcript unfiltered" for every one of the four read boundaries
// this suite already covered.
//
// Traces to: pkg/gateway/rest.go (getSession/getSessionMessages, U18),
// pkg/session/daypartition.go TranscriptEntry.ParentSpawnCallID (retained
// for provenance, FR-036) — the retired IsDelegateChildEntry predicate this
// file originally named no longer exists (FR-034).

package gateway

import (
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// seedParentAndChildTranscript appends one genuine top-level ("parent")
// assistant entry and one legacy delegation-child-shaped entry
// (ParentSpawnCallID set) to the given session, mirroring the shape a
// pre-cutover spawnSubTurn/turn.go produced: the child carries the same
// Role/Content/AgentID/TurnID/Model shape as any other assistant entry,
// distinguishable only by ParentSpawnCallID (now provenance-only, FR-036).
func seedParentAndChildTranscript(t *testing.T, store *session.UnifiedStore, sessionID string) {
	t.Helper()

	const spawnCallID = "delegate-call-1"

	parentEntry := session.TranscriptEntry{
		ID:        "parent-msg-1",
		Role:      "assistant",
		Content:   "I'll delegate this research task.",
		Timestamp: time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC),
		AgentID:   "jim",
	}
	childEntry := session.TranscriptEntry{
		ID:                "child-msg-1",
		Role:              "assistant",
		Content:           "[external-cli permission] requesting read access to /tmp/report.md",
		Timestamp:         time.Date(2026, 7, 18, 10, 0, 5, 0, time.UTC),
		AgentID:           "researcher",
		Model:             "z-ai/glm-5.2",
		TurnID:            "child-turn-1",
		ParentSpawnCallID: spawnCallID,
	}

	require.NoError(t, store.AppendTranscript(sessionID, parentEntry),
		"seeding the parent entry must succeed")
	require.NoError(t, store.AppendTranscript(sessionID, childEntry),
		"seeding the delegation-child-shaped entry must succeed")
}

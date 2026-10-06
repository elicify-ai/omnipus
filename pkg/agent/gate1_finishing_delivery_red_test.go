package agent

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// F8: amended ADR-20260928 D-D/D4. A healthy first post-commit steer
// must use its accepted identity, not a random transcript entry bearing the
// same text. The later item is a positive control for the ordinary consumer.
func TestGate1Finishing_FirstSteerKeepsAcceptedTranscriptIdentity(t *testing.T) {
	f := gate1CommittedFinishing(t)
	first := f.accept(t, "First accepted post-commit instruction.\nKeep its identity.", "gate1-finishing-first")
	second := f.accept(t, "Second accepted post-commit instruction.", "gate1-finishing-second")
	f.publication.open()
	if err := f.completionError(t); err != nil {
		t.Fatalf("healthy real post-commit completion returned an error: %v", err)
	}
	f.provider.openAll()
	joinGoalFixtureRuns(t, f.al)
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{
		first.ControlID: "delivered", second.ControlID: "delivered",
	})
	entries := qaReceiptReopenTranscript(t, f.al.ResolveSessionStore(f.child.SessionID), f.child.SessionID)
	for i, accepted := range []qaReceiptLine{first, second} {
		// Exact ID follows D4's accepted identity and the normal injected-entry
		// contract. It is not derived from an observed random entry.
		wantID := "instruction-" + accepted.ControlID
		identityMatches, textMatches := 0, 0
		for _, entry := range entries {
			if entry.Content == accepted.Text && entry.Role == "user" {
				textMatches++
			}
			if entry.ID != wantID {
				continue
			}
			identityMatches++
			if entry.Role != "user" || entry.AgentID != testDefaultAgentID || entry.Content != accepted.Text || entry.Timestamp.IsZero() {
				t.Errorf("F8: instruction %d durable accepted entry=%+v, want exact original user text/agent and a saved timestamp", i+1, entry)
			}
		}
		if identityMatches != 1 || textMatches != 1 {
			t.Errorf("F8: instruction %d delivered receipt has %d transcript entries with accepted ID %q and %d exact user-text copies; want exactly one identity-bearing copy", i+1, identityMatches, wantID, textMatches)
		}
	}
	if depth := f.al.pendingSteeringCountForScope(f.child.SessionID); depth != 0 {
		t.Errorf("healthy finishing delivery left %d pending items, want zero", depth)
	}
	current := rootReopenedRecord(t, f.al, f.child.SessionID)
	if current.Generation != f.child.Generation+1 || current.State != session.LifecycleCompleted {
		t.Errorf("post-commit inputs did not finish exactly the next round: state=%q G=%d, want completed G=%d", current.State, current.Generation, f.child.Generation+1)
	}
}

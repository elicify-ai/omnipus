// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — helper report class (FR-012, BDD-04.1/04.3, T06).
//
// RED pack, qa-lead. Oracle source: docs/internal/specs/session-core-spec.md
// FR-012 ("All accepted helper report kinds MUST use existing inbox/dedupe/ack/
// consumption, wake idle non-stopped parent ... Expanded wake eligibility
// cannot widen preserved rate/count exemptions.") and BDD-04.1 ("Kinds:
// progress/checkpoint/artifact/question/blocker/handback-final/engine
// lifecycle ... Wake expansion does not widen cap exemptions.").
//
// No expected value here is read off the implementation: the accepted-kind set
// and the exemption rule come from the spec text above.
//
// Q4 (ARCHITECT-ANSWER-U4-GAPS): decision_request stays OUT of U4 — it is not a
// message_parent kind and not in BDD-04.1's accepted set. It is deliberately
// not asserted here.

package session

import (
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// u4ProgressMessage / u4CheckpointMessage / u4ArtifactMessage build one message
// of each newly-wake-eligible accepted kind. Field shapes mirror the generated
// structs the spec cites (contracts/components/schemas/SessionMessage*.yaml).
func u4ProgressMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageProgress(generated.SessionMessageProgress{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: "working on it",
	}); err != nil {
		t.Fatalf("FromSessionMessageProgress: %v", err)
	}
	return sm
}

func u4CheckpointMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageCheckpoint(generated.SessionMessageCheckpoint{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Summary: "halfway",
	}); err != nil {
		t.Fatalf("FromSessionMessageCheckpoint: %v", err)
	}
	return sm
}

func u4ArtifactMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	note := "draft report"
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageArtifact(generated.SessionMessageArtifact{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Paths: []string{"reports/summary.md"}, Note: &note,
	}); err != nil {
		t.Fatalf("FromSessionMessageArtifact: %v", err)
	}
	return sm
}

func u4BlockerMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageBlocker(generated.SessionMessageBlocker{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: "blocked", Severity: generated.SessionMessageBlockerSeverityHigh,
	}); err != nil {
		t.Fatalf("FromSessionMessageBlocker: %v", err)
	}
	return sm
}

// TestClassifySessionMessage_AcceptedReportKindsWakeEligible pins FR-012's
// wake-eligibility set against the spec's accepted-kind list (BDD-04.1):
// progress/checkpoint/artifact must be wake-eligible, exactly like the already
// eligible question/blocker/handback-final/engine lifecycle kinds. It also
// pins that the already-eligible kinds did not lose eligibility.
//
// RED today: progress, checkpoint and artifact all classify WakeEligible=false
// (classifyEnvelope), so an accepted report to an idle parent is stored but
// never wakes it (FR-012 "wake idle non-stopped parent").
func TestClassifySessionMessage_AcceptedReportKindsWakeEligible(t *testing.T) {
	const childID = "child-1"

	wantEligible := []struct {
		name string
		msg  generated.SessionMessage
	}{
		{"progress", u4ProgressMessage(t, childID, "m-progress")},
		{"checkpoint", u4CheckpointMessage(t, childID, "m-checkpoint")},
		{"artifact", u4ArtifactMessage(t, childID, "m-artifact")},
		{"blocker (already eligible)", u4BlockerMessage(t, childID, "m-blocker")},
	}
	for _, tc := range wantEligible {
		class, err := ClassifySessionMessage(tc.msg)
		if err != nil {
			t.Fatalf("ClassifySessionMessage(%s): %v", tc.name, err)
		}
		if !class.WakeEligible {
			t.Fatalf("kind %s: WakeEligible = false, want true — FR-012 requires every accepted helper "+
				"report kind to wake an idle non-stopped parent (BDD-04.1)", tc.name)
		}
	}

	// Control (spec: "engine lifecycle" is accepted; an ordinary non-fatal
	// error that is NOT a lifecycle notice is not). This half is expected green
	// on the pre-change code — it guards that the expansion did not over-widen.
	var nonNotice generated.SessionMessage
	if err := nonNotice.FromSessionMessageError(generated.SessionMessageError{
		MessageId: "m-error", SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: "some ordinary error", Fatal: false,
	}); err != nil {
		t.Fatalf("FromSessionMessageError: %v", err)
	}
	class, err := ClassifySessionMessage(nonNotice)
	if err != nil {
		t.Fatalf("ClassifySessionMessage(error): %v", err)
	}
	if class.WakeEligible {
		t.Fatalf("non-fatal ordinary error: WakeEligible = true, want false — the expansion must not " +
			"make every error wake the parent")
	}
}

// TestAppend_WakeExpansionDoesNotWidenCaps pins the second half of FR-012:
// "Expanded wake eligibility cannot widen preserved rate/count exemptions."
// A wake-eligible kind still bypasses the unacked cap and the rate cap (the
// existing preserved exemption, unchanged); the newly accepted kinds
// (progress) must NOT gain that bypass.
//
// This is a regression pin, not a RED witness: on the pre-change code progress
// is not wake-eligible, so it is already subject to both caps and this test is
// green. It exists so the GREEN change that makes progress wake-eligible cannot
// silently carry the cap exemption along with the wake (the single line in
// Append that couples them).
func TestAppend_WakeExpansionDoesNotWidenCaps(t *testing.T) {
	const ownerKey, childID = "parent-1", "child-1"

	t.Run("progress still rate-limited", func(t *testing.T) {
		store := NewMessageInboxStore(t.TempDir())
		store.ChildSendRatePerMinute = 1
		store.InboxUnackedMax = 200

		if _, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-1")); err != nil {
			t.Fatalf("first progress Append: %v", err)
		}
		_, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-2"))
		if !errors.Is(err, ErrInboxRateLimited) {
			t.Fatalf("second progress Append error = %v, want ErrInboxRateLimited — progress must stay "+
				"subject to the preserved rate cap after the wake expansion (FR-012)", err)
		}
	})

	t.Run("progress still bounded by the unacked cap", func(t *testing.T) {
		store := NewMessageInboxStore(t.TempDir())
		store.ChildSendRatePerMinute = 200
		store.InboxUnackedMax = 1

		if _, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-1")); err != nil {
			t.Fatalf("first progress Append: %v", err)
		}
		_, err := store.Append(ownerKey, u4ProgressMessage(t, childID, "p-2"))
		if !errors.Is(err, ErrInboxSessionFull) {
			t.Fatalf("second progress Append error = %v, want ErrInboxSessionFull — progress must stay "+
				"subject to the preserved unacked cap after the wake expansion (FR-012)", err)
		}
	})

	t.Run("wake-eligible kind keeps its bypass", func(t *testing.T) {
		store := NewMessageInboxStore(t.TempDir())
		store.ChildSendRatePerMinute = 1
		store.InboxUnackedMax = 1

		// A blocker is already wake-eligible and must keep its preserved
		// exemption at the same full caps that refuse progress above.
		if _, err := store.Append(ownerKey, u4BlockerMessage(t, childID, "b-1")); err != nil {
			t.Fatalf("blocker Append at full caps: %v, want an exempt (successful) Append", err)
		}
	})
}

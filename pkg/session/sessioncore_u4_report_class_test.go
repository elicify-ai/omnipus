// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session-core U4 — helper report class (FR-012, BDD-04.1/04.3, T06).
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md, PW-D4,
// FR-011/012/026 and §9.8; §16 narrows the earlier session-core wake expansion.
// PW-D4 (founder 2026-10-15): progress, checkpoint and artifact never wake.
// Accepted kinds still use the existing inbox/dedupe/ack/consumption path;
// the preserved rate/count exemptions do not expand.
//
// No expected value here is read off the implementation.
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
// of each accepted non-waking chatter kind. Field shapes mirror the generated
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

// TestClassifySessionMessage_AcceptedReportKindsWakeEligible keeps the accepted
// report classes covered while separating inbox admission from wake eligibility.
// PW-D4 (founder 2026-10-15): chatter never wakes; a blocker still does.
func TestClassifySessionMessage_AcceptedReportKindsWakeEligible(t *testing.T) {
	const childID = "child-1"

	cases := []struct {
		name         string
		msg          generated.SessionMessage
		wakeEligible bool
	}{
		{"progress", u4ProgressMessage(t, childID, "m-progress"), false},
		{"checkpoint", u4CheckpointMessage(t, childID, "m-checkpoint"), false},
		{"artifact", u4ArtifactMessage(t, childID, "m-artifact"), false},
		{"blocker (still eligible)", u4BlockerMessage(t, childID, "m-blocker"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, err := ClassifySessionMessage(tc.msg)
			if err != nil {
				t.Fatalf("ClassifySessionMessage(%s): %v", tc.name, err)
			}
			if class.WakeEligible != tc.wakeEligible {
				t.Errorf("kind %s: WakeEligible = %t, want %t — PW-D4 separates chatter from outcomes and questions",
					tc.name, class.WakeEligible, tc.wakeEligible)
			}
		})
	}

	// PW-D4 (founder 2026-10-15): an ordinary non-fatal error does not wake.
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

// TestAppend_WakeExpansionDoesNotWidenCaps retains the original cap guarantees.
// PW-D4 (founder 2026-10-15): progress remains accepted but capped and never wakes.
// The parent-wake spec's FR-012 keeps both progress caps and the existing blocker
// exemption; narrowing wake eligibility must not change inbox admission.
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

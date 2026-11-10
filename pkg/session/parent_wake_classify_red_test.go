// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for parent-wake visibility, Test 1.
//
// Oracle: docs/internal/specs/parent-wake-visibility-spec.md
//   - Machine-Verifiable Constraints ("Caps that stay", wake reasons)
//   - §9.8 Progress
//   - FR-011, FR-012, FR-026
//   - BDD "Chatter is stored and shown and does not wake"
//   - BDD "A progress line over the inbox cap wakes nobody"
//   - Planned test TestClassifyEnvelope_ProgressCheckpointArtifactDoNotWake
//
// A stopped_child: prefix is not, by itself, a wake. max_tool_iterations: still is.
// Hand-back, question, blocker and goal status stay wake-eligible.
// Progress, checkpoint and artifact are not wake-eligible and not cap-exempt.
//
// Step 4 items 2–3 (green, mutation) are deferred to CHECK.
package session

import (
	"errors"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestClassifyEnvelope_ProgressCheckpointArtifactDoNotWake(t *testing.T) {
	const childID = "child-pw-classify"

	t.Run("progress checkpoint and artifact are not wake-eligible and not cap-exempt", func(t *testing.T) {
		cases := []struct {
			name string
			msg  generated.SessionMessage
		}{
			{"progress", pwProgressMessage(t, childID, "m-progress")},
			{"checkpoint", pwCheckpointMessage(t, childID, "m-checkpoint")},
			{"artifact", pwArtifactMessage(t, childID, "m-artifact")},
		}
		for _, tc := range cases {
			class, err := ClassifySessionMessage(tc.msg)
			if err != nil {
				t.Fatalf("ClassifySessionMessage(%s): %v", tc.name, err)
			}
			if class.WakeEligible {
				t.Errorf("kind %s: WakeEligible = true, want false — progress, checkpoint and artifact do not wake (PW-D4 / FR-011 / §9.8)", tc.name)
			}
			if class.CapExempt {
				t.Errorf("kind %s: CapExempt = true, want false — those three stay under the inbox caps (FR-012)", tc.name)
			}
		}
	})

	t.Run("hand-back question blocker and goal status stay wake-eligible", func(t *testing.T) {
		cases := []struct {
			name string
			msg  generated.SessionMessage
		}{
			{"handback", pwHandbackMessage(t, childID, "m-handback")},
			{"question", pwQuestionMessage(t, childID, "m-question", "which file?")},
			{"blocker", pwBlockerMessage(t, childID, "m-blocker", "need a decision")},
			{"goal_status", pwGoalStatusMessage(t, childID, "m-goal")},
		}
		for _, tc := range cases {
			class, err := ClassifySessionMessage(tc.msg)
			if err != nil {
				t.Fatalf("ClassifySessionMessage(%s): %v", tc.name, err)
			}
			if !class.WakeEligible {
				t.Errorf("kind %s: WakeEligible = false, want true — endings, questions and blockers still wake (PW-D4 / FR-025)", tc.name)
			}
		}
	})

	t.Run("stopped_child prefix does not wake and max_tool_iterations still does", func(t *testing.T) {
		stopped := pwErrorMessage(t, childID, "m-stopped", false, LifecycleNoticePrefixStoppedChild+" cause: stop")
		class, err := ClassifySessionMessage(stopped)
		if err != nil {
			t.Fatalf("ClassifySessionMessage(stopped_child): %v", err)
		}
		if class.WakeEligible {
			t.Errorf("stopped_child: prefix: WakeEligible = true, want false — the prefix alone is not a wake (PW-D13 / Machine-Verifiable Constraints)")
		}

		ceiling := pwErrorMessage(t, childID, "m-ceiling", false, LifecycleNoticeReasonPrefixToolIterations+" 10")
		class, err = ClassifySessionMessage(ceiling)
		if err != nil {
			t.Fatalf("ClassifySessionMessage(max_tool_iterations): %v", err)
		}
		if !class.WakeEligible {
			t.Errorf("max_tool_iterations: WakeEligible = false, want true — a tool-iteration ceiling stays a failure wake, not a Stop")
		}

		ordinary := pwErrorMessage(t, childID, "m-ordinary", false, "disk full")
		class, err = ClassifySessionMessage(ordinary)
		if err != nil {
			t.Fatalf("ClassifySessionMessage(ordinary): %v", err)
		}
		if class.WakeEligible {
			t.Errorf("ordinary non-fatal error: WakeEligible = true, want false (FR-026)")
		}

		fatal := pwErrorMessage(t, childID, "m-fatal", true, "the helper failed")
		class, err = ClassifySessionMessage(fatal)
		if err != nil {
			t.Fatalf("ClassifySessionMessage(fatal): %v", err)
		}
		if !class.WakeEligible {
			t.Errorf("fatal error: WakeEligible = false, want true (FR-025)")
		}
	})

	t.Run("a progress line over the rate cap is refused and not stored", func(t *testing.T) {
		const ownerKey = "parent-pw-rate"
		store := NewMessageInboxStore(t.TempDir())
		store.ChildSendRatePerMinute = 1
		store.InboxUnackedMax = 200
		if _, err := store.Append(ownerKey, pwProgressMessage(t, childID, "p-1")); err != nil {
			t.Fatalf("first progress Append: %v", err)
		}
		_, err := store.Append(ownerKey, pwProgressMessage(t, childID, "p-2"))
		if !errors.Is(err, ErrInboxRateLimited) {
			t.Fatalf("second progress Append error = %v, want ErrInboxRateLimited — the refusal is visible to the sender (FR-012)", err)
		}
		msgs, _, _, derr := store.Drain(ownerKey, childID, "", 10)
		if derr != nil {
			t.Fatalf("Drain: %v", derr)
		}
		if len(msgs) != 1 {
			t.Fatalf("stored progress lines = %d, want 1 — the refused line is not stored and cannot wake anyone", len(msgs))
		}
	})

	t.Run("a progress line over the unacked cap is refused and not stored", func(t *testing.T) {
		const ownerKey = "parent-pw-unacked"
		store := NewMessageInboxStore(t.TempDir())
		store.ChildSendRatePerMinute = 200
		store.InboxUnackedMax = 1
		if _, err := store.Append(ownerKey, pwProgressMessage(t, childID, "u-1")); err != nil {
			t.Fatalf("first progress Append: %v", err)
		}
		_, err := store.Append(ownerKey, pwProgressMessage(t, childID, "u-2"))
		if !errors.Is(err, ErrInboxSessionFull) {
			t.Fatalf("second progress Append error = %v, want ErrInboxSessionFull (FR-012)", err)
		}
		msgs, _, _, derr := store.Drain(ownerKey, childID, "", 10)
		if derr != nil {
			t.Fatalf("Drain: %v", derr)
		}
		if len(msgs) != 1 {
			t.Fatalf("stored progress lines = %d, want 1 — the refused line is not stored", len(msgs))
		}
	})
}

func pwProgressMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
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

func pwCheckpointMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
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

func pwArtifactMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
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

func pwHandbackMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageHandback(generated.SessionMessageHandback{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Mode: generated.SessionMessageHandbackModeFinal,
		ResultSoFar: "the answer", Artifacts: []string{}, OpenQuestions: []string{},
	}); err != nil {
		t.Fatalf("FromSessionMessageHandback: %v", err)
	}
	return sm
}

func pwQuestionMessage(t *testing.T, childID, messageID, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageQuestion(generated.SessionMessageQuestion{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", CorrelationId: "corr-pw", Text: text,
	}); err != nil {
		t.Fatalf("FromSessionMessageQuestion: %v", err)
	}
	return sm
}

func pwBlockerMessage(t *testing.T, childID, messageID, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageBlocker(generated.SessionMessageBlocker{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: text, Severity: generated.SessionMessageBlockerSeverityHigh,
	}); err != nil {
		t.Fatalf("FromSessionMessageBlocker: %v", err)
	}
	return sm
}

func pwGoalStatusMessage(t *testing.T, childID, messageID string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageGoalStatus(generated.SessionMessageGoalStatus{
		Kind: generated.SessionMessageGoalStatusKindGoalStatus, MessageId: messageID, SessionId: childID,
		SenderIdentity: "judge", CreatedAt: time.Now().UTC(), Depth: 1, GoalId: "goal-pw",
		Condition: generated.SessionMessageGoalStatusConditionMet,
		Direction: generated.SessionMessageGoalStatusDirectionSessionToParent,
	}); err != nil {
		t.Fatalf("FromSessionMessageGoalStatus: %v", err)
	}
	return sm
}

func pwErrorMessage(t *testing.T, childID, messageID string, fatal bool, text string) generated.SessionMessage {
	t.Helper()
	var sm generated.SessionMessage
	if err := sm.FromSessionMessageError(generated.SessionMessageError{
		MessageId: messageID, SessionId: childID, CreatedAt: time.Now().UTC(), Depth: 1,
		SenderIdentity: "worker", Text: text, Fatal: fatal,
	}); err != nil {
		t.Fatalf("FromSessionMessageError: %v", err)
	}
	return sm
}

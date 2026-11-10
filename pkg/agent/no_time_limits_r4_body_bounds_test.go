package agent

// PR #1311 ROUND 4 ADDED G12. The trusted COMPLETION TAIL may deliver a
// final bounded by SessionMessageHandback's 50,000-character contract. A
// child-originated message_parent call keeps its 32-KiB child-send bound;
// a final-shaped message_id must grant no larger authority. All sends below
// traverse real Launch/Dispatch and the wired production tools/stores.
// CHECK: apply child cap to system final; bypass cap by ID suffix or handback
// kind; reject an under-cap child report.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func r4SystemFinalFortyKiB(t *testing.T) {
	const prefix = "SYSTEM-FINAL-R4: the completed analysis must survive the completion tail. "
	const size = 40 * 1024 // above the 32-KiB child cap, below the 50,000-character final contract
	answer := prefix + strings.Repeat("x", size-len(prefix))
	h := newR4DelegateHarness(t, &claimingWorkerProvider{content: answer})
	child := h.launch(t, false) // NON-goal child isolates this defect from evidence-as-answer
	h.dispatch(t, child)
	h.al.steerAdmission().turns.Wait()
	messages, _, _, err := h.al.GetMessageInboxStore().Drain(h.parentID, child.SessionID, "", 0)
	if err != nil {
		t.Fatalf("read parent's actual completion inbox: %v", err)
	}
	finals := 0
	for _, message := range messages {
		if final, decodeErr := message.AsSessionMessageHandback(); decodeErr == nil &&
			final.Mode == generated.SessionMessageHandbackModeFinal {
			finals++
			if final.ResultSoFar != answer {
				t.Errorf("G12 system-authored 40-KiB final was altered below the contract cap: got %d chars, want the entire %d-character answer", len(final.ResultSoFar), len(answer))
			}
		}
	}
	if finals != 1 {
		t.Errorf("G12 system completion tail dropped the 40-KiB answer: parent received %d final handbacks, want exactly one containing the substantive answer (never nothing)", finals)
	}
}

func r4ChildBodyCapControl(t *testing.T, overCap, finalShapedID bool) {
	const childCap = 32 * 1024 // accepted G12 decision: child message_parent still has this byte ceiling
	const prefix = "CHILD-REPORT-R4: explicit child-originated report. "
	// Reserve 1 KiB for the real message's JSON envelope in the accepted case.
	// The over-cap text alone exceeds the bound before any envelope is added.
	size := childCap - 1024
	if overCap {
		size = childCap + 1
	}
	body := prefix + strings.Repeat("y", size-len(prefix))
	provider := testutil.NewScenario()
	h := newR4DelegateHarness(t, provider)
	child := h.launch(t, false)
	messageID := "r4-explicit-child-report"
	if finalShapedID {
		messageID = fmt.Sprintf("%s:%d:final", child.SessionID, child.Generation)
	}
	args := fmt.Sprintf(`{"kind":"handback","mode":"final","message_id":%q,"result_so_far":%q}`, messageID, body)
	provider.WithToolCall("message_parent", args).WithText("The child-send cap probe has completed.")
	h.dispatch(t, child)
	h.al.steerAdmission().turns.Wait()
	if provider.CallCount() != 2 {
		t.Fatalf("CONTROL real child send did not execute its tool and final response: calls=%d, want 2", provider.CallCount())
	}
	results := []string{}
	for _, request := range provider.AllRequests() {
		for _, message := range request {
			if message.Role == "tool" {
				results = append(results, message.Content)
			}
		}
	}
	if len(results) != 1 {
		t.Fatalf("CONTROL child observed %d actual tool results, want one: %q", len(results), results)
	}
	result := results[0]
	if overCap {
		if !strings.Contains(result, "reason=body_too_large") || !strings.Contains(result, session.ErrInboxBodyTooLarge.Error()) {
			t.Errorf("G12 child-send control bypassed the 32-KiB cap (final-shaped ID=%v): result=%q, want the specific body_too_large refusal", finalShapedID, result)
		}
	} else if !strings.Contains(result, `"accepted":true`) || strings.Contains(result, "body_too_large") {
		t.Errorf("G12 under-cap child handback was refused: %q", result)
	}
	messages, _, _, err := h.al.GetMessageInboxStore().Drain(h.parentID, child.SessionID, "", 0)
	if err != nil {
		t.Fatalf("CONTROL inspect actually saved parent reports: %v", err)
	}
	foundBody := false
	for _, message := range messages {
		if final, decodeErr := message.AsSessionMessageHandback(); decodeErr == nil && final.ResultSoFar == body {
			foundBody = true
		}
	}
	if overCap && foundBody {
		t.Error("G12 oversized child-originated handback was saved despite its refusal/cap")
	}
	if !overCap && !foundBody {
		t.Error("G12 accepted under-cap child handback never reached the parent's durable inbox")
	}
}

func TestR4CompletionBodyBounds_SystemFinalUsesContractCapButChildCannotSpoofIt(t *testing.T) {
	t.Run("non_goal_system_final_40_KiB_is_delivered", r4SystemFinalFortyKiB)
	t.Run("oversized_child_handback_is_refused", func(t *testing.T) { r4ChildBodyCapControl(t, true, false) })
	t.Run("final_shaped_child_message_ID_does_not_bypass_cap", func(t *testing.T) { r4ChildBodyCapControl(t, true, true) })
	t.Run("under_cap_child_handback_is_still_delivered", func(t *testing.T) { r4ChildBodyCapControl(t, false, false) })
}

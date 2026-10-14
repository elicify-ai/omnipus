package agent

// PR #1311 ROUND 4 G9: consuming a completion message changes delivery
// bookkeeping, not access to the completed child's work. Exercise the wired
// delegate tool through its real registry/schema/ownership path, not private
// executeStatus or an inbox double. Accept status or a published result read.
// CHECK: replace completed result with 'final already delivered'; delete the
// retained answer; let an unrelated session retrieve the completed result.

import (
	"encoding/json"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func r4AdvertisedResultReadActions(t *testing.T, h *r4DelegateHarness) []string {
	t.Helper()
	tool, ok := h.agent.Tools.Get("delegate")
	if !ok {
		t.Fatal("SETUP delegate must be registered")
	}
	// Read the actual public schema. We do not require the implementer to add
	// a new result action when returning the answer from status satisfies G9.
	body, err := json.Marshal(tool.Parameters())
	if err != nil {
		t.Fatalf("read delegate's public schema: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatalf("decode delegate's public action enum: %v", err)
	}
	actions := []string{"status"}
	for _, name := range schema.Properties["action"].Enum {
		if strings.Contains(name, "result") && name != "status" {
			actions = append(actions, name)
		}
	}
	return actions
}

func r4CompletedAnswerStillReadable(t *testing.T) {
	const answer = "RESULT-READ-R4: Completed analysis: the worker stored three concrete findings, a validated correction, and the acceptance evidence. This is the final answer, not a delivery flag."
	h := newR4DelegateHarness(t, &claimingWorkerProvider{content: answer})
	child := h.launch(t, false) // isolate result-read from the goal/evidence handback defect
	h.dispatch(t, child)
	h.al.steerAdmission().turns.Wait()
	if rec := mustLoadRec(t, h.al, child.SessionID); rec.State != session.LifecycleCompleted {
		t.Fatalf("SETUP child did not actually complete: %+v", rec)
	}
	handback := r4FinalHandback(t, h, child.SessionID)
	if handback.ResultSoFar != answer {
		t.Fatalf("CONTROL real completion handback=%q, want original answer=%q", handback.ResultSoFar, answer)
	}

	// Consume the final via the real parent-facing tool. Its production
	// final-read hook persists the consumed marker and acknowledges the inbox.
	read := h.publicDelegate(map[string]any{"action": "inbox", "session_id": child.SessionID})
	if read.IsError {
		t.Fatalf("SETUP public delegate inbox refused its own completed child: %s", read.ForLLM)
	}
	var inbox generated.DelegateInboxResponse
	if err := json.Unmarshal([]byte(read.ContentForLLM()), &inbox); err != nil {
		t.Fatalf("decode real delegate inbox response: %v", err)
	}
	found := false
	for _, message := range inbox.Messages {
		if final, err := message.AsSessionMessageHandback(); err == nil && final.MessageId == handback.MessageId {
			found = true
			if final.ResultSoFar != answer {
				t.Fatalf("CONTROL consumed final answer=%q, want %q", final.ResultSoFar, answer)
			}
		}
	}
	if !found {
		t.Fatal("SETUP the parent's delegate inbox did not actually consume the final handback")
	}
	ack := h.publicDelegate(map[string]any{"action": "inbox_ack", "session_id": child.SessionID,
		"message_ids": []any{handback.MessageId}})
	if ack.IsError {
		t.Fatalf("SETUP final acknowledgement failed: %s", ack.ForLLM)
	}
	if count, err := h.al.GetMessageInboxStore().UnackedCount(h.parentID, child.SessionID); err != nil || count != 0 {
		t.Fatalf("SETUP completed child's final is still unconsumed: unacked=%d err=%v", count, err)
	}

	var results []string
	retrievable := false
	for _, action := range r4AdvertisedResultReadActions(t, h) {
		result := h.publicDelegate(map[string]any{"action": action, "session_id": child.SessionID})
		results = append(results, action+": "+result.ContentForLLM())
		if !result.IsError && strings.Contains(result.ContentForLLM(), answer) {
			retrievable = true
		}
	}
	if !retrievable {
		t.Errorf("G9 completed answer is not retrievable through the public delegate tool after final consumption: responses=%q, want the RESULT-READ-R4 answer text (not just final already delivered)", results)
	}
}

func TestR4DelegateResult_CompletedAnswerRemainsReadableAfterParentConsumesFinal(t *testing.T) {
	r4CompletedAnswerStillReadable(t)
}

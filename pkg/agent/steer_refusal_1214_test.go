package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// #1214 item 1: the text a caller sees for a refused steer is curated - no
// generation, stale, ledger or filesystem internals - while the cause stays
// reachable with errors.Is.
func TestSteerRefusal_1214_CallerSeesACuratedSentenceOnly(t *testing.T) {
	f := newQAReceiptFixture(t)
	forbidden := func(msg string) []string {
		var hit []string
		for _, bad := range []string{"generation", "stale", "ledger", "follow_up", f.al.GetSessionLifecycleStore().Dir(), ".jsonl", "control_id", "/var/", "/tmp"} {
			if strings.Contains(strings.ToLower(msg), strings.ToLower(bad)) {
				hit = append(hit, bad)
			}
		}
		return hit
	}

	// Refused because the helper already finished: the scope is closed.
	f.al.steering.mu.Lock()
	f.al.steering.closedGenerations[f.child.SessionID] = f.child.Generation
	f.al.steering.mu.Unlock()
	_, _, err := f.al.EnqueueSteeringMessageWithStatus(f.child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: "late instruction"}, "corr-1214-closed")
	if err == nil {
		t.Fatal("a steer to a finished helper must be refused")
	}
	if !strings.Contains(err.Error(), "already finished") {
		t.Errorf("refusal %q should say the helper already finished", err.Error())
	}
	if hits := forbidden(err.Error()); len(hits) != 0 {
		t.Errorf("refusal text %q leaks internals %v", err.Error(), hits)
	}

	f.al.steering.reopenScopeForGeneration(f.child.SessionID, f.child.Generation)

	// Refused because the control ledger cannot be written: the raw error
	// carries a filesystem path and ledger wording.
	controls := filepath.Join(f.al.GetSessionLifecycleStore().Dir(), "controls")
	if mkErr := os.MkdirAll(controls, 0o700); mkErr != nil {
		t.Fatalf("SETUP controls dir: %v", mkErr)
	}
	ledger := filepath.Join(controls, f.child.SessionID+".jsonl")
	if _, statErr := os.Stat(ledger); statErr != nil {
		if werr := os.WriteFile(ledger, nil, 0o600); werr != nil {
			t.Fatalf("SETUP ledger: %v", werr)
		}
	}
	if cerr := os.Chmod(ledger, 0o400); cerr != nil {
		t.Fatalf("SETUP chmod: %v", cerr)
	}
	t.Cleanup(func() { _ = os.Chmod(ledger, 0o600) })
	if fh, werr := os.OpenFile(ledger, os.O_WRONLY|os.O_APPEND, 0); werr == nil {
		fh.Close()
		t.Fatal("instrument check: the control ledger must really be unwritable (not root)")
	}
	_, _, err = f.al.EnqueueSteeringMessageWithStatus(f.child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: "instruction that cannot be recorded"}, "corr-1214-ledger")
	if err == nil {
		t.Fatal("a steer whose control ledger cannot be written must be refused")
	}
	if hits := forbidden(err.Error()); len(hits) != 0 {
		t.Errorf("refusal text %q leaks internals %v", err.Error(), hits)
	}
	if !strings.Contains(err.Error(), "retry") {
		t.Errorf("refusal %q should tell the caller to retry", err.Error())
	}
	var refusal *steerRefusalError
	if !errors.As(err, &refusal) || refusal.Unwrap() == nil {
		t.Errorf("the curated refusal must keep its cause for errors.Is/As, got %T", err)
	}
}

// #1214 item 2: refused text is never consumed by the target - it is on no
// queue, not in the transcript, and its ledger line (if one was written) ends
// superseded, never delivered.
func TestSteerRefusal_1214_RefusedTextIsNeverConsumed(t *testing.T) {
	f := newQAReceiptFixture(t)
	for i := 0; i < MaxQueueSize; i++ {
		if err := f.al.steering.pushScope(f.child.SessionID, providers.Message{Role: "user", Content: "filler"}); err != nil {
			t.Fatalf("SETUP slot %d: %v", i, err)
		}
	}
	const refused = "REFUSED-1214 text that must never reach the model"
	if _, err := f.al.EnqueueSteeringMessage(f.child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: refused}, "corr-1214-full"); err == nil {
		t.Fatal("SETUP: the full queue must refuse")
	}
	_, _, messages, _, err := f.al.dequeueSteeringItemsForScopeWithFallbackResult(f.child.SessionID)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "REFUSED-1214") {
			t.Fatalf("refused text reached the consumed model input: %q", m.Content)
		}
	}
	entries, err := f.store.ReadTranscript(f.child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Content, "REFUSED-1214") {
			t.Fatalf("refused text was appended to the transcript: %+v", e)
		}
	}
}

// #1214 item 4: a steer accepted and then beaten by Stop ends superseded (reason
// stop) and is never reported delivered, even if the queue is consumed after.
func TestSteerRefusal_1214_SteerBeatenByStopEndsSupersededNeverDelivered(t *testing.T) {
	f := newQAReceiptFixture(t)
	line := qaReceiptEnqueue(t, f, "Beaten by the Stop.", "corr-1214-stop")
	if _, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: f.child.SessionID, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "u7-owner"}, Channel: "webchat",
	}); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	// A late consumer finds nothing to deliver.
	_, _, messages, _, _ := f.al.dequeueSteeringItemsForScopeWithFallbackResult(f.child.SessionID)
	for _, m := range messages {
		if strings.Contains(m.Content, "Beaten by the Stop") {
			t.Fatalf("a steer beaten by Stop was consumed: %q", m.Content)
		}
	}
	qaReceiptRequireFinalSteers(t, f.al, f.child.SessionID, map[string]string{line.ControlID: "superseded"})
	rec, err := f.al.GetSessionLifecycleStore().Load(f.child.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != session.LifecycleStopped {
		t.Fatalf("state = %s, want stopped", rec.State)
	}
}

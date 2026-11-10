package agent

// Oracles: frozen ADR-20260928 at cd20cf8b, D2/D4/D6, and finisher/receipts
// dispatch A/B/C. These helpers read real storage; none replaces the ledger,
// Stop mechanism, steering queue, or transcript writer.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// not-wire-format: test observation of the internal, on-disk ledger.
type qaReceiptLine struct {
	Seq             int64               `json:"seq"`
	ControlID       string              `json:"control_id"`
	Verb            string              `json:"verb"`
	State           string              `json:"state"`
	Text            string              `json:"text,omitempty"`
	Reason          string              `json:"reason,omitempty"`
	SupersededBySeq *int64              `json:"superseded_by_seq,omitempty"`
	Actor           string              `json:"actor,omitempty"`
	Cause           session.StopCause   `json:"cause,omitempty"`
	Generation      int                 `json:"generation,omitempty"`
	AcceptedAt      time.Time           `json:"accepted_at"`
	StopEffect      *session.StopEffect `json:"stop_effect,omitempty"`
	LandedStop      json.RawMessage     `json:"landed_stop,omitempty"`
}

// not-wire-format: private fixture with concrete production stores.
type qaReceiptFixture struct {
	al       *AgentLoop
	store    *session.UnifiedStore
	parentID string
	child    *session.LifecycleRecord
}

func newQAReceiptFixture(t *testing.T) qaReceiptFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	parentID := newTestSteeringSession(t, al, "ws-control-receipts-qa")
	child, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID, TargetAgentID: testDefaultAgentID,
		Task:   "Original instruction stays durable.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "qa-receipts-launch"},
	})
	if err != nil {
		t.Fatalf("SETUP real delegated Launch: %v", err)
	}
	rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
	if err != nil {
		t.Fatalf("SETUP real launched lifecycle: %v", err)
	}
	return qaReceiptFixture{al: al, store: al.ResolveSessionStore(child.SessionID), parentID: parentID, child: rec}
}

func qaReceiptLedgerPath(al *AgentLoop, sessionID string) string {
	return filepath.Join(al.GetSessionLifecycleStore().Dir(), "controls", sessionID+".jsonl")
}

func qaReceiptJournalPath(al *AgentLoop, sessionID string) string {
	return filepath.Join(al.GetSessionLifecycleStore().Dir(), sessionID+".jsonl")
}

func qaReceiptReadLines(t *testing.T, al *AgentLoop, sessionID string) []qaReceiptLine {
	t.Helper()
	raw, err := os.ReadFile(qaReceiptLedgerPath(al, sessionID))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("read real control ledger: %v", err)
	}
	if len(raw) != 0 && raw[len(raw)-1] != '\n' {
		t.Fatal("control ledger has a torn append; cannot trust filtered receipts")
	}
	var lines []qaReceiptLine
	for i, part := range bytes.Split(raw, []byte{'\n'}) {
		if len(bytes.TrimSpace(part)) == 0 {
			continue
		}
		var line qaReceiptLine
		if err := json.Unmarshal(part, &line); err != nil {
			t.Fatalf("physical control line %d is malformed: %v", i+1, err)
		}
		lines = append(lines, line)
	}
	return lines
}

func qaReceiptLatest(lines []qaReceiptLine) map[string]qaReceiptLine {
	latest := make(map[string]qaReceiptLine)
	for _, line := range lines {
		if line.ControlID != "" {
			latest[line.ControlID] = line
		}
	}
	return latest
}

func qaReceiptAcceptedSteer(t *testing.T, al *AgentLoop, sessionID, text string) qaReceiptLine {
	t.Helper()
	var matches []qaReceiptLine
	for _, line := range qaReceiptReadLines(t, al, sessionID) {
		if line.Verb == "steer" && line.State == "queued" && line.Text == text {
			matches = append(matches, line)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("D4 exact steer text %q has %d durable queued acceptances, want exactly 1", text, len(matches))
	}
	line := matches[0]
	if line.ControlID == "" || line.Seq < 1 || line.AcceptedAt.IsZero() || line.Actor != "agent:"+testDefaultAgentID {
		t.Fatalf("D4 steer acceptance has invalid identity/time/actor: %+v", line)
	}
	return line
}

func qaReceiptEnqueue(t *testing.T, f qaReceiptFixture, text, correlationID string) qaReceiptLine {
	t.Helper()
	id, err := f.al.EnqueueSteeringMessage(f.child.SessionID, testDefaultAgentID,
		providers.Message{Role: "user", Content: text}, correlationID)
	if err != nil || id != correlationID {
		t.Fatalf("accepted delegate steer returned %q/%v, want %q/nil", id, err, correlationID)
	}
	return qaReceiptAcceptedSteer(t, f.al, f.child.SessionID, text)
}

func qaReceiptItemControlID(item steeringQueueItem) string {
	field := reflect.ValueOf(item).FieldByName("steerControlID")
	if !field.IsValid() {
		return "" // The pre-fix queue has no receipt carrier: a behavioural absence.
	}
	return field.String()
}

func qaReceiptRequireFinalSteers(t *testing.T, al *AgentLoop, sessionID string, want map[string]string) {
	t.Helper()
	latest := qaReceiptLatest(qaReceiptReadLines(t, al, sessionID))
	seen := 0
	for id, line := range latest {
		if line.Verb != "steer" && line.Verb != "respond" {
			continue
		}
		seen++
		state, expected := want[id]
		if !expected || line.State != state {
			t.Errorf("D4 receipt %q = %q, want %q (expected=%v); no queued or unaccounted orphan allowed", id, line.State, state, expected)
		}
	}
	if seen != len(want) {
		t.Errorf("D4 final steer receipt count = %d, want exactly %d", seen, len(want))
	}
}

func qaReceiptReopenTranscript(t *testing.T, store *session.UnifiedStore, sessionID string) []session.TranscriptEntry {
	t.Helper()
	return q2ConsumerReadDurable(t, store, sessionID)
}

//go:build linux || darwin

package agent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// A4/D6 #1053: fault only the real child's lifecycle append. Parent failure
// must land without waiting; its fatal hand-back names this child incomplete,
// and the ORIGINAL accepted control remains queued for boot (D6 QA1).
func TestQAFailedParentCascade_PersistFailureNamedIncompleteAndBootRetried(t *testing.T) {
	f := newQAReceiptFixture(t) // f.child is the failing middle session.
	// Genuine runtime-generated admission identity, persisted by the normal
	// admission writer before the synthetic completion boundary. A bare Launch
	// is not a producing run and must not be used to fake a final commit.
	if err := stampAdmissionExecution(f.al.GetSessionLifecycleStore(), f.child.SessionID,
		f.child.Generation, freshRunID(), f.al.bootEpochFor(), nil); err != nil {
		t.Fatalf("SETUP real producing admission identity: %v", err)
	}
	var err error
	f.child, err = f.al.GetSessionLifecycleStore().Load(f.child.SessionID)
	if err != nil || f.child.ExecutionID == nil {
		t.Fatalf("SETUP read durable producing identity: %+v/%v", f.child, err)
	}
	launched, err := NewSteerLauncher(f.al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: f.child.SessionID, TargetAgentID: testDefaultAgentID,
		Task:   "Child work survives a failed parent's incomplete Stop.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "qa-cascade-child"},
	})
	if err != nil {
		t.Fatalf("SETUP real descendant Launch: %v", err)
	}
	leaf, err := f.al.GetSessionLifecycleStore().Load(launched.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	restore := qaReceiptDenyAppend(t, qaReceiptJournalPath(f.al, leaf.SessionID))
	if completionErr := f.al.completeSteeredTurn(context.Background(), f.child, turnResult{}, errors.New("qa genuine parent failure")); completionErr != nil {
		t.Fatalf("parent's genuine failure must land despite incomplete child Stop: %v", completionErr)
	}
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	parent, err := fresh.Load(f.child.SessionID)
	if err != nil || parent.State != session.LifecycleFailed || !parent.Terminal() {
		t.Fatalf("#1053/#947 parent did not land failed promptly: %+v/%v", parent, err)
	}
	currentLeaf, err := fresh.Load(leaf.SessionID)
	if err != nil || currentLeaf.State != leaf.State || currentLeaf.Stop != nil || currentLeaf.StopNote != nil {
		t.Fatalf("failed child fence must not claim a stopped child: %+v/%v", currentLeaf, err)
	}
	entries, err := f.al.GetMessageInboxStore().Entries(f.parentID)
	if err != nil {
		t.Fatal(err)
	}
	fatalCount := 0
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		raw, marshalErr := entry.Message.MarshalJSON()
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["session_id"] != f.child.SessionID || fields["fatal"] != true {
			continue
		}
		fatalCount++
		text, ok := fields["text"].(string)
		if !ok || !strings.Contains(text, leaf.SessionID) || !strings.Contains(text, "could not be stopped") || !strings.Contains(text, "may still be running") {
			t.Errorf("#1053 fatal hand-back hides/misreports the exact incomplete child %s: %s", leaf.SessionID, raw)
		}
	}
	if fatalCount != 1 {
		t.Errorf("#1053 fatal hand-back count = %d, want exactly 1", fatalCount)
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, leaf.SessionID))
	if len(latest) != 1 {
		t.Fatalf("D6 QA1/#1053 failed cascade retry allocated %d control identities, want exactly the ORIGINAL acceptance (1), not replacement Stops", len(latest))
	}
	var original session.ControlGrant
	for id, line := range latest {
		if line.Verb != "stop" || line.State != "queued" || line.Cause != session.StopCauseCascade || line.Actor != "agent:"+f.child.SessionID || len(line.LandedStop) != 0 {
			t.Errorf("#1053 incomplete retry item = %+v, want real queued cascade intent with no invented landing", line)
		}
		original = session.ControlGrant{ControlID: id, Seq: line.Seq}
	}
	parentBytes, err := os.ReadFile(qaReceiptJournalPath(f.al, f.child.SessionID))
	if err != nil {
		t.Fatal(err)
	}
	restore()
	if finishErr := qaReceiptRunBootFinisher(t, f.al); finishErr != nil {
		t.Fatalf("boot retry after real append repair: %v", finishErr)
	}
	leafFixture := qaReceiptFixture{al: f.al, store: f.al.ResolveSessionStore(leaf.SessionID), parentID: f.child.SessionID, child: leaf}
	qaReceiptRequireOriginalStop(t, leafFixture, original, session.StopCauseCascade, "agent:"+f.child.SessionID)
	after, err := os.ReadFile(qaReceiptJournalPath(f.al, f.child.SessionID))
	if err != nil || !bytes.Equal(parentBytes, after) {
		t.Errorf("boot retry rewrote the already-failed parent's outcome: equal=%v err=%v", bytes.Equal(parentBytes, after), err)
	}
}

// A4 one-shot failure INSIDE the production cascade cannot be synchronized
// at the concrete LifecycleStore's direct AppendJSONL seam from pkg/agent.
// A chmod repair triggered by ledger polling can race the first persist and
// is not evidence. Dispatcher agreed to keep this loud and route the seam to
// architect + fresh CHECK, rather than fake the Stop or add a production hook.
func TestQAFailedParentCascade_OneShotFenceFailureNeedsPersistSeam(t *testing.T) {
	t.Fatal("BLOCKED: deterministic one-shot child fence persist seam not exposed to the real cascade — required by finisher dispatch A4 / ADR-20260928 D6 #1053; architect and fresh CHECK must resolve testability")
}

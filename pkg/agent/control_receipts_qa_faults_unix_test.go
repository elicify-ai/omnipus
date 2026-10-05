//go:build linux || darwin

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

const qaReceiptStopActor = "human:qa-intent-owner"

// Fault only the concrete store's append seam. Prove that reads still work
// and the actual O_RDWR append cannot succeed; a bypassing runner is BLOCKED,
// never skipped. Restore permissions before the real-store cleanup.
func qaReceiptDenyAppend(t *testing.T, path string) func() {
	t.Helper()
	stat, err := os.Stat(path)
	if err != nil {
		t.Fatalf("SETUP stat real append target: %v", err)
	}
	restore := func() {
		if err := os.Chmod(path, stat.Mode().Perm()); err != nil {
			t.Errorf("restore real append target: %v", err)
		}
	}
	t.Cleanup(restore)
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("install readable-only append fault: %v", err)
	}
	if _, err := os.ReadFile(path); err != nil {
		t.Fatalf("instrument incorrectly denied journal reads: %v", err)
	}
	probe, openErr := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0o600)
	if openErr == nil {
		if err := probe.Close(); err != nil {
			t.Errorf("close append instrument: %v", err)
		}
		t.Fatal("BLOCKED: runner bypasses filesystem write permissions; cannot prove the real persist seam failure")
	}
	if !errors.Is(openErr, os.ErrPermission) {
		t.Fatalf("append fault = %v, want real permission denial", openErr)
	}
	return restore
}

// AcceptStopControl writes the real ledger first. Its existing stamp callback
// is the store/persist seam: install the fence's normal fields, then deny the
// physical lifecycle append. No agent Stop/finisher code is replaced.
func qaReceiptAcceptFailedFence(t *testing.T, f qaReceiptFixture) (session.ControlGrant, func()) {
	t.Helper()
	var restore func()
	outcome, grant, generation, err := f.al.GetSessionLifecycleStore().AcceptStopControl(f.child.SessionID,
		session.StopControlIntent{Cause: session.StopCauseStop, Actor: qaReceiptStopActor},
		func(rec *session.LifecycleRecord, grant session.ControlGrant, target session.StopEffectTarget) error {
			at := time.Now().UTC()
			rec.Stop = &session.Stop{At: at, Generation: rec.Generation,
				By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-intent-owner"}}
			rec.StopNote = &session.StopNote{At: at, By: qaReceiptStopActor, Seq: uint64(grant.Seq), Cause: session.StopCauseStop}
			rec.StopEffect = &session.StopEffect{ControlID: grant.ControlID, Target: target}
			restore = qaReceiptDenyAppend(t, qaReceiptJournalPath(f.al, f.child.SessionID))
			return nil
		})
	var pathErr *os.PathError
	if outcome != session.StopAcceptGranted || grant.ControlID == "" || generation != f.child.Generation ||
		!errors.Is(err, os.ErrPermission) || !errors.As(err, &pathErr) || pathErr.Path != qaReceiptJournalPath(f.al, f.child.SessionID) || !strings.Contains(err.Error(), "persist refused") {
		t.Fatalf("instrument must reach actual persist AFTER durable acceptance: outcome=%v grant=%+v gen=%d err=%v path=%+v", outcome, grant, generation, err, pathErr)
	}
	cur, loadErr := f.al.GetSessionLifecycleStore().Load(f.child.SessionID)
	if loadErr != nil || cur.Stop != nil || cur.StopNote != nil || cur.StopEffect != nil || cur.State != f.child.State {
		t.Fatalf("failed fence changed real session: current=%+v err=%v", cur, loadErr)
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))
	if line := latest[grant.ControlID]; len(latest) != 1 || line.State != "queued" || line.Verb != "stop" || line.Cause != session.StopCauseStop || line.Actor != qaReceiptStopActor {
		t.Fatalf("persist refusal lost durable queued intent: %+v", latest)
	}
	return grant, restore
}

// The public method exists only after 30ac279f6. Structural capability
// detection keeps the shared pre-fix A4 pack compilable, without implementing
// or mocking a finisher. Missing implementation remains loud.
func qaReceiptRunBootFinisher(t *testing.T, al *AgentLoop) error {
	t.Helper()
	finisher, ok := any(al).(interface{ FinishUnfinishedStopIntents(context.Context) error })
	if !ok {
		t.Fatal("BLOCKED: boot stop-intent finisher not implemented — required by ADR-20260928 D4")
	}
	return finisher.FinishUnfinishedStopIntents(context.Background())
}

func qaReceiptRequireOriginalStop(t *testing.T, f qaReceiptFixture, original session.ControlGrant, cause session.StopCause, actor string) {
	t.Helper()
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	cur, err := fresh.Load(f.child.SessionID)
	if err != nil || cur.State != session.LifecycleStopped || cur.Generation != f.child.Generation || cur.Stop != nil || cur.StopNote == nil || cur.StopEffect == nil {
		t.Fatalf("D4 retry did not land a same-generation stopped session: current=%+v err=%v", cur, err)
	}
	if cur.StopNote.Cause != cause || cur.StopNote.By != actor {
		t.Errorf("retry changed accepted cause/actor: note=%+v, want %s/%s", cur.StopNote, cause, actor)
	}
	latest := qaReceiptLatest(qaReceiptReadLines(t, f.al, f.child.SessionID))
	if len(latest) != 1 || cur.StopEffect.ControlID != original.ControlID || cur.StopNote.Seq != uint64(original.Seq) {
		t.Errorf("D6 QA1 retry replaced the accepted control: latest=%+v effect=%+v note=%+v, want ORIGINAL %s/seq %d and no new acceptance", latest, cur.StopEffect, cur.StopNote, original.ControlID, original.Seq)
	}
	line := latest[original.ControlID]
	if line.Verb != "stop" || line.Seq != original.Seq || line.State != "applied" || line.Cause != cause || line.Actor != actor || len(line.LandedStop) == 0 {
		t.Errorf("original Stop receipt lacks its durable landing/notice or was superseded: %+v", line)
	}
	if line.StopEffect == nil || *line.StopEffect != *cur.StopEffect {
		t.Errorf("retry lost original accepted execution/control selection: ledger=%+v current=%+v", line.StopEffect, cur.StopEffect)
	}
	transitions, err := fresh.ListStoppedTransitions(f.child.SessionID)
	if err != nil || len(transitions) != 1 || transitions[0].ControlID != original.ControlID || transitions[0].StopSeq != uint64(original.Seq) {
		t.Errorf("reopened real Stop history = %+v/%v, want one ORIGINAL seq/control", transitions, err)
	}
	ownerHint := ""
	if strings.HasPrefix(actor, "human:") {
		ownerHint = actor
	}
	noticeID, _ := assertU1StoppedChildNotice(t, f.al, f.parentID, f.child, string(cause), ownerHint)
	inbox := session.NewMessageInboxStore(filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "session_messages"))
	entries, err := inbox.Entries(f.parentID)
	if err != nil {
		t.Fatalf("reopen direct-parent notice inbox: %v", err)
	}
	matching := 0
	for _, entry := range entries {
		if entry.Message == nil {
			continue
		}
		raw, err := entry.Message.MarshalJSON()
		if err != nil {
			t.Fatalf("decode durable notice: %v", err)
		}
		fields := u1DecodeMessageFields(t, raw)
		if fields["message_id"] == noticeID {
			matching++
		}
	}
	if matching != 1 {
		t.Errorf("D6 reopened parent inbox has %d copies of landed Stop notice, want exactly 1", matching)
	}
}

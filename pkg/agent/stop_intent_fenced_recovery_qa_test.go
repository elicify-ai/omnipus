//go:build linux || darwin

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// D6 QA2: an already accepted, persisted fence is NOT a reason for boot to
// leave the original Stop queued forever. Establish the real crash cut at the
// store boundary, reopen the store, then drive recovery in gateway order.
func TestQAStopIntentFinisher_FencedUnlandedStopFinishedAfterReopen(t *testing.T) {
	f := newQAReceiptFixture(t)
	at := time.Now().UTC()
	outcome, grant, generation, err := f.al.GetSessionLifecycleStore().AcceptStopControl(f.child.SessionID,
		session.StopControlIntent{Cause: session.StopCauseStop, Actor: qaReceiptStopActor, AcceptedAt: at},
		func(rec *session.LifecycleRecord, grant session.ControlGrant, target session.StopEffectTarget) error {
			rec.Stop = &session.Stop{At: at, Generation: rec.Generation,
				By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "qa-intent-owner"}}
			rec.StopNote = &session.StopNote{At: at, By: qaReceiptStopActor, Seq: uint64(grant.Seq), Cause: session.StopCauseStop}
			rec.StopEffect = &session.StopEffect{ControlID: grant.ControlID, Target: target}
			return nil
		})
	if err != nil || outcome != session.StopAcceptGranted || generation != f.child.Generation {
		t.Fatalf("SETUP real durable fence acceptance: outcome=%v grant=%+v gen=%d err=%v", outcome, grant, generation, err)
	}
	fresh := session.NewLifecycleStore(f.al.GetSessionLifecycleStore().Dir())
	cur, err := fresh.Load(f.child.SessionID)
	if err != nil || cur.State != f.child.State || cur.Stop == nil || cur.StopEffect == nil || cur.StopEffect.ControlID != grant.ControlID {
		t.Fatalf("SETUP reopened accepted-but-unlanded fence: %+v/%v", cur, err)
	}
	transitions, err := fresh.ListStoppedTransitions(f.child.SessionID)
	if err != nil || len(transitions) != 0 {
		t.Fatalf("SETUP fence is not a landed stop: history=%+v/%v", transitions, err)
	}
	f.al.SetSessionMessagingStores(f.al.GetMessageInboxStore(), fresh)
	var operatorNotices []string
	recovery := u1BootRecovery(t, f.al, &operatorNotices)
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("actual boot recovery: %v", err)
	}
	if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
		t.Fatalf("accepted-fence boot finisher: %v", err)
	}
	qaReceiptRequireOriginalStop(t, f, grant, session.StopCauseStop, qaReceiptStopActor)
	if err := qaReceiptRunBootFinisher(t, f.al); err != nil {
		t.Fatalf("accepted-fence finisher replay: %v", err)
	}
	qaReceiptRequireOriginalStop(t, f, grant, session.StopCauseStop, qaReceiptStopActor)
}

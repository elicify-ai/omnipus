// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Re-pinned to the ADR-093 amendment of 2026-10-23 (founder decision).
//
// SPEC CHANGE — OLD: this file was the RED pack for pkg/agent/
// steer_launcher.go::Launch's D2 refusal gate, pinning that a parent which
// has already LANDED session.LifecycleStopped with its fence cleared is
// refused with steer.ErrSteeringStopped. NEW (amendment Decision 1): D2's
// launch-time backstop is removed outright ("this check must be removed, a
// session that ended can not fire anything") — a delegate launch under a
// stopped-record parent PUBLISHES the child, and nothing is stamped at
// launch (founder Q1, option A: the rare race may run once). The parent
// record is still not written (D1 stands). The test below now pins the
// publish outcome; TestAdr093RootRevival_ClassifyFirstOneTurnOneGeneration
// (adr093_open_conversation_test.go) keeps pinning that the next HUMAN
// message revives the landed-stopped root before tools run.

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestAdr093Launch_LandedStoppedParent_PublishesUnstampedChild replaces
// TestAdr093Launch_LandedAndClearedStoppedParentRefusesLaunch. Oracle:
// ADR-093 amendment 2026-10-23, Decision 1 ("A delegate launch under a
// terminal-record or stopped-record parent publishes the child") + Q1
// option A (no stamp at launch) + D1 (the parent record is not written).
// The parent here is the landed-and-cleared stopped shape: state
// LifecycleStopped, stop_note retained, live fence already cleared.
func TestAdr093Launch_LandedStoppedParent_PublishesUnstampedChild(t *testing.T) {
	al, cleanup := newSteerAL(t)
	t.Cleanup(cleanup)
	parentID := newTestSteeringSession(t, al, adr093Workspace)

	rec := adr093Record(parentID, 1, session.LifecycleStopped)
	rec.StopNote = &session.StopNote{
		At:    time.Now(),
		By:    "human:tester",
		Seq:   1,
		Cause: session.StopCauseStop,
	}
	// rec.Stop deliberately nil: the landed-and-cleared shape, not the live
	// in-flight fence (TestLaunch_UnderStampedParent_PublishesUnstampedChild
	// covers the live-fence shape).
	adr093Persist(t, al, rec)

	res, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Follow up on the draft",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})
	if err != nil {
		t.Fatalf("Launch under a landed-and-cleared stopped parent = %v — ADR-093 amendment 2026-10-23 Decision 1 removes D2's launch-time refusal; the launch must publish the child (old spec: steer.ErrSteeringStopped refusal)", err)
	}
	if res.SessionID == "" || res.SessionID == parentID {
		t.Fatalf("Launch result session id = %q, want a fresh child distinct from the parent %s", res.SessionID, parentID)
	}
	child, err := al.GetSessionLifecycleStore().Load(res.SessionID)
	if err != nil {
		t.Fatalf("child lifecycle record %s missing after the launch (amendment Decision 1: the child is published): %v", res.SessionID, err)
	}
	if child.Stopped() {
		t.Fatalf("child was stamped at launch: state=%q Stop=%+v — founder Q1 option A: no stamp at launch", child.State, child.Stop)
	}
	if child.SteeredBy == nil || child.SteeredBy.SteeringSessionID != parentID {
		t.Fatalf("child steering edge = %+v, want the landed-stopped parent %s as its steering session", child.SteeredBy, parentID)
	}

	// D1: the launch never writes the parent — the landed stopped shape is
	// exactly as it was seeded.
	parent := adr093Load(t, al, parentID)
	if parent.Generation != 1 || parent.State != session.LifecycleStopped || parent.StopNote == nil || parent.Stop != nil {
		t.Fatalf("parent after the launch = gen %d state %q note %+v Stop %+v, want the seeded landed shape unchanged (D1: the launch does not write the parent)",
			parent.Generation, parent.State, parent.StopNote, parent.Stop)
	}
}

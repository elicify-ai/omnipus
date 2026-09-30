// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for the #890 follow-up (call site #2 of 6):
// pkg/agent/steer_launcher.go::(*SteerLauncher).Launch, line ~431:
//
//	if parentRec.Terminal() || parentRec.Stopped() {
//	    ... refuse the launch with steer.ErrSteeringStopped ...
//	}
//
// This mirrors TestAdr093Launch_StoppedParentRefusalLeavesNoArtifacts
// (adr093_open_conversation_test.go), which pins the LIVE-fence half of D2
// (parentRec.Stop.Generation == parentRec.Generation). This file pins the
// other half the ADR-20260928-sub-agent-control-plane.md line ~636 fix
// adds: a parent that has already LANDED session.LifecycleStopped with its
// fence cleared must be refused too — a Launch under it today WRONGLY
// succeeds, because neither parentRec.Terminal() (LifecycleStopped is
// deliberately non-terminal) nor the unwidened parentRec.Stopped() (no live
// fence left once TransitionSession lands the stop) catches this shape.
//
// Production code is untouched here — RED only (elicify-test-writing skill,
// qa-lead RED duty).

package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// TestAdr093Launch_LandedAndClearedStoppedParentRefusesLaunch is RED for
// steer_launcher.go::Launch's D2 refusal gate. Oracle: ADR-093 D2 itself
// ("a launch whose steering conversation is not active is refused outright
// ... no child lifecycle record, no unified session, no goal record") plus
// ADR-20260928-sub-agent-control-plane.md line ~636's intended Stopped()
// redefinition. A landed-and-cleared stopped parent's steering conversation
// is NOT active — it is exactly as inactive as the live-fence case the
// existing sibling test already pins — so the oracle says this Launch must
// be refused with steer.ErrSteeringStopped, never succeed.
func TestAdr093Launch_LandedAndClearedStoppedParentRefusesLaunch(t *testing.T) {
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
	// in-flight fence the sibling test (TestAdr093Launch_
	// StoppedParentRefusalLeavesNoArtifacts) already covers.
	adr093Persist(t, al, rec)

	_, err := NewSteerLauncher(al).Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentID,
		TargetAgentID:     testDefaultAgentID,
		Task:              "Follow up on the draft",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate},
	})

	if err == nil {
		t.Fatalf("Launch under a landed-and-cleared stopped parent (state=%q, stop_note set, fence already cleared) SUCCEEDED — ADR-093 D2 refuses a launch whose steering "+
			"conversation is not active, and this parent is exactly that: neither parentRec.Terminal() (LifecycleStopped is non-terminal) nor the unwidened "+
			"parentRec.Stopped() (no live fence left) catches it, so steer_launcher.go:431 lets the launch through (ADR-20260928-sub-agent-control-plane.md line ~636: "+
			"Stopped() must check landed state OR current fence)", session.LifecycleStopped)
	}
	if !errors.Is(err, steer.ErrSteeringStopped) {
		t.Fatalf("Launch refusal error = %v, want steer.ErrSteeringStopped (ADR-093 D2's sentinel for an inactive steering conversation)", err)
	}

	// No artifacts: only the parent's lifecycle record exists, unchanged.
	records, lerr := al.GetSessionLifecycleStore().List(session.LifecycleFilter{})
	if lerr != nil {
		t.Fatalf("List lifecycle after refused launch: %v", lerr)
	}
	if len(records) != 1 || records[0].SessionID != parentID {
		ids := make([]string, 0, len(records))
		for _, r := range records {
			ids = append(ids, r.SessionID)
		}
		t.Fatalf("lifecycle records after the refused launch = %v, want only the parent %s (ADR-093 D2: the refusal leaves no child record)", ids, parentID)
	}
}

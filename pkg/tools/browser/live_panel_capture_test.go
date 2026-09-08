package browser

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func newPanelPicture(t *testing.T, lv *LiveView, panel, targetID string) (*CaptureSession, *fakeRelay, CaptureFrameState) {
	t.Helper()
	relay := &fakeRelay{}
	cs, _ := newRecoveryTestSession(t, relay)
	frame, err := cs.BeginFrameTransition(targetID, 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.CommitFrameBoundary(frame.Generation, targetID, 123) {
		t.Fatal("fixture boundary rejected")
	}
	if lv.mgr.captures == nil {
		lv.mgr.captures = make(map[string]*CaptureSession)
	}
	lv.mgr.captures[panel] = cs
	return cs, relay, frame
}

func TestLivePanelInputUsesOnlyOwnPicture(t *testing.T) {
	for _, scenario := range []string{"own picture", "foreign picture", "only foreign capture", "no capture"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
			lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: lv.tabCtx, targetID: "target-panel"}}}
			_, _, own := newPanelPicture(t, lv, lv.sessionID, "target-panel")
			_, _, foreign := newPanelPicture(t, lv, lv.mgr.OperatorSessionID(), "target-operator")
			proof := own
			switch scenario {
			case "foreign picture":
				proof = foreign
			case "only foreign capture":
				delete(lv.mgr.captures, lv.sessionID)
			case "no capture":
				clear(lv.mgr.captures)
			}
			err := lv.dispatchInput("viewer", LiveInput{Kind: "text", Text: "panel text", CaptureID: proof.CaptureID, CaptureGeneration: proof.Generation})
			if scenario == "own picture" {
				if err != nil || calls != 1 {
					t.Fatalf("own panel must dispatch exactly once: calls=%d err=%v", calls, err)
				}
			} else if err == nil || !IsBenignLiveInputError(err) || calls != 0 {
				t.Fatalf("unproven panel must reject before browser delivery: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestLivePanelRecaptureRoutesOnlyOwnCapture(t *testing.T) {
	for _, kind := range []string{"viewport", "tab"} {
		for _, present := range []bool{true, false} {
			name := kind + " missing"
			if present {
				name = kind + " present"
			}
			t.Run(name, func(t *testing.T) {
				lv := newNavigateTestLiveView(t, nil)
				_, own, _ := newPanelPicture(t, lv, lv.sessionID, "target-panel")
				_, foreign, _ := newPanelPicture(t, lv, lv.mgr.OperatorSessionID(), "target-operator")
				if !present {
					delete(lv.mgr.captures, lv.sessionID)
				}
				if kind == "viewport" {
					lv.signalRecapture(800, 600)
				} else {
					lv.signalRecaptureForTabChange(800, 600)
				}
				want := 0
				if present {
					want = 1
				}
				if own.recaptureCount() != want || foreign.recaptureCount() != 0 {
					t.Fatalf("recapture crossed panels: own=%d want=%d foreign=%d", own.recaptureCount(), want, foreign.recaptureCount())
				}
			})
		}
	}
}

func TestLivePanelDeathStopsOnlyOwnCapture(t *testing.T) {
	for _, alive := range []bool{false, true} {
		name := "dead panel"
		if alive {
			name = "tab ended within live panel"
		}
		t.Run(name, func(t *testing.T) {
			lv := newNavigateTestLiveView(t, nil)
			_, own, _ := newPanelPicture(t, lv, lv.sessionID, "target-panel")
			_, foreign, _ := newPanelPicture(t, lv, lv.mgr.OperatorSessionID(), "target-operator")
			if alive {
				lv.mgr.sessions[lv.sessionID] = &sessionEntry{browserCtx: context.Background()}
			}
			watched, cancel := context.WithCancel(context.Background())
			lv.listenCtx = watched
			cancel()
			lv.watchForUnexpectedDeath(watched)
			want := 1
			if alive {
				want = 0
			}
			if own.closeCount() != want || foreign.closeCount() != 0 {
				t.Fatalf("panel death crossed ownership: own=%d want=%d foreign=%d", own.closeCount(), want, foreign.closeCount())
			}
		})
	}
}

// Existing input behavior tests supply one fixed, independently installed
// picture. Replacing the current capture later does not update this proof.
func installInputTestPicture(t *testing.T, lv *LiveView) CaptureFrameState {
	t.Helper()
	if lv.mgr == nil {
		lv.mgr = newCaptureTestManager(t)
	}
	lv.mgr.sessions[lv.sessionID] = &sessionEntry{tabs: []*tabEntry{{ctx: lv.tabCtx, targetID: "input-fixture-target"}}}
	_, _, frame := newPanelPicture(t, lv, lv.sessionID, "input-fixture-target")
	return frame
}

func inputWithTestPicture(frame CaptureFrameState, in LiveInput) LiveInput {
	in.CaptureID = frame.CaptureID
	in.CaptureGeneration = frame.Generation
	return in
}

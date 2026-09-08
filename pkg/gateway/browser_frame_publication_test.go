package gateway

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

// publicationRelay exposes the legacy relay-live notification at the transport
// boundary; frame, recovery, and publication state remain real.
type publicationRelay struct {
	fakeRelay
	onLive func()
}

func (r *publicationRelay) SetOnIngestLive(fn func()) { r.onLive = fn }

type videoPublicationFixture struct {
	h     *BrowserWSHandler
	cs    *browser.CaptureSession
	relay *fakeRelay
	wc    *browserWSConn
	state *browserConnState
	ctx   context.Context
	frame browser.CaptureFrameState
	event browser.VideoHealthEvent
}

func registerPublicationViewer(t *testing.T, h *BrowserWSHandler, cs *browser.CaptureSession, viewerID string) (*browserWSConn, *browserConnState, context.Context) {
	t.Helper()
	wc, state := newTabActionTestFixtures(t)
	t.Cleanup(func() { state.clearAttachment() })
	original := state.commandAttachment()
	epoch := state.beginWebRTCOffer()
	if !state.commitWebRTCAttachment(epoch, &webrtcAttachment{capture: cs}) || !h.registerWebRTCViewerConnForCapture(state, epoch, original.ctx, viewerID, wc, original.sessionID, cs) {
		t.Fatal("fixture could not register original attachment")
	}
	return wc, state, original.ctx
}

func newVideoPublicationFixture(t *testing.T) videoPublicationFixture {
	t.Helper()
	relay := &publicationRelay{}
	cs, err := browser.NewCaptureSessionWithDeps(nil, "publication", relay, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Stop)
	frame, err := cs.BeginFrameTransition("health-target", 800, 600, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !cs.CommitFrameBoundary(frame.Generation, "health-target", 0) {
		t.Fatal("fixture boundary rejected")
	}
	cs.AddViewer("viewer")
	if relay.onLive == nil {
		t.Fatal("fixture relay-live observer was not registered")
	}
	// The source becomes live before the tested loss; initial capture's
	// settle window must end through the real callback, not a timer override.
	relay.onLive()
	cs.ReportCaptureFailure()
	event, ok := cs.CurrentVideoHealthEvent()
	if !ok || event.State != browser.VideoHealthRecovering || event.Attempt != 1 {
		t.Fatalf("fixture must claim first recovery attempt: %+v ok=%v", event, ok)
	}
	h := &BrowserWSHandler{}
	wc, state, ctx := registerPublicationViewer(t, h, cs, "viewer")
	return videoPublicationFixture{h: h, cs: cs, relay: &relay.fakeRelay, wc: wc, state: state, ctx: ctx, frame: frame, event: event}
}

func assertNoVideoPublicationQueued(t *testing.T, wc *browserWSConn) {
	t.Helper()
	wc.latestMu.Lock()
	queued := wc.latestSlots[browserLatestVideoState].data != nil
	wc.latestMu.Unlock()
	if queued || len(wc.sendCh) != 0 {
		t.Fatalf("stale publication entered outbound queues: latest=%v critical=%d", queued, len(wc.sendCh))
	}
}

func TestBrowserVideoPublicationCarriesOriginalFrame(t *testing.T) {
	f := newVideoPublicationFixture(t)
	f.h.onVideoHealth(f.event)
	pending, ok := f.wc.takeLatestFrame()
	if !ok {
		t.Fatal("health must use replaceable latest-video slot")
	}
	var body map[string]any
	if err := json.Unmarshal(pending.data, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"type": "browser_video_health", "session_id": f.state.commandAttachment().sessionID, "state": "recovering", "capture_id": f.frame.CaptureID, "capture_generation": float64(f.frame.Generation), "target_id": "health-target", "css_width": float64(800), "css_height": float64(600), "rtp_timestamp": float64(0), "attempt": float64(1), "max_attempts": float64(3)}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("wire claim lost original metadata: got=%#v want=%#v", body, want)
	}
	if !f.wc.canSendFrame(pending) || len(f.wc.sendCh) != 0 {
		t.Fatal("current scoped health was not admitted exclusively through latest-video slot")
	}
}

func TestBrowserVideoPublicationRejectsRetiredClaim(t *testing.T) {
	for _, kind := range []string{"foreign capture", "foreign capture ID", "canceled origin", "missing origin", "obsolete version"} {
		t.Run(kind, func(t *testing.T) {
			f := newVideoPublicationFixture(t)
			raw, _ := f.h.viewerConns.Load("viewer")
			vc := raw.(*webrtcViewerConn)
			switch kind {
			case "foreign capture":
				other, err := browser.NewCaptureSessionWithDeps(nil, "replacement", &fakeRelay{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(other.Stop)
				vc.capture = other
			case "foreign capture ID":
				vc.captureID = "replacement-capture"
			case "canceled origin":
				f.state.beginAttach()
			case "missing origin":
				vc.attachmentCtx = nil
			case "obsolete version":
				f.relay.setStats(webrtc.Stats{VideoPackets: 1})
				f.cs.RecordVideoProgress()
				current, ok := f.cs.CurrentVideoHealthEvent()
				if !ok || current.Version <= f.event.Version || current.State != browser.VideoHealthRecovered {
					t.Fatalf("fixture did not recover same frame: %+v ok=%v", current, ok)
				}
			}
			f.h.onVideoHealth(f.event)
			assertNoVideoPublicationQueued(t, f.wc)
		})
	}
}

func TestBrowserVideoPublicationRetainsVersionThroughWriterAdmission(t *testing.T) {
	f := newVideoPublicationFixture(t)
	f.h.onVideoHealth(f.event)
	pending, ok := f.wc.takeLatestFrame()
	if !ok {
		t.Fatal("current claim was not queued")
	}
	f.relay.setStats(webrtc.Stats{VideoPackets: 1})
	f.cs.RecordVideoProgress()
	if f.wc.canSendFrame(pending) {
		t.Fatal("old same-frame recovery attempt survived newer recovered claim at final writer admission")
	}
}

func TestBrowserVideoPublicationReplaysForLateViewer(t *testing.T) {
	f := newVideoPublicationFixture(t)
	f.cs.AddViewer("late-viewer")
	wc, _, ctx := registerPublicationViewer(t, f.h, f.cs, "late-viewer")
	if !f.h.publishCurrentVideoHealth("late-viewer", f.cs, ctx) {
		t.Fatal("late viewer did not receive current health snapshot")
	}
	pending, ok := wc.takeLatestFrame()
	if !ok || !wc.canSendFrame(pending) {
		t.Fatal("late viewer replay lost original attachment authority")
	}
	current, ok := f.cs.CurrentVideoHealthEvent()
	if !ok || current.Version != f.event.Version || len(current.ViewerIDs) != 1 || current.ViewerIDs[0] != "viewer" {
		t.Fatalf("replay invented a new claim/audience: %+v", current)
	}
	var body map[string]any
	if err := json.Unmarshal(pending.data, &body); err != nil {
		t.Fatal(err)
	}
	if body["capture_id"] != f.frame.CaptureID || body["state"] != "recovering" {
		t.Fatalf("replay did not retain original claim: %#v", body)
	}
	assertNoVideoPublicationQueued(t, f.wc)
}

func TestBrowserVideoPublicationReplayRejectsForeignOrigin(t *testing.T) {
	f := newVideoPublicationFixture(t)
	foreign, cancel := context.WithCancel(context.Background())
	defer cancel()
	if f.h.publishCurrentVideoHealth("viewer", f.cs, foreign) {
		t.Fatal("replay replaced its supplied origin with the current attachment")
	}
	assertNoVideoPublicationQueued(t, f.wc)
}

func TestBrowserVideoStoppedPublication(t *testing.T) {
	for _, kind := range []string{"current", "foreign capture", "canceled origin", "replacement before write"} {
		t.Run(kind, func(t *testing.T) {
			f := newVideoPublicationFixture(t)
			f.cs.Stop()
			if kind == "foreign capture" {
				other, err := browser.NewCaptureSessionWithDeps(nil, "replacement", &fakeRelay{}, nil, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(other.Stop)
				registerPublicationViewer(t, f.h, other, "viewer")
			}
			if kind == "canceled origin" {
				f.state.beginAttach()
			}
			f.h.notifyViewersStreamStopped(f.cs, []string{"viewer"})
			if kind == "foreign capture" {
				value, _ := f.h.viewerConns.Load("viewer")
				assertNoVideoPublicationQueued(t, value.(*webrtcViewerConn).wc)
				return
			}
			if kind == "canceled origin" {
				assertNoVideoPublicationQueued(t, f.wc)
				return
			}
			select {
			case pending := <-f.wc.sendCh:
				if kind == "replacement before write" {
					f.h.viewerConns.Delete("viewer")
					if f.wc.canSendFrame(pending) {
						t.Fatal("retired stopped-capture event remained authorized at writer")
					}
					return
				}
				if !f.wc.canSendFrame(pending) {
					t.Fatal("current stopped-capture notification rejected")
				}
				var body map[string]any
				if err := json.Unmarshal(pending.data, &body); err != nil {
					t.Fatal(err)
				}
				if body["type"] != "browser_webrtc_state" || body["available"] != false || body["reason"] != "error" {
					t.Fatalf("wrong stopped notification: %#v", body)
				}
			default:
				t.Fatal("stopped capture did not notify original viewer")
			}
		})
	}
}

func TestBrowserVideoPublicationRejectsUninitializedFrame(t *testing.T) {
	cs, err := browser.NewCaptureSessionWithDeps(nil, "uninitialized", &fakeRelay{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Stop)
	cs.AddViewer("viewer")
	h := &BrowserWSHandler{}
	wc, _, _ := registerPublicationViewer(t, h, cs, "viewer")
	cs.SetOnVideoHealth(h.onVideoHealth)
	cs.ReportCaptureFailure()
	assertNoVideoPublicationQueued(t, wc)
}

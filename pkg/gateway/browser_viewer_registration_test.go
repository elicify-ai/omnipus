package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func TestBrowserViewerRegistrationRetainsOriginalAttachmentAndCapture(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	t.Cleanup(func() { state.clearAttachment() })
	attachment := state.commandAttachment()
	cs, err := browser.NewCaptureSessionWithDeps(nil, "registration", &fakeRelay{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cs.Stop)
	epoch := state.beginWebRTCOffer()
	if !state.commitWebRTCAttachment(epoch, &webrtcAttachment{capture: cs}) {
		t.Fatal("fixture failed to commit capture")
	}
	h := &BrowserWSHandler{}
	if !h.registerWebRTCViewerConnForCapture(state, epoch, attachment.ctx, "viewer", wc, attachment.sessionID, cs) {
		t.Fatal("current committed viewer was rejected")
	}
	value, ok := h.viewerConns.Load("viewer")
	if !ok {
		t.Fatal("accepted viewer has no route")
	}
	got := value.(*webrtcViewerConn)
	if got.wc != wc || got.sessionID != attachment.sessionID || got.attachmentCtx != attachment.ctx || got.capture != cs || got.captureID == "" || got.captureID != cs.FrameState().CaptureID {
		t.Fatalf("registry lost the original attachment/capture: %+v", got)
	}
	state.beginAttach()
	if got.attachmentCtx == nil || got.attachmentCtx.Err() == nil {
		t.Fatal("replacement did not retire the stored origin")
	}
}

func TestBrowserViewerRegistrationRejectsRetiredOrMismatchedOrigin(t *testing.T) {
	for _, kind := range []string{"old_offer", "canceled_attachment", "foreign_context", "wrong_capture", "no_committed_capture", "wrong_session", "stopped_capture", "missing_context"} {
		t.Run(kind, func(t *testing.T) {
			wc, state := newTabActionTestFixtures(t)
			t.Cleanup(func() { state.clearAttachment() })
			attachment := state.commandAttachment()
			cs, err := browser.NewCaptureSessionWithDeps(nil, "registration", &fakeRelay{}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cs.Stop)
			epoch := state.beginWebRTCOffer()
			if !state.commitWebRTCAttachment(epoch, &webrtcAttachment{capture: cs}) {
				t.Fatal("fixture failed to commit capture")
			}
			ctx, sessionID := attachment.ctx, attachment.sessionID
			switch kind {
			case "old_offer":
				state.beginWebRTCOffer()
			case "canceled_attachment":
				state.clearAttachment()
			case "foreign_context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				t.Cleanup(cancel)
			case "wrong_capture":
				state.commitWebRTCAttachment(epoch, &webrtcAttachment{capture: &browser.CaptureSession{}})
			case "no_committed_capture":
				state.takeWebRTCAttachment()
			case "wrong_session":
				sessionID = "other-chat"
			case "stopped_capture":
				cs.Stop()
			case "missing_context":
				ctx = nil
			}
			h := &BrowserWSHandler{}
			existing := &webrtcViewerConn{wc: wc, sessionID: "replacement"}
			h.viewerConns.Store("viewer", existing)
			if h.registerWebRTCViewerConnForCapture(state, epoch, ctx, "viewer", wc, sessionID, cs) {
				t.Error("invalid origin was admitted to viewer registry")
			}
			if got, _ := h.viewerConns.Load("viewer"); got != existing {
				t.Error("invalid origin replaced an existing viewer route")
			}
		})
	}
}

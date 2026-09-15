package gateway

import (
	"encoding/json"
	"testing"
)

// Exercise the actual handler: a failed request must report its error while it
// is current, but its queued result must not reach a replacement attachment.
func TestBrowserAttachFailureRetainsRequestOwnership(t *testing.T) {
	for _, frame := range []struct {
		name string
		data string
	}{
		{"invalid_json", `{`},
		{"missing_fields", `{"type":"browser_attach"}`},
	} {
		t.Run(frame.name, func(t *testing.T) {
			wc, state := newTabActionTestFixtures(t)
			t.Cleanup(func() { state.clearAttachment() })
			epoch := state.beginAttach()
			h := &BrowserWSHandler{}
			h.handleAttach(wc, state, "viewer", "user", []byte(frame.data), nil, epoch)
			var result browserOutboundFrame
			select {
			case result = <-wc.sendCh:
			default:
				t.Fatal("current failed attachment did not report an error")
			}
			var status browserFrameDecoder
			if err := json.Unmarshal(result.data, &status); err != nil {
				t.Fatal(err)
			}
			if status.State != "error" || status.Message == "" {
				t.Fatalf("unexpected failure result: %s", result.data)
			}
			if !wc.canSendFrame(result) {
				t.Error("handler completion suppressed the current request's error")
			}
			state.beginAttach()
			if wc.canSendFrame(result) {
				t.Error("failed attachment's queued error remains deliverable to its replacement")
			}
		})
	}
}

func TestBrowserAttachFailureScopeEndsOnClear(t *testing.T) {
	wc, state := newTabActionTestFixtures(t)
	epoch := state.beginAttach()
	h := &BrowserWSHandler{}
	h.handleAttach(wc, state, "viewer", "user", []byte(`{`), nil, epoch)
	request := state.attachmentRequest()
	state.clearAttachment()
	if request.ctx == nil || request.ctx.Err() == nil {
		t.Fatal("clearing failed attachment retained its publication lifetime")
	}
	select {
	case result := <-wc.sendCh:
		if wc.canSendFrame(result) {
			t.Error("cleared failed attachment's error is still deliverable")
		}
	default:
		t.Fatal("failed attachment did not enqueue its current error")
	}
}

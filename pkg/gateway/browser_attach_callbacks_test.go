package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func TestBrowserAttachCallbacksRetainOriginalPublicationScope(t *testing.T) {
	for _, kind := range []string{"status", "control"} {
		t.Run(kind, func(t *testing.T) {
			wc := latestTestConn()
			t.Cleanup(wc.close)
			original, cancel := context.WithCancel(context.Background())
			defer cancel()
			status, control, _ := browserAttachCallbacks(wc, original, "chat-a", "viewer")
			if kind == "status" {
				status("target died")
			} else {
				control(true)
			}
			var frame browserOutboundFrame
			select {
			case frame = <-wc.sendCh:
			default:
				t.Fatal("current callback did not publish")
			}
			var body map[string]any
			if err := json.Unmarshal(frame.data, &body); err != nil {
				t.Fatal(err)
			}
			if body["type"] != "browser_status" || body["session_id"] != "chat-a" {
				t.Fatalf("callback routing changed: %s", frame.data)
			}
			if kind == "status" && (body["state"] != "error" || body["message"] != "target died") {
				t.Errorf("status content changed: %s", frame.data)
			}
			if kind == "control" && (body["control_only"] != true || body["controlled_by_other"] != true) {
				t.Errorf("control semantics changed: %s", frame.data)
			}
			if !wc.canSendFrame(frame) {
				t.Error("current callback was suppressed")
			}
			cancel()
			if wc.canSendFrame(frame) {
				t.Error("old callback remained deliverable after attachment replacement")
			}
		})
	}
}

func TestBrowserAttachTabsCallbackDoesNotWaitForCriticalQueue(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	for i := 0; i < cap(wc.sendCh); i++ {
		wc.sendCh <- browserOutboundFrame{data: []byte("reserved")}
	}
	original, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, _, tabs := browserAttachCallbacks(wc, original, "chat-a", "viewer")
	done := make(chan struct{})
	go func() {
		tabs([]browser.Tab{{Index: 0, Title: "old"}}, 0)
		tabs([]browser.Tab{{Index: 0, Title: "new"}}, 0)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		wc.close()
		<-done
		t.Fatal("tab callback blocked on the critical queue")
	}
	frame, ok := wc.takeLatestFrame()
	if !ok {
		t.Fatal("tab callback published no latest snapshot")
	}
	var body struct {
		SessionID string `json:"session_id"`
		Tabs      []struct {
			Title string `json:"title"`
		} `json:"tabs"`
	}
	if err := json.Unmarshal(frame.data, &body); err != nil {
		t.Fatal(err)
	}
	if body.SessionID != "chat-a" || len(body.Tabs) != 1 || body.Tabs[0].Title != "new" {
		t.Fatalf("latest tab snapshot incorrect: %s", frame.data)
	}
	cancel()
	if wc.canSendFrame(frame) {
		t.Error("dequeued old tab snapshot remained deliverable after replacement")
	}
}

func TestBrowserAttachAvailabilityRetainsOriginalPublicationScope(t *testing.T) {
	wc := latestTestConn()
	t.Cleanup(wc.close)
	original, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &BrowserWSHandler{}
	h.announceWebRTCAvailabilityContext(original, wc, nil, "chat-a", "viewer", &config.Config{})
	var frame browserOutboundFrame
	select {
	case frame = <-wc.sendCh:
	default:
		t.Fatal("initial availability was not published")
	}
	var body map[string]any
	if err := json.Unmarshal(frame.data, &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "browser_webrtc_state" || body["session_id"] != "chat-a" || body["available"] != false || body["reason"] != "disabled" {
		t.Fatalf("availability semantics changed: %s", frame.data)
	}
	if !wc.canSendFrame(frame) {
		t.Error("current availability was suppressed")
	}
	cancel()
	if wc.canSendFrame(frame) {
		t.Error("old availability remained deliverable after replacement")
	}
}

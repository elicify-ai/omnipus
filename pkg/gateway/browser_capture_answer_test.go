package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

func TestCaptureAnswerRetainsOriginalIdentityThroughWriterWait(t *testing.T) {
	for _, kind := range []string{"current", "binding", "frame", "newer offer", "socket canceled", "deadline", "stopped", "retired error", "current error"} {
		t.Run(kind, func(t *testing.T) {
			writer, client := captureWriterPair(t)
			negotiated := make(chan context.Context, 4)
			relay := &wireIngestRelay{entered: make(chan wireIngestOffer, 4)}
			relay.negotiate = func(ctx context.Context, _ wireIngestOffer) (string, error) {
				negotiated <- ctx
				if kind == "retired error" || kind == "current error" {
					return "", errors.New("encoder negotiation failed")
				}
				return "expected-answer", nil
			}
			cs, err := browser.NewCaptureSessionWithDeps(nil, "answer", relay, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cs.Stop)
			if _, err = cs.BeginFrameTransition("page-a", 800, 600, 1); err != nil {
				t.Fatal(err)
			}
			source, cancel := context.WithCancel(context.Background())
			defer cancel()
			bind := func(ctx context.Context) uint64 {
				t.Helper()
				_, epoch, err := cs.BindIngestContext(ctx, func(string, *string, int, int, int) error { return nil }, func() {})
				if err != nil {
					t.Fatal(err)
				}
				return epoch
			}
			epoch := bind(source)
			id, generation, target := 9, 1, "page-a"
			deadline := time.Now().Add(time.Second)
			if kind == "deadline" {
				deadline = time.Now().Add(250 * time.Millisecond)
			}
			offer := queuedCaptureOffer{frame: generated.BrowserCaptureOfferFrame{Sdp: "sdp", OfferId: &id, CaptureGeneration: &generation, TargetId: &target}, deadline: deadline}
			release, err := writer.acquireWrite(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			var released sync.Once
			defer released.Do(release)
			result := make(chan error, 1)
			go func() { result <- writer.answerCaptureOffer(source, cs, epoch, offer) }()
			select {
			case negotiation := <-negotiated:
				select {
				case <-negotiation.Done():
				case <-time.After(time.Second):
					t.Fatal("negotiation did not return before response wait")
				}
			case <-time.After(time.Second):
				t.Fatal("offer did not reach real capture admission")
			}
			switch kind {
			case "binding":
				bind(context.Background())
			case "frame":
				if _, err := cs.BeginFrameTransition("page-b", 640, 480, 1); err != nil {
					t.Fatal(err)
				}
			case "newer offer":
				if _, err := cs.HandleIngestOfferForBinding(source, epoch, 10, "new-sdp", 1, "page-a"); err != nil {
					t.Fatal(err)
				}
			case "socket canceled", "retired error":
				cancel()
			case "deadline":
				<-time.After(time.Until(deadline) + time.Millisecond)
			case "stopped":
				cs.Stop()
			}
			var responseErr error
			if kind == "socket canceled" || kind == "retired error" || kind == "deadline" {
				select {
				case responseErr = <-result:
				case <-time.After(100 * time.Millisecond):
					t.Error("retired response kept waiting for writer ownership")
					released.Do(release)
					responseErr = <-result
				}
			} else {
				released.Do(release)
				select {
				case responseErr = <-result:
				case <-time.After(time.Second):
					t.Fatal("response did not finish after writer release")
				}
			}
			released.Do(release)
			current := kind == "current" || kind == "current error"
			if kind == "current" && responseErr != nil {
				t.Fatalf("current answer failed: %v", responseErr)
			}
			if kind != "current" && responseErr == nil {
				t.Error("retired or failed offer returned success")
			}
			if err := writer.sendJSONContext(context.Background(), map[string]string{"type": "marker"}, nil); err != nil {
				t.Fatal(err)
			}
			client.SetReadDeadline(time.Now().Add(time.Second))
			_, raw, err := client.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err = json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if !current {
				if body["type"] != "marker" {
					t.Fatalf("retired response escaped before marker: %s", raw)
				}
			} else if kind == "current" {
				if body["type"] != "browser_capture_answer" || body["sdp"] != "expected-answer" || body["capture_generation"] != float64(1) || body["target_id"] != "page-a" || body["offer_id"] != float64(9) {
					t.Fatalf("current answer lost immutable identity: %s", raw)
				}
			} else if body["type"] != "error" || body["message"] != "capture ingest offer failed: encoder negotiation failed" {
				t.Fatalf("current error was lost: %s", raw)
			}
		})
	}
}

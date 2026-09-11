package browser

import (
	"context"
	"testing"
	"time"
)

func TestCaptureGraceCallbackRetainsOriginalEligibility(t *testing.T) {
	for _, scenario := range []string{"idle", "legacy viewer", "active request", "pending request", "new grace"} {
		t.Run(scenario, func(t *testing.T) {
			r := &requestOfferProbe{}
			captureRequestSuccess(r)
			cs := captureRequestFixture(t, r)
			var callbacks []func()
			cs.graceAfterFunc = func(_ time.Duration, callback func()) *time.Timer {
				callbacks = append(callbacks, callback)
				timer := time.NewTimer(time.Hour)
				t.Cleanup(func() { timer.Stop() })
				return timer
			}
			cs.mu.Lock()
			cs.armGraceStopLocked()
			cs.mu.Unlock()
			if len(callbacks) != 1 {
				t.Fatalf("idle capture scheduled %d callbacks, want one", len(callbacks))
			}
			old := callbacks[0] // Already scheduled by the clock; Stop cannot retract this invocation.
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var finish func()
			switch scenario {
			case "legacy viewer", "new grace":
				cs.AddViewer("viewer")
				if scenario == "new grace" {
					cs.RemoveViewer("viewer")
				}
			case "active request":
				if _, _, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "offer"); err != nil {
					t.Fatal(err)
				}
			case "pending request":
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				r.offer = func(context.Context, context.Context, uint64, string, string) (string, any, error) {
					close(entered)
					<-release
					return "answer", r.registerHandleOK("viewer"), nil
				}
				go func() {
					defer close(done)
					_, handle, err := cs.HandleViewerOfferRequest(context.Background(), parent, 1, "viewer", "offer")
					if err != nil {
						cs.CleanupViewerOffer(handle)
					}
				}()
				<-entered
				finish = func() { close(release); <-done }
				defer finish()
			}
			old()
			closed := r.closeCount()
			if scenario == "idle" {
				old()
				if closed != 1 || r.closeCount() != 1 {
					t.Fatalf("current idle grace closes=%d then %d, want exactly one", closed, r.closeCount())
				}
				return
			}
			if closed != 0 {
				t.Errorf("retired grace stopped %s capture: relay closes=%d", scenario, closed)
			}
			select {
			case <-cs.Done():
				t.Error("retired grace closed capture lifetime")
			default:
			}
			if scenario == "new grace" {
				if len(callbacks) != 2 {
					t.Fatalf("replacement grace callbacks=%d, want two", len(callbacks))
				}
				callbacks[1]()
				if r.closeCount() != 1 {
					t.Error("current replacement grace did not stop idle capture")
				}
			}
		})
	}
}

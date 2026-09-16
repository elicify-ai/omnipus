package gateway

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestBrowserViewportBudgetBoundaries(t *testing.T) {
	for _, viewport := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "viewport"}[viewport], func(t *testing.T) {
			var operationErr func() error
			synctest.Test(t, func(t *testing.T) {
				var q browserCommandQueue
				var wg sync.WaitGroup
				budget := 5 * time.Second
				if viewport {
					budget = 10 * time.Second
				}
				q.submit(&wg, browserCommand{viewport: viewport, run: func(ctx context.Context) { operationErr = ctx.Err; <-ctx.Done() }})
				synctest.Wait()
				time.Sleep(budget - time.Nanosecond)
				if operationErr() != nil {
					t.Fatalf("expired before boundary: %v", operationErr())
				}
				time.Sleep(time.Nanosecond)
				synctest.Wait()
				if !errors.Is(operationErr(), context.DeadlineExceeded) {
					t.Fatalf("did not expire at boundary: %v", operationErr())
				}
				wg.Wait()
			})
		})
	}
}

func TestBrowserViewportBudgetCountsQueueAge(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var q browserCommandQueue
		var wg sync.WaitGroup
		release := make(chan struct{})
		q.submit(&wg, browserCommand{run: func(context.Context) { <-release }})
		var remaining time.Duration
		q.submit(&wg, browserCommand{viewport: true, run: func(ctx context.Context) { deadline, _ := ctx.Deadline(); remaining = time.Until(deadline) }})
		time.Sleep(4 * time.Second)
		close(release)
		wg.Wait()
		if remaining != 6*time.Second {
			t.Fatalf("remaining=%v want6s", remaining)
		}
	})
}

func TestBrowserViewportBudgetCancellation(t *testing.T) {
	for _, closeQueue := range []bool{false, true} {
		t.Run(map[bool]string{false: "discard", true: "close"}[closeQueue], func(t *testing.T) {
			var operationErr func() error
			synctest.Test(t, func(t *testing.T) {
				var q browserCommandQueue
				var wg sync.WaitGroup
				discarded := false
				ran := false
				q.submit(&wg, browserCommand{viewport: true, run: func(ctx context.Context) { operationErr = ctx.Err; <-ctx.Done() }})
				synctest.Wait()
				q.submit(&wg, browserCommand{viewport: true, run: func(context.Context) { ran = true }, onDiscard: func() { discarded = true }})
				if closeQueue {
					q.close()
				} else {
					q.discard()
				}
				wg.Wait()
				if !errors.Is(operationErr(), context.Canceled) || !discarded || ran {
					t.Fatalf("cancellation=%v discarded=%v ran=%v", operationErr(), discarded, ran)
				}
			})
		})
	}
}

func TestBrowserViewportBudgetClassification(t *testing.T) {
	for _, tc := range []struct {
		name, typ, raw   string
		queued, viewport bool
	}{
		{"viewport", "browser_viewport", `{"type":"browser_viewport","width":800,"height":600,"input_epoch":0,"control_epoch":1}`, true, true},
		{"release", "browser_control", `{"type":"browser_control","action":"release","input_epoch":0,"control_epoch":1}`, true, false},
		{"invalid viewport", "browser_viewport", `{"type":"browser_viewport","width":"bad","height":600,"input_epoch":0,"control_epoch":1}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			defer f.state.setDedicatedInput(false)
			f.state.commands.running = true
			f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", []byte(tc.raw), tc.typ, f.cfg)
			q := &f.state.commands
			q.mu.Lock()
			jobs := q.jobs
			q.jobs = nil
			q.running = false
			q.mu.Unlock()
			if len(jobs) != map[bool]int{false: 0, true: 1}[tc.queued] {
				t.Fatalf("queued=%d", len(jobs))
			}
			if tc.queued && jobs[0].viewport != tc.viewport {
				t.Fatalf("viewport=%v want%v", jobs[0].viewport, tc.viewport)
			}
		})
	}
}

func TestBrowserViewportSupersession(t *testing.T) {
	for _, control := range []bool{false, true} {
		t.Run(map[bool]string{false: "gesture", true: "administrative"}[control], func(t *testing.T) {
			var activeErr func() error
			synctest.Test(t, func(t *testing.T) {
				var q browserCommandQueue
				var wg sync.WaitGroup
				ran := false
				q.submit(&wg, browserCommand{viewport: true, run: func(ctx context.Context) { activeErr = ctx.Err; <-ctx.Done() }})
				synctest.Wait()
				q.submit(&wg, browserCommand{supersedesViewport: control, run: func(ctx context.Context) {
					if ctx.Err() != nil {
						t.Error("successor expired")
					}
					ran = true
				}})
				synctest.Wait()
				if control {
					if !errors.Is(activeErr(), context.Canceled) || !ran {
						t.Errorf("control did not promptly supersede: err=%v ran=%v", activeErr(), ran)
					}
				} else if activeErr() != nil || ran {
					t.Errorf("gesture interrupted viewport: err=%v ran=%v", activeErr(), ran)
				}
				q.close()
				wg.Wait()
			})
		})
	}
}

func TestBrowserViewportSupersessionValidatedClassification(t *testing.T) {
	for _, tc := range []struct {
		name, typ, raw     string
		queued, supersedes bool
	}{
		{"resize", "browser_viewport", `{"type":"browser_viewport","width":800,"height":600,"input_epoch":0,"control_epoch":1}`, true, true},
		{"navigate", "browser_input", `{"type":"browser_input","kind":"navigate","url":"https://example.com","input_epoch":0,"control_epoch":1}`, true, true},
		{"back", "browser_input", `{"type":"browser_input","kind":"navigate_back","input_epoch":0,"control_epoch":1}`, true, true},
		{"reload", "browser_input", `{"type":"browser_input","kind":"reload","input_epoch":0,"control_epoch":1}`, true, true},
		{"stop", "browser_input", `{"type":"browser_input","kind":"stop_loading","input_epoch":0,"control_epoch":1}`, true, true},
		{"release", "browser_control", `{"type":"browser_control","action":"release","input_epoch":0,"control_epoch":1}`, true, true},
		{"tab switch", "browser_tab_action", `{"type":"browser_tab_action","action":"switch","index":0,"input_epoch":0,"control_epoch":1}`, true, true},
		{"tab close", "browser_tab_action", `{"type":"browser_tab_action","action":"close","index":0,"input_epoch":0,"control_epoch":1}`, true, true},
		{"tab new", "browser_tab_action", `{"type":"browser_tab_action","action":"open","input_epoch":0,"control_epoch":1}`, true, true},
		{"take", "browser_control", `{"type":"browser_control","action":"take","input_epoch":0,"control_epoch":1}`, true, false},
		{"gesture", "browser_input", `{"type":"browser_input","kind":"mouse_move","input_epoch":0,"control_epoch":1}`, false, false},
		{"stale", "browser_viewport", `{"type":"browser_viewport","width":800,"height":600,"input_epoch":0,"control_epoch":0}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			defer f.state.setDedicatedInput(false)
			f.state.commands.running = true
			f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", []byte(tc.raw), tc.typ, f.cfg)
			q := &f.state.commands
			q.mu.Lock()
			jobs := q.jobs
			q.jobs = nil
			q.running = false
			q.mu.Unlock()
			if len(jobs) != map[bool]int{false: 0, true: 1}[tc.queued] {
				t.Fatalf("queued=%d", len(jobs))
			}
			if tc.queued && jobs[0].supersedesViewport != tc.supersedes {
				t.Fatalf("supersedes=%v want%v", jobs[0].supersedesViewport, tc.supersedes)
			}
		})
	}
}

func TestBrowserViewportQueuedIntermediateCannotDelayStop(t *testing.T) {
	var activeErr func() error
	synctest.Test(t, func(t *testing.T) {
		var q browserCommandQueue
		var wg sync.WaitGroup
		release := make(chan struct{})
		q.submit(&wg, browserCommand{viewport: true, run: func(ctx context.Context) { activeErr = ctx.Err; <-release }})
		synctest.Wait()
		queuedRan := false
		queuedCanceled := false
		stopRan := false
		discarded := false
		q.submit(&wg, browserCommand{viewport: true, supersedesViewport: true, run: func(ctx context.Context) {
			queuedRan = true
			queuedCanceled = errors.Is(ctx.Err(), context.Canceled)
			if !queuedCanceled {
				<-ctx.Done()
			}
		}, onDiscard: func() { discarded = true }})
		q.submit(&wg, browserCommand{supersedesViewport: true, run: func(ctx context.Context) { stopRan = ctx.Err() == nil }})
		if !errors.Is(activeErr(), context.Canceled) {
			t.Error("active viewport not canceled")
		}
		close(release)
		synctest.Wait()
		if !queuedRan || !queuedCanceled || !stopRan || discarded {
			t.Errorf("queued ran=%v canceled=%v stop=%v discard=%v", queuedRan, queuedCanceled, stopRan, discarded)
		}
		q.close()
		wg.Wait()
	})
}

func TestBrowserViewportQueuedHandlerSupersessionDoesNotFailNewControl(t *testing.T) {
	for _, age := range []time.Duration{0, 11 * time.Second} {
		t.Run(age.String(), func(t *testing.T) {
			f := newHandlerContextFixture(t, false)
			f.state.setDedicatedInput(true)
			defer f.state.setDedicatedInput(false)
			q := &f.state.commands
			q.running = true
			f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", []byte(`{"type":"browser_viewport","width":800,"height":600,"input_epoch":0,"control_epoch":1}`), "browser_viewport", f.cfg)
			f.handler.dispatchDedicatedControl(f.conn, f.state, "fixture-viewer", "user", []byte(`{"type":"browser_input","kind":"stop_loading","input_epoch":0,"control_epoch":2}`), "browser_input", f.cfg)
			q.mu.Lock()
			if len(q.jobs) != 2 {
				q.mu.Unlock()
				t.Fatalf("jobs=%d", len(q.jobs))
			}
			// Only the obsolete viewport is aged; the successor remains current.
			q.jobs[0].enqueued = time.Now().Add(-age)
			ran := false
			q.jobs[1].run = func(ctx context.Context) {
				if ctx.Err() != nil {
					t.Errorf("new control expired: %v", ctx.Err())
				}
				ran = true
			}
			q.mu.Unlock()
			var wg sync.WaitGroup
			wg.Add(1)
			go q.drain(&wg)
			wg.Wait()
			if !ran {
				t.Fatal("new control did not execute")
			}
			select {
			case envelope := <-f.conn.sendCh:
				t.Fatalf("obsolete viewport emitted failure/ack: %s", envelope.data)
			default:
			}
		})
	}
}

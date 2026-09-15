package browser

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// ADR-081 R3: returning success means the retired source's release completed,
// without releasing a live source. Seed ownership directly to make this test
// independent of the existing asynchronous cancellation callback's scheduling.
func TestReleaseInputSourceContextCompletesOwnedRelease(t *testing.T) {
	var released []string
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			if key, ok := action.(*input.DispatchKeyEventParams); ok && key.Type == input.KeyUp {
				released = append(released, key.Code)
			}
		}
		return nil
	})
	source, cancel := context.WithCancel(context.Background())
	cancel()
	state := lv.inputStateLocked()
	for code, owner := range map[string]context.Context{"KeyA": source, "KeyB": context.Background()} {
		held := LiveInput{SourceContext: owner, Kind: "key_down", Key: code, Code: code}
		state.held[heldInputID{target: lv.tabCtx, viewer: "viewer", key: inputHoldKey(held), source: owner}] = held
	}
	r := &LiveViewRegistry{mgr: lv.mgr, views: map[string]*LiveView{"s1": lv}}
	for range 2 {
		if err := r.ReleaseInputSourceContext(context.Background(), "s1", source); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(released, []string{"KeyA"}) {
		t.Fatalf("release commands = %v, want exactly KeyA once", released)
	}
	if len(state.held) != 1 {
		t.Fatalf("remaining held inputs = %d, want live source's one hold", len(state.held))
	}
}

func TestReleaseInputSourceContextRequiresEndedSource(t *testing.T) {
	r := &LiveViewRegistry{}
	for _, source := range []context.Context{nil, context.Background()} {
		err := r.ReleaseInputSourceContext(context.Background(), "s1", source)
		if err == nil || err.Error() != "browser live: input source must be canceled before draining" {
			t.Fatalf("live source error = %v", err)
		}
	}
}

func TestReleaseInputSourceContextWaitsForBothCommandGates(t *testing.T) {
	for _, gate := range []string{"tab", "input"} {
		t.Run(gate, func(t *testing.T) {
			lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { return nil })
			source, cancel := context.WithCancel(context.Background())
			cancel()
			if gate == "tab" {
				release, err := lv.mgr.acquireLiveTabCommand(context.Background(), "s1")
				if err != nil {
					t.Fatal(err)
				}
				defer release()
			} else {
				state := lv.inputStateLocked()
				state.gate <- struct{}{}
				defer func() { <-state.gate }()
			}
			r := &LiveViewRegistry{mgr: lv.mgr, views: map[string]*LiveView{"s1": lv}}
			ctx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer stop()
			if err := r.ReleaseInputSourceContext(ctx, "s1", source); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("busy %s gate result=%v, want caller deadline", gate, err)
			}
		})
	}
}

func TestReleaseInputSourceContextSurfacesCleanupFailure(t *testing.T) {
	failure := errors.New("release transport failed")
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { return failure })
	source, cancel := context.WithCancel(context.Background())
	cancel()
	held := LiveInput{SourceContext: source, Kind: "key_down", Key: "a", Code: "KeyA"}
	state := lv.inputStateLocked()
	state.held[heldInputID{target: lv.tabCtx, viewer: "viewer", key: inputHoldKey(held), source: source}] = held
	r := &LiveViewRegistry{mgr: lv.mgr, views: map[string]*LiveView{"s1": lv}}
	if err := r.ReleaseInputSourceContext(context.Background(), "s1", source); !errors.Is(err, failure) {
		t.Fatalf("cleanup result=%v, want transport failure", err)
	}
	if len(state.held) != 1 {
		t.Fatal("failed release lost held state")
	}
}

package browser

import (
	"context"
	"errors"
	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Expectations derive from FR-004/FR-011: cancellation bounds input, and a
// departing viewer releases its own held state without releasing another's.
func TestLiveInputCallerCancellationStopsDispatch(t *testing.T) {
	entered := make(chan struct{})
	lv := newNavigateTestLiveView(t, func(ctx context.Context, timeout time.Duration, _ ...chromedp.Action) error {
		close(entered)
		bounded, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		<-bounded.Done()
		return bounded.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- lv.dispatchInputContext(ctx, "a", LiveInput{Kind: "text", Text: "a"}) }()
	<-entered
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("want cancellation, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("caller cancellation did not stop browser input")
	}
}

func TestLiveInputDetachReleasesOnlyFinalKeyOwner(t *testing.T) {
	var keys []*input.DispatchKeyEventParams
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, a := range actions {
			if key, ok := a.(*input.DispatchKeyEventParams); ok {
				keys = append(keys, key)
			}
		}
		return nil
	})
	down := LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft", KeyCode: 16}
	for _, viewer := range []string{"a", "b"} {
		if err := lv.dispatchInput(viewer, down); err != nil {
			t.Fatal(err)
		}
	}
	lv.detach("a")
	if len(keys) != 2 {
		t.Fatalf("detach released another viewer's hold: %#v", keys)
	}
	lv.detach("b")
	if len(keys) != 3 || keys[2].Type != input.KeyUp || keys[2].Code != "ShiftLeft" {
		t.Fatalf("last owner did not release ShiftLeft: %#v", keys)
	}
}

func TestLiveInputExplicitReleasePreservesOtherOwner(t *testing.T) {
	var types []input.KeyType
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, a := range actions {
			if key, ok := a.(*input.DispatchKeyEventParams); ok {
				types = append(types, key.Type)
			}
		}
		return nil
	})
	down := LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}
	for _, viewer := range []string{"a", "b"} {
		if err := lv.dispatchInput(viewer, down); err != nil {
			t.Fatal(err)
		}
	}
	up := down
	up.Kind = "key_up"
	if err := lv.dispatchInput("a", up); err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 {
		t.Fatalf("release from first owner emitted keyUp while second still held it: %v", types)
	}
	if err := lv.dispatchInput("b", up); err != nil {
		t.Fatal(err)
	}
	if len(types) != 3 || types[2] != input.KeyUp {
		t.Fatalf("last release sequence: %v", types)
	}
}

func TestLiveInputDetachedViewerCannotReplay(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	lv.detach("a")
	reg := &LiveViewRegistry{mgr: lv.mgr, views: map[string]*LiveView{"s1": lv}}
	err := reg.InputContext(context.Background(), "s1", "a", LiveInput{Kind: "text", Text: "stale"})
	if err == nil || !IsBenignLiveInputError(err) || calls != 0 {
		t.Fatalf("detached viewer input reached browser: error=%v calls=%d", err, calls)
	}
}

func TestLiveInputViewportReadUsesRemainingSettleBudget(t *testing.T) {
	var requested time.Duration
	lv := newNavigateTestLiveView(t, func(_ context.Context, timeout time.Duration, actions ...chromedp.Action) error {
		requested = timeout
		a := actions[0].(layoutMetricsAction)
		*a.w = 800
		*a.h = 600
		return nil
	})
	w, h, err := lv.settleCSSViewport(context.Background(), 800, 600)
	if err != nil || w != 800 || h != 600 {
		t.Fatalf("viewport result %dx%d %v", w, h, err)
	}
	if requested <= 0 || requested > 600*time.Millisecond {
		t.Fatalf("one layout read can overrun entire600ms settle budget: %s", requested)
	}
}

func TestLiveInputMouseDragCarriesHeldButtons(t *testing.T) {
	var buttons []int64
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, a := range actions {
			if mouse, ok := a.(*input.DispatchMouseEventParams); ok {
				buttons = append(buttons, mouse.Buttons)
			}
		}
		return nil
	})
	for _, in := range []LiveInput{
		{Kind: "mouse_down", Button: "left", HasXY: true, X: 10, Y: 20},
		{Kind: "mouse_move", Button: "left", HasXY: true, X: 30, Y: 40},
		{Kind: "mouse_up", Button: "left", HasXY: true, X: 30, Y: 40},
	} {
		if err := lv.dispatchInput("a", in); err != nil {
			t.Fatal(err)
		}
	}
	if len(buttons) != 3 || buttons[0] != 1 || buttons[1] != 1 || buttons[2] != 0 {
		t.Fatalf("left-button drag must carry pressed/pressed/released state: %v", buttons)
	}
}

func TestLiveInputUncertainPressIsReleasedOnDetach(t *testing.T) {
	var keys []*input.DispatchKeyEventParams
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		keys = append(keys, actions[0].(*input.DispatchKeyEventParams))
		if len(keys) == 1 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Control", Code: "ControlLeft", KeyCode: 17}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ambiguous timeout, got %v", err)
	}
	lv.detach("a")
	if len(keys) != 2 || keys[1].Type != input.KeyUp || keys[1].Code != "ControlLeft" {
		t.Fatalf("uncertain press was not conservatively released: %#v", keys)
	}
}

func TestLiveInputRejectedPressDoesNotCreateHeldState(t *testing.T) {
	calls := 0
	rejection := &cdproto.Error{Code: -32602, Message: "invalid key parameter"}
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return rejection })
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Control", Code: "ControlLeft"}); !errors.Is(err, rejection) {
		t.Fatalf("want rejection, got %v", err)
	}
	lv.detach("a")
	if calls != 1 {
		t.Fatalf("definitively rejected press generated a release: %d calls", calls)
	}
}

func TestLiveInputHeldReleaseSurvivesRatePressure(t *testing.T) {
	releases := 0
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if a, ok := actions[0].(*input.DispatchKeyEventParams); ok && a.Type == input.KeyUp {
			releases++
		}
		return nil
	})
	// Existing public limit is100discrete events/second. Releases of tracked
	// holds must still be accepted after that budget is consumed.
	for range 100 {
		if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_up", Key: "Shift", Code: "ShiftLeft"}); err != nil {
		t.Fatalf("held release rejected under rate pressure: %v", err)
	}
	if releases != 1 {
		t.Fatalf("want one Shift release, got %d", releases)
	}
}

func TestLiveInputFailedReleaseRetriesBeforeNextCommand(t *testing.T) {
	var sequence []string
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case *input.DispatchKeyEventParams:
			sequence = append(sequence, string(a.Type))
		default:
			sequence = append(sequence, "text")
		}
		if len(sequence) == 2 {
			return context.DeadlineExceeded
		}
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}); err != nil {
		t.Fatal(err)
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_up", Key: "Shift", Code: "ShiftLeft"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("release error: %v", err)
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "text", Text: "safe"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"rawKeyDown", "keyUp", "keyUp", "text"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("release must retry before new input: got %v want %v", sequence, want)
	}
}

func TestLiveInputTransportFailurePressIsReleased(t *testing.T) {
	var sequence []input.KeyType
	disconnected := errors.New("CDP connection closed after send")
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		sequence = append(sequence, actions[0].(*input.DispatchKeyEventParams).Type)
		if len(sequence) == 1 {
			return disconnected
		}
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}); !errors.Is(err, disconnected) {
		t.Fatalf("error: %v", err)
	}
	lv.detach("a")
	if len(sequence) != 2 || sequence[1] != input.KeyUp {
		t.Fatalf("ambiguous transport failure left hold: %v", sequence)
	}
}

func TestLiveInputCanceledViewportDoesNotPoisonNextInput(t *testing.T) {
	entered := make(chan struct{})
	lv := newNavigateTestLiveView(t, func(ctx context.Context, _ time.Duration, _ ...chromedp.Action) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- lv.dispatchInputContext(ctx, "a", LiveInput{Kind: "mouse_move", HasXY: true, X: 10, Y: 20, CaptureWidth: 800, CaptureHeight: 600})
	}()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("want caller cancellation, got %v", err)
	}
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.viewportFetchFailures != 0 || !lv.nextFetchAfter.IsZero() {
		t.Fatalf("obsolete input poisoned geometry: failures=%d backoff=%v", lv.viewportFetchFailures, lv.nextFetchAfter)
	}
}

// This executor replaces only the external Chrome protocol boundary. The
// production action must finish on the acknowledgment; no load event is sent.
type liveInputExecutor func(context.Context, string, any, any) error

func (f liveInputExecutor) Execute(ctx context.Context, method string, params, res any) error {
	return f(ctx, method, params, res)
}
func TestLiveInputNavigationAcknowledgesWithoutLoadEvent(t *testing.T) {
	for _, destinationError := range []string{"", "net::ERR_NAME_NOT_RESOLVED"} {
		t.Run(destinationError, func(t *testing.T) {
			calls := 0
			ctx := cdp.WithExecutor(context.Background(), liveInputExecutor(func(_ context.Context, method string, params, result any) error {
				calls++
				if method != "Page.navigate" {
					t.Fatalf("unexpected protocol command %s", method)
				}
				if params.(*page.NavigateParams).URL != "https://example.com/destination" {
					t.Fatalf("wrong destination: %+v", params)
				}
				result.(*page.NavigateReturns).ErrorText = destinationError
				return nil
			}))
			action, err := buildInputAction(LiveInput{Kind: "navigate", URL: "https://example.com/destination"})
			if err != nil {
				t.Fatal(err)
			}
			err = action.Do(ctx)
			if destinationError == "" && err != nil {
				t.Fatal(err)
			}
			if destinationError != "" && (err == nil || !strings.Contains(err.Error(), destinationError)) {
				t.Fatalf("destination error lost: %v", err)
			}
			if calls != 1 {
				t.Fatalf("navigation issued %d protocol commands", calls)
			}
		})
	}
}

func TestLiveInputQueuedCancellationNeverDispatches(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	lv.mu.Lock()
	gate := lv.inputStateLocked().gate
	lv.mu.Unlock()
	gate <- struct{}{}
	defer func() { <-gate }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- lv.dispatchInputContext(ctx, "a", LiveInput{Kind: "text", Text: "obsolete"}) }()
	deadline := time.After(time.Second)
	for {
		lv.mu.Lock()
		queued := len(lv.inputState.requests) == 1
		lv.mu.Unlock()
		if queued {
			break
		}
		select {
		case <-deadline:
			t.Fatal("request did not reach input gate")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued cancellation: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("canceled request remained blocked behind input gate")
	}
	if calls != 0 {
		t.Fatalf("canceled input was dispatched: %d calls", calls)
	}
}

func TestLiveInputDetachCancelsInFlightPressThenReleases(t *testing.T) {
	entered := make(chan struct{})
	var sequence []input.KeyType
	lv := newNavigateTestLiveView(t, func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		sequence = append(sequence, actions[0].(*input.DispatchKeyEventParams).Type)
		if len(sequence) == 1 {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	result := make(chan error, 1)
	go func() { result <- lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}) }()
	<-entered
	lv.detach("a")
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("inflight cancellation: %v", err)
	}
	if len(sequence) != 2 || sequence[1] != input.KeyUp {
		t.Fatalf("detach raced past accepted press: %v", sequence)
	}
}

func TestLiveInputTabRetirementReleasesOriginalTarget(t *testing.T) {
	type targetKey struct{}
	oldTarget := context.WithValue(context.Background(), targetKey{}, "old")
	newTarget := context.WithValue(context.Background(), targetKey{}, "new")
	released := make(chan string, 1)
	lv := newNavigateTestLiveView(t, func(ctx context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if key, ok := actions[0].(*input.DispatchKeyEventParams); ok && key.Type == input.KeyUp {
			released <- ctx.Value(targetKey{}).(string)
		}
		return nil
	})
	lv.tabCtx = oldTarget
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "Shift", Code: "ShiftLeft"}); err != nil {
		t.Fatal(err)
	}
	lv.mu.Lock()
	lv.tabCtx = newTarget
	lv.mu.Unlock()
	lv.retireInputTarget(oldTarget, newTarget)
	select {
	case target := <-released:
		if target != "old" {
			t.Fatalf("release targeted %q instead of old tab", target)
		}
	case <-time.After(time.Second):
		t.Fatal("tab retirement did not release held key")
	}
	// A subsequent request also waits for the cleanup worker's gate to drain.
	if err := lv.dispatchInput("a", LiveInput{Kind: "text", Text: "new tab"}); err != nil {
		t.Fatal(err)
	}
}

func TestLiveInputUnmappedMouseReleaseRetriesBeforeNextCommand(t *testing.T) {
	var sequence []string
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		switch a := actions[0].(type) {
		case *input.DispatchMouseEventParams:
			sequence = append(sequence, string(a.Type))
		case layoutMetricsAction:
			return errors.New("layout unavailable")
		default:
			sequence = append(sequence, "text")
		}
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "mouse_down", Button: "left", HasXY: true, X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "mouse_up", Button: "left", HasXY: true, X: 30, Y: 40, CaptureWidth: 800, CaptureHeight: 600}); err == nil {
		t.Fatal("unknown coordinate mapping should reject pointer position")
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "text", Text: "next"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"mousePressed", "mouseReleased", "text"}
	if !reflect.DeepEqual(sequence, want) {
		t.Fatalf("lost mouse release after geometry failure: got %v want %v", sequence, want)
	}
}

func TestLiveInputKeyCodeIdentitySurvivesKeyCaseChange(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_down", Key: "A", KeyCode: 65}); err != nil {
		t.Fatal(err)
	}
	if err := lv.dispatchInput("a", LiveInput{Kind: "key_up", Key: "a", KeyCode: 65}); err != nil {
		t.Fatal(err)
	}
	lv.detach("a")
	if calls != 2 {
		t.Fatalf("released physical key retained stale case-sensitive ownership: %d commands", calls)
	}
}

func TestLiveInputUnownedReleaseNeverReachesBrowser(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	for _, in := range []LiveInput{{Kind: "key_up", Key: "Shift", Code: "ShiftLeft"}, {Kind: "mouse_up", Button: "left", HasXY: true, X: 10, Y: 20}} {
		if err := lv.dispatchInput("never-pressed", in); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatalf("unowned releases reached browser: %d", calls)
	}
}

func TestLiveInputRepeatedDetachDoesNotRepeatSuccessfulRelease(t *testing.T) {
	var sequence []input.MouseType
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		sequence = append(sequence, actions[0].(*input.DispatchMouseEventParams).Type)
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "mouse_down", Button: "left", HasXY: true, X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	lv.detach("a")
	lv.detach("a")
	if len(sequence) != 2 || sequence[1] != input.MouseReleased {
		t.Fatalf("repeated successful cleanup: %v", sequence)
	}
}

func TestLiveInputCleanupUsesSharedPointersLatestPosition(t *testing.T) {
	var release *input.DispatchMouseEventParams
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		if a, ok := actions[0].(*input.DispatchMouseEventParams); ok && a.Type == input.MouseReleased {
			release = a
		}
		return nil
	})
	if err := lv.dispatchInput("a", LiveInput{Kind: "mouse_down", Button: "left", HasXY: true, X: 10, Y: 20}); err != nil {
		t.Fatal(err)
	}
	if err := lv.dispatchInput("b", LiveInput{Kind: "mouse_move", HasXY: true, X: 30, Y: 40}); err != nil {
		t.Fatal(err)
	}
	lv.detach("a")
	if release == nil || release.X != 30 || release.Y != 40 {
		t.Fatalf("cleanup moved shared pointer backwards: %+v", release)
	}
}

package browser

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/chromedp"
)

// The oracle is the sequence of browser commands: source replacement must
// release the original hold, and an old source must never release a new hold.
func TestLiveInputSourceCancellationReleasesWithoutNextInput(t *testing.T) {
	released := make(chan string, 4)
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		for _, action := range actions {
			if key, ok := action.(*input.DispatchKeyEventParams); ok && key.Type == input.KeyUp {
				released <- key.Code
			}
		}
		return nil
	})
	picture := installInputTestPicture(t, lv)
	source, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := lv.dispatchInput("viewer", inputWithTestPicture(picture, LiveInput{SourceContext: source, Kind: "key_down", Key: "Shift", Code: "ShiftLeft", KeyCode: 16})); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case code := <-released:
		if code != "ShiftLeft" {
			t.Fatalf("released %q, want original ShiftLeft", code)
		}
	case <-time.After(time.Second):
		t.Fatal("disconnect left Shift held until another input arrived")
	}
}

func TestLiveInputSourcesShareViewerWithoutSharingHoldOwnership(t *testing.T) {
	var mu sync.Mutex
	var sequence []input.KeyType
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		mu.Lock()
		defer mu.Unlock()
		for _, action := range actions {
			if key, ok := action.(*input.DispatchKeyEventParams); ok {
				sequence = append(sequence, key.Type)
			}
		}
		return nil
	})
	picture := installInputTestPicture(t, lv)
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	send := func(source context.Context, kind string) {
		t.Helper()
		if err := lv.dispatchInput("viewer", inputWithTestPicture(picture, LiveInput{SourceContext: source, Kind: kind, Key: "Shift", Code: "ShiftLeft", KeyCode: 16})); err != nil {
			t.Fatal(err)
		}
	}
	send(first, "key_down")
	send(second, "key_down")
	send(first, "key_up")
	mu.Lock()
	if len(sequence) != 2 {
		t.Errorf("one source released another source's hold: %v", sequence)
	}
	mu.Unlock()
	send(second, "key_up")
	mu.Lock()
	defer mu.Unlock()
	if len(sequence) != 3 || sequence[2] != input.KeyUp {
		t.Fatalf("last source did not release exactly once: %v", sequence)
	}
}

func TestLiveInputCanceledSourceCannotDispatch(t *testing.T) {
	calls := 0
	lv := newNavigateTestLiveView(t, func(context.Context, time.Duration, ...chromedp.Action) error { calls++; return nil })
	picture := installInputTestPicture(t, lv)
	source, cancel := context.WithCancel(context.Background())
	cancel()
	err := lv.dispatchInput("viewer", inputWithTestPicture(picture, LiveInput{SourceContext: source, Kind: "text", Text: "obsolete"}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled source accepted: %v", err)
	}
	if calls != 0 {
		t.Fatalf("canceled source delivered %d commands", calls)
	}
}

func TestLiveInputSourceCancellationStopsInFlightCommand(t *testing.T) {
	entered := make(chan struct{})
	lv := newNavigateTestLiveView(t, func(ctx context.Context, _ time.Duration, _ ...chromedp.Action) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	picture := installInputTestPicture(t, lv)
	source, cancelSource := context.WithCancel(context.Background())
	defer cancelSource()
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	result := make(chan error, 1)
	go func() {
		result <- lv.dispatchInputContext(caller, "viewer", inputWithTestPicture(picture, LiveInput{SourceContext: source, Kind: "text", Text: "obsolete"}))
	}()
	<-entered
	cancelSource()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("source cancellation lost: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		cancelCaller()
		<-result
		t.Fatal("source cancellation did not interrupt its browser command")
	}
}

func TestLiveInputReplacementFlushesOldSourceBeforeNewPress(t *testing.T) {
	var mu sync.Mutex
	var sequence []input.KeyType
	lv := newNavigateTestLiveView(t, func(_ context.Context, _ time.Duration, actions ...chromedp.Action) error {
		mu.Lock()
		defer mu.Unlock()
		for _, action := range actions {
			if key, ok := action.(*input.DispatchKeyEventParams); ok {
				sequence = append(sequence, key.Type)
			}
		}
		return nil
	})
	picture := installInputTestPicture(t, lv)
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	second, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	down := LiveInput{SourceContext: first, Kind: "key_down", Key: "a", Code: "KeyA", KeyCode: 65}
	if err := lv.dispatchInput("viewer", inputWithTestPicture(picture, down)); err != nil {
		t.Fatal(err)
	}
	cancelFirst()
	down.SourceContext = second
	if err := lv.dispatchInput("viewer", inputWithTestPicture(picture, down)); err != nil {
		t.Fatal(err)
	}
	down.Kind = "key_up"
	if err := lv.dispatchInput("viewer", inputWithTestPicture(picture, down)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []input.KeyType{input.KeyRawDown, input.KeyUp, input.KeyRawDown, input.KeyUp}
	if len(sequence) != len(want) {
		t.Fatalf("source replacement sequence %v, want %v", sequence, want)
	}
	for i := range want {
		if sequence[i] != want[i] {
			t.Fatalf("source replacement sequence %v, want %v", sequence, want)
		}
	}
}

package gateway

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
)

func webRTCInputRouteManager(t *testing.T) *browser.BrowserManager {
	t.Helper()
	cfg, err := browser.DefaultConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.ProfileDir = filepath.Join(t.TempDir(), "profile")
	cfg.ExecPath = filepath.Join(cfg.ProfileDir, "missing-chrome")
	mgr, err := browser.NewBrowserManager(cfg, security.NewSSRFChecker(nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mgr.Shutdown)
	return mgr
}

func TestWebRTCContextRouteRejectsInvalidOrigin(t *testing.T) {
	mgr := webRTCInputRouteManager(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		parent  context.Context
		manager *browser.BrowserManager
		panel   string
	}{
		{"nil-parent", nil, mgr, "panel"}, {"canceled-parent", canceled, mgr, "panel"},
		{"nil-manager", context.Background(), nil, "panel"}, {"empty-panel", context.Background(), mgr, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := withWebRTCInputRoute(tc.parent, tc.manager, tc.panel, nil)
			if got != nil || err == nil {
				t.Fatalf("invalid origin produced context=%v error=%v", got, err)
			}
			if tc.name == "canceled-parent" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestWebRTCContextSinkKeepsOriginalRouteAndExactSource(t *testing.T) {
	type call struct {
		ctx           context.Context
		manager       *browser.BrowserManager
		panel, viewer string
		input         browser.LiveInput
	}
	var calls []call
	failure := errors.New("browser command failed")
	sink := newWebRTCContextInputSinkWithDispatch(true, func(ctx context.Context, mgr *browser.BrowserManager, panel, viewer string, in browser.LiveInput) error {
		calls = append(calls, call{ctx, mgr, panel, viewer, in})
		return failure
	})
	first, second := webRTCInputRouteManager(t), webRTCInputRouteManager(t)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	var replies [2]int
	sources := make([]context.Context, 0, 2)
	newSource := func(route context.Context) context.Context {
		source, cancel := context.WithCancel(route)
		t.Cleanup(cancel)
		return source
	}
	for index, mgr := range []*browser.BrowserManager{first, second} {
		route, err := withWebRTCInputRoute(parent, mgr, []string{"panel-A", "panel-B"}[index], func(ctx context.Context, kind string, err error) {
			replies[index]++
			if ctx != sources[index] || kind != "mouse_down" || !errors.Is(err, failure) {
				t.Errorf("reply changed source/kind/error: ctx%v kind%s err%v", ctx, kind, err)
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, newSource(route))
	}
	raw := []byte(`{"type":"browser_input","kind":"mouse_down","x":0,"y":27.5,"button":"left","modifiers":3,"capture_width":640,"capture_height":480,"capture_generation":9,"capture_id":"capture-A"}`)
	// Creating a second route for the same viewer must not replace the first.
	for _, source := range sources {
		sink(source, "same-viewer", raw)
	}
	if len(calls) != 2 || replies != [2]int{1, 1} {
		t.Fatalf("dispatches=%d replies=%v want2 and[1,1]", len(calls), replies)
	}
	for index, got := range calls {
		want := browser.LiveInput{SourceContext: sources[index], Kind: "mouse_down", X: 0, Y: 27.5, HasXY: true, Button: "left", Modifiers: 3, CaptureWidth: 640, CaptureHeight: 480, CaptureGeneration: 9, CaptureID: "capture-A"}
		if got.ctx != sources[index] || got.manager != []*browser.BrowserManager{first, second}[index] || got.panel != []string{"panel-A", "panel-B"}[index] || got.viewer != "same-viewer" || got.input != want {
			t.Errorf("dispatch%d changed immutable route/context/input: %+v want input%+v", index, got, want)
		}
		if sources[index].Err() != nil {
			t.Error("sink canceled its surviving persistent source after return")
		}
	}
	cancelParent()
	sink(sources[0], "same-viewer", raw)
	if len(calls) != 2 || replies != [2]int{1, 1} {
		t.Fatal("canceled old route dispatched or reported through a replacement")
	}
}

func TestWebRTCContextSinkRejectsMissingOrEndedSource(t *testing.T) {
	mgr := webRTCInputRouteManager(t)
	parent, cancelParent := context.WithCancel(context.Background())
	route, err := withWebRTCInputRoute(parent, mgr, "panel", nil)
	if err != nil {
		t.Fatal(err)
	}
	source, cancelSource := context.WithCancel(route)
	cancelSource()
	detachedCancellation := context.WithoutCancel(route)
	cancelParent()
	calls := 0
	sink := newWebRTCContextInputSinkWithDispatch(false, func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error {
		calls++
		return nil
	})
	for _, ctx := range []context.Context{nil, context.Background(), source, detachedCancellation} {
		sink(ctx, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"old"}`))
	}
	if calls != 0 {
		t.Fatalf("missing/ended origin made%d dispatches", calls)
	}
}

func TestWebRTCContextSinkSuppressesCanceledCompletion(t *testing.T) {
	for _, which := range []string{"source", "original"} {
		t.Run(which, func(t *testing.T) {
			mgr := webRTCInputRouteManager(t)
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			replies := 0
			route, err := withWebRTCInputRoute(parent, mgr, "panel", func(context.Context, string, error) { replies++ })
			if err != nil {
				t.Fatal(err)
			}
			source, cancelSource := context.WithCancel(context.WithoutCancel(route))
			defer cancelSource()
			calls := 0
			sink := newWebRTCContextInputSinkWithDispatch(false, func(ctx context.Context, _ *browser.BrowserManager, _, _ string, _ browser.LiveInput) error {
				calls++
				if ctx != source {
					t.Errorf("dispatch lost exact source context")
				}
				if which == "source" {
					cancelSource()
				} else {
					cancelParent()
				}
				return errors.New("late command failure")
			})
			sink(source, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"in-flight"}`))
			if calls != 1 || replies != 0 {
				t.Fatalf("canceled completion dispatches=%d replies=%d want1,0", calls, replies)
			}
		})
	}
}

// The queue-timing observer range obeys the same safe-integer ceiling as the
// counters themselves: a range ending at the boundary is adopted, one ending
// past it keeps the single-input default. The sampling failure window is the
// only surface where the adopted range becomes observable.
func TestWebRTCContextSinkTimingRangeSafeIntegerBound(t *testing.T) {
	const maximum = 9007199254740991
	for _, tc := range []struct {
		name                      string
		first, last, count        int
		wantFirst, wantLast, want int
	}{
		{"midrange range is adopted", 0, 5, 6, 0, 5, 6},
		{"boundary range is adopted", 0, maximum, maximum + 1, 0, maximum, maximum + 1},
		{"one past boundary is refused", 0, maximum + 1, maximum + 2, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIPUS_BROWSER_INPUT_TIMING", "1")
			mgr := webRTCInputRouteManager(t)
			route, err := withWebRTCInputRoute(context.Background(), mgr, "panel", nil)
			if err != nil {
				t.Fatal(err)
			}
			sampling := &browserInputTimingSampling{}
			queued := webrtc.InputQueueTiming{FirstReliableSeq: tc.first, LastReliableSeq: tc.last, InputCount: tc.count}
			sink := newWebRTCContextInputSinkWithDispatchSampling(false, sampling, func(context.Context, *browser.BrowserManager, string, string, browser.LiveInput) error {
				return nil
			}, func() webrtc.InputQueueTiming { return queued })
			sink(route, "viewer", []byte(`{"type":"browser_input","kind":"mouse_down"}`))
			var record map[string]any
			sampling.failure("queue_full", time.Now(), func(args ...any) {
				if record != nil {
					return
				}
				record = map[string]any{}
				for i := 0; i+1 < len(args); i += 2 {
					if key, ok := args[i].(string); ok {
						record[key] = args[i+1]
					}
				}
			})
			if record == nil {
				t.Fatal("timing record never reached the failure window")
			}
			if record["first_reliable_seq"] != tc.wantFirst || record["last_reliable_seq"] != tc.wantLast || record["input_count"] != tc.want {
				t.Fatalf("adopted range first=%v last=%v count=%v want %d/%d/%d", record["first_reliable_seq"], record["last_reliable_seq"], record["input_count"], tc.wantFirst, tc.wantLast, tc.want)
			}
		})
	}
}

func TestWebRTCContextSinkSchemaAndParsingBoundaries(t *testing.T) {
	mgr := webRTCInputRouteManager(t)
	route, err := withWebRTCInputRoute(context.Background(), mgr, "panel", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, validate := range []bool{true, false} {
		var texts []string
		sink := newWebRTCContextInputSinkWithDispatch(validate, func(_ context.Context, _ *browser.BrowserManager, _, _ string, in browser.LiveInput) error {
			texts = append(texts, in.Text)
			return nil
		})
		sink(route, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"`+strings.Repeat("a", 8192)+`"}`))
		sink(route, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"`+strings.Repeat("b", 8193)+`"}`))
		sink(route, "viewer", []byte(`{"kind":`))
		want := 1
		if !validate {
			want = 2
		}
		if len(texts) != want || len(texts[0]) != 8192 || (!validate && len(texts[1]) != 8193) {
			t.Fatalf("validate%v dispatched text lengths%v want count%d with8192/8193 boundaries", validate, func() []int {
				lengths := make([]int, 0, len(texts))
				for _, text := range texts {
					lengths = append(lengths, len(text))
				}
				return lengths
			}(), want)
		}
	}
}

func TestWebRTCContextSinkProductionWrapperUsesRealManagerErrors(t *testing.T) {
	mgr := webRTCInputRouteManager(t)
	type observation struct {
		ctx  context.Context
		kind string
		err  error
	}
	observations := make(chan observation, 1)
	route, err := withWebRTCInputRoute(context.Background(), mgr, "original-panel", func(ctx context.Context, kind string, err error) {
		observations <- observation{ctx: ctx, kind: kind, err: err}
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := newWebRTCContextInputSink(true)
	sink(route, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"probe"}`))
	var observed observation
	select {
	case observed = <-observations:
	default:
		t.Fatal("real manager error did not reach its original reply")
	}
	if observed.ctx != route || observed.kind != "text" || observed.err == nil || !strings.Contains(observed.err.Error(), `session "original-panel"`) {
		t.Fatalf("real manager error route: ctx%v kind%s err%v", observed.ctx, observed.kind, observed.err)
	}
	// This creates a live-view record without attaching the viewer. Its real
	// benign rejection must remain silent, unlike a missing live view above.
	mgr.Live().TakeControl("original-panel", "another-viewer")
	sink(route, "viewer", []byte(`{"type":"browser_input","kind":"text","text":"probe"}`))
	select {
	case observed = <-observations:
		t.Fatalf("benign real-manager rejection surfaced: %v", observed.err)
	default:
	}
}

package gateway

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func TestBrowserInputTimingIsOptInBoundedAndConcurrent(t *testing.T) {
	frame := generated.BrowserInputFrame{Kind: "mouse_down"}
	require.Nil(t, newBrowserInputTiming(false, nil, frame, time.Time{}), "disabled path must not access clock or counter")
	require.Nil(t, newBrowserInputTiming(true, nil, generated.BrowserInputFrame{Kind: "text"}, time.Time{}), "text is never instrumented")
	var seq atomic.Uint64
	// The authorized diagnostic budget is 512 records, independent of the
	// implementation constant. Buffer all attempts so an overrun fails, not hangs.
	const expectedLimit = 512
	ordinals := make(chan uint64, 1024)
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 64 {
				if p := newBrowserInputTiming(true, &seq, frame, time.Now()); p != nil {
					ordinals <- p.ordinal
				}
			}
		})
	}
	wg.Wait()
	close(ordinals)
	seen := map[uint64]bool{}
	for n := range ordinals {
		require.False(t, seen[n], "duplicate input ordinal")
		seen[n] = true
	}
	require.Len(t, seen, expectedLimit)
	require.Equal(t, uint64(expectedLimit), seq.Load())
	require.True(t, seen[1])
	require.True(t, seen[expectedLimit])
	require.Nil(t, newBrowserInputTiming(true, &seq, frame, time.Now()), "limit+1 must not allocate a record")
}

func TestBrowserInputTimingUsesLocalOffsetsAndRedactsPayload(t *testing.T) {
	var seq atomic.Uint64
	sensitive := "private URL, typed text, or supplied capture identifier"
	frame := generated.BrowserInputFrame{Kind: "mouse_up", CaptureId: &sensitive, Text: &sensitive, Url: &sensitive}
	origin := time.Unix(100, 0)
	p := newBrowserInputTiming(true, &seq, frame, origin)
	require.NotNil(t, p)
	now := origin.Add(10 * time.Millisecond)
	p.now = func() time.Time { return now }
	var record map[string]any
	p.emit = func(args ...any) {
		record = make(map[string]any)
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			require.True(t, ok)
			record[key] = args[i+1]
		}
	}
	p.mark("queue_started")
	now = origin.Add(25 * time.Millisecond)
	p.mark("cdp_start")
	now = origin.Add(37 * time.Millisecond)
	p.mark("cdp_done")
	now = origin.Add(40 * time.Millisecond)
	p.finish()
	require.Equal(t, map[string]any{"input_ordinal": uint64(1), "kind": "mouse_up", "capture_id": "", "capture_generation": 0, "received_unix_ms": int64(100000), "stage_offsets_ms": map[string]float64{"queue_started": 10, "cdp_start": 25, "cdp_done": 37, "finished": 40}, "outcome": "not_dispatched"}, record)
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	require.NotContains(t, string(raw), sensitive)
	valid := strings.Repeat("a", 64)
	frame.CaptureId = &valid
	require.Equal(t, valid, newBrowserInputTiming(true, &seq, frame, origin).captureID)
}

func TestBrowserInputTimingQueuedDiscardIsReported(t *testing.T) {
	for _, closeQueue := range []bool{false, true} {
		name := "discard"
		if closeQueue {
			name = "close"
		}
		t.Run(name, func(t *testing.T) {
			q := &browserCommandQueue{}
			var wg sync.WaitGroup
			started, release := make(chan struct{}), make(chan struct{})
			require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { close(started); <-release }}))
			<-started
			var seq atomic.Uint64
			p := newBrowserInputTiming(true, &seq, generated.BrowserInputFrame{Kind: "mouse_down"}, time.Now())
			reports := 0
			p.emit = func(...any) { reports++ }
			require.True(t, q.submit(&wg, browserCommand{run: func(context.Context) { t.Error("discarded input ran") }, onDiscard: func() { p.outcome = "queue_discarded"; p.finish() }}))
			if closeQueue {
				q.close()
			} else {
				q.discard()
			}
			close(release)
			wg.Wait()
			require.Equal(t, 1, reports)
			require.Equal(t, "queue_discarded", p.outcome)
			require.Contains(t, p.offsets, "finished")
			require.NotContains(t, p.offsets, "cdp_start")
		})
	}
}

func TestBrowserInputTimingReachesLiveAdmission(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	attachment := f.state.commandAttachment()
	var seq atomic.Uint64
	p := newBrowserInputTiming(true, &seq, generated.BrowserInputFrame{Kind: "mouse_down"}, time.Now())
	// Missing picture is deliberately rejected by real live admission. Timing
	// must reach that boundary and must never invent CDP success for a refusal.
	f.handler.handleInputContext(attachment.ctx, f.conn, f.state, attachment, "fixture-viewer", []byte(`{"type":"browser_input","kind":"mouse_down","button":"left","x":10,"y":10}`), p)
	require.Contains(t, p.offsets, "live_entry")
	require.NotContains(t, p.offsets, "cdp_start")
	require.Equal(t, "failed", p.outcome)
}

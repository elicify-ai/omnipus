package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc"
	"github.com/stretchr/testify/require"
)

type dedicatedTimingLog struct {
	records chan map[string]any
	message string
}

func (h *dedicatedTimingLog) Enabled(context.Context, slog.Level) bool { return true }
func (h *dedicatedTimingLog) Handle(_ context.Context, r slog.Record) error {
	message := h.message
	if message == "" {
		message = "browser input timing"
	}
	if r.Message == message {
		record := map[string]any{}
		r.Attrs(func(a slog.Attr) bool { record[a.Key] = a.Value.Any(); return true })
		h.records <- record
	}
	return nil
}

func TestDedicatedFailureLogSurvivesCanceledSourceAndIsBounded(t *testing.T) {
	capture := &dedicatedTimingLog{records: make(chan map[string]any, 8), message: "browser dedicated input failed"}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	source, cancel := context.WithCancel(context.Background())
	cancel()
	d := &browserDedicatedInput{epoch: 1, offer: 1, control: 2}
	conn := newTestBrowserWSConn()
	sender := d.stateSender(conn, browserAttachmentRequest{ctx: source}, "PRIVATE viewer", generated.BrowserInputOfferFrame{InputEpoch: 1, OfferId: 1, SessionId: "PRIVATE session"}, source)
	sender("reliable input queue expired")
	sender("input connection closed")
	select {
	case record := <-capture.records:
		require.Equal(t, map[string]any{"input_epoch": int64(1), "offer_id": int64(1), "control_epoch": int64(2), "reason": "queue_expired"}, record)
	default:
		t.Fatal("queue retirement lost its server diagnostic when source ended")
	}
	select {
	case <-capture.records:
		t.Fatal("duplicate failure log for one epoch")
	default:
	}
	d.epoch, d.offer = 2, 2
	sender("reliable input queue expired")
	select {
	case <-capture.records:
		t.Fatal("stale source logged a new failure")
	default:
	}
	d.stateSender(conn, browserAttachmentRequest{ctx: source}, "PRIVATE viewer", generated.BrowserInputOfferFrame{InputEpoch: 2, OfferId: 2}, source)("PRIVATE\nFORGED error")
	select {
	case record := <-capture.records:
		require.Equal(t, map[string]any{"input_epoch": int64(2), "offer_id": int64(2), "control_epoch": int64(2), "reason": "other"}, record)
	default:
		t.Fatal("new epoch must retain its own bounded failure diagnostic")
	}
}

func TestDedicatedTimingReportsMergedRangeAndOldestArrival(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	t.Setenv("OMNIPUS_BROWSER_INPUT_TIMING", "1")
	capture := &dedicatedTimingLog{records: make(chan map[string]any, 8)}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	source, err := withWebRTCInputRoute(f.original, f.manager, "panel", nil)
	require.NoError(t, err)
	oldest := time.Now().Add(-250 * time.Millisecond)
	queued := webrtc.InputQueueTiming{EnqueuedAt: oldest, FirstReliableSeq: 41, LastReliableSeq: 44, InputCount: 4}
	sink := newWebRTCContextInputSinkWithDispatch(true, func(ctx context.Context, _ *browser.BrowserManager, _, _ string, in browser.LiveInput) error {
		require.Same(t, source, ctx, "metadata must not replace source identity")
		require.Same(t, source, in.SourceContext)
		return nil
	}, func() webrtc.InputQueueTiming { return queued })
	sink(source, "fixture-viewer", []byte(`{"type":"browser_input","kind":"wheel","input_epoch":7,"control_epoch":3,"reliable_seq":41}`))
	select {
	case record := <-capture.records:
		require.Equal(t, int64(41), record["reliable_seq"], "merged wire identity remains the first sequence")
		require.Equal(t, int64(41), record["first_reliable_seq"])
		require.Equal(t, int64(44), record["last_reliable_seq"])
		require.Equal(t, int64(4), record["input_count"])
		require.Equal(t, oldest.UnixMilli(), record["received_unix_ms"])
		require.Equal(t, "completed", record["outcome"])
	default:
		t.Fatal("merged dispatch emitted no diagnostic")
	}
}

func TestDedicatedTimingKeepsFailureQuotaAfterHoverSamples(t *testing.T) {
	sampling := &browserInputTimingSampling{}
	counts := map[string]int{}
	finish := func(outcome string) {
		probe := sampling.begin(generated.BrowserInputFrame{Kind: "mouse_move"}, time.Now())
		if probe == nil {
			return
		}
		probe.mark("live_entry")
		require.Nil(t, probe.offsets, "unsampled events must use fixed storage, not allocate maps")
		probe.outcome = outcome
		probe.emit = func(...any) { counts[outcome]++ }
		probe.finish()
	}
	for range 600 {
		finish("completed")
	}
	for range 100 {
		finish("deadline_exceeded")
	}
	require.Equal(t, map[string]int{"completed": 512, "deadline_exceeded": 64}, counts)
	require.NotNil(t, sampling.begin(generated.BrowserInputFrame{Kind: "text"}, time.Now()), "rolling diagnostics must survive the initial sample budgets")
}

func TestDedicatedTimingReservesCriticalQuotaAfterBenignTraffic(t *testing.T) {
	for _, noise := range []string{"benign_rejection", "canceled"} {
		t.Run(noise, func(t *testing.T) {
			sampling := &browserInputTimingSampling{}
			counts := map[string]int{}
			for i := 0; i < 513; i++ {
				outcome := noise
				if i == 512 {
					outcome = "deadline_exceeded"
				}
				probe := sampling.begin(generated.BrowserInputFrame{Kind: "mouse_move"}, time.Now())
				require.NotNil(t, probe, "ordinary traffic must preserve critical failure diagnostics")
				probe.outcome = outcome
				probe.emit = func(...any) { counts[outcome]++ }
				probe.finish()
			}
			require.Equal(t, map[string]int{noise: 512, "deadline_exceeded": 1}, counts)
		})
	}
}

func TestDedicatedTimingBudgetsAndErrorClasses(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, "completed"}, {context.DeadlineExceeded, "deadline_exceeded"},
		{context.Canceled, "canceled"}, {errors.New("PRIVATE Chrome error"), "dispatch_error"},
	} {
		require.Equal(t, tc.want, browserTimingOutcome(tc.err))
	}
	sampling := &browserInputTimingSampling{}
	p := sampling.begin(generated.BrowserInputFrame{Kind: "key_down"}, time.Unix(100, 0))
	p.outcome = "deadline_exceeded"
	p.now = func() time.Time { return time.Unix(100, 0).Add(4 * time.Second) }
	p.mark("cdp_start")
	p.observeBudget("live_budget", 5*time.Second)
	p.observeBudget("cdp_start", time.Second)
	var record map[string]any
	p.emit = func(args ...any) {
		record = map[string]any{}
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				t.Fatalf("timing field name has type %T, want string", args[i])
			}
			record[key] = args[i+1]
		}
	}
	p.finish()
	require.Equal(t, float64(5000), record["live_budget_ms"])
	require.Equal(t, float64(1000), record["cdp_remaining_budget_ms"])
	require.Equal(t, map[string]float64{"cdp_start": 4000, "finished": 4000}, record["stage_offsets_ms"])
	require.Equal(t, "deadline_exceeded", record["outcome"])
}

func TestDedicatedTimingBenignReasonUsesOnlyFixedLabels(t *testing.T) {
	for _, tc := range []struct{ message, want string }{
		{"browser live: input rate limit exceeded for wheel (40/s)", "rate_limited"},
		{"browser live: page change pending; wait for the current picture", "page_pending"},
		{"browser live: displayed frame changed; PRIVATE\nFORGED", "frame_changed"},
		{"browser live: input target changed; retry on the current tab", "frame_changed"},
		{"browser live: viewport unknown, PRIVATE", "other"},
		{"PRIVATE\nFORGED", "other"},
	} {
		require.Equal(t, tc.want, browserTimingBenignReason(tc.message))
	}
}
func (h *dedicatedTimingLog) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *dedicatedTimingLog) WithGroup(string) slog.Handler      { return h }

func TestDedicatedTimingReportsRealLiveRefusal(t *testing.T) {
	f := newHandlerContextFixture(t, false)
	t.Setenv("OMNIPUS_BROWSER_INPUT_TIMING", "1")
	capture := &dedicatedTimingLog{records: make(chan map[string]any, 8)}
	previous := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(previous) })
	source, err := withWebRTCInputRoute(f.original, f.manager, "panel", nil)
	require.NoError(t, err)
	// Missing capture claim must reach real live admission and fail before CDP.
	newWebRTCContextInputSink(true)(source, "fixture-viewer", []byte(`{"type":"browser_input","kind":"text","text":"PRIVATE","input_epoch":7,"control_epoch":3,"reliable_seq":41}`))
	select {
	case record := <-capture.records:
		require.Equal(t, "text", record["kind"])
		require.Equal(t, "benign_rejection", record["outcome"])
		offsets, ok := record["stage_offsets_ms"].(map[string]float64)
		require.True(t, ok)
		require.Contains(t, offsets, "live_entry")
		require.Contains(t, offsets, "finished")
		require.NotContains(t, offsets, "cdp_start")
	default:
		t.Fatal("actual dedicated input sink emitted no timing for real live refusal")
	}
}

func TestDedicatedTimingCoversEveryGestureWithoutPayload(t *testing.T) {
	for _, kind := range []string{"mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text"} {
		t.Run(kind, func(t *testing.T) {
			var sequence atomic.Uint64
			private := "PRIVATE typed content, key, coordinate, URL"
			frame := generated.BrowserInputFrame{Kind: kind, Text: &private, Key: &private, Url: &private,
				InputEpoch: payloadInt(7), ControlEpoch: payloadInt(3), ReliableSeq: payloadInt(41)}
			probe := newBrowserInputTiming(true, &sequence, frame, time.Unix(100, 0))
			require.NotNil(t, probe, "every dedicated gesture kind needs timing visibility")
			var record map[string]any
			probe.emit = func(args ...any) {
				record = map[string]any{}
				for i := 0; i < len(args); i += 2 {
					key, ok := args[i].(string)
					if !ok {
						t.Fatalf("timing field name has type %T, want string", args[i])
					}
					record[key] = args[i+1]
				}
			}
			probe.finish()
			require.Equal(t, kind, record["kind"])
			require.Equal(t, 7, record["input_epoch"])
			require.Equal(t, 3, record["control_epoch"])
			require.Equal(t, 41, record["reliable_seq"])
			require.Equal(t, 41, record["first_reliable_seq"])
			require.Equal(t, 41, record["last_reliable_seq"])
			require.Equal(t, 1, record["input_count"])
			for _, field := range []string{"text", "key", "code", "x", "y", "url"} {
				require.NotContains(t, record, field, "diagnostics must not contain gesture contents")
			}
		})
	}
}

func TestDedicatedTimingRollingFailureWindowSurvivesInitialBudgets(t *testing.T) {
	sampling := &browserInputTimingSampling{}
	now := time.Unix(100, 0)
	var records []map[string]any
	emit := func(args ...any) {
		record := map[string]any{}
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				t.Fatalf("timing field name has type %T, want string", args[i])
			}
			record[key] = args[i+1]
		}
		records = append(records, record)
	}
	finish := func(seq int, outcome string) {
		private := "SECRET keyboard text https://private.example"
		p := sampling.begin(generated.BrowserInputFrame{Kind: "wheel", ReliableSeq: &seq, Text: &private, Key: &private, Url: &private}, now)
		require.NotNil(t, p, "long sessions must still retain a bounded failure window")
		p.now = func() time.Time { return now }
		p.emit = emit
		p.outcome = outcome
		p.mark("cdp_start")
		p.finish()
	}
	for i := 1; i <= 512; i++ {
		finish(i, "completed")
	}
	for i := 513; i <= 576; i++ {
		finish(i, "deadline_exceeded")
	}
	require.Len(t, records, 576)
	records = nil
	now = now.Add(10 * time.Second)
	for i := 577; i <= 596; i++ {
		finish(i, "completed")
	}
	require.Empty(t, records, "ordinary post-budget traffic stays in fixed memory")
	finish(597, "deadline_exceeded")
	require.Len(t, records, 17, "exactly the preceding sixteen inputs plus the current failure")
	for i, record := range records {
		require.Equal(t, 581+i, record["reliable_seq"])
	}
	raw, err := json.Marshal(records)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "SECRET")
	require.NotContains(t, string(raw), "private.example")
	records = nil
	finish(598, "dispatch_error")
	require.Empty(t, records, "failure bursts must be rate limited")
	now = now.Add(10 * time.Second)
	finish(599, "dispatch_error")
	require.Len(t, records, 17, "later failures retain a fresh rolling window, not a lifetime quota")
	require.Equal(t, 599, records[16]["reliable_seq"])
}

func TestDedicatedTimingClosedConnectionFlushesWithoutActiveInput(t *testing.T) {
	sampling := &browserInputTimingSampling{}
	now := time.Unix(100, 0)
	for seq := 1; seq <= 600; seq++ {
		p := sampling.begin(generated.BrowserInputFrame{Kind: "wheel", ReliableSeq: &seq}, now)
		require.NotNil(t, p)
		p.now = func() time.Time { return now }
		p.emit = func(...any) {}
		p.outcome = "completed"
		p.finish()
	}
	var records []map[string]any
	emit := func(args ...any) {
		record := map[string]any{}
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				t.Fatalf("timing field name has type %T, want string", args[i])
			}
			record[key] = args[i+1]
		}
		records = append(records, record)
	}
	sampling.failure("input connection closed", now, emit)
	require.Len(t, records, 17, "connection closure must flush sixteen prior inputs even with no active dispatch")
	for i := 0; i < 16; i++ {
		require.Equal(t, 585+i, records[i]["reliable_seq"])
	}
	require.Equal(t, map[string]any{"event": "failure_window", "reason": "connection_closed"}, records[16])
	records = nil
	sampling.failure("PRIVATE key text https://secret.example", now, emit)
	require.Empty(t, records, "duplicate callbacks cannot produce unbounded windows")
	sampling.failure("PRIVATE key text https://secret.example", now.Add(10*time.Second), emit)
	require.Len(t, records, 17)
	require.Equal(t, map[string]any{"event": "failure_window", "reason": "other"}, records[16])
}

func TestDedicatedTimingSlowCanceledWindowBoundary(t *testing.T) {
	for _, elapsed := range []time.Duration{249 * time.Millisecond, 250 * time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			sampling := &browserInputTimingSampling{}
			now := time.Unix(100, 0)
			for range 512 {
				p := sampling.begin(generated.BrowserInputFrame{Kind: "wheel"}, now)
				p.now = func() time.Time { return now }
				p.emit = func(...any) {}
				p.outcome = "completed"
				p.finish()
			}
			p := sampling.begin(generated.BrowserInputFrame{Kind: "wheel"}, now)
			now = now.Add(elapsed)
			p.now = func() time.Time { return now }
			count := 0
			p.emit = func(...any) { count++ }
			p.outcome = "canceled"
			p.finish()
			expected := 0
			if elapsed == 250*time.Millisecond {
				expected = 17
			}
			require.Equal(t, expected, count)
			for _, snapshot := range sampling.recent {
				require.Nil(t, snapshot.now)
				require.Nil(t, snapshot.emit)
				require.Nil(t, snapshot.sampling)
				require.Nil(t, snapshot.offsets)
			}
		})
	}
}

func TestDedicatedTimingFailureWindowIncludesCanceledInFlightCompletion(t *testing.T) {
	sampling := &browserInputTimingSampling{}
	now := time.Unix(100, 0)
	for i := 0; i < 576; i++ {
		p := sampling.begin(generated.BrowserInputFrame{Kind: "wheel"}, now)
		p.now = func() time.Time { return now }
		p.emit = func(...any) {}
		p.outcome = "completed"
		if i >= 512 {
			p.outcome = "deadline_exceeded"
		}
		p.finish()
	}
	now = now.Add(10 * time.Second)
	seq := 577
	active := sampling.begin(generated.BrowserInputFrame{Kind: "wheel", ReliableSeq: &seq}, now)
	active.now = func() time.Time { return now }
	active.mark("cdp_start")
	var records []map[string]any
	emit := func(args ...any) {
		record := map[string]any{}
		for i := 0; i < len(args); i += 2 {
			key, ok := args[i].(string)
			if !ok {
				t.Fatalf("timing field name has type %T, want string", args[i])
			}
			record[key] = args[i+1]
		}
		records = append(records, record)
	}
	active.emit = emit
	sampling.failure("reliable input queue expired", now, emit)
	require.Len(t, records, 17)
	now = now.Add(35 * time.Millisecond)
	active.outcome = "canceled"
	active.finish()
	require.Len(t, records, 18, "retain the one already-running completion despite the just-flushed window")
	require.Equal(t, 577, records[17]["reliable_seq"])
	require.Equal(t, "canceled", records[17]["outcome"])
	require.Equal(t, map[string]float64{"cdp_start": 0, "finished": 35}, records[17]["stage_offsets_ms"])
	fresh := sampling.begin(generated.BrowserInputFrame{Kind: "wheel"}, now)
	fresh.now = func() time.Time { return now }
	fresh.emit = emit
	fresh.outcome = "canceled"
	fresh.finish()
	require.Len(t, records, 18, "the completion allowance must not leak into fresh input")
}

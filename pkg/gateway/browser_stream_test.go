// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_stream_test.go — hermetic tests for Wave 2 component M (live-browser
// video streaming orchestration). Every external dependency (classifier,
// ingest endpoint, encoder-page launch, capture driver, encoder-page listener)
// is faked, so these tests run with no real Chrome, no CDP, no network — they
// prove the orchestration LOGIC: capable-attach starts a stream and fans a
// chunk to the viewer; not-capable/kill-switch/codec-mismatch move the viewer
// to the unavailable state with no stream; an ingest drop re-mints a fresh
// token (old one dead) and relaunches; and the browserWSConn Viewer adapter
// drops on a full queue and enforces per-stream authz.

package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// ---- fakes ----

type fakeIngest struct {
	mu        sync.Mutex
	mintCalls []string
	endCalls  []string
	feedCalls int
	tokenSeq  int
	current   map[string]string // streamID -> current (live) token
}

func newFakeIngest() *fakeIngest { return &fakeIngest{current: map[string]string{}} }

func (f *fakeIngest) MintIngestToken(streamID string) ingestToken {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenSeq++
	tok := "tok-" + strconv.Itoa(f.tokenSeq)
	f.mintCalls = append(f.mintCalls, streamID)
	f.current[streamID] = tok // re-mint supersedes: only the newest token is live
	return ingestToken(tok)
}

func (f *fakeIngest) EndStream(streamID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.endCalls = append(f.endCalls, streamID)
	delete(f.current, streamID)
}

func (f *fakeIngest) FeedFrame(streamID string, jpeg []byte, seq uint32, ts uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedCalls++
}

func (f *fakeIngest) mintCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.mintCalls) }
func (f *fakeIngest) endCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return len(f.endCalls) }
func (f *fakeIngest) liveToken(streamID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.current[streamID]
}
func (f *fakeIngest) feedCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.feedCalls }

type fakeEncoderTab struct {
	done   chan struct{}
	closed atomic.Bool
}

func (t *fakeEncoderTab) Done() <-chan struct{} { return t.done }
func (t *fakeEncoderTab) Close() error          { t.closed.Store(true); return nil }

type fakeLauncher struct {
	mu   sync.Mutex
	cfgs []browser.EncoderLaunchCfg
	tabs []*fakeEncoderTab
	err  error
}

func (f *fakeLauncher) launch(_ context.Context, cfg browser.EncoderLaunchCfg) (encoderTab, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.cfgs = append(f.cfgs, cfg)
	t := &fakeEncoderTab{done: make(chan struct{})}
	f.tabs = append(f.tabs, t)
	return t, nil
}

func (f *fakeLauncher) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.cfgs) }
func (f *fakeLauncher) cfgAt(i int) browser.EncoderLaunchCfg {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfgs[i]
}
func (f *fakeLauncher) tabAt(i int) *fakeEncoderTab {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tabs[i]
}

type fakeCaptureDriver struct{ stopped atomic.Bool }

func (d *fakeCaptureDriver) Stop() { d.stopped.Store(true) }

type fakeCaptureStarter struct {
	mu      sync.Mutex
	calls   int
	drivers []*fakeCaptureDriver
	onFrame func(jpeg []byte, seq uint32, tsMillis uint64)
	err     error
}

func (f *fakeCaptureStarter) start(_ context.Context, _ browser.CaptureOptions, onFrame func(jpeg []byte, seq uint32, tsMillis uint64)) (captureDriver, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.calls++
	f.onFrame = onFrame
	d := &fakeCaptureDriver{}
	f.drivers = append(f.drivers, d)
	return d, nil
}

func (f *fakeCaptureStarter) count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

type fakeEncoderServer struct {
	mu    sync.Mutex
	stops int
}

func (f *fakeEncoderServer) Serve() (string, string, func(), error) {
	return "http://127.0.0.1:12345", "http://127.0.0.1:12345/enc/secret", func() {
		f.mu.Lock()
		f.stops++
		f.mu.Unlock()
	}, nil
}

func (f *fakeEncoderServer) stopCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.stops }

// ---- helpers ----

func capable() classifyFunc {
	return func(string) browser.VideoCapability { return browser.VideoCapability{Capable: true} }
}

func newTestVideoConn() *browserWSConn {
	return &browserWSConn{sendCh: make(chan *wsSendItem, 64), doneCh: make(chan struct{})}
}

func newTestOrchestrator(classify classifyFunc, ing ingestController, launch encoderLauncher, cap captureStarter, srv encoderPageServer) *BrowserVideoOrchestrator {
	o := newOrchestrator(BrowserVideoDeps{
		Config: BrowserVideoConfig{
			IngestWSURL:     "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
			LivenessTimeout: time.Hour, // never fire during a fast unit test
		},
		Classify:      classify,
		LaunchEncoder: launch,
		StartCapture:  cap,
		EncoderServer: srv,
	})
	o.ingest = ing
	return o
}

func drainStreamInit(t *testing.T, wc *browserWSConn) generated.BrowserStreamInitFrame {
	t.Helper()
	select {
	case item := <-wc.sendCh:
		if item == nil || item.Binary {
			t.Fatalf("expected a text browser_stream_init frame first, got binary=%v", item != nil && item.Binary)
		}
		var f generated.BrowserStreamInitFrame
		if err := json.Unmarshal(item.Data, &f); err != nil {
			t.Fatalf("unmarshal stream_init: %v", err)
		}
		return f
	default:
		t.Fatal("expected a browser_stream_init frame, got none")
	}
	return generated.BrowserStreamInitFrame{}
}

// nextBinaryChunk returns the next binary (encoded chunk) item, skipping text
// frames.
func nextBinaryChunk(t *testing.T, wc *browserWSConn, within time.Duration) []byte {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case item := <-wc.sendCh:
			if item != nil && item.Binary {
				return item.Data
			}
		case <-deadline:
			t.Fatal("expected a binary video chunk, got none")
			return nil
		}
	}
}

// findStatusError drains up to `max` items looking for a browser_status(error)
// frame carrying the generic unavailable message.
func findStatusError(t *testing.T, wc *browserWSConn) generated.BrowserStatusFrame {
	t.Helper()
	for i := 0; i < 16; i++ {
		select {
		case item := <-wc.sendCh:
			if item == nil || item.Binary {
				continue
			}
			var f generated.BrowserStatusFrame
			if err := json.Unmarshal(item.Data, &f); err != nil {
				continue
			}
			if f.State == "error" {
				return f
			}
		default:
			t.Fatal("no browser_status(error) frame found")
		}
	}
	t.Fatal("no browser_status(error) frame found within drained frames")
	return generated.BrowserStatusFrame{}
}

func eventually(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// ---- tests ----

func TestStream_CapableAttach_StreamsToViewer(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, cap.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h == nil {
		t.Fatal("expected a non-nil handle for a capable attach")
	}
	defer h.Detach()

	// A stream was brought up: one token minted, one encoder launched, one
	// capture started.
	if ing.mintCount() != 1 {
		t.Fatalf("expected 1 MintIngestToken, got %d", ing.mintCount())
	}
	if launch.count() != 1 {
		t.Fatalf("expected 1 encoder launch, got %d", launch.count())
	}
	if cap.count() != 1 {
		t.Fatalf("expected 1 capture start, got %d", cap.count())
	}
	streamID := ing.mintCalls[0]

	// The encoder-launch cfg carries the minted token, the unguessable origin,
	// and the negotiated (H.264-first) codec.
	cfg := launch.cfgAt(0)
	if cfg.Token != ing.liveToken(streamID) {
		t.Fatalf("encoder launched with token %q, live token is %q", cfg.Token, ing.liveToken(streamID))
	}
	if cfg.Origin != "http://127.0.0.1:12345" || cfg.EncoderURL == "" {
		t.Fatalf("encoder cfg origin/url not wired: origin=%q url=%q", cfg.Origin, cfg.EncoderURL)
	}
	if !strings.HasPrefix(codecFamily(cfg.VideoCodec), "h264") {
		t.Fatalf("expected H.264-first codec, got %q", cfg.VideoCodec)
	}

	// browser_stream_init is sent first, before any chunk.
	initFrame := drainStreamInit(t, wc)
	if !strings.HasPrefix(codecFamily(initFrame.Codec), "h264") {
		t.Fatalf("stream_init codec = %q, want h264 family", initFrame.Codec)
	}

	// Capture frames are wired to the ingest FeedFrame path.
	cap.mu.Lock()
	onFrame := cap.onFrame
	cap.mu.Unlock()
	if onFrame == nil {
		t.Fatal("capture onFrame callback was not captured")
	}
	onFrame([]byte{0xFF, 0xD8}, 0, 1)
	if ing.feedCount() != 1 {
		t.Fatalf("expected capture frame to reach FeedFrame, got %d feeds", ing.feedCount())
	}

	// Now an encoded chunk arriving from the encoder page (ingest -> relay
	// adapter) must fan out to the attached viewer as a binary chunk.
	adapter := &videoRelayAdapter{orch: o}
	payload := []byte{1, 2, 3, 4}
	adapter.Ingest(streamID, EncodedChunk{Seq: 0, TS: 7, Key: true, Codec: cfg.VideoCodec, Kind: "video", Payload: payload})

	chunk := nextBinaryChunk(t, wc, time.Second)
	if len(chunk) != 18+len(payload) {
		t.Fatalf("binary chunk len = %d, want %d", len(chunk), 18+len(payload))
	}
	if chunk[12] != 1 { // key flag
		t.Fatalf("expected keyframe flag=1, got %d", chunk[12])
	}
	if chunk[13] != 0 { // video kind
		t.Fatalf("expected video kind=0, got %d", chunk[13])
	}
	if string(chunk[18:]) != string(payload) {
		t.Fatalf("chunk payload mismatch: got %v want %v", chunk[18:], payload)
	}
}

func TestStream_NotCapable_Unavailable(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	notCapable := func(string) browser.VideoCapability {
		return browser.VideoCapability{Capable: false, Reason: "Xvfb not available"}
	}
	o := newTestOrchestrator(notCapable, ing, launch.launch, cap.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E", "vp8"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h != nil {
		t.Fatal("expected a nil handle (no stream) for a not-capable attach")
	}

	// No stream machinery was engaged.
	if ing.mintCount() != 0 || launch.count() != 0 || cap.count() != 0 {
		t.Fatalf("expected no stream to start; mint=%d launch=%d capture=%d", ing.mintCount(), launch.count(), cap.count())
	}

	// The viewer got the generic unavailable state (never the specific cause).
	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected generic unavailable message, got %v", f.Message)
	}
}

func TestStream_KillSwitch_TearsDown(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, cap.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil || h == nil {
		t.Fatalf("expected a live stream; err=%v handle=%v", err, h)
	}
	streamID := ing.mintCalls[0]
	tab := launch.tabAt(0)
	drv := cap.drivers[0]

	// Flip the kill-switch OFF: active stream must be torn down.
	o.SetVideoEnabled(false)

	if o.Enabled() {
		t.Fatal("kill-switch should be off")
	}
	if !tab.closed.Load() {
		t.Fatal("encoder tab was not closed on kill-switch")
	}
	if !drv.stopped.Load() {
		t.Fatal("capture was not stopped on kill-switch")
	}
	if ing.endCount() != 1 || ing.endCalls[0] != streamID {
		t.Fatalf("expected EndStream(%q); end calls=%v", streamID, ing.endCalls)
	}
	if srv.stopCount() != 1 {
		t.Fatalf("expected the encoder-page listener to be stopped once, got %d", srv.stopCount())
	}

	// The attached viewer was moved to the unavailable state (via relay Failed).
	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected unavailable message on teardown, got %v", f.Message)
	}

	// A new attach while the kill-switch is off gets the unavailable state and
	// starts NO new stream.
	wc2 := newTestVideoConn()
	h2, err := o.AttachViewer(AttachParams{
		WC: wc2, AgentID: "agent2", SessionID: "sess2", ViewerID: "v2",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h2 != nil {
		t.Fatal("expected nil handle while kill-switch is off")
	}
	if ing.mintCount() != 1 {
		t.Fatalf("kill-switch attach must not mint a new token; mint count=%d", ing.mintCount())
	}
}

// TestNewOrchestrator_EnabledFromConfig proves the orchestrator's enabled
// state is read from BrowserVideoConfig.Enabled at construction (FR-020) —
// never a hardcoded true — so the gateway.browser_video_enabled config value
// (resolved by config.GatewayConfig.IsBrowserVideoEnabled and passed in as
// deps.Config.Enabled) takes effect from the very first attach, not only
// after a later SetVideoEnabled call.
func TestNewOrchestrator_EnabledFromConfig(t *testing.T) {
	falseVal := false
	trueVal := true

	unset := newTestOrchestrator(capable(), newFakeIngest(), (&fakeLauncher{}).launch, (&fakeCaptureStarter{}).start, &fakeEncoderServer{})
	if !unset.Enabled() {
		t.Fatal("an unset Config.Enabled must default to enabled (FR-020 default true)")
	}

	offOrch := newOrchestrator(BrowserVideoDeps{
		Config: BrowserVideoConfig{
			IngestWSURL:     "ws://127.0.0.1:5000/api/v1/browser/capture-ingest",
			LivenessTimeout: time.Hour,
			Enabled:         &falseVal,
		},
		Classify:      capable(),
		LaunchEncoder: (&fakeLauncher{}).launch,
		StartCapture:  (&fakeCaptureStarter{}).start,
		EncoderServer: &fakeEncoderServer{},
	})
	offOrch.ingest = newFakeIngest()
	if offOrch.Enabled() {
		t.Fatal("Config.Enabled=false must start the orchestrator disabled — not a hardcoded true")
	}

	// A construction-time-disabled orchestrator must reject an attach exactly
	// like a runtime SetVideoEnabled(false) does: unavailable state, no stream.
	wc := newTestVideoConn()
	h, err := offOrch.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if h != nil {
		t.Fatal("expected nil handle — construction-time kill-switch-off must block the very first attach")
	}
	f := findStatusError(t, wc)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("expected generic unavailable message, got %v", f.Message)
	}

	onOrch := newOrchestrator(BrowserVideoDeps{
		Config: BrowserVideoConfig{Enabled: &trueVal},
	})
	if !onOrch.Enabled() {
		t.Fatal("Config.Enabled=true must start the orchestrator enabled")
	}
}

// TestStream_KillSwitch_ReEnable_AllowsNewAttach extends the FR-020 kill-switch
// coverage (TestStream_KillSwitch_TearsDown covers OFF blocking new attaches +
// tearing down the active one) to the full runtime round-trip a config
// reload flips gateway.browser_video_enabled back to true drives:
// SetVideoEnabled(true) after an OFF period must let a brand-new attach start
// a brand-new stream — the reload hook (executeReload calling
// SetVideoEnabled(newCfg.Gateway.IsBrowserVideoEnabled())) is the only path
// that can re-enable it, so this proves that path's target API behaves
// correctly for BOTH directions of the live toggle, not just OFF.
func TestStream_KillSwitch_ReEnable_AllowsNewAttach(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, cap.start, srv)

	// Flip OFF with no active stream (simulates a reload landing while idle).
	o.SetVideoEnabled(false)
	if o.Enabled() {
		t.Fatal("kill-switch should be off")
	}

	wcBlocked := newTestVideoConn()
	hBlocked, err := o.AttachViewer(AttachParams{
		WC: wcBlocked, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if hBlocked != nil {
		t.Fatal("expected nil handle while kill-switch is off")
	}
	if ing.mintCount() != 0 || launch.count() != 0 {
		t.Fatalf("kill-switch-off attach must start no stream machinery; mint=%d launch=%d", ing.mintCount(), launch.count())
	}

	// Flip back ON (simulates a second reload restoring browser_video_enabled=true).
	o.SetVideoEnabled(true)
	if !o.Enabled() {
		t.Fatal("kill-switch should be back on")
	}

	// A new attach must now succeed and bring up a real stream.
	wcOK := newTestVideoConn()
	hOK, err := o.AttachViewer(AttachParams{
		WC: wcOK, AgentID: "agent1", SessionID: "sess1", ViewerID: "v2",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil {
		t.Fatalf("AttachViewer error: %v", err)
	}
	if hOK == nil {
		t.Fatal("expected a live stream once the kill-switch is back on")
	}
	defer hOK.Detach()

	if ing.mintCount() != 1 || launch.count() != 1 || cap.count() != 1 {
		t.Fatalf("expected exactly one stream to start post-re-enable; mint=%d launch=%d capture=%d",
			ing.mintCount(), launch.count(), cap.count())
	}
	drainStreamInit(t, wcOK)
}

func TestStream_IngestDrop_Remints(t *testing.T) {
	ing := newFakeIngest()
	launch := &fakeLauncher{}
	cap := &fakeCaptureStarter{}
	srv := &fakeEncoderServer{}
	o := newTestOrchestrator(capable(), ing, launch.launch, cap.start, srv)

	wc := newTestVideoConn()
	h, err := o.AttachViewer(AttachParams{
		WC: wc, AgentID: "agent1", SessionID: "sess1", ViewerID: "v1",
		RootCtx: context.Background(), AgentCtx: context.Background(),
		VideoCaps: []string{"avc1.4D401E"},
	})
	if err != nil || h == nil {
		t.Fatalf("expected a live stream; err=%v handle=%v", err, h)
	}
	defer h.Detach()

	streamID := ing.mintCalls[0]
	firstTab := launch.tabAt(0)
	firstToken := launch.cfgAt(0).Token

	// Simulate the ingest drop: the gateway-owned encoder page crashes → its
	// tab's Done fires.
	close(firstTab.done)

	// Orchestrator must re-mint a fresh token and relaunch the encoder page.
	eventually(t, 2*time.Second, func() bool {
		return launch.count() == 2 && ing.mintCount() == 2
	})

	secondCfg := launch.cfgAt(1)
	// The re-mint produced a DIFFERENT, live token; the old token is dead.
	if secondCfg.Token == firstToken {
		t.Fatalf("relaunch reused the old token %q — must re-mint (CRIT-002)", firstToken)
	}
	if secondCfg.Token != ing.liveToken(streamID) {
		t.Fatalf("relaunch token %q is not the live token %q", secondCfg.Token, ing.liveToken(streamID))
	}
	if firstToken == ing.liveToken(streamID) {
		t.Fatal("old token is still live — it must be superseded (dead) after re-mint")
	}
	// The dead encoder tab was closed.
	eventually(t, time.Second, func() bool { return firstTab.closed.Load() })
	// Relaunch reused the SAME unguessable origin/streamID.
	if secondCfg.Origin != launch.cfgAt(0).Origin || secondCfg.StreamID != streamID {
		t.Fatalf("relaunch changed origin/streamID unexpectedly: %+v", secondCfg)
	}
}

func TestBrowserWSViewerAdapter_AuthzAndDrop(t *testing.T) {
	// Queue depth 1 so the second SendChunk sees a full queue.
	wc := &browserWSConn{sendCh: make(chan *wsSendItem, 1), doneCh: make(chan struct{})}
	vv := newWSVideoViewer(wc, "stream-A", "sess", "v1")

	// Authz: only the exact stream it was authorized for passes (FR-015).
	if !vv.Authorized("stream-A") {
		t.Fatal("Authorized(stream-A) should be true")
	}
	if vv.Authorized("stream-B") {
		t.Fatal("Authorized(stream-B) must be false — a viewer may only pull its own stream")
	}

	// First chunk enqueues (true); second, with the queue full, drops (false).
	if !vv.SendChunk([]byte("chunk-1")) {
		t.Fatal("first SendChunk should enqueue (true)")
	}
	if vv.SendChunk([]byte("chunk-2")) {
		t.Fatal("second SendChunk on a full queue must drop (false)")
	}

	// After the connection closes, SendChunk still reports false (the queue is
	// full AND the done sentinel is closed — never a spurious true).
	wc.close()
	if vv.SendChunk([]byte("chunk-3")) {
		t.Fatal("SendChunk on a closed/full connection must return false")
	}

	// Failed moves the viewer to the unavailable state via a status(error).
	wc2 := newTestVideoConn()
	vv2 := newWSVideoViewer(wc2, "stream-A", "sess", "v1")
	vv2.Failed("stream-A")
	f := findStatusError(t, wc2)
	if f.Message == nil || *f.Message != browserVideoUnavailableMessage {
		t.Fatalf("Failed should surface the generic unavailable message, got %v", f.Message)
	}
}

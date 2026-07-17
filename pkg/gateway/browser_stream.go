// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// browser_stream.go — Wave 2 component M (live-browser video streaming,
// ADR-044; docs/internal/specs/live-browser-video-streaming-spec.md R7). This
// file is the STREAM ORCHESTRATOR: the piece that ties the capture driver
// (component L), the encoder page (component G) + its CDP launch (component M /
// encoder_launch.go), the loopback capture-ingest endpoint (component E), and
// the stream relay + GOP cache (component D) into a working video stream for a
// live-browser WS viewer.
//
// Lifecycle of one video-capable attach (US-1/US-3/US-9):
//
//	viewer attaches ─▶ classify (K) ─▶ not capable / kill-switch off / codec
//	                     mismatch ─▶ browser_status(error) unavailable state
//	                                 (no stream, never JPEG, never A1)
//	                 └▶ capable, no stream yet for this agent tab:
//	                        mint per-stream token (E) ─▶ serve encoder page on an
//	                        UNGUESSABLE loopback origin + secret path (FR-016) ─▶
//	                        LaunchEncoderPage (M) ─▶ StartCapture on the agent tab
//	                        (L), onFrame ─▶ ingest.FeedFrame ─▶ encoder page ─▶
//	                        browser_ingest_chunk ─▶ relay.Ingest (D) ─▶ fan out to
//	                        viewers as binary browser_video/audio_chunk (F)
//	                 └▶ send browser_stream_init first, then GOP replay, then live.
//
// Failure handling:
//   - CRIT-002 (ingest drop): the gateway-owned encoder page crashing IS the
//     "ingest drop" — its CDP target dies, EncoderTab.Done fires, and we
//     re-mint a FRESH token (invalidating the old one, no same-token reconnect)
//     and relaunch the encoder page (re-granting audio). Bounded relaunches;
//     exceeding the bound fails the stream to the unavailable state.
//   - FR-018 (timeouts): capture bring-up is bounded by StartCapture itself;
//     mid-stream liveness is bounded here (no chunk within LivenessTimeout while
//     viewers are attached ⇒ fail to unavailable, never an infinite spinner).
//   - FR-020 (kill-switch): SetVideoEnabled(false) makes new attaches get the
//     unavailable state AND tears down every active stream (no redeploy).
//
// This file OWNS only orchestration + config surface. It depends on components
// D/E/G/K/L/M purely through the small seams below (all default to the real
// implementations; every seam is injectable so the orchestrator is tested
// hermetically with no real Chrome / listener / capture).

package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
	"github.com/elicify-ai/omnipus/pkg/tools/browser/encoderpage"
)

// browserVideoUnavailableMessage is the GENERIC, install-agnostic end-user
// string for the unavailable state (FR-007/US-5/O-3). The specific cause is
// logged operator-side only, never surfaced here — this string never
// fingerprints the install.
const browserVideoUnavailableMessage = "Live view needs a video-capable browser"

// Default orchestrator tuning (all overridable via BrowserVideoConfig).
const (
	defaultPanelWidth       = 1280
	defaultPanelHeight      = 720
	defaultStreamFramerate  = 30
	defaultKeyframeInterval = 60 // frames between keyframes (framerate * 2)
	defaultJPEGQuality      = 60
	defaultLivenessTimeout  = 15 * time.Second
	defaultMaxRelaunches    = 3
	defaultStepDownCooldown = 3 * time.Second
	stepDownMinWidth        = 320
	stepDownMinHeight       = 240
)

// defaultProducibleVideoCodecs is the encoder's producible-codec priority order
// (FR-006): H.264 main first, VP8 fallback. EC-4 (iPad) may later invert this;
// it is config so that inversion touches only data, not control flow.
//
// CS1: the H.264 string here MUST match what the encoder page's WebCodecs
// VideoEncoder is actually configured to produce — the SPA configures its
// VideoDecoder from the announced codec (browser_stream_init), so a mismatch
// between "announced" and "produced" breaks decode. The encoder
// (encoderpage) and the SPA (src/lib/browserLiveWs.ts) both use
// "avc1.4D4028"; this used to say "avc1.4D401E" (a different H.264 level),
// which never matched either producer.
var defaultProducibleVideoCodecs = []string{"avc1.4D4028", "vp8"}

// BrowserVideoConfig is component M's config surface (FR-018/019/020). Zero
// values fall back to the defaults above, so a zero BrowserVideoConfig is a
// valid, fully-defaulted config.
type BrowserVideoConfig struct {
	// Enabled is the FR-020 kill-switch initial value (default true). Runtime
	// flips go through SetVideoEnabled, which also tears down active streams.
	Enabled *bool
	// PanelWidth / PanelHeight bound the captured/encoded frame to the live
	// panel size (spec: "capture dimensions follow the panel/window size").
	PanelWidth  int
	PanelHeight int
	// Framerate / KeyframeInterval / JPEGQuality tune the capture + encoder.
	Framerate        int
	KeyframeInterval int
	JPEGQuality      int
	// ProducibleVideoCodecs overrides the producible-codec priority order.
	ProducibleVideoCodecs []string
	// IngestWSURL is the loopback capture-ingest WebSocket URL the encoder page
	// connects back to, e.g. ws://127.0.0.1:<gateway-port>/api/v1/browser/capture-ingest.
	// REQUIRED in production (an empty value fails a launch closed to the
	// unavailable state); the gateway.go hook derives it from the gateway port.
	IngestWSURL string
	// LivenessTimeout / MaxRelaunches bound FR-018 mid-stream liveness and
	// CRIT-002 encoder relaunches.
	LivenessTimeout time.Duration
	MaxRelaunches   int
	// StepDownCooldown rate-limits the FR-014 oversize-chunk step-down.
	StepDownCooldown time.Duration
}

// resolvedConfig is BrowserVideoConfig with all zero values filled in.
type resolvedConfig struct {
	panelWidth, panelHeight int
	framerate, keyframe     int
	jpegQuality             int
	producibleCodecs        []string
	ingestWSURL             string
	livenessTimeout         time.Duration
	maxRelaunches           int
	stepDownCooldown        time.Duration
}

func resolveConfig(c BrowserVideoConfig) resolvedConfig {
	rc := resolvedConfig{
		panelWidth:       orDefaultInt(c.PanelWidth, defaultPanelWidth),
		panelHeight:      orDefaultInt(c.PanelHeight, defaultPanelHeight),
		framerate:        orDefaultInt(c.Framerate, defaultStreamFramerate),
		keyframe:         orDefaultInt(c.KeyframeInterval, defaultKeyframeInterval),
		jpegQuality:      orDefaultInt(c.JPEGQuality, defaultJPEGQuality),
		producibleCodecs: c.ProducibleVideoCodecs,
		ingestWSURL:      c.IngestWSURL,
		livenessTimeout:  c.LivenessTimeout,
		maxRelaunches:    orDefaultInt(c.MaxRelaunches, defaultMaxRelaunches),
		stepDownCooldown: c.StepDownCooldown,
	}
	if len(rc.producibleCodecs) == 0 {
		rc.producibleCodecs = defaultProducibleVideoCodecs
	}
	if rc.livenessTimeout <= 0 {
		rc.livenessTimeout = defaultLivenessTimeout
	}
	if rc.stepDownCooldown <= 0 {
		rc.stepDownCooldown = defaultStepDownCooldown
	}
	return rc
}

func orDefaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

// ---- injectable seams (all default to the real implementations) ----

// captureDriver is the subset of *browser.CaptureDriver the orchestrator uses.
type captureDriver interface{ Stop() }

// captureStarter starts a screencast capture on the agent tab (component L).
type captureStarter func(ctx context.Context, opts browser.CaptureOptions, onFrame func(jpeg []byte, seq uint32, tsMillis uint64)) (captureDriver, error)

func defaultCaptureStarter(
	ctx context.Context,
	opts browser.CaptureOptions,
	onFrame func(jpeg []byte, seq uint32, tsMillis uint64),
) (captureDriver, error) {
	return browser.StartCapture(ctx, opts, onFrame)
}

// encoderTab is the subset of *browser.EncoderTab the orchestrator uses.
type encoderTab interface {
	Done() <-chan struct{}
	Close() error
}

// encoderLauncher launches the encoder page (component M / encoder_launch.go).
type encoderLauncher func(rootCtx context.Context, cfg browser.EncoderLaunchCfg) (encoderTab, error)

func defaultEncoderLauncher(
	rootCtx context.Context,
	cfg browser.EncoderLaunchCfg,
) (encoderTab, error) {
	return browser.LaunchEncoderPage(rootCtx, cfg)
}

// ingestController is the subset of *CaptureIngestHandler (component E) the
// orchestrator drives.
type ingestController interface {
	MintIngestToken(streamID string) ingestToken
	EndStream(streamID string)
	FeedFrame(streamID string, jpeg []byte, seq uint32, ts uint64)
	// RequestKeyframe (SF-H2) is also the exact shape browser.keyframeRequester
	// requires, so o.ingest can be handed straight to
	// browser.SetKeyframeRequester in RegisterBrowserVideo with no adapter.
	RequestKeyframe(streamID string)
}

// classifyFunc is the video-capability classifier (component K).
type classifyFunc func(installRoot string) browser.VideoCapability

// encoderPageServer serves the encoder page on an UNGUESSABLE loopback origin
// (FR-016). Serve returns the origin (scheme://host:port), the full page URL
// (origin + secret path), a stop func, or an error.
type encoderPageServer interface {
	Serve() (origin, pageURL string, stop func(), err error)
}

// loopbackEncoderServer is the production encoderPageServer: it binds a fresh
// 127.0.0.1:0 (OS-assigned random port) listener per stream and serves the
// embedded encoder page at a random secret path. Random port + secret path =
// the unguessable origin FR-016 requires so an agent-navigated page can neither
// guess the origin (to inherit the origin-scoped audio grant) nor the resource.
type loopbackEncoderServer struct{}

func (loopbackEncoderServer) Serve() (string, string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", nil, fmt.Errorf("browser video: bind loopback encoder listener: %w", err)
	}
	secret := randHex(24)
	path := "/enc/" + secret
	origin := "http://" + ln.Addr().String()
	pageURL := origin + path

	mux := http.NewServeMux()
	mux.Handle(path, encoderpage.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	stop := func() { _ = srv.Close() }
	return origin, pageURL, stop, nil
}

// BrowserVideoDeps are the injected dependencies for RegisterBrowserVideo.
type BrowserVideoDeps struct {
	// Audit is the audit sink (FR-024); nil disables audit.
	Audit *audit.Logger
	// InstallRoot is the managed-Chromium install root passed to the classifier.
	InstallRoot string
	// Config is the FR-018/019/020 config surface.
	Config BrowserVideoConfig

	// The seams below default to the real implementations when nil/zero.
	Relay         *browser.StreamRelay
	Classify      classifyFunc
	LaunchEncoder encoderLauncher
	StartCapture  captureStarter
	EncoderServer encoderPageServer
}

// videoStream is one active stream's orchestration state (one per agent tab;
// shared by every viewer of that agent). Guarded by its owning agent gate for
// lifecycle transitions; the fields the relay/ingest goroutines touch
// concurrently (token, liveness) are additionally atomic/mutex-guarded as noted.
type videoStream struct {
	streamID  string
	agentID   string
	sessionID string
	codec     string
	hasAudio  bool

	rootCtx  context.Context
	agentCtx context.Context

	origin    string
	pageURL   string
	serveStop func()

	encoderTab encoderTab
	capture    captureDriver

	// dims are the current encode dimensions (reduced by StepDown); guarded by
	// the agent gate.
	width, height int

	viewers    map[*wsVideoViewer]struct{}
	relaunches int
	failed     bool

	stopCh   chan struct{} // closed once on teardown; stops watcher + liveness
	stopOnce sync.Once
	liveness *time.Timer

	lastStepDown time.Time

	// lastChunkAt is the arrival time of the most recently ingested chunk for
	// this stream (SF-L4), set by noteChunk under the agent gate. Read by
	// onLivenessTimeout to re-validate against a genuine stall rather than
	// trusting Timer.Reset alone, which cannot cancel an already-fired (now
	// running) timer callback invocation — see both methods' doc comments.
	lastChunkAt time.Time
}

func (s *videoStream) stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

// BrowserVideoOrchestrator is component M. Safe for concurrent use.
type BrowserVideoOrchestrator struct {
	cfg         resolvedConfig
	installRoot string
	audit       *audit.Logger

	relay         *browser.StreamRelay
	classify      classifyFunc
	launchEncoder encoderLauncher
	startCapture  captureStarter
	encoderServer encoderPageServer
	ingest        ingestController

	enabled atomic.Bool

	mu            sync.Mutex
	streams       map[string]*videoStream // by streamID
	agentStreamID map[string]string       // agentID -> streamID
	agentGates    map[string]*sync.Mutex  // per-agent serialization of lifecycle ops
}

// RegisterBrowserVideo wires the live-browser video streaming subsystem and
// returns the orchestrator. It creates the stream relay, registers the
// loopback capture-ingest endpoint (component E) on mux with a relay adapter +
// the orchestrator's StepDown hook, and returns the orchestrator for the WS
// attach path to drive.
//
// INTEGRATION SEAM (for the lead — gateway.go, do NOT edit here): register this
// exactly where the browser WS handler is wired (pkg/gateway/gateway.go ~line
// 2091, right after
//
//	browserWSHandler := newBrowserWSHandler(agentLoop, allowedOrigin)
//	runningServices.ChannelManager.RegisterHTTPHandler("/api/v1/browser/ws", browserWSHandler)
//
// add ONE line:
//
//	browserVideo := gateway.RegisterBrowserVideo(runningServices.ChannelManager, gateway.BrowserVideoDeps{
//	    Audit:       agentLoop.AuditLogger(),
//	    InstallRoot: browserInstallRoot,        // same root ClassifyVideoCapability inspects
//	    Config: gateway.BrowserVideoConfig{
//	        IngestWSURL: fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", cfg.Gateway.Port),
//	        // Enabled defaults true; wire the FR-020 kill-switch (config reload
//	        // hook) to browserVideo.SetVideoEnabled(...).
//	    },
//	})
//	// Hand `browserVideo` to the browser WS attach path so a video-capable
//	// attach calls browserVideo.AttachViewer(...) (see AttachViewer).
//
// *channels.Manager already satisfies IngestMux (used by RegisterCaptureIngest).
func RegisterBrowserVideo(mux IngestMux, deps BrowserVideoDeps) *BrowserVideoOrchestrator {
	o := newOrchestrator(deps)
	// Register the loopback ingest endpoint (component E) with a relay adapter
	// (bridges gateway.EncodedChunk -> browser.EncodedChunk + resets liveness),
	// the orchestrator's StepDown hook, and its ConnFailed hook (SF-M3: a
	// downstream write failure on the ingest connection proactively triggers
	// the SAME re-mint+relaunch recovery a genuine CRIT-002 encoder-tab crash
	// does, instead of waiting out the liveness timeout).
	o.ingest = RegisterCaptureIngest(mux, IngestDeps{
		Relay:    &videoRelayAdapter{orch: o},
		Audit:    deps.Audit,
		StepDown: o.StepDown,
		ConnFailed: func(streamID string) {
			// No specific tab handle from the ingest layer — see
			// handleEncoderDrop's doc comment on deadTab==nil.
			o.handleEncoderDrop(streamID, nil)
		},
	})
	// SF-H2: wire the ingest handler as the relay's keyframe-request seam —
	// o.ingest already satisfies browser.keyframeRequester (RequestKeyframe is
	// part of ingestController), so no adapter is needed.
	browser.SetKeyframeRequester(o.ingest)
	// FR-019 (Test 28): wire pkg/tools/browser's relay-drop and
	// Xvfb/PulseAudio-sidecar-restart hooks into the gateway's metrics
	// singleton (browser_metrics.go) — mirrors tools.SetToolMetricsRecorder
	// (FR-039/C4).
	browser.SetBrowserMetricsRecorder(globalBrowserVideoMetrics())
	return o
}

// newOrchestrator builds the orchestrator with all seams resolved (real
// defaults unless injected). Tests call this directly with fakes and set
// o.ingest to a fake ingest controller.
func newOrchestrator(deps BrowserVideoDeps) *BrowserVideoOrchestrator {
	o := &BrowserVideoOrchestrator{
		cfg:           resolveConfig(deps.Config),
		installRoot:   deps.InstallRoot,
		audit:         deps.Audit,
		relay:         deps.Relay,
		classify:      deps.Classify,
		launchEncoder: deps.LaunchEncoder,
		startCapture:  deps.StartCapture,
		encoderServer: deps.EncoderServer,
		streams:       make(map[string]*videoStream),
		agentStreamID: make(map[string]string),
		agentGates:    make(map[string]*sync.Mutex),
	}
	if o.relay == nil {
		o.relay = browser.NewStreamRelay()
	}
	if o.classify == nil {
		o.classify = browser.ClassifyVideoCapability
	}
	if o.launchEncoder == nil {
		o.launchEncoder = defaultEncoderLauncher
	}
	if o.startCapture == nil {
		o.startCapture = defaultCaptureStarter
	}
	if o.encoderServer == nil {
		o.encoderServer = loopbackEncoderServer{}
	}
	// FR-019 "live-stream count" gauge: read straight from the relay's own
	// StreamCount() (stream_relay.go already tracks this for exactly this
	// purpose). Re-registering on every newOrchestrator call (including in
	// tests) is harmless — it just points the gauge at the current relay.
	globalBrowserVideoMetrics().setStreamCounter(o.relay.StreamCount)
	enabled := true
	if deps.Config.Enabled != nil {
		enabled = *deps.Config.Enabled
	}
	o.enabled.Store(enabled)
	return o
}

// AttachParams is what the WS attach path supplies to AttachViewer. RootCtx is
// the coordinator's shared headful-Chrome root context (coordinator.Register's
// return); AgentCtx is the agent tab's own CDP context (a child of RootCtx),
// used for StartCapture.
type AttachParams struct {
	WC        *browserWSConn
	AgentID   string
	SessionID string
	ViewerID  string
	RootCtx   context.Context
	AgentCtx  context.Context
	VideoCaps []string
	AudioCaps []string
}

// VideoViewerHandle is returned to the WS attach path for a successful
// video-capable attach; Detach unbinds the viewer (and tears the stream down
// when it was the last viewer). A nil handle means no stream was started (the
// viewer was moved to the unavailable state) — the caller does nothing further.
type VideoViewerHandle struct {
	orch     *BrowserVideoOrchestrator
	streamID string
	agentID  string
	vv       *wsVideoViewer
}

// Detach unbinds this viewer from its stream.
func (h *VideoViewerHandle) Detach() {
	if h == nil || h.orch == nil {
		return
	}
	h.orch.detachViewer(h.agentID, h.streamID, h.vv)
}

// AttachViewer is the entry point the WS attach path calls for a viewer that
// wants live video. It returns a non-nil handle when a stream is (or already
// was) running and this viewer is now attached; it returns nil (having already
// sent the unavailable state to the viewer) when video is off, the install is
// not video-capable, or no offered codec intersects the viewer's caps.
func (o *BrowserVideoOrchestrator) AttachViewer(p AttachParams) (*VideoViewerHandle, error) {
	if p.WC == nil {
		return nil, fmt.Errorf("browser video: AttachViewer requires a connection")
	}

	// FR-020: kill-switch off ⇒ unavailable, no stream.
	if !o.enabled.Load() {
		o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "kill_switch_off")
		return nil, nil
	}

	// Component K: classify. Not capable ⇒ unavailable, no stream (US-5).
	capab := o.classify(o.installRoot)
	if !capab.Capable {
		o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "not_video_capable:"+capab.Reason)
		return nil, nil
	}

	// Serialize all lifecycle ops for this agent so concurrent first-attaches
	// don't double-start, and a warm attach never races a start/teardown.
	gate := o.agentGate(p.AgentID)
	gate.Lock()
	defer gate.Unlock()

	st, codec, ok := o.ensureStreamLocked(p, capab)
	if !ok {
		// ensureStreamLocked already sent the unavailable state with the
		// specific reason.
		return nil, nil
	}

	vv := newWSVideoViewer(p.WC, st.streamID, p.SessionID, p.ViewerID)

	// browser_stream_init MUST be sent before the first chunk (contract).
	o.sendStreamInit(p.WC, p.SessionID, p.ViewerID, st, codec)

	// FR-015/CR2: relay.Attach authorizes (via vv.Authorized) BEFORE any GOP
	// replay, then flushes the replay (keyframe first, then deltas) to vv
	// itself while still holding the stream's own lock — this guarantees the
	// replay is fully delivered before any concurrently in-flight Ingest
	// chunk can reach vv. The returned slice is for introspection only (see
	// StreamRelay.Attach's doc comment) — it must NOT be resent here.
	if _, err := o.relay.Attach(st.streamID, vv); err != nil {
		o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "relay_attach_failed:"+err.Error())
		if len(st.viewers) == 0 {
			// SF4: ensureStreamLocked may have just cold-started this stream
			// for this very attach — a pre-existing stream can never
			// legitimately have 0 viewers at this point (detachViewer tears
			// one down the instant its last viewer leaves), so don't leak a
			// viewerless stream that will never get one now that its only
			// attach failed.
			o.teardownStreamLocked(st)
		}
		return nil, nil
	}

	st.viewers[vv] = struct{}{}
	return &VideoViewerHandle{orch: o, streamID: st.streamID, agentID: p.AgentID, vv: vv}, nil
}

// ensureStreamLocked returns the agent's live stream (creating it if none),
// with the codec negotiated against this viewer's caps. Returns ok=false (after
// sending the unavailable state) on any negotiation or bring-up failure. The
// caller MUST hold the agent gate.
func (o *BrowserVideoOrchestrator) ensureStreamLocked(
	p AttachParams,
	capab browser.VideoCapability,
) (*videoStream, string, bool) {
	if st := o.lookupAgentStream(p.AgentID); st != nil && !o.streamFailed(st) {
		// Existing stream: v1 is single-encode-per-source, so this viewer must
		// support the ALREADY-ACTIVE codec (US-6/AC-2) or it gets the
		// unavailable state — no second concurrent encoder.
		if !viewerSupportsCodec(p.VideoCaps, st.codec) {
			o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "codec_mismatch_active:"+st.codec)
			return nil, "", false
		}
		return st, st.codec, true
	}

	// New stream: negotiate a producible codec against the viewer's caps.
	codec := o.negotiateVideoCodec(p.VideoCaps)
	if codec == "" {
		o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "no_intersecting_codec")
		return nil, "", false
	}
	hasAudio := o.negotiateAudio(capab, p.AudioCaps)

	st, err := o.startStreamLocked(p, codec, hasAudio)
	if err != nil {
		o.sendUnavailable(p.WC, p.SessionID, p.ViewerID, "stream_bringup_failed:"+err.Error())
		return nil, "", false
	}
	return st, codec, true
}

// startStreamLocked mints a token, serves the encoder page on an unguessable
// loopback origin, launches the encoder page, and starts capture on the agent
// tab. On any step failure it unwinds everything it created and returns an
// error. The caller MUST hold the agent gate. Blocking CDP work runs with the
// agent gate held (serializing only this agent's cold-start) but NOT the
// orchestrator mu.
// auditBringupFailed records a cold-start bring-up failure (serve / launch /
// capture) as a stream-lifecycle failure event, so a bring-up that never
// reaches the running state is as visible in the audit trail as a mid-stream
// failure or a kill-switch teardown (FR-024). The manual unwind paths in
// startStreamLocked can't use failStreamLocked — no *videoStream exists yet.
func (o *BrowserVideoOrchestrator) auditBringupFailed(streamID, agentID, codec, stage string, err error) {
	o.auditStream(audit.EventBrowserLiveVideoStreamFailed, audit.SeverityWarn, map[string]any{
		"stream_id": streamID,
		"agent_id":  agentID,
		"codec":     codec,
		"stage":     stage,
		"reason":    "bringup_failed",
		"error":     err.Error(),
	})
}

func (o *BrowserVideoOrchestrator) startStreamLocked(
	p AttachParams,
	codec string,
	hasAudio bool,
) (*videoStream, error) {
	streamID := randHex(16)
	token := o.ingest.MintIngestToken(streamID)

	origin, pageURL, serveStop, err := o.encoderServer.Serve()
	if err != nil {
		o.auditBringupFailed(streamID, p.AgentID, codec, "serve_encoder_page", err)
		o.ingest.EndStream(streamID)
		return nil, fmt.Errorf("serve encoder page: %w", err)
	}

	width, height := o.cfg.panelWidth, o.cfg.panelHeight

	encTab, err := o.launchEncoder(
		p.RootCtx,
		o.encoderLaunchCfg(string(token), streamID, codec, pageURL, origin, hasAudio),
	)
	if err != nil {
		o.auditBringupFailed(streamID, p.AgentID, codec, "launch_encoder_page", err)
		serveStop()
		o.ingest.EndStream(streamID)
		return nil, fmt.Errorf("launch encoder page: %w", err)
	}

	capture, err := o.startCaptureAt(p.AgentCtx, streamID, width, height)
	if err != nil {
		o.auditBringupFailed(streamID, p.AgentID, codec, "start_capture", err)
		_ = encTab.Close()
		serveStop()
		o.ingest.EndStream(streamID)
		return nil, fmt.Errorf("start capture: %w", err)
	}

	st := &videoStream{
		streamID:   streamID,
		agentID:    p.AgentID,
		sessionID:  p.SessionID,
		codec:      codec,
		hasAudio:   hasAudio,
		rootCtx:    p.RootCtx,
		agentCtx:   p.AgentCtx,
		origin:     origin,
		pageURL:    pageURL,
		serveStop:  serveStop,
		encoderTab: encTab,
		capture:    capture,
		width:      width,
		height:     height,
		viewers:    make(map[*wsVideoViewer]struct{}),
		stopCh:     make(chan struct{}),
	}
	st.liveness = time.AfterFunc(o.cfg.livenessTimeout, func() { o.onLivenessTimeout(streamID) })

	o.mu.Lock()
	o.streams[streamID] = st
	o.agentStreamID[p.AgentID] = streamID
	o.mu.Unlock()

	go o.watchEncoder(streamID, st.stopCh, encTab)

	o.auditStream(audit.EventBrowserLiveVideoStreamStarted, audit.SeverityInfo, map[string]any{
		"stream_id": streamID,
		"agent_id":  p.AgentID,
		"codec":     codec,
		"has_audio": hasAudio,
	})
	return st, nil
}

// encoderLaunchCfg builds the browser.EncoderLaunchCfg used for BOTH a
// stream's initial cold-start (startStreamLocked) and its CRIT-002 relaunch
// (handleEncoderDrop) — the two call sites previously repeated this literal
// verbatim, differing only in token/streamID/codec/pageURL/origin/hasAudio
// (S-F3, review-round-2 dedup); every other field is always sourced from
// o.cfg the same way in both places.
func (o *BrowserVideoOrchestrator) encoderLaunchCfg(
	token, streamID, codec, pageURL, origin string,
	hasAudio bool,
) browser.EncoderLaunchCfg {
	return browser.EncoderLaunchCfg{
		Token:            token,
		WSURL:            o.cfg.ingestWSURL,
		StreamID:         streamID,
		VideoCodec:       codec,
		HasAudio:         hasAudio,
		AudioCodec:       "opus",
		EncoderURL:       pageURL,
		Origin:           origin,
		Framerate:        o.cfg.framerate,
		KeyframeInterval: o.cfg.keyframe,
	}
}

// startCaptureAt starts (or restarts) screencast capture at width×height for
// streamID, wiring its JPEG output straight into the ingest feed (component
// E's FeedFrame) — the exact CaptureOptions/onFrame shape previously
// repeated verbatim at a stream's cold-start (startStreamLocked) and
// StepDown's reduced-size restart (S-F4, review-round-2 dedup).
func (o *BrowserVideoOrchestrator) startCaptureAt(
	ctx context.Context,
	streamID string,
	width, height int,
) (captureDriver, error) {
	return o.startCapture(ctx, browser.CaptureOptions{
		Format:        "jpeg",
		Quality:       o.cfg.jpegQuality,
		MaxWidth:      width,
		MaxHeight:     height,
		EveryNthFrame: 1,
	}, func(jpeg []byte, seq uint32, tsMillis uint64) {
		o.ingest.FeedFrame(streamID, jpeg, seq, tsMillis)
	})
}

// watchEncoder waits for the encoder tab to die (crash == "ingest drop",
// CRIT-002) or the stream to be torn down, and on a genuine crash triggers the
// re-mint + relaunch. One watcher per live tab. tab is passed through to
// handleEncoderDrop so it can tell a genuine crash of the CURRENTLY-active
// tab apart from this watcher's own stale re-fire: EncoderTab.Done()
// documents that it also closes when Close is called, and
// handleEncoderDrop's own tail (`oldTab.Close()`, right below) intentionally
// closes the tab THIS exact watcher is watching once it has already been
// superseded — without the tab-identity check that fire would otherwise
// re-enter handleEncoderDrop a second time for a drop that was already
// handled.
func (o *BrowserVideoOrchestrator) watchEncoder(
	streamID string,
	stopCh chan struct{},
	tab encoderTab,
) {
	select {
	case <-tab.Done():
		select {
		case <-stopCh:
			return // torn down / relaunched by us — nothing to recover
		default:
		}
		o.handleEncoderDrop(streamID, tab)
	case <-stopCh:
		return
	}
}

// handleEncoderDrop performs the CRIT-002 recovery: invalidate the old token by
// re-minting a FRESH one (the ingest handler drops the old token + closes any
// stale connection in place — there is NO same-token reconnect), then relaunch
// the encoder page (re-granting audio). Bounded by MaxRelaunches; exceeding it
// fails the stream to the unavailable state (FR-018). Runs under the agent gate.
//
// deadTab is the SPECIFIC encoder tab the caller observed die, when known:
// watchEncoder always supplies it (see its doc comment above); the SF-M3
// ConnFailed hook wired in RegisterBrowserVideo (browser_ingest.go's
// writePump, on a downstream write failure) has no tab handle to offer and
// passes nil, deliberately skipping the identity check below — a write
// failure is itself a single, non-repeating signal (writePump returns
// immediately after triggering it), so it needs no self-refire guard the way
// watchEncoder's Close()-triggers-Done() re-entry does.
func (o *BrowserVideoOrchestrator) handleEncoderDrop(streamID string, deadTab encoderTab) {
	st := o.streamByID(streamID)
	if st == nil {
		return
	}
	gate := o.agentGate(st.agentID)
	gate.Lock()
	defer gate.Unlock()

	// Re-check under the gate — a concurrent teardown may have removed it.
	if cur := o.streamByID(streamID); cur != st || o.streamFailed(st) || o.stopped(st) {
		return
	}
	// A stale signal for a tab this stream has already moved past (this
	// watcher's own trigger, superseded by a concurrent relaunch that won
	// the race for the gate first) — nothing to recover, it was already
	// recovered.
	if deadTab != nil && st.encoderTab != deadTab {
		return
	}

	st.relaunches++
	if st.relaunches > o.cfg.maxRelaunches {
		slog.Warn("browser video: encoder relaunch budget exhausted; failing stream",
			"stream_id", streamID, "relaunches", st.relaunches)
		o.failStreamLocked(st)
		return
	}

	// Re-mint (CRIT-002): fresh token, old one dead, old connection closed.
	newToken := o.ingest.MintIngestToken(streamID)
	oldTab := st.encoderTab

	newTab, err := o.launchEncoder(
		st.rootCtx,
		o.encoderLaunchCfg(
			string(newToken),
			streamID,
			st.codec,
			st.pageURL,
			st.origin,
			st.hasAudio,
		),
	)
	if err != nil {
		slog.Warn("browser video: encoder relaunch failed; failing stream",
			"stream_id", streamID, "error", err)
		o.failStreamLocked(st)
		return
	}

	st.encoderTab = newTab
	if oldTab != nil {
		_ = oldTab.Close()
	}
	go o.watchEncoder(streamID, st.stopCh, newTab)

	// FR-019 "capture restart count" (Test 28): the CRIT-002 re-mint +
	// relaunch path is one of the two recovery mechanisms this metric
	// covers (the other is StepDown's capture-driver restart, below).
	globalBrowserVideoMetrics().IncCaptureRestart()

	o.auditStream(audit.EventBrowserLiveVideoStreamRelaunched, audit.SeverityWarn, map[string]any{
		"stream_id":  streamID,
		"agent_id":   st.agentID,
		"relaunches": st.relaunches,
	})
}

// onLivenessTimeout fires when no chunk has been relayed within LivenessTimeout.
// If the stream still exists and has viewers, that is a mid-stream stall
// (FR-018) ⇒ fail it to the unavailable state (never an infinite spinner).
//
// SF-L4: noteChunk's Timer.Reset races this callback — Reset only reschedules
// FUTURE firings, it cannot cancel an invocation that has already started
// running (Go's time.AfterFunc semantics), so a chunk that legitimately
// arrived in the narrow window between this timer firing and this goroutine
// acquiring the agent gate would otherwise still get failed here even though
// the stream just proved itself live. Guard against that spurious failure by
// re-validating against st.lastChunkAt (written by noteChunk under the SAME
// agent gate) before declaring a genuine stall.
func (o *BrowserVideoOrchestrator) onLivenessTimeout(streamID string) {
	st := o.streamByID(streamID)
	if st == nil {
		return
	}
	gate := o.agentGate(st.agentID)
	gate.Lock()
	defer gate.Unlock()

	if cur := o.streamByID(streamID); cur != st || o.streamFailed(st) || o.stopped(st) {
		return
	}
	if len(st.viewers) == 0 {
		return // no one watching; teardown handles idle streams
	}
	if !st.lastChunkAt.IsZero() && time.Since(st.lastChunkAt) < o.cfg.livenessTimeout {
		// A chunk arrived (and reset the timer) just before this callback
		// acquired the gate — the timer's own Reset already re-armed it for
		// the next real stall, so there's nothing further to do here.
		return
	}
	slog.Warn("browser video: mid-stream liveness timeout; failing stream",
		"stream_id", streamID, "timeout", o.cfg.livenessTimeout)
	o.failStreamLocked(st)
}

// StepDown is the FR-014 oversize-chunk hook the ingest endpoint invokes. v1
// has no ABR, so the real step-down is: halve the stream's encode dimensions
// (floored) and restart the capture leg at the smaller size so subsequent
// keyframes are smaller. Rate-limited to avoid thrash; runs asynchronously so
// it never blocks the ingest read goroutine.
func (o *BrowserVideoOrchestrator) StepDown(streamID string) {
	go func() {
		st := o.streamByID(streamID)
		if st == nil {
			return
		}
		gate := o.agentGate(st.agentID)
		gate.Lock()
		defer gate.Unlock()

		if cur := o.streamByID(streamID); cur != st || o.streamFailed(st) || o.stopped(st) {
			return
		}
		if !st.lastStepDown.IsZero() && time.Since(st.lastStepDown) < o.cfg.stepDownCooldown {
			return // rate-limited
		}
		newW, newH := st.width/2, st.height/2
		if newW < stepDownMinWidth || newH < stepDownMinHeight {
			return // already at the floor — nothing more to give
		}

		newCapture, err := o.startCaptureAt(st.agentCtx, streamID, newW, newH)
		if err != nil {
			slog.Warn(
				"browser video: step-down capture restart failed",
				"stream_id",
				streamID,
				"error",
				err,
			)
			return
		}
		oldCapture := st.capture
		st.capture = newCapture
		st.width, st.height = newW, newH
		st.lastStepDown = time.Now()
		if oldCapture != nil {
			oldCapture.Stop()
		}
		// FR-019 "capture restart count" (Test 28): the capture driver was
		// actually stopped and a new one started at reduced dimensions —
		// the other of the two recovery mechanisms this metric covers (see
		// handleEncoderDrop's CRIT-002 relaunch above).
		globalBrowserVideoMetrics().IncCaptureRestart()
		slog.Info(
			"browser video: stepped down encode dimensions",
			"stream_id",
			streamID,
			"width",
			newW,
			"height",
			newH,
		)
	}()
}

// SetVideoEnabled flips the FR-020 kill-switch. Turning it OFF makes new
// attaches get the unavailable state AND tears down every active stream (moving
// their viewers to the unavailable state) without a redeploy.
func (o *BrowserVideoOrchestrator) SetVideoEnabled(enabled bool) {
	prev := o.enabled.Swap(enabled)
	if enabled || !prev {
		return // no transition to OFF
	}
	// Snapshot the active agents, then fail each stream under its own gate.
	o.mu.Lock()
	agents := make([]string, 0, len(o.agentStreamID))
	for agentID := range o.agentStreamID {
		agents = append(agents, agentID)
	}
	o.mu.Unlock()

	for _, agentID := range agents {
		gate := o.agentGate(agentID)
		gate.Lock()
		if st := o.lookupAgentStream(agentID); st != nil {
			o.failStreamLocked(st)
		}
		gate.Unlock()
	}
	o.auditStream(audit.EventBrowserLiveVideoKillSwitch, audit.SeverityWarn, map[string]any{
		"enabled":   false,
		"torn_down": len(agents),
	})
}

// Enabled reports the current kill-switch state.
func (o *BrowserVideoOrchestrator) Enabled() bool { return o.enabled.Load() }

// detachViewer unbinds one viewer; when it was the last viewer of its stream,
// the stream is torn down (encoder stops, GOP cache cleared — Edge case "all
// viewers detach"). Runs under the agent gate.
func (o *BrowserVideoOrchestrator) detachViewer(agentID, streamID string, vv *wsVideoViewer) {
	gate := o.agentGate(agentID)
	gate.Lock()
	defer gate.Unlock()

	st := o.streamByID(streamID)
	if st == nil {
		o.relay.Detach(streamID, vv)
		return
	}
	delete(st.viewers, vv)
	o.relay.Detach(streamID, vv)
	if len(st.viewers) == 0 {
		o.teardownStreamLocked(st)
	}
}

// failStreamLocked marks the stream failed, notifies its viewers via the relay
// (Failed ⇒ unavailable state), and tears it down. Caller holds the agent gate.
func (o *BrowserVideoOrchestrator) failStreamLocked(st *videoStream) {
	// MarkFailed fans Failed() to every attached viewer BEFORE teardown so each
	// viewer moves to the unavailable state rather than freezing.
	o.relay.MarkFailed(st.streamID)
	o.teardownStreamLocked(st)
	o.auditStream(audit.EventBrowserLiveVideoStreamFailed, audit.SeverityWarn, map[string]any{
		"stream_id": st.streamID,
		"agent_id":  st.agentID,
	})
}

// teardownStreamLocked stops capture, closes the encoder tab + its loopback
// listener, ends the ingest stream (killing the token, emitting stream_stopped),
// stops the liveness timer, and removes the stream from the maps. Idempotent
// per stream. Caller holds the agent gate.
func (o *BrowserVideoOrchestrator) teardownStreamLocked(st *videoStream) {
	o.mu.Lock()
	if o.streams[st.streamID] != st {
		o.mu.Unlock()
		return // already torn down
	}
	delete(o.streams, st.streamID)
	if o.agentStreamID[st.agentID] == st.streamID {
		delete(o.agentStreamID, st.agentID)
	}
	st.failed = true
	o.mu.Unlock()

	st.stop() // stops the encoder watcher; also our own Close below is safe
	if st.liveness != nil {
		st.liveness.Stop()
	}
	if st.capture != nil {
		st.capture.Stop()
	}
	if st.encoderTab != nil {
		_ = st.encoderTab.Close()
	}
	if st.serveStop != nil {
		st.serveStop()
	}
	o.ingest.EndStream(st.streamID)
	// FR-019: drop this stream's fps/bitrate counters so cardinality tracks
	// live streams, not total-streams-ever (browser_metrics.go's
	// removeStream doc note).
	globalBrowserVideoMetrics().removeStream(st.streamID)
}

// noteChunk resets the mid-stream liveness timer for streamID (called by the
// relay adapter on every ingested chunk) and records the chunk's arrival
// time under the agent gate (SF-L4) so onLivenessTimeout can re-validate
// against a genuinely fresh chunk rather than trusting Timer.Reset alone —
// see onLivenessTimeout's doc comment for why Reset by itself isn't enough
// to close that race.
func (o *BrowserVideoOrchestrator) noteChunk(streamID string) {
	st := o.streamByID(streamID)
	if st == nil || st.liveness == nil {
		return
	}
	gate := o.agentGate(st.agentID)
	gate.Lock()
	st.lastChunkAt = time.Now()
	st.liveness.Reset(o.cfg.livenessTimeout)
	gate.Unlock()
}

// ---- small helpers ----

func (o *BrowserVideoOrchestrator) agentGate(agentID string) *sync.Mutex {
	o.mu.Lock()
	defer o.mu.Unlock()
	g, ok := o.agentGates[agentID]
	if !ok {
		g = &sync.Mutex{}
		o.agentGates[agentID] = g
	}
	return g
}

func (o *BrowserVideoOrchestrator) lookupAgentStream(agentID string) *videoStream {
	o.mu.Lock()
	defer o.mu.Unlock()
	id, ok := o.agentStreamID[agentID]
	if !ok {
		return nil
	}
	return o.streams[id]
}

func (o *BrowserVideoOrchestrator) streamByID(streamID string) *videoStream {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.streams[streamID]
}

func (o *BrowserVideoOrchestrator) streamFailed(st *videoStream) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return st.failed
}

func (o *BrowserVideoOrchestrator) stopped(st *videoStream) bool {
	select {
	case <-st.stopCh:
		return true
	default:
		return false
	}
}

func (o *BrowserVideoOrchestrator) sendStreamInit(
	wc *browserWSConn,
	sessionID, viewerID string,
	st *videoStream,
	codec string,
) {
	sid := sessionID
	wc.sendCriticalGen(generated.BrowserStreamInitFrame{
		Type:             string(generated.WsFrameTypeBrowserStreamInit),
		Codec:            codec,
		HasAudio:         st.hasAudio,
		Width:            st.width,
		Height:           st.height,
		KeyframeInterval: o.cfg.keyframe,
		SessionId:        &sid,
	}, dropContext(sessionID, viewerID, "stream-init"))
}

// sendUnavailable moves a viewer to the generic unavailable state (US-5) and
// logs the SPECIFIC cause operator-side only (O-3).
func (o *BrowserVideoOrchestrator) sendUnavailable(
	wc *browserWSConn,
	sessionID, viewerID, cause string,
) {
	slog.Info("browser video: unavailable state", "session_id", sessionID, "cause", cause)
	msg := browserVideoUnavailableMessage
	sid := sessionID
	wc.sendCriticalGen(generated.BrowserStatusFrame{
		Type:      string(generated.WsFrameTypeBrowserStatus),
		State:     "error",
		Message:   &msg,
		SessionId: &sid,
	}, dropContext(sessionID, viewerID, "video-unavailable"))
}

func (o *BrowserVideoOrchestrator) auditStream(
	event string,
	sev audit.Severity,
	fields map[string]any,
) {
	if o.audit == nil {
		return
	}
	audit.Emit(context.Background(), o.audit, event, sev, fields)
}

// negotiateVideoCodec returns the first producible codec (priority order) whose
// family the viewer advertises, or "" if none intersect (US-5/US-6).
func (o *BrowserVideoOrchestrator) negotiateVideoCodec(viewerCaps []string) string {
	for _, producible := range o.cfg.producibleCodecs {
		if viewerSupportsCodec(viewerCaps, producible) {
			return producible
		}
	}
	return ""
}

// negotiateAudio reports whether audio should stream: the host must have audio
// available (component K) AND the viewer must advertise Opus.
func (o *BrowserVideoOrchestrator) negotiateAudio(
	capab browser.VideoCapability,
	audioCaps []string,
) bool {
	if !capab.AudioAvailable {
		return false
	}
	for _, c := range audioCaps {
		if codecFamily(c) == "opus" {
			return true
		}
	}
	return false
}

// viewerSupportsCodec reports whether any of the viewer's advertised codecs is
// in the same family as codec.
func viewerSupportsCodec(viewerCaps []string, codec string) bool {
	want := codecFamily(codec)
	for _, c := range viewerCaps {
		if codecFamily(c) == want {
			return true
		}
	}
	return false
}

// codecFamily normalizes a codec string to a coarse family so a viewer's
// specific H.264 profile string (e.g. "avc1.4D4028") matches the producible
// codec (and any other H.264 profile the viewer might advertise) without
// exact-string brittleness.
func codecFamily(c string) string {
	lc := strings.ToLower(strings.TrimSpace(c))
	switch {
	case strings.HasPrefix(lc, "avc1."), strings.HasPrefix(lc, "avc3."):
		// Discriminate the H.264 family by PROFILE (avc1.PPCCLL — PP: 42=baseline,
		// 4d=main, 64=high), NOT collapsed to a single "h264", so a baseline-only
		// viewer does not negotiate a main-profile producible codec (FR-006). Only
		// the profile byte (PP) forms the family; level (LL) differences still match
		// (e.g. avc1.4d401e ~ avc1.4d4028 both → "h264-4d").
		if len(lc) >= 7 {
			return "h264-" + lc[5:7]
		}
		return "h264"
	case strings.HasPrefix(lc, "avc1"),
		strings.HasPrefix(lc, "avc3"),
		strings.HasPrefix(lc, "h264"):
		return "h264"
	case strings.HasPrefix(lc, "vp8"):
		return "vp8"
	case strings.HasPrefix(lc, "vp9"):
		return "vp9"
	case strings.HasPrefix(lc, "av01"), strings.HasPrefix(lc, "av1"):
		return "av1"
	default:
		return lc
	}
}

// randHex returns n cryptographically-random bytes as a hex string. A
// crypto/rand failure is catastrophic for the unguessable-key security model,
// so it panics (fail closed) rather than returning a predictable value —
// matching browser_ingest.go's newIngestToken.
func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("browser video: crypto/rand failed: %v", err))
	}
	return hex.EncodeToString(b)
}

// videoRelayAdapter bridges the ingest endpoint's gateway.EncodedChunk into the
// relay's browser.EncodedChunk (field-identical) and resets the stream's
// mid-stream liveness timer on every chunk. Passed to RegisterCaptureIngest as
// IngestDeps.Relay.
type videoRelayAdapter struct {
	orch *BrowserVideoOrchestrator
}

func (a *videoRelayAdapter) Ingest(streamID string, c EncodedChunk) {
	a.orch.noteChunk(streamID)
	// FR-019 "per-stream fps/bitrate": account this chunk's count + bytes
	// (see writeBrowserVideoMetrics for the rate()-based fps/bitrate
	// derivation).
	globalBrowserVideoMetrics().recordChunk(streamID, c.Kind, len(c.Payload))
	// TD3: this is the one production boundary between the gateway's
	// wire-adjacent EncodedChunk (Kind as a plain string, from
	// classifyChunkKind) and the relay's typed browser.EncodedChunk
	// (TD1/TD2). Route through browser.ParseChunkKind + the
	// NewVideoChunk/NewAudioChunk constructors instead of a raw
	// field-by-field struct copy, so the illegal audio+key=true state stays
	// unconstructable from here on: NewAudioChunk takes no Key parameter at
	// all, so c.Key is simply discarded for an audio chunk regardless of
	// what the gateway-side value was.
	var bc browser.EncodedChunk
	if browser.ParseChunkKind(c.Kind) == browser.KindAudio {
		bc = browser.NewAudioChunk(c.Seq, c.TS, c.Codec, c.Payload)
	} else {
		bc = browser.NewVideoChunk(c.Seq, c.TS, c.Key, c.Codec, c.Payload)
	}
	a.orch.relay.Ingest(streamID, bc)
}

// compile-time assertion: videoRelayAdapter satisfies the ingest Relay seam.
var _ Relay = (*videoRelayAdapter)(nil)

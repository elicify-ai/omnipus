//go:build browservideo_e2e

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// live_video_pipeline_e2e_test.go — REAL-HARDWARE end-to-end proof for the
// live-browser video pipeline (ADR-044 Option A rearchitecture,
// docs/internal/specs/live-browser-video-streaming-spec.md). Runs ONLY under
// the browservideo_e2e build tag on a host that can run real Chrome (e.g. the
// ci-omnipus worker). NEVER in the default suite or the dev pod.
//
// Option A dedicates a SEPARATE, full-Chrome-headless process to encoding
// (the orchestrator's own encoderBrowser, browser_stream.go) — the agent's
// own browser is always chrome-headless-shell, which has no WebCodecs
// VideoEncoder at all. There is no virtual-display or audio sidecar anywhere
// in this path: chrome-headless-shell needs no display to run headless, and
// the dedicated encoder browser renders offscreen via new-headless +
// SwiftShader (see EncoderChromeCmdline's doc comment). Audio capture is
// deferred to phase 2 (ADR-044) — HasAudio is always false in this
// increment.
//
// PIPELINE DEPTH ACHIEVED: FULL. This test lives in package gateway
// specifically so it can construct the REAL gateway.BrowserVideoOrchestrator
// via RegisterBrowserVideo, wired with EVERY seam left at its production
// default (real browser.StartCapture, real browser.LaunchEncoderPage, real
// loopbackEncoderServer, a real loopback HTTP server hosting the real
// CaptureIngestHandler) — mirroring setupAndStartServices' construction
// (pkg/gateway/gateway.go) field for field. It then drives AttachViewer
// exactly the way the browser WS attach path does and reads genuine binary
// browser_video_chunk frames off a real *browserWSConn sink. This is the
// level the round-1 deadlock (ADR-038 postmortem referenced throughout
// browser_stream.go/capture.go) actually lived at — a hermetic unit test
// with fakes cannot reach it, because the deadlock was a real chromedp/CDP
// dispatch-goroutine reentrancy bug that only manifests against real Chrome.
//
// The one deliberate simplification versus true gateway boot: the agent's
// own Chrome bring-up is driven directly through browser.NewBrowserCoordinator
// + browser.NewBrowserManager (mirroring registerSharedTools'
// mgr.AttachSharedChrome wiring) rather than spinning up a full
// *agent.AgentLoop — everything downstream of "a live coordinator-mediated
// agent Chrome tab" is the real production path. The dedicated encoder
// browser itself is launched by the REAL orchestrator (o.encoder.ensureRoot,
// via launchEncoderTab) — this test never fakes or pre-launches it; the first
// AttachViewer call below is what triggers its lazy cold-start, exactly as in
// production.

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools/browser"
)

// videoChunkSample is one decoded binary browser_video_chunk envelope
// collected off the real viewer sink during the assertion phase.
type videoChunkSample struct {
	seq     uint32
	ts      uint64
	key     bool
	payload []byte
}

// e2eIngestMux is the minimal IngestMux (httpHandlerRegistrar) this test
// wires RegisterBrowserVideo/RegisterCaptureIngest against: a real
// net/http.ServeMux served over a real loopback TCP listener, mirroring what
// runningServices.ChannelManager provides in production
// (setupAndStartServices) without pulling in the full channel manager's
// config/credential machinery — RegisterHTTPHandler is the only surface
// either component actually needs (IngestMux's doc comment,
// browser_ingest.go).
type e2eIngestMux struct {
	mux *http.ServeMux
}

func newE2EIngestMux() *e2eIngestMux {
	return &e2eIngestMux{mux: http.NewServeMux()}
}

func (m *e2eIngestMux) RegisterHTTPHandler(pattern string, handler http.Handler) {
	m.mux.Handle(pattern, handler)
}

// motionPageDataURL returns a self-contained, base64 data: URL (no network
// dependency) whose <canvas> repaints via requestAnimationFrame, so
// successive Page.startScreencast frames genuinely differ — letting the test
// assert the encoded chunks carry real motion rather than a frozen/stubbed
// capture.
func motionPageDataURL() string {
	const html = `<!doctype html><html><body style="margin:0;background:#000">
<canvas id="c" width="640" height="480"></canvas>
<script>
var cv = document.getElementById('c');
var ctx = cv.getContext('2d');
var x = 0;
function draw() {
  ctx.fillStyle = '#000';
  ctx.fillRect(0, 0, 640, 480);
  ctx.fillStyle = '#f00';
  ctx.fillRect(x, 200, 80, 80);
  x = (x + 7) % 640;
  requestAnimationFrame(draw);
}
draw();
</script>
</body></html>`
	return "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))
}

// findProcessCmdlineContaining walks the /proc process tree rooted at rootPID
// (inclusive) and returns the first process whose /proc/<pid>/cmdline
// contains needle (case-insensitive), space-joined for readability. Best-
// effort: any read failure just means that pid is skipped, never a hard
// error — this is corroboration, not the sole proof. Linux-only (/proc);
// this whole file only ever runs on the ci-omnipus worker.
func findProcessCmdlineContaining(rootPID int, needle string) (string, bool) {
	needle = strings.ToLower(needle)
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return "", false
	}
	type procInfo struct{ pid, ppid int }
	var all []procInfo
	for _, e := range entries {
		pid, convErr := strconv.Atoi(e.Name())
		if convErr != nil {
			continue
		}
		statBytes, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if readErr != nil {
			continue
		}
		closeParen := strings.LastIndex(string(statBytes), ")")
		if closeParen < 0 {
			continue
		}
		fields := strings.Fields(string(statBytes)[closeParen+1:])
		if len(fields) < 2 {
			continue
		}
		ppid, ppidErr := strconv.Atoi(fields[1])
		if ppidErr != nil {
			continue
		}
		all = append(all, procInfo{pid: pid, ppid: ppid})
	}

	visited := map[int]bool{rootPID: true}
	queue := []int{rootPID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		cmdlineBytes, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", cur))
		if readErr == nil {
			cmdline := strings.TrimSpace(strings.ReplaceAll(string(cmdlineBytes), "\x00", " "))
			if cmdline != "" && strings.Contains(strings.ToLower(cmdline), needle) {
				return cmdline, true
			}
		}
		for _, p := range all {
			if p.ppid == cur && !visited[p.pid] {
				visited[p.pid] = true
				queue = append(queue, p.pid)
			}
		}
	}
	return "", false
}

// processAlive reports whether pid still has a /proc entry — best-effort
// liveness check used to corroborate that a killed Chrome process has
// actually exited, not merely that Shutdown/close was called.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	return err == nil
}

// assertAgentIsHeadlessShell corroborates, via the real /proc process tree,
// that the coordinator's OWN agent Chrome (rootPid = coordinator.PID()) is
// running chrome-headless-shell in headless mode — the Option-A (ADR-044)
// invariant that the agent's own browser is UNCONDITIONALLY headless-shell,
// never the dedicated encoder's full-Chrome build. The deterministic
// guarantee lives in source (exec_resolver.go: managedExecAllocatorOpts
// always appends "--headless", and selectDownloadBuild always resolves
// headlessShellBuild() — there is no video-capable branch on the agent launch
// path anymore); this function is corroboration via a genuinely independent
// observation channel, not the sole proof.
func assertAgentIsHeadlessShell(t *testing.T, rootPid int) {
	t.Helper()
	if rootPid <= 0 {
		t.Fatalf("coordinator.PID() returned %d — no live Chrome process to inspect", rootPid)
	}
	cmdline, found := findProcessCmdlineContaining(rootPid, "chrom")
	if !found {
		t.Fatalf("could not locate a chrome[ium] process under pid %d via /proc", rootPid)
	}
	t.Logf("AGENT CHROME ARGS: %s", cmdline)
	if !strings.Contains(cmdline, "--headless") {
		t.Fatalf(
			"expected the agent's own Chrome to run --headless (chrome-headless-shell, unconditional under Option A), got cmdline: %s",
			cmdline,
		)
	}
	if !strings.Contains(strings.ToLower(cmdline), "headless-shell") {
		t.Fatalf("expected the agent's own Chrome binary to be chrome-headless-shell, got cmdline: %s", cmdline)
	}
}

// TestLiveVideoPipeline_RealChrome_EmitsChunks is the full-depth ADR-044
// Option A real-hardware proof: a real chrome-headless-shell agent Chrome
// launched by the real BrowserCoordinator, a real animated page, the REAL
// gateway.BrowserVideoOrchestrator (every seam at its production default)
// wired against a real loopback capture-ingest HTTP server, a real
// AttachViewer that lazily cold-starts the orchestrator's OWN dedicated
// full-Chrome-headless encoder browser, and assertions against genuine binary
// browser_video_chunk envelopes read off a real *browserWSConn sink — then a
// real Detach + Shutdown teardown check. This is what catches the class of
// bug hermetic tests with fakes cannot: the round-1 CDP dispatch-goroutine
// deadlock (capture.go's ADR-038 DEADLOCK POSTMORTEM comment) only ever
// manifested here, against real Chrome's real CDP event delivery.
func TestLiveVideoPipeline_RealChrome_EmitsChunks(t *testing.T) {
	homeDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// ---- STEP 1: real full-Chrome install (for the dedicated encoder) + DS-5 capability classification ----
	// Option A: ClassifyVideoCapability is satisfied by linux + a full-Chrome
	// build already present under installRoot alone — no display/audio
	// sidecar is involved. The agent's own chrome-headless-shell is a
	// SEPARATE, non-interchangeable build (see installer.go's
	// EnsureChromiumBuild doc) that the coordinator resolves lazily on its
	// own in STEP 2 below — it is deliberately NOT pre-fetched here.
	installRoot := os.Getenv("DIAG_INSTALL_ROOT")
	if installRoot == "" {
		installRoot = filepath.Join(homeDir, "chromium")
	}
	fullChromePath, ensureErr := browser.EnsureChromiumFullBuild(ctx, installRoot)
	if ensureErr != nil {
		t.Fatalf("BLOCKED: EnsureChromiumFullBuild(%q): %v", installRoot, ensureErr)
	}
	t.Logf("CHROME: full-Chrome build ready at %q (install_root=%q)", fullChromePath, installRoot)

	capability := browser.ClassifyVideoCapability(installRoot)
	t.Logf(
		"CAPABILITY: level=%v capable=%v audio_available=%v reason=%q audio_reason=%q",
		capability.Level,
		capability.Capable,
		capability.AudioAvailable,
		capability.Reason,
		capability.AudioReason,
	)
	if !capability.Capable {
		t.Fatalf("BLOCKED: host not classified video-capable — reason: %s", capability.Reason)
	}
	if capability.Level.String() != "video_only" {
		t.Fatalf("BLOCKED: expected video_only capability level (audio deferred to phase 2), got %v", capability.Level)
	}

	// ---- STEP 2: real BrowserCoordinator + BrowserManager — agent Chrome is UNCONDITIONALLY chrome-headless-shell ----
	profileDir := filepath.Join(homeDir, "browser-profile")
	coordinator := browser.NewBrowserCoordinator(homeDir, browser.BrowserConfig{
		Enabled:     true,
		ProfileDir:  profileDir,
		MaxTabs:     5,
		PageTimeout: 30 * time.Second,
	}, 10)
	t.Cleanup(coordinator.Shutdown)

	ssrf := security.NewSSRFChecker(nil)
	mgr, mgrErr := browser.NewBrowserManager(
		browser.BrowserConfig{Enabled: true, ProfileDir: profileDir},
		ssrf,
	)
	if mgrErr != nil {
		t.Fatalf("NewBrowserManager: %v", mgrErr)
	}
	const agentID = "e2e-agent"
	// Wires this manager to the coordinator BEFORE any Session/tab call, so
	// ensureStarted asks the coordinator for the shared Chrome (ADR-043)
	// instead of launching its own separate instance — mirrors
	// pkg/agent/loop.go's registerSharedTools call site exactly.
	mgr.AttachSharedChrome(coordinator, agentID)

	agentTabCtx, sessionErr := mgr.Session("e2e-session")
	if sessionErr != nil {
		t.Fatalf("mgr.Session (triggers the coordinator-mediated Chrome launch): %v", sessionErr)
	}
	t.Logf("CHROME LAUNCH: coordinator pid=%d", coordinator.PID())
	assertAgentIsHeadlessShell(t, coordinator.PID())

	motionURL := motionPageDataURL()
	if navErr := chromedp.Run(agentTabCtx, chromedp.Navigate(motionURL)); navErr != nil {
		t.Fatalf("navigate agent tab to the motion page: %v", navErr)
	}
	t.Logf("PAGE: agent tab navigated to a %d-byte animated data: URL", len(motionURL))

	// ---- STEP 3: REAL BrowserVideoOrchestrator, mirroring setupAndStartServices ----
	ingestMux := newE2EIngestMux()
	ln, lnErr := net.Listen("tcp", "127.0.0.1:0")
	if lnErr != nil {
		t.Fatalf("listen for the real capture-ingest server: %v", lnErr)
	}
	ingestSrv := &http.Server{Handler: ingestMux.mux}
	go func() { _ = ingestSrv.Serve(ln) }()
	t.Cleanup(func() { _ = ingestSrv.Close() })
	ingestPort := ln.Addr().(*net.TCPAddr).Port
	ingestWSURL := fmt.Sprintf("ws://127.0.0.1:%d/api/v1/browser/capture-ingest", ingestPort)
	t.Logf("INGEST: real loopback capture-ingest server listening, url=%s", ingestWSURL)

	enabled := true
	// Every seam below (Relay/Classify/LaunchEncoder/StartCapture/EncoderServer)
	// is left zero-valued, so newOrchestrator resolves EVERY ONE to its real
	// production default (browser.NewStreamRelay, browser.ClassifyVideoCapability,
	// browser.LaunchEncoderPage, browser.StartCapture, loopbackEncoderServer{}) —
	// field-for-field the same call setupAndStartServices makes
	// (pkg/gateway/gateway.go). Nothing here is a fake — including the
	// orchestrator's OWN dedicated encoder browser (o.encoder), which the
	// first AttachViewer call below launches lazily via the real
	// resolveExecPath/pipeLauncher seams (defaultEncoderExecPath ->
	// browser.EnsureChromiumFullBuild, defaultEncoderPipeLaunch ->
	// browser.EncoderChromeCmdline over cdppipe).
	orch := RegisterBrowserVideo(ingestMux, BrowserVideoDeps{
		InstallRoot: installRoot,
		Config: BrowserVideoConfig{
			IngestWSURL:     ingestWSURL,
			LivenessTimeout: 30 * time.Second,
			Enabled:         &enabled,
		},
	})
	t.Cleanup(orch.Shutdown)

	wc := newTestVideoConn()
	handle, attachErr := orch.AttachViewer(AttachParams{
		WC:        wc,
		AgentID:   agentID,
		SessionID: "e2e-session",
		ViewerID:  "e2e-viewer",
		AgentCtx:  agentTabCtx,
		VideoCaps: []string{
			"avc1.4D4028",
			"vp8",
		}, // H.264 main first, matches defaultProducibleVideoCodecs
		AudioCaps: []string{"opus"},
	})
	if attachErr != nil {
		t.Fatalf("AttachViewer: %v", attachErr)
	}
	if handle == nil {
		f := findStatusError(t, wc)
		msg := ""
		if f.Message != nil {
			msg = *f.Message
		}
		t.Fatalf("BLOCKED: AttachViewer produced no stream (unavailable state) — message=%q", msg)
	}
	t.Logf("ATTACH: stream started for agent=%q", agentID)

	// ---- STEP 3 cont'd: the dedicated encoder browser must now be a REAL, DISTINCT process ----
	// AttachViewer's first call lazily cold-starts the orchestrator's own
	// encoderBrowser (o.encoder.ensureRoot, via launchEncoderTab) — assert it
	// is a genuine, separate OS process from the agent's chrome-headless-shell
	// (coordinator.PID()), and corroborate via /proc that it is full Chrome
	// running new-headless (--headless present, but NOT chrome-headless-shell).
	orch.encoder.mu.Lock()
	var encoderPID int
	if orch.encoder.cmd != nil && orch.encoder.cmd.Process != nil {
		encoderPID = orch.encoder.cmd.Process.Pid
	}
	orch.encoder.mu.Unlock()
	if encoderPID == 0 {
		t.Fatal(
			"BLOCKED: no encoder Chrome process captured after a successful AttachViewer — the dedicated encoder browser never launched",
		)
	}
	if encoderPID == coordinator.PID() {
		t.Fatalf(
			"expected the encoder Chrome process (pid=%d) to be DISTINCT from the agent's own chrome-headless-shell process (pid=%d)",
			encoderPID,
			coordinator.PID(),
		)
	}
	if encoderCmdline, found := findProcessCmdlineContaining(encoderPID, "chrom"); found {
		t.Logf("ENCODER CHROME ARGS: %s", encoderCmdline)
		if !strings.Contains(encoderCmdline, "--headless") {
			t.Fatalf("expected the dedicated encoder Chrome to run --headless, got cmdline: %s", encoderCmdline)
		}
		if strings.Contains(strings.ToLower(encoderCmdline), "headless-shell") {
			t.Fatalf(
				"expected the dedicated encoder Chrome to be the FULL chrome build, not chrome-headless-shell: %s",
				encoderCmdline,
			)
		}
	} else {
		t.Logf(
			"WARNING: could not locate the encoder Chrome process (pid=%d) via /proc — relying on the captured PID alone",
			encoderPID,
		)
	}
	t.Logf(
		"ENCODER: dedicated full-Chrome encoder browser live at pid=%d (agent pid=%d)",
		encoderPID,
		coordinator.PID(),
	)

	// ---- STEP 4: assert real browser_stream_init, then real binary chunks ----
	initFrame := drainStreamInit(t, wc)
	t.Logf(
		"INIT: codec=%q has_audio=%v width=%d height=%d keyframe_interval=%d",
		initFrame.Codec,
		initFrame.HasAudio,
		initFrame.Width,
		initFrame.Height,
		initFrame.KeyframeInterval,
	)
	if initFrame.Codec == "" {
		t.Fatal("browser_stream_init carried an empty codec — hardcoded/stub-shaped response")
	}

	const wantVideoChunks = 8
	deadline := time.After(30 * time.Second)
	var chunks []videoChunkSample

collectLoop:
	for len(chunks) < wantVideoChunks {
		select {
		case item := <-wc.sendCh:
			if item == nil || !item.Binary {
				continue // ignore keepalive/other text frames
			}
			raw := item.Data
			if len(raw) < 18 {
				t.Fatalf("binary chunk shorter than the 18-byte envelope: %d bytes", len(raw))
			}
			seq := binary.BigEndian.Uint32(raw[0:4])
			ts := binary.BigEndian.Uint64(raw[4:12])
			key := raw[12] == 1
			kind := raw[13]
			declaredLen := binary.BigEndian.Uint32(raw[14:18])
			payload := raw[18:]
			if uint32(len(payload)) != declaredLen {
				t.Fatalf("envelope declared len %d != actual payload len %d", declaredLen, len(payload))
			}
			if len(payload) == 0 {
				t.Fatal("chunk envelope carried an empty payload")
			}
			switch kind {
			case 1: // audio (only if HasAudio negotiated) — logged, not part of the video assertion
				t.Logf("CHUNK(audio): seq=%d ts=%d payload_bytes=%d", seq, ts, len(payload))
				continue
			case 0: // video
			default:
				t.Fatalf("chunk envelope carried an unrecognized kind byte %d (want 0=video or 1=audio)", kind)
			}
			if len(chunks) > 0 && seq <= chunks[len(chunks)-1].seq {
				t.Fatalf("video seq did not strictly advance (prev=%d cur=%d)", chunks[len(chunks)-1].seq, seq)
			}
			t.Logf("CHUNK %d(video): seq=%d ts=%d key=%v payload_bytes=%d", len(chunks), seq, ts, key, len(payload))
			chunks = append(chunks, videoChunkSample{seq: seq, ts: ts, key: key, payload: payload})
		case <-deadline:
			break collectLoop
		}
	}

	if len(chunks) == 0 {
		t.Fatal(
			"BLOCKED: no browser_video_chunk frames arrived within 30s — the real pipeline produced zero video chunks",
		)
	}
	if !chunks[0].key {
		t.Fatalf(
			"expected the FIRST video chunk to be a keyframe (key=1), got key=%v",
			chunks[0].key,
		)
	}
	if len(chunks) < 2 {
		t.Fatalf(
			"expected multiple video chunks (keyframe + deltas) within 30s, got only %d",
			len(chunks),
		)
	}
	sawDelta := false
	for _, c := range chunks[1:] {
		if !c.key {
			sawDelta = true
			break
		}
	}
	if !sawDelta {
		t.Fatalf(
			"expected at least one delta (key=0) chunk following the initial keyframe among %d chunks — "+
				"every chunk was flagged a keyframe, which is not real encoder behavior at the default "+
				"60-frame keyframe interval and would indicate a stub/hardcoded key flag",
			len(chunks),
		)
	}

	// Differentiation (anti-stub): successive payloads over a genuinely
	// animated page must not all be byte-identical — this is what catches a
	// frozen/no-op capture or a hardcoded encoder output.
	distinct := map[string]struct{}{}
	for _, c := range chunks {
		distinct[string(c.payload)] = struct{}{}
	}
	if len(distinct) < 2 {
		t.Fatalf(
			"all %d video chunk payloads were byte-identical over an animated page — capture/encoder "+
				"appears frozen or stubbed (real motion MUST produce varying encoded output)",
			len(chunks),
		)
	}
	t.Logf(
		"MOTION: %d distinct payloads among %d video chunks — real, varying encoded content",
		len(distinct),
		len(chunks),
	)

	// ---- STEP 5: Detach, then orch.Shutdown() — assert clean teardown of BOTH the stream and the encoder process ----
	streamID := handle.streamID
	var encTabDone <-chan struct{}
	if st := orch.streamByID(streamID); st != nil && st.encoderTab != nil {
		encTabDone = st.encoderTab.Done()
	}

	handle.Detach()

	eventually(t, 5*time.Second, func() bool { return orch.streamByID(streamID) == nil })
	t.Logf("TEARDOWN: stream %q removed from orchestrator state after Detach", streamID)

	if encTabDone != nil {
		select {
		case <-encTabDone:
			t.Log("TEARDOWN: encoder tab Done() fired — no orphan encoder tab/target")
		case <-time.After(5 * time.Second):
			t.Fatal(
				"encoder tab Done() did not fire within 5s of teardown — possible orphan encoder tab/Chrome target",
			)
		}
	} else {
		t.Log("TEARDOWN: no encoder tab handle captured before Detach (stream already gone) — skipping Done() check")
	}

	// orch.Shutdown() tears down the dedicated encoder browser process itself
	// (encoderBrowser.close, via its cdppipe CancelFunc) — assert the real OS
	// process captured above genuinely exits, not just that the in-process
	// bookkeeping was cleared.
	orch.Shutdown()
	eventually(t, 5*time.Second, func() bool { return !processAlive(encoderPID) })
	t.Logf("TEARDOWN: encoder Chrome process (pid=%d) confirmed gone after orch.Shutdown()", encoderPID)
}

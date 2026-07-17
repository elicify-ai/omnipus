//go:build browservideo_e2e

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// live_video_pipeline_e2e_test.go — REAL-HARDWARE end-to-end proof for the
// live-browser video pipeline (ADR-044 Amendment: single full-Chrome,
// encoder-as-tab; docs/internal/specs/single-chrome-video-blueprint.md).
// Runs ONLY under the browservideo_e2e build tag on a host that can run real
// Chrome (e.g. the ci-omnipus worker). NEVER in the default suite or the dev
// pod.
//
// Single-Chrome collapse: there is no dedicated, orchestrator-owned encoder
// Chrome process anymore. The coordinator's ONE full-Chrome-headless process
// hosts BOTH the per-agent browsing tabs (each in its own named browser
// context, ADR-043) AND the WebCodecs encoder page, which the orchestrator
// launches as a TAB in that same process's DEFAULT browser context
// (browser_stream.go's coordinatorRoot seam / launchEncoderTab, sourced from
// browser.BrowserCoordinator.RootContext()). There is no virtual-display or
// audio sidecar anywhere in this path: full Chrome renders the encoder page
// offscreen via plain --headless + SwiftShader (see
// pkg/tools/browser/exec_resolver.go's chromeHardeningBaseFlags doc). Audio
// capture is deferred to phase 2 (ADR-044) — HasAudio is always false in
// this increment.
//
// PIPELINE DEPTH ACHIEVED: FULL. This test lives in package gateway
// specifically so it can construct the REAL gateway.BrowserVideoOrchestrator
// via RegisterBrowserVideo, wired with EVERY seam left at its production
// default (real browser.StartCapture, real browser.LaunchEncoderPage, real
// loopbackEncoderServer, a real loopback HTTP server hosting the real
// CaptureIngestHandler) plus a real Coordinator seam sourced from a real
// *browser.BrowserCoordinator — mirroring setupAndStartServices' construction
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
// agent Chrome tab" is the real production path. The encoder tab itself is
// launched by the REAL orchestrator (browser.LaunchEncoderPage, via
// launchEncoderTab, rooted at coordinator.RootContext()) — this test never
// fakes or pre-launches it; the first AttachViewer call below is what
// triggers its lazy cold-start, exactly as in production. The Coordinator
// seam this test wires (BrowserVideoDeps.Coordinator) is the same closure
// shape gateway.go wires in production (pkg/gateway/gateway.go, "Coordinator:"
// field at the RegisterBrowserVideo call site).

package gateway

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
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

	"github.com/elicify-ai/omnipus/pkg/api/generated"
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

// staticLabelPageDataURL returns a tiny self-contained data: URL whose
// <title> is the given label — used to prove a SECOND agent's own tab
// context is independently navigable/functional while a first agent's video
// stream (encoder tab) is concurrently live in the same shared Chrome
// process (the single-Chrome-collapse isolation proof).
func staticLabelPageDataURL(label string) string {
	html := fmt.Sprintf(`<!doctype html><html><head><title>%s</title></head><body>%s</body></html>`, label, label)
	return "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))
}

// procEntry is one /proc-derived (pid, ppid) pair — the minimal shape needed
// to walk the real OS process tree. Shared by findProcessCmdlineContaining
// and rogueChromeProcesses below.
type procEntry struct{ pid, ppid int }

// listProcEntries enumerates every live (pid, ppid) pair via /proc. Best-
// effort: a read failure for the directory as a whole yields nil (callers
// treat that as "nothing found", never a hard error) — this is corroboration
// tooling, not the sole proof of anything. Linux-only (/proc); this whole
// file only ever runs on the ci-omnipus worker.
func listProcEntries() []procEntry {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var all []procEntry
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
		all = append(all, procEntry{pid: pid, ppid: ppid})
	}
	return all
}

// chromeCmdline returns pid's /proc cmdline (space-joined) iff it contains
// "chrom" (case-insensitive) — i.e. iff pid looks like a Chrome/Chromium
// process of any kind (browser, zygote, renderer, gpu, …).
func chromeCmdline(pid int) (string, bool) {
	cmdlineBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "", false
	}
	cmdline := strings.TrimSpace(strings.ReplaceAll(string(cmdlineBytes), "\x00", " "))
	if cmdline == "" || !strings.Contains(strings.ToLower(cmdline), "chrom") {
		return "", false
	}
	return cmdline, true
}

// findProcessCmdlineContaining walks the /proc process tree rooted at rootPID
// (inclusive) and returns the first process whose /proc/<pid>/cmdline
// contains needle (case-insensitive), space-joined for readability. Best-
// effort: any read failure just means that pid is skipped, never a hard
// error — this is corroboration, not the sole proof.
func findProcessCmdlineContaining(rootPID int, needle string) (string, bool) {
	needle = strings.ToLower(needle)
	all := listProcEntries()

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

// ancestryReaches reports whether pid's ancestor chain (via ppid, walking up
// through the process tree described by all) reaches target before running
// out of known ancestors or hitting pid 1 (init/reaper). Bounded to 200 hops
// so a corrupt/cyclic ppid chain can never spin forever.
func ancestryReaches(pid, target int, all []procEntry) bool {
	byPID := make(map[int]int, len(all))
	for _, p := range all {
		byPID[p.pid] = p.ppid
	}
	cur := pid
	for i := 0; i < 200; i++ {
		if cur == target {
			return true
		}
		ppid, ok := byPID[cur]
		if !ok || ppid <= 1 {
			return cur == target
		}
		cur = ppid
	}
	return false
}

// rogueChromeProcesses returns every live process whose cmdline looks like
// Chrome/Chromium but whose ancestry does NOT pass through rootPID — i.e. a
// Chrome process tree that is NOT part of the coordinator's own shared
// Chrome. Single-Chrome collapse (ADR-044 Amendment): the encoder tab is a
// CDP-level target inside the coordinator's existing process, never a
// separately-spawned Chrome — so a non-empty result here is exactly the
// two-Chrome regression this wave eliminated. Best-effort (see
// listProcEntries' doc comment); an empty /proc read yields an empty
// (non-failing) result rather than a false positive.
func rogueChromeProcesses(rootPID int) []string {
	all := listProcEntries()
	var rogue []string
	for _, p := range all {
		if p.pid == rootPID {
			continue
		}
		cmdline, ok := chromeCmdline(p.pid)
		if !ok {
			continue
		}
		if !ancestryReaches(p.pid, rootPID, all) {
			rogue = append(rogue, fmt.Sprintf("pid=%d ppid=%d cmdline=%s", p.pid, p.ppid, cmdline))
		}
	}
	return rogue
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

// assertCoordinatorChromeIsFullBuild corroborates, via the real /proc process
// tree, that the coordinator's ONE shared Chrome (rootPid = coordinator.PID())
// is running the FULL Chrome build in headless mode — the single-Chrome
// collapse (ADR-044 Amendment) invariant that the coordinator's shared Chrome
// process (hosting every agent's browsing tabs AND, on the video-attach path,
// the encoder tab) is full Chrome, never chrome-headless-shell (which has no
// WebCodecs VideoEncoder at all — the whole reason the two-Chrome topology
// existed before this wave). The deterministic guarantee lives in source
// (pkg/tools/browser/installer.go: selectDownloadBuild now returns
// fullChromeBuild(); there is no video-capable branch on the agent launch
// path anymore); this function is corroboration via a genuinely independent
// observation channel, not the sole proof.
func assertCoordinatorChromeIsFullBuild(t *testing.T, rootPid int) {
	t.Helper()
	if rootPid <= 0 {
		t.Fatalf("coordinator.PID() returned %d — no live Chrome process to inspect", rootPid)
	}
	cmdline, found := findProcessCmdlineContaining(rootPid, "chrom")
	if !found {
		t.Fatalf("could not locate a chrome[ium] process under pid %d via /proc", rootPid)
	}
	t.Logf("COORDINATOR CHROME ARGS: %s", cmdline)
	if !strings.Contains(cmdline, "--headless") {
		t.Fatalf(
			"expected the coordinator's shared Chrome to run --headless, got cmdline: %s",
			cmdline,
		)
	}
	if strings.Contains(strings.ToLower(cmdline), "headless-shell") {
		t.Fatalf(
			"expected the coordinator's shared Chrome to be the FULL chrome build (single-Chrome collapse — "+
				"it must be able to host the WebCodecs encoder tab), got chrome-headless-shell: %s",
			cmdline,
		)
	}
}

// TestLiveVideoPipeline_RealChrome_EmitsChunks is the full-depth real-hardware
// proof for the single-Chrome collapse (ADR-044 Amendment): a real full-Chrome
// process launched by the real BrowserCoordinator for the agent tab, the REAL
// gateway.BrowserVideoOrchestrator (every seam at its production default,
// Coordinator wired to the real coordinator's RootContext) wired against a
// real loopback capture-ingest HTTP server, a real AttachViewer that lazily
// cold-starts the encoder page as a TAB inside that SAME coordinator Chrome
// process (never a second process), and assertions against genuine binary
// browser_video_chunk envelopes read off a real *browserWSConn sink — then a
// real Detach + Shutdown teardown check. This is what catches the class of
// bug hermetic tests with fakes cannot: the round-1 CDP dispatch-goroutine
// deadlock (capture.go's ADR-038 DEADLOCK POSTMORTEM comment) only ever
// manifested here, against real Chrome's real CDP event delivery.
func TestLiveVideoPipeline_RealChrome_EmitsChunks(t *testing.T) {
	homeDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// ---- STEP 1: real full-Chrome install + DS-5 capability classification ----
	// Single-Chrome collapse: ClassifyVideoCapability is satisfied by linux +
	// a full-Chrome build already present under installRoot alone. That SAME
	// full-Chrome build is now also what the coordinator resolves as the
	// AGENT's own binary (installer.go's selectDownloadBuild ->
	// fullChromeBuild()) — there is no separate, non-interchangeable
	// headless-shell build for the agent anymore. Pre-fetching it here mirrors
	// the boot-time prefetch recommendation (blueprint §1.6) and lets STEP 2's
	// coordinator launch resolve the binary that is already on disk.
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

	// ---- STEP 2: real BrowserCoordinator + BrowserManager — the agent's Chrome IS the coordinator's shared full Chrome ----
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
		browser.BrowserConfig{
			Enabled:     true,
			ProfileDir:  profileDir,
			MaxTabs:     5, // must be >0 — the manager's own tab cap gates mgr.Session
			PageTimeout: 30 * time.Second,
		},
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
	coordinatorPID := coordinator.PID()
	t.Logf("CHROME LAUNCH: coordinator pid=%d", coordinatorPID)
	assertCoordinatorChromeIsFullBuild(t, coordinatorPID)

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
	// (pkg/gateway/gateway.go), EXCEPT Coordinator, which this test wires
	// explicitly (gateway.go wires the same shape off agentLoop.BrowserCoordinator()
	// in production; this test has no *agent.AgentLoop, so it wires the real
	// *browser.BrowserCoordinator constructed in STEP 2 directly). Nothing here
	// is a fake — including the encoder tab itself, which the first
	// AttachViewer call below launches lazily as a tab of coordinator's shared
	// Chrome via the real launchEncoderTab -> browser.LaunchEncoderPage path.
	orch := RegisterBrowserVideo(ingestMux, BrowserVideoDeps{
		InstallRoot: installRoot,
		Config: BrowserVideoConfig{
			IngestWSURL:     ingestWSURL,
			LivenessTimeout: 30 * time.Second,
			Enabled:         &enabled,
		},
		Coordinator: func() (context.Context, bool) { return coordinator.RootContext() },
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

	// ---- STEP 3 cont'd: the encoder tab is a TAB in the coordinator's Chrome — NOT a second process ----
	// Single-Chrome collapse (ADR-044 Amendment): AttachViewer's first call
	// lazily launched the encoder page as a default-context CDP target inside
	// coordinator.RootContext() (launchEncoderTab -> browser.LaunchEncoderPage)
	// — no process-spawn code path is reachable from that call at all anymore.
	// Assert the invariant directly, via two independent real-process
	// observations: (a) the coordinator's Chrome pid is UNCHANGED (no
	// relaunch/substitution happened to host the encoder), and (b) no
	// "chrom"-cmdline process exists anywhere on the host outside the
	// coordinator's own process tree — i.e. the encoder tab's browser PID IS
	// the coordinator's Chrome PID, by construction (there is no other Chrome
	// for it to be).
	if got := coordinator.PID(); got != coordinatorPID {
		t.Fatalf(
			"coordinator Chrome pid changed after the encoder tab launched (before=%d after=%d) — a "+
				"relaunch/substitution would mean the encoder tab is no longer hosted by the SAME Chrome "+
				"the agent tab runs in",
			coordinatorPID, got,
		)
	}
	if rogue := rogueChromeProcesses(coordinatorPID); len(rogue) > 0 {
		t.Fatalf(
			"found %d chrome-cmdline process(es) OUTSIDE the coordinator's own process tree after the "+
				"encoder tab launched — this is exactly the two-Chrome regression the single-Chrome collapse "+
				"eliminated: %v",
			len(rogue), rogue,
		)
	}
	t.Logf(
		"ENCODER: encoder tab hosted inside the coordinator's shared Chrome (pid=%d) — no second Chrome process exists",
		coordinatorPID,
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

	// ---- STEP 5: Detach, then orch.Shutdown() — assert clean teardown of the STREAM, with the Chrome process surviving ----
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
				"encoder tab Done() did not fire within 5s of teardown — possible orphan encoder tab/target",
			)
		}
	} else {
		t.Log("TEARDOWN: no encoder tab handle captured before Detach (stream already gone) — skipping Done() check")
	}

	// orch.Shutdown() tears down every active stream (closing the encoder
	// tab) but — single-Chrome collapse (ADR-044 Amendment) — the
	// orchestrator no longer owns or kills any Chrome process itself; the
	// coordinator's shared Chrome MUST still be alive afterward (it hosts the
	// agent's own browsing tab too, so tearing it down on every
	// browser-video Shutdown would be a severe regression against every
	// other in-flight browser tool use).
	orch.Shutdown()
	if !processAlive(coordinatorPID) {
		t.Fatal(
			"orch.Shutdown() killed the coordinator's shared Chrome process — single-Chrome collapse means " +
				"the video orchestrator must never own Chrome process lifecycle",
		)
	}
	t.Logf("TEARDOWN: coordinator Chrome (pid=%d) still alive after orch.Shutdown() — correct, it is not process-owning", coordinatorPID)

	// The coordinator's OWN Shutdown is the SOLE process-kill path (FR-008,
	// coordinator.go). Call it explicitly (t.Cleanup registered one earlier
	// in STEP 2 too — Shutdown is documented idempotent) so this test itself
	// observes the real process exit rather than trusting cleanup ordering.
	coordinator.Shutdown()
	eventually(t, 5*time.Second, func() bool { return !processAlive(coordinatorPID) })
	t.Logf("TEARDOWN: coordinator Chrome (pid=%d) confirmed gone after coordinator.Shutdown()", coordinatorPID)
}

// TestSingleChromeCoordinator_HostsAgentContextsAndEncoderTab_Concurrently is
// the wave's behavioral gate (docs/internal/specs/single-chrome-video-blueprint.md
// §3 Unit H DoD + "Review/UAT" section, [FACT-1]): it proves ONE coordinator
// full-Chrome process hosts MULTIPLE per-agent browser contexts (ADR-043
// isolation) AND a default-context encoder tab (ADR-044 Amendment)
// CONCURRENTLY and on the coordinator's REAL launch — not just structurally
// (TestLiveVideoPipeline_RealChrome_EmitsChunks proves the encoder-tab side
// alone with a single agent) but with a second, independent agent actively
// browsing at the same time the first agent's video stream is live.
//
// Coverage this adds beyond TestLiveVideoPipeline_RealChrome_EmitsChunks:
//   - TWO agents registered with the coordinator (two distinct named browser
//     contexts) rather than one.
//   - Agent B's tab is exercised (navigated, its <title> read back) WHILE
//     agent A's video stream (encoder tab) is actively producing chunks —
//     proving the encoder tab's presence in the default context does not
//     perturb a concurrently-live named-context agent tab, and vice versa.
//   - The "no rogue Chrome process" and "single coordinator pid" invariants
//     are re-checked with N=2 agent contexts live, not just N=1.
func TestSingleChromeCoordinator_HostsAgentContextsAndEncoderTab_Concurrently(t *testing.T) {
	homeDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	installRoot := os.Getenv("DIAG_INSTALL_ROOT")
	if installRoot == "" {
		installRoot = filepath.Join(homeDir, "chromium")
	}
	if _, ensureErr := browser.EnsureChromiumFullBuild(ctx, installRoot); ensureErr != nil {
		t.Fatalf("BLOCKED: EnsureChromiumFullBuild(%q): %v", installRoot, ensureErr)
	}
	capability := browser.ClassifyVideoCapability(installRoot)
	if !capability.Capable {
		t.Fatalf("BLOCKED: host not classified video-capable — reason: %s", capability.Reason)
	}

	// ---- ONE coordinator, TWO agents ----
	profileDir := filepath.Join(homeDir, "browser-profile")
	coordinator := browser.NewBrowserCoordinator(homeDir, browser.BrowserConfig{
		Enabled:     true,
		ProfileDir:  profileDir,
		MaxTabs:     5,
		PageTimeout: 30 * time.Second,
	}, 10)
	t.Cleanup(coordinator.Shutdown)

	ssrf := security.NewSSRFChecker(nil)
	newAgentMgr := func(agentID string) *browser.BrowserManager {
		t.Helper()
		mgr, mgrErr := browser.NewBrowserManager(
			browser.BrowserConfig{
				Enabled:     true,
				ProfileDir:  profileDir,
				MaxTabs:     5,
				PageTimeout: 30 * time.Second,
			},
			ssrf,
		)
		if mgrErr != nil {
			t.Fatalf("NewBrowserManager(%s): %v", agentID, mgrErr)
		}
		mgr.AttachSharedChrome(coordinator, agentID)
		return mgr
	}

	const agentA = "e2e-agent-a"
	const agentB = "e2e-agent-b"
	mgrA := newAgentMgr(agentA)
	mgrB := newAgentMgr(agentB)

	tabA, errA := mgrA.Session("sess-a")
	if errA != nil {
		t.Fatalf("mgrA.Session (agent A's coordinator-mediated tab): %v", errA)
	}
	coordinatorPID := coordinator.PID()
	assertCoordinatorChromeIsFullBuild(t, coordinatorPID)

	tabB, errB := mgrB.Session("sess-b")
	if errB != nil {
		t.Fatalf("mgrB.Session (agent B's coordinator-mediated tab): %v", errB)
	}
	// A second Register must NOT have relaunched/replaced Chrome — one
	// process still backs both agents (ADR-043's whole premise).
	if got := coordinator.PID(); got != coordinatorPID {
		t.Fatalf("registering agent B changed the coordinator Chrome pid (before=%d after=%d) — expected ONE shared process", coordinatorPID, got)
	}
	registered := coordinator.RegisteredAgents()
	if len(registered) != 2 {
		t.Fatalf("expected 2 registered agents (distinct browser contexts), got %d: %v", len(registered), registered)
	}
	t.Logf("REGISTER: coordinator pid=%d hosts %d agent(s): %v", coordinatorPID, len(registered), registered)

	motionURL := motionPageDataURL()
	if navErr := chromedp.Run(tabA, chromedp.Navigate(motionURL)); navErr != nil {
		t.Fatalf("navigate agent A's tab to the motion page: %v", navErr)
	}
	const agentBLabel = "agent-b-independent-tab"
	if navErr := chromedp.Run(tabB, chromedp.Navigate(staticLabelPageDataURL(agentBLabel))); navErr != nil {
		t.Fatalf("navigate agent B's tab to its own static page: %v", navErr)
	}
	var titleBeforeStream string
	if evalErr := chromedp.Run(tabB, chromedp.Title(&titleBeforeStream)); evalErr != nil {
		t.Fatalf("read agent B's tab title before agent A's stream starts: %v", evalErr)
	}
	if titleBeforeStream != agentBLabel {
		t.Fatalf("agent B's tab title = %q, want %q — agent B's own context is not behaving independently", titleBeforeStream, agentBLabel)
	}

	// ---- REAL orchestrator, wired to the SAME coordinator ----
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

	enabled := true
	orch := RegisterBrowserVideo(ingestMux, BrowserVideoDeps{
		InstallRoot: installRoot,
		Config: BrowserVideoConfig{
			IngestWSURL:     ingestWSURL,
			LivenessTimeout: 30 * time.Second,
			Enabled:         &enabled,
		},
		Coordinator: func() (context.Context, bool) { return coordinator.RootContext() },
	})
	t.Cleanup(orch.Shutdown)

	// ---- Attach a viewer for agent A ONLY — this cold-starts the encoder tab in the DEFAULT context ----
	wc := newTestVideoConn()
	handle, attachErr := orch.AttachViewer(AttachParams{
		WC:        wc,
		AgentID:   agentA,
		SessionID: "sess-a",
		ViewerID:  "viewer-a",
		AgentCtx:  tabA,
		VideoCaps: []string{"avc1.4D4028", "vp8"},
	})
	if attachErr != nil {
		t.Fatalf("AttachViewer for agent A: %v", attachErr)
	}
	if handle == nil {
		f := findStatusError(t, wc)
		msg := ""
		if f.Message != nil {
			msg = *f.Message
		}
		t.Fatalf("BLOCKED: AttachViewer produced no stream for agent A — message=%q", msg)
	}
	defer handle.Detach()

	// [FACT-1] combination, on the real coordinator launch: N (=2) per-agent
	// browser contexts AND a default-context encoder tab coexist in the ONE
	// coordinator process. Re-check the process-identity invariants with
	// agent A's encoder tab now live.
	if got := coordinator.PID(); got != coordinatorPID {
		t.Fatalf("coordinator Chrome pid changed after the encoder tab launched (before=%d after=%d)", coordinatorPID, got)
	}
	if rogue := rogueChromeProcesses(coordinatorPID); len(rogue) > 0 {
		t.Fatalf("found %d chrome-cmdline process(es) outside the coordinator's own tree with 2 agent contexts + an encoder tab live: %v", len(rogue), rogue)
	}
	if got := len(coordinator.RegisteredAgents()); got != 2 {
		t.Fatalf("agent context count changed once the encoder tab launched: got %d, want 2 — the encoder's default-context tab must not disturb per-agent context isolation", got)
	}

	// ---- Prove agent A's stream is genuinely producing video WHILE agent B's tab is independently exercised ----
	drainStreamInit(t, wc)

	// Interleave: pull a couple of real video chunks for A, then drive B's
	// tab, then pull a couple more chunks for A — proving both stay live and
	// independent of each other concurrently, not just at two disjoint
	// instants.
	firstChunk := nextBinaryChunk(t, wc, 15*time.Second)
	if len(firstChunk) < 18 {
		t.Fatalf("agent A's first chunk shorter than the 18-byte envelope: %d bytes", len(firstChunk))
	}

	const agentBLabel2 = "agent-b-still-independent"
	if navErr := chromedp.Run(tabB, chromedp.Navigate(staticLabelPageDataURL(agentBLabel2))); navErr != nil {
		t.Fatalf("re-navigate agent B's tab WHILE agent A's stream is live: %v", navErr)
	}
	var titleDuringStream string
	if evalErr := chromedp.Run(tabB, chromedp.Title(&titleDuringStream)); evalErr != nil {
		t.Fatalf("read agent B's tab title while agent A's stream is live: %v", evalErr)
	}
	if titleDuringStream != agentBLabel2 {
		t.Fatalf("agent B's tab title = %q, want %q — agent B's tab was disturbed by agent A's concurrently-live encoder tab", titleDuringStream, agentBLabel2)
	}
	t.Logf("ISOLATION: agent B's own browser context navigated + read back correctly while agent A's encoder tab was live")

	secondChunk := nextBinaryChunk(t, wc, 15*time.Second)
	if string(secondChunk) == string(firstChunk) {
		t.Fatal("agent A's video chunks were byte-identical across the interleaved window — capture/encoder appears frozen or stubbed")
	}
	t.Logf("STREAM: agent A produced distinct video chunks before and after agent B's tab activity — genuinely concurrent, not serialized/stubbed")

	// Final re-check: pid stable, no rogue process, both contexts still
	// registered, after the full interleaved sequence.
	if got := coordinator.PID(); got != coordinatorPID {
		t.Fatalf("coordinator Chrome pid changed by end of test (before=%d after=%d)", coordinatorPID, got)
	}
	if rogue := rogueChromeProcesses(coordinatorPID); len(rogue) > 0 {
		t.Fatalf("found %d chrome-cmdline process(es) outside the coordinator's own tree at test end: %v", len(rogue), rogue)
	}
	if got := len(coordinator.RegisteredAgents()); got != 2 {
		t.Fatalf("final agent context count = %d, want 2", got)
	}
	t.Logf(
		"[FACT-1] CONFIRMED: coordinator pid=%d hosts 2 per-agent browser contexts + 1 default-context encoder tab concurrently, no rogue Chrome process",
		coordinatorPID,
	)
}

// staticSettledPageDataURL returns a self-contained data: URL for a page that
// paints ONCE on load and then NEVER repaints again — no <canvas>, no
// requestAnimationFrame, no timers, nothing that would produce a second CDP
// Page.screencastFrame event. This is deliberately the adversarial case for
// the encoder page's idle-page keepalive (encoder.html's
// KEEPALIVE_INTERVAL_MS / scheduleKeepalive): a settled article/search-
// results/form page — the overwhelming majority of real agent browsing —
// looks EXACTLY like this to the CDP screencast capture driver.
func staticSettledPageDataURL() string {
	const html = `<!doctype html><html><head><title>Settled Static Page</title></head>` +
		`<body style="margin:0;background:#123456"><h1>This page never repaints.</h1></body></html>`
	return "data:text/html;base64," + base64.StdEncoding.EncodeToString([]byte(html))
}

// TestLiveVideoPipeline_RealChrome_StaticPageSurvivesLivenessTimeout is the
// real-hardware regression proof for the static-page keepalive fix
// (encoder.html's scheduleKeepalive/encodeAndSendFrame, added alongside the
// Chrome 151 hidden-tab manager.go createTab fix). Before this fix: CDP
// Page.startScreencast fires exactly ONE Page.screencastFrame event for a
// settled page (the initial paint) — handleFrameFeed encodes exactly ONE
// chunk, then the encoder goes silent forever, and browser_stream.go's
// chunk-driven mid-stream liveness timer (noteChunk/onLivenessTimeout) fails
// the stream LivenessTimeout after it settles, even though the encoder, its
// ingest connection, and the capture driver are all perfectly healthy — the
// source is just idle. This test uses a SHORT LivenessTimeout (well under
// KEEPALIVE_INTERVAL_MS's multiples) and asserts the stream survives several
// multiples of it, with real binary video chunks continuing to arrive at
// roughly the keepalive cadence, on a page that never repaints.
func TestLiveVideoPipeline_RealChrome_StaticPageSurvivesLivenessTimeout(t *testing.T) {
	homeDir := t.TempDir()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

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
	if !capability.Capable {
		t.Fatalf("BLOCKED: host not classified video-capable — reason: %s", capability.Reason)
	}

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
		browser.BrowserConfig{
			Enabled:     true,
			ProfileDir:  profileDir,
			MaxTabs:     5,
			PageTimeout: 30 * time.Second,
		},
		ssrf,
	)
	if mgrErr != nil {
		t.Fatalf("NewBrowserManager: %v", mgrErr)
	}
	const agentID = "e2e-static-agent"
	mgr.AttachSharedChrome(coordinator, agentID)

	agentTabCtx, sessionErr := mgr.Session("e2e-static-session")
	if sessionErr != nil {
		t.Fatalf("mgr.Session (triggers the coordinator-mediated Chrome launch): %v", sessionErr)
	}
	coordinatorPID := coordinator.PID()
	assertCoordinatorChromeIsFullBuild(t, coordinatorPID)

	staticURL := staticSettledPageDataURL()
	if navErr := chromedp.Run(agentTabCtx, chromedp.Navigate(staticURL)); navErr != nil {
		t.Fatalf("navigate agent tab to the static page: %v", navErr)
	}
	t.Logf("PAGE: agent tab navigated to a %d-byte STATIC (never-repainting) data: URL", len(staticURL))

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

	enabled := true
	// LivenessTimeout deliberately short (well under a minute, comfortably
	// above encoder.html's KEEPALIVE_INTERVAL_MS=3s so a HEALTHY keepalive
	// cadence never trips it by design) — the whole point is to prove the
	// stream survives MULTIPLE liveness windows on a page that never
	// produces a real screencast frame after the first.
	const livenessTimeout = 6 * time.Second
	orch := RegisterBrowserVideo(ingestMux, BrowserVideoDeps{
		InstallRoot: installRoot,
		Config: BrowserVideoConfig{
			IngestWSURL:     ingestWSURL,
			LivenessTimeout: livenessTimeout,
			Enabled:         &enabled,
		},
		Coordinator: func() (context.Context, bool) { return coordinator.RootContext() },
	})
	t.Cleanup(orch.Shutdown)

	wc := newTestVideoConn()
	handle, attachErr := orch.AttachViewer(AttachParams{
		WC:        wc,
		AgentID:   agentID,
		SessionID: "e2e-static-session",
		ViewerID:  "e2e-static-viewer",
		AgentCtx:  agentTabCtx,
		VideoCaps: []string{"avc1.4D4028", "vp8"},
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
	streamID := handle.streamID
	t.Logf("ATTACH: stream %q started for agent=%q (livenessTimeout=%s)", streamID, agentID, livenessTimeout)

	initFrame := drainStreamInit(t, wc)
	if initFrame.Codec == "" {
		t.Fatal("browser_stream_init carried an empty codec — hardcoded/stub-shaped response")
	}

	// Collect binary video chunks for a window comfortably past several
	// multiples of livenessTimeout, tracking arrival timestamps so we can
	// assert BOTH "the stream never got marked failed" AND "chunks kept
	// arriving at roughly the keepalive cadence, not just the first one".
	const observeWindow = 4 * livenessTimeout // 24s, ~4x the liveness window
	observeStart := time.Now()
	deadline := time.After(observeWindow)
	var arrivals []time.Time
	var sawKeyframe, sawAnyBinary bool

collectLoop:
	for {
		select {
		case item := <-wc.sendCh:
			if item == nil {
				continue
			}
			if !item.Binary {
				// A text frame this late (other than the init frame already
				// drained above) would be a browser_status — most likely the
				// exact regression this test guards against: "error" state
				// after the liveness timer incorrectly fires.
				var f generated.BrowserStatusFrame
				if err := json.Unmarshal(item.Data, &f); err == nil && f.State == "error" {
					msg := ""
					if f.Message != nil {
						msg = *f.Message
					}
					t.Fatalf(
						"REGRESSION: received browser_status(error) at t+%s — stream was marked failed on a "+
							"static page despite the keepalive fix; message=%q",
						time.Since(observeStart).Truncate(time.Millisecond), msg,
					)
				}
				continue
			}
			sawAnyBinary = true
			raw := item.Data
			if len(raw) < 18 {
				t.Fatalf("binary chunk shorter than the 18-byte envelope: %d bytes", len(raw))
			}
			key := raw[12] == 1
			kind := raw[13]
			if kind == 0 && key {
				sawKeyframe = true
			}
			arrivals = append(arrivals, time.Now())
			t.Logf("CHUNK %d: t+%s key=%v kind=%d payload_bytes=%d",
				len(arrivals)-1,
				time.Since(observeStart).Truncate(time.Millisecond),
				key, kind, len(raw)-18,
			)
		case <-deadline:
			break collectLoop
		}
	}

	if !sawAnyBinary {
		t.Fatal("BLOCKED: no binary video chunk arrived at all — even the initial keyframe never made it through")
	}
	if !sawKeyframe {
		t.Fatal("expected at least one video keyframe chunk (the initial paint) among the collected chunks")
	}

	// The REGRESSION this test targets: on a static page, without the
	// keepalive, exactly ONE chunk would ever arrive (the initial paint) and
	// the stream would be marked failed at t+livenessTimeout. Assert we
	// collected chunks spanning WELL PAST livenessTimeout — proof the
	// keepalive kept real traffic flowing on the ingest connection long after
	// the page stopped producing genuine screencast frames.
	if len(arrivals) < 2 {
		t.Fatalf(
			"REGRESSION: only %d chunk(s) arrived over %s (>%dx the %s liveness window) — "+
				"the static-page keepalive did not produce any follow-up chunks",
			len(arrivals), observeWindow, int(observeWindow/livenessTimeout), livenessTimeout,
		)
	}
	lastArrivalOffset := arrivals[len(arrivals)-1].Sub(arrivals[0])
	if lastArrivalOffset < 2*livenessTimeout {
		t.Fatalf(
			"REGRESSION: last chunk arrived only %s after the first — expected keepalive traffic to span "+
				"well past 2x the %s liveness window (observed over %s)",
			lastArrivalOffset.Truncate(time.Millisecond), livenessTimeout, observeWindow,
		)
	}
	t.Logf(
		"KEEPALIVE: %d chunks spanning %s on a page that never repaints — stream survived %d+ liveness windows (%s each)",
		len(arrivals), lastArrivalOffset.Truncate(time.Millisecond), int(lastArrivalOffset/livenessTimeout), livenessTimeout,
	)

	// Positive confirmation the orchestrator itself never marked the stream
	// failed (belt-and-suspenders alongside the browser_status(error) check
	// in the collect loop above, which would have already failed the test).
	if st := orch.streamByID(streamID); st == nil {
		t.Fatal("REGRESSION: stream was removed from the orchestrator (torn down) during the observation window")
	} else if orch.streamFailed(st) {
		t.Fatal("REGRESSION: orch.streamFailed(st) == true after the observation window — liveness timeout fired")
	}
	t.Log("LIVENESS: stream still present and not marked failed after the full observation window")

	// ---- teardown ----
	handle.Detach()
	eventually(t, 5*time.Second, func() bool { return orch.streamByID(streamID) == nil })
	orch.Shutdown()
	coordinator.Shutdown()
	eventually(t, 5*time.Second, func() bool { return !processAlive(coordinatorPID) })
	t.Logf("TEARDOWN: clean shutdown, coordinator Chrome (pid=%d) confirmed gone", coordinatorPID)
}

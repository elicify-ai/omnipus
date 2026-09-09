package captureext

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// init registers 1.0.17's content hash from THIS file rather than from
// captureext_test.go's versionContentHashes literal, following the same
// deliberate, temporary accommodation the 1.0.8 entry documents: two agents
// are editing this package in parallel with a strict file-ownership split,
// and encoder.js (mine) and captureext_test.go (not mine) would otherwise
// have to be touched in the same change. Fold this entry into the literal at
// merge, where the per-version history belongs — exactly as 1.0.8's note asks.
//
// 1.0.17 — a server-initiated recapture no longer tears down a healthy
// capture when the geometry it asks for is the geometry already running. The
// gateway issues one on EVERY panel open (browser_webrtc.go's
// applyColdStartRecapture), so the standard "open the live panel" path was
// destroying a working PeerConnection and renegotiating a new one at the
// exact moment somebody started watching.
func init() {
	versionContentHashes["1.0.17"] = "a38a7f7ee336657fb970d0f1d5a853c78a9818354e7ff60b76ae755979e84491"
}

// TestEncoderJS_RecaptureSkipGuards is the no-node backstop: it pins the
// SHAPE of the fix into the embedded asset, so a merge that resurrects the
// unconditional teardown fails here even on a machine with no JS runtime.
// The behavioural proof is TestEncoderJS_RecaptureSkipsUnchangedGeometry.
func TestEncoderJS_RecaptureSkipGuards(t *testing.T) {
	src := embeddedEncoderJS(t)

	// The decision must exist and must be consulted from the recapture
	// control handler — a decision function nobody calls is the classic
	// green-but-dead shape.
	if !strings.Contains(src, "function recaptureGeometryChangeReason(want, applied)") {
		t.Error("encoder.js: recaptureGeometryChangeReason must exist — it is the whole 'only rebuild if the " +
			"size really changed' decision")
	}
	if !strings.Contains(src, "const changeReason = recaptureGeometryChangeReason(") {
		t.Error("encoder.js: the recapture control handler must consult recaptureGeometryChangeReason before " +
			"rebuilding, or every panel open destroys a working stream again")
	}

	// The skip must be reachable: an early return on the no-change path.
	if !strings.Contains(src, "recapture: SKIPPED") {
		t.Error("encoder.js: the no-change path must return without rebuilding, and must say so in the " +
			"diagnostic history — an invisible optimisation cannot be verified in the field")
	}

	// The applied-vs-requested guard. Skipping on a value we merely ASKED for
	// is how a stale-geometry picture becomes permanent.
	if !strings.Contains(src, "track.getSettings()") {
		t.Error("encoder.js: appliedCaptureGeometry must read the running track's getSettings() — the pinned " +
			"size is what was requested, only the track reports what Chrome actually produced")
	}
	if !strings.Contains(src, "if (captureInFlight || !currentPC || !currentStream || !lastPinnedCapDims) return null;") {
		t.Error("encoder.js: appliedCaptureGeometry must report 'nothing running' on a cold start or during a " +
			"rebuild, so a needed capture is never skipped")
	}
	if !strings.Contains(src, "if (currentPC.connectionState !== 'connected') return null;") {
		t.Error("encoder.js: a capture whose PeerConnection is not connected is not a stream worth protecting — " +
			"the recapture may well BE the recovery")
	}

	// One tolerance, shared with the two judgements this file already makes.
	if !strings.Contains(src, "const RECAPTURE_SAME_SIZE_TOLERANCE_CSS_PX = 8;") {
		t.Error("encoder.js: the same-size tolerance must stay 8 CSS px — the value captureActiveTabStream's " +
			"convergence poll and the post-connect self-heal already use for 'this is the same size'")
	}

	// The source-tab check: identical dimensions on a DIFFERENT tab must
	// still rebuild, or a tab switch leaves the viewer on the wrong page.
	if !strings.Contains(src, "activeTabId === applied.tabId") {
		t.Error("encoder.js: the skip must also require the active tab to still be the captured one — an " +
			"active-tab switch can land on a tab of identical size")
	}

	// The scale must be pinned WITH the capture, not read off the live
	// module variable a later control frame overwrites.
	if !strings.Contains(src, "lastPinnedCapDims = { w: capW, h: capH, scale: captureScale };") {
		t.Error("encoder.js: lastPinnedCapDims must record the scale the capture was actually built at")
	}
}

// TestEncoderJS_RecaptureSkipsUnchangedGeometry is the behavioural proof. It
// loads the embedded encoder.js into a Node vm context and drives the REAL
// handleControlFrame('recapture') against a fake-but-connected capture, with
// only the two browser-bound collaborators (captureActiveTabStream, which
// needs chrome.tabCapture, and newPeerConnection, which needs a WebRTC
// stack) replaced. teardownCapture — the destructive act this change is
// about — stays real, so "the stream survived" is asserted on the actual
// MediaStreamTrack objects, not on a mock's call count alone.
func TestEncoderJS_RecaptureSkipsUnchangedGeometry(t *testing.T) {
	node, lookErr := exec.LookPath("node")
	if lookErr != nil {
		if os.Getenv(encoderHarnessOptOutEnv) == "1" {
			t.Skipf("node not on PATH and %s=1: skipping the recapture-decision behavioural coverage", encoderHarnessOptOutEnv)
		}
		t.Fatalf("node is not on PATH, so the recapture-skip decision has NO behavioural coverage in this run. "+
			"Install node (CI and the Fly CI worker both have it), or set %s=1 to accept running without that "+
			"coverage. lookup error: %v", encoderHarnessOptOutEnv, lookErr)
	}

	dir := t.TempDir()
	encPath := filepath.Join(dir, "encoder.js")
	if err := os.WriteFile(encPath, []byte(embeddedEncoderJS(t)), 0o644); err != nil {
		t.Fatalf("write encoder.js: %v", err)
	}
	harnessPath := filepath.Join(dir, "recapture_harness.cjs")
	if err := os.WriteFile(harnessPath, []byte(recaptureHarnessJS), 0o644); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	out, runErr := exec.Command(node, harnessPath, encPath).CombinedOutput()
	if runErr != nil {
		t.Fatalf("node harness failed: %v\n%s", runErr, out)
	}
	var jsonLine string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "OMNIPUS_RESULTS ") {
			jsonLine = strings.TrimPrefix(line, "OMNIPUS_RESULTS ")
		}
	}
	if jsonLine == "" {
		t.Fatalf("harness produced no result line:\n%s", out)
	}
	var results []struct {
		Name   string `json:"name"`
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(jsonLine), &results); err != nil {
		t.Fatalf("parse harness results: %v\n%s", err, jsonLine)
	}
	if len(results) == 0 {
		t.Fatal("harness reported no cases")
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("%s: %s", r.Name, r.Detail)
		} else {
			t.Logf("%s: %s", r.Name, r.Detail)
		}
	}
}

// recaptureHarnessJS drives the recapture decision two ways: the pure
// function in isolation (window.__omnipusRecapture.changeReason) and the real
// control-frame handler end to end.
const recaptureHarnessJS = `'use strict';
const fs = require('fs');
const vm = require('vm');

const src = fs.readFileSync(process.argv[2], 'utf8');
const results = [];
function check(name, ok, detail) { results.push({ name: name, ok: !!ok, detail: String(detail) }); }

const win = {};
const sentFrames = [];
const fakeWS = { readyState: 1, send: function (s) { sentFrames.push(JSON.parse(s)); } };

// activeTabs is what the page's findActiveTargetTab resolves against.
const harness = { activeTabId: 7, captureCalls: 0, pcCalls: 0 };

const sandbox = {
  window: win,
  console: { log: function () {}, warn: function () {}, error: function () {} },
  setTimeout: setTimeout, clearTimeout: clearTimeout,
  setInterval: setInterval, clearInterval: clearInterval,
  WebSocket: { OPEN: 1 },
  chrome: {
    runtime: { getManifest: function () { return { version: 'harness' }; } },
    tabs: {
      query: function () { return Promise.resolve([{ id: harness.activeTabId, url: 'https://example.test/' }]); },
      get: function (id) { return Promise.resolve({ id: id, width: 1280, height: 720 }); },
      update: function () { return Promise.resolve(); },
    },
  },
  __harness: harness,
};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
// No window.__omnipusCapture, so encoder.js does NOT self-start.
vm.runInContext(src, sandbox, { filename: 'encoder.js' });

// A missing decision surface is recorded as a failure, NOT an early exit:
// part 2 below drives the real control-frame handler either way, so a run
// against an encoder.js without the fix reports the actual defect ("the panel
// open tore the working stream down") rather than merely "a symbol is
// absent".
const R = win.__omnipusRecapture;
if (!R) {
  check('recapture_decision_surface_exists', false,
    'window.__omnipusRecapture is missing — encoder.js exposes no recapture decision to drive');
}
const TOL = R ? R.constants.sizeToleranceCssPx : 8;

// ---- fakes -------------------------------------------------------------
// A "running capture" as the page sees one: a connected PC, a live video
// track whose getSettings() reports what Chrome produced, and the pin the
// capture converged on.
function makeTrack(kind, w, h) {
  return {
    kind: kind,
    readyState: 'live',
    stopped: false,
    getSettings: function () { return kind === 'video' ? { width: w, height: h } : {}; },
    stop: function () { this.stopped = true; this.readyState = 'ended'; },
  };
}
function makeStream(physW, physH) {
  const v = makeTrack('video', physW, physH);
  const a = makeTrack('audio', 0, 0);
  return {
    video: v,
    getTracks: function () { return [v, a]; },
    getVideoTracks: function () { return [v]; },
    getAudioTracks: function () { return [a]; },
  };
}
function makePC(state) {
  const sender = {
    track: { kind: 'video' },
    getParameters: function () { return { encodings: [{ ssrc: 1 }] }; },
    setParameters: function () { return Promise.resolve(); },
  };
  return {
    connectionState: state,
    signalingState: 'stable',
    iceGatheringState: 'complete',
    closed: false,
    localDescription: { type: 'offer', sdp: 'v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n' },
    close: function () { this.closed = true; },
    getSenders: function () { return [sender]; },
    getTransceivers: function () { return []; },
    addTrack: function () { return sender; },
    createOffer: function () { return Promise.resolve({ type: 'offer', sdp: 'v=0\r\nm=video 9 UDP/TLS/RTP/SAVPF 96\r\n' }); },
    setLocalDescription: function () { return Promise.resolve(); },
  };
}

// Replace ONLY the two collaborators that need a real browser. teardownCapture,
// runCaptureAndOffer, runCaptureAndOfferOnce and handleControlFrame stay real.
vm.runInContext(
  'captureActiveTabStream = async function () {' +
  '  globalThis.__harness.captureCalls++;' +
  '  return globalThis.__harness.nextStream;' +
  '};' +
  'newPeerConnection = function () {' +
  '  globalThis.__harness.pcCalls++;' +
  '  return globalThis.__harness.nextPC;' +
  '};' +
  'ws = globalThis.__harness.ws;',
  Object.assign(sandbox, { __harness: Object.assign(harness, { ws: fakeWS }) })
);

// installRunningCapture puts the page into "a healthy capture is running"
// state, exactly as a completed runCaptureAndOfferOnce would leave it.
function installRunningCapture(opts) {
  opts = opts || {};
  const cssW = opts.cssW === undefined ? 1280 : opts.cssW;
  const cssH = opts.cssH === undefined ? 720 : opts.cssH;
  const scale = opts.scale === undefined ? 1 : opts.scale;
  const stream = makeStream(
    opts.physW === undefined ? Math.round(cssW * scale) : opts.physW,
    opts.physH === undefined ? Math.round(cssH * scale) : opts.physH
  );
  const pc = makePC(opts.connectionState || 'connected');
  harness.liveStream = stream;
  harness.livePC = pc;
  sandbox.__harnessLiveStream = stream;
  sandbox.__harnessLivePC = pc;
  vm.runInContext(
    'currentStream = globalThis.__harnessLiveStream;' +
    'currentPC = globalThis.__harnessLivePC;' +
    'capturedTabId = ' + (opts.tabId === undefined ? 7 : opts.tabId) + ';' +
    'captureScale = ' + scale + ';' +
    'lastPinnedCapDims = { w: ' + cssW + ', h: ' + cssH + ', scale: ' + scale + ' };',
    sandbox
  );
  return { stream: stream, pc: pc };
}
function clearRunningCapture() {
  vm.runInContext('currentStream = null; currentPC = null; capturedTabId = null; lastPinnedCapDims = null;', sandbox);
}
function armRebuildTargets() {
  harness.nextStream = makeStream(640, 480);
  harness.nextPC = makePC('connecting');
}
function recaptureFrame(w, h, scale) {
  const f = { type: 'browser_capture_control', action: 'recapture' };
  if (w !== null) { f.expected_width = w; f.expected_height = h; }
  if (scale !== null && scale !== undefined) { f.capture_scale = scale; }
  return f;
}
async function sendRecapture(frame) {
  sandbox.__harnessMsg = frame;
  harness.captureCalls = 0;
  harness.pcCalls = 0;
  armRebuildTargets();
  vm.runInContext('globalThis.__harnessPromise = handleControlFrame(globalThis.__harnessMsg);', sandbox);
  await sandbox.__harnessPromise;
}
function currentGlobals() {
  vm.runInContext(
    'globalThis.__harnessSnapshot = { pc: currentPC, stream: currentStream, pinned: lastPinnedCapDims };',
    sandbox
  );
  return sandbox.__harnessSnapshot;
}

(async function () {
  // ================= part 1: the decision function alone ================
  const applied = { tabId: 7, cssW: 1280, cssH: 720, scale: 1, physW: 1280, physH: 720 };

  if (R) {
  check('same_geometry_is_no_change',
    R.changeReason({ cssW: 1280, cssH: 720, scale: 1 }, applied) === '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 1280, cssH: 720, scale: 1 }, applied)));

  check('one_pixel_jitter_is_no_change',
    R.changeReason({ cssW: 1281, cssH: 719, scale: 1 }, applied) === '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 1281, cssH: 719, scale: 1 }, applied)));

  check('tolerance_boundary_is_inclusive',
    R.changeReason({ cssW: 1280 + TOL, cssH: 720 - TOL, scale: 1 }, applied) === '' &&
      R.changeReason({ cssW: 1280 + TOL + 1, cssH: 720, scale: 1 }, applied) !== '',
    'at ' + TOL + 'px: ' + JSON.stringify(R.changeReason({ cssW: 1280 + TOL, cssH: 720, scale: 1 }, applied)) +
      '; at ' + (TOL + 1) + 'px: ' + JSON.stringify(R.changeReason({ cssW: 1280 + TOL + 1, cssH: 720, scale: 1 }, applied)));

  check('real_resize_is_a_change',
    R.changeReason({ cssW: 900, cssH: 720, scale: 1 }, applied) !== '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 900, cssH: 720, scale: 1 }, applied)));

  check('scale_change_is_a_change',
    R.changeReason({ cssW: 1280, cssH: 720, scale: 2 }, applied) !== '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 1280, cssH: 720, scale: 2 }, applied)));

  check('cold_start_is_a_change',
    R.changeReason({ cssW: 1280, cssH: 720, scale: 1 }, null) !== '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 1280, cssH: 720, scale: 1 }, null)));

  check('unhinted_recapture_is_a_change',
    R.changeReason(null, applied) !== '',
    'reason=' + JSON.stringify(R.changeReason(null, applied)));

  // The applied-vs-requested guard: the pin says 1280x720 but the track
  // Chrome actually produced is nothing like it, so the pin is not evidence.
  check('pin_that_the_track_contradicts_is_a_change',
    R.changeReason({ cssW: 1280, cssH: 720, scale: 1 },
      { tabId: 7, cssW: 1280, cssH: 720, scale: 1, physW: 800, physH: 600 }) !== '',
    'reason=' + JSON.stringify(R.changeReason({ cssW: 1280, cssH: 720, scale: 1 },
      { tabId: 7, cssW: 1280, cssH: 720, scale: 1, physW: 800, physH: 600 })));
  }

  // ================= part 2: the real control-frame handler =============

  // (a) same geometry -> the working stream must SURVIVE, untouched.
  harness.activeTabId = 7;
  let live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  await sendRecapture(recaptureFrame(1280, 720, 1));
  let after = currentGlobals();
  check('panel_open_at_same_size_keeps_the_stream',
    harness.captureCalls === 0 && harness.pcCalls === 0 &&
      after.pc === live.pc && after.stream === live.stream &&
      live.pc.closed === false && live.stream.video.stopped === false &&
      typeof win.__omnipusState.lastRecaptureSkipAt === 'number',
    'captureActiveTabStream calls=' + harness.captureCalls + ', pc rebuilt=' + (after.pc !== live.pc) +
      ', pc.closed=' + live.pc.closed + ', video track stopped=' + live.stream.video.stopped +
      ', lastRecaptureSkipAt=' + win.__omnipusState.lastRecaptureSkipAt);

  // (a2) a one-pixel difference is still the same panel.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  await sendRecapture(recaptureFrame(1281, 719, 1));
  check('one_pixel_jitter_keeps_the_stream',
    harness.captureCalls === 0 && live.stream.video.stopped === false,
    'captureActiveTabStream calls=' + harness.captureCalls + ', video track stopped=' + live.stream.video.stopped);

  // (b) genuinely changed geometry -> it MUST still rebuild, and the new
  // capture must be built against the new size.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  await sendRecapture(recaptureFrame(615, 744, 1));
  after = currentGlobals();
  check('real_resize_still_rebuilds',
    harness.captureCalls === 1 && after.pc === harness.nextPC && after.stream === harness.nextStream &&
      live.pc.closed === true && live.stream.video.stopped === true,
    'captureActiveTabStream calls=' + harness.captureCalls + ', old pc closed=' + live.pc.closed +
      ', old track stopped=' + live.stream.video.stopped + ', pc swapped=' + (after.pc === harness.nextPC));

  // (b2) a scale change alone must rebuild — a Retina viewer taking over.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  await sendRecapture(recaptureFrame(1280, 720, 2));
  check('scale_change_still_rebuilds',
    harness.captureCalls === 1,
    'captureActiveTabStream calls=' + harness.captureCalls);

  // (c) cold start: nothing running at all -> it MUST capture.
  clearRunningCapture();
  await sendRecapture(recaptureFrame(1280, 720, 1));
  after = currentGlobals();
  check('cold_start_still_captures',
    harness.captureCalls === 1 && after.pc === harness.nextPC && after.stream === harness.nextStream,
    'captureActiveTabStream calls=' + harness.captureCalls + ' with no capture running (want 1)');

  // (d) same size, DIFFERENT tab -> rebuild, or the viewer watches the wrong page.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  harness.activeTabId = 99;
  await sendRecapture(recaptureFrame(1280, 720, 1));
  check('tab_switch_at_identical_size_still_rebuilds',
    harness.captureCalls === 1,
    'captureActiveTabStream calls=' + harness.captureCalls + ' after the active tab moved 7 -> 99');
  harness.activeTabId = 7;

  // (e) same size but the PeerConnection is not connected -> rebuild; the
  // recapture may be the recovery.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7, connectionState: 'failed' });
  await sendRecapture(recaptureFrame(1280, 720, 1));
  check('broken_connection_still_rebuilds',
    harness.captureCalls === 1,
    'captureActiveTabStream calls=' + harness.captureCalls + ' with connectionState=failed');

  // (f) same size but the live track ended -> rebuild.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  live.stream.video.readyState = 'ended';
  await sendRecapture(recaptureFrame(1280, 720, 1));
  check('ended_track_still_rebuilds',
    harness.captureCalls === 1,
    'captureActiveTabStream calls=' + harness.captureCalls + ' with an ended video track');

  // (g) a recapture with NO geometry hint (an active-tab switch) must rebuild
  // — there is no verified target to compare against.
  live = installRunningCapture({ cssW: 1280, cssH: 720, scale: 1, tabId: 7 });
  await sendRecapture(recaptureFrame(null, null, null));
  check('unhinted_recapture_still_rebuilds',
    harness.captureCalls === 1,
    'captureActiveTabStream calls=' + harness.captureCalls + ' for a recapture carrying no expected dims');

  console.log('OMNIPUS_RESULTS ' + JSON.stringify(results));
  process.exit(0);
})().catch(function (e) {
  results.push({ name: 'harness', ok: false, detail: 'threw: ' + (e && e.stack ? e.stack : String(e)) });
  console.log('OMNIPUS_RESULTS ' + JSON.stringify(results));
  process.exit(0);
});
`

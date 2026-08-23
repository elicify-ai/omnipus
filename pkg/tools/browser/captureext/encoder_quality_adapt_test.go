package captureext

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// NOTE (integration, 2026-08-16): the 1.0.8 content-hash entry that this file
// previously registered from an init() now lives in the versionContentHashes
// literal in captureext_test.go, where the per-version history belongs. The
// init() was a deliberate, temporary accommodation for the parallel-agent file
// ownership split and was folded in at merge, as its own comment asked.

// TestEncoderJS_QualityAdaptGuards is the no-node backstop for the adaptive
// resolution loop: it pins the SHAPE of the fix into the embedded asset so a
// merge that resurrects the old hardcoded pin fails here even on a machine
// with no JS runtime. The behavioural proof is
// TestEncoderJS_QualityAdaptLoop below.
func TestEncoderJS_QualityAdaptGuards(t *testing.T) {
	src := embeddedEncoderJS(t)

	// The 2026-08-15 defect itself: scaleResolutionDownBy pinned to a literal
	// 1 forbids the encoder from ever shrinking the picture, so frame rate
	// becomes the only thing that can give (1 fps on a shared-cpu box).
	if strings.Contains(src, "scaleResolutionDownBy = 1;") {
		t.Error("encoder.js: scaleResolutionDownBy must not be pinned to a literal 1 — " +
			"applyVideoSenderConstraints must read currentAdaptScale() or its post-connected " +
			"re-apply silently undoes every step the adaptation loop takes")
	}
	if !strings.Contains(src, "params.encodings[0].scaleResolutionDownBy = currentAdaptScale();") {
		t.Error("encoder.js: applyVideoSenderConstraints must set scaleResolutionDownBy from currentAdaptScale()")
	}

	// The hard floor. 2026-07-31 collapsed the stream to 319x158; the loop
	// must not be able to express anything worse than half linear scale.
	if !strings.Contains(src, "const ADAPT_SCALE_STEPS = [1, 1.5, 2];") {
		t.Error("encoder.js: ADAPT_SCALE_STEPS must stay [1, 1.5, 2] — the last entry IS the hard floor (quarter pixels)")
	}

	// The parity gate: stepping down is conditioned on the encoder's own
	// cpu self-report, which is what keeps the loop inert on a hardware
	// encoder over loopback.
	if !strings.Contains(src, "reason === 'cpu' && fps < ADAPT_TARGET_FPS") {
		t.Error("encoder.js: the step-down condition must be gated on qualityLimitationReason === 'cpu' " +
			"(low fps alone is not pressure — a static page legitimately encodes near 0 fps)")
	}

	// Diagnosability: every step must be logged.
	if !strings.Contains(src, "record('qualityAdapt: step '") {
		t.Error("encoder.js: every adaptation step must be recorded so the behaviour is diagnosable in the field")
	}

	// Lifecycle wiring — a loop nobody starts is the classic green-but-dead shape.
	if !strings.Contains(src, "startQualityAdaptLoop();") {
		t.Error("encoder.js: startQualityAdaptLoop() must be called on the PC's first 'connected' transition")
	}
	if !strings.Contains(src, "stopQualityAdaptLoop();") {
		t.Error("encoder.js: stopQualityAdaptLoop() must be called from teardownCapture")
	}

	// --- round-2 F2: adaptation must not outlive the evidence behind it ----
	if !strings.Contains(src, "const ADAPT_EVIDENCE_TTL_MS") {
		t.Error("encoder.js: ADAPT_EVIDENCE_TTL_MS must exist — without it a scale learned during one busy " +
			"moment (or by a boot-warmed capture nobody was watching) can never be given back on a static page, " +
			"which cannot produce the 24fps the restore path demands")
	}
	if !strings.Contains(src, "adaptCarryOverIndex(adaptState, adaptCycleCount, now)") {
		t.Error("encoder.js: stopQualityAdaptLoop must decide what a rebuild inherits via adaptCarryOverIndex — " +
			"unconditionally carrying the learned index is what handed a viewer the resolution an unwatched " +
			"boot warm-up settled on")
	}
	if !strings.Contains(src, "if (!(cycleCount > 1)) return 0;") {
		t.Error("encoder.js: adaptCarryOverIndex must discard what a page's FIRST capture learned — that is the " +
			"boot-warmed, viewerless, boot-contended one")
	}
	// The counting basis is the whole correctness of rule 1: runCaptureAndOfferOnce
	// calls teardownCapture as its FIRST step, so counting teardowns instead of
	// capture starts puts the count one cycle ahead and the viewerless warm-up
	// becomes "not the first" — silently restoring the defect.
	if !strings.Contains(src, "  noteAdaptCycleStarted();") {
		t.Error("encoder.js: startQualityAdaptLoop must count the cycle (noteAdaptCycleStarted) — counting " +
			"teardowns instead is off by one, because the first teardown happens before any capture exists")
	}

	// --- round-2 F7: an adaptation that fails to APPLY must leave the page --
	if !strings.Contains(src, "reportAdaptFailure(msg);") {
		t.Error("encoder.js: a rejected setParameters must be reported to the gateway (reportAdaptFailure) — " +
			"record()/warn() reach console.log and nothing forwards the extension page's console anywhere, so " +
			"the picture would stay collapsed while every layer reported success")
	}
	if !strings.Contains(src, "sendFrame({ type: 'browser_capture_control', action: 'ping', reason: reason });") {
		t.Error("encoder.js: the adaptation-failure report must ride the existing browser_capture_control frame — " +
			"a new action would be a wire-contract change (Constraint #8) the encoder cannot make on its own")
	}

	// --- 1.0.10: do not apply a stale answer or a pre-negotiation setParameters --
	if !strings.Contains(src, "signalingState !== 'have-local-offer'") {
		t.Error("encoder.js: must ignore a browser_capture_answer unless the PC is in have-local-offer — " +
			"a second answer in stable froze CI live video (ingest ICE failed after " +
			"Failed to set remote answer sdp: Called in wrong state: stable)")
	}
	if !strings.Contains(src, "encodings not negotiated, skipping setParameters") {
		t.Error("encoder.js: applyVideoSenderConstraints must skip setParameters when encodings are empty — " +
			"synthesizing encodings:[{}] is what Chrome rejects as " +
			"'getParameters() has never been called on this sender'")
	}
	// --- 1.0.15: the viewer-derived bitrate ceiling (ADR-069 Finding 2) -----
	if !strings.Contains(src, "action === 'set_bitrate'") {
		t.Error("encoder.js: must handle browser_capture_control{set_bitrate} — this page's own " +
			"PeerConnection is loopback, so it cannot measure the link that matters; the gateway " +
			"derives the ceiling from the VIEWER's RTCP receiver reports and sends it down")
	}
	if !strings.Contains(src, "viewerBitrateCeiling > 0 && viewerBitrateCeiling < maxBitrate") {
		t.Error("encoder.js: applyVideoSenderConstraints must CLAMP its locally-computed maxBitrate to the " +
			"viewer-reported ceiling — storing the value without applying it is the exact green-but-broken " +
			"shape ADR-069 Finding 2 describes (27.6% loss, 1-6 fps, while the encoder aimed at 24 Mbps)")
	}

	// --- 1.0.14: the warm-handover reset must NOT be a capture rebuild ------
	if !strings.Contains(src, "action === 'adapt_reset'") {
		t.Error("encoder.js: must handle browser_capture_control{adapt_reset} — the gateway sends it at the " +
			"boot-warm handover so the first real viewer does not inherit a resolution chosen with nobody watching")
	}
	if i := strings.Index(src, "if (action === 'adapt_reset') {"); i >= 0 {
		j := strings.Index(src[i:], "if (action === 'recapture') {")
		if j <= 0 {
			t.Error("encoder.js: could not isolate the adapt_reset branch to check it")
		} else if branch := src[i : i+j]; strings.Contains(branch, "runCaptureAndOffer") {
			t.Error("encoder.js: adapt_reset must NOT rebuild the capture — a rebuild at handover measured " +
				"~17s to first frame against ~4s without it (hosted box 2026-08-17), which is exactly what " +
				"made keeping a warm capture alive worse than letting it stop")
		}
	}

	if !strings.Contains(src, "senderParamsChain") || !strings.Contains(src, "queueSenderParams(") {
		t.Error("encoder.js: every getParameters()->setParameters() pair must go through " +
			"queueSenderParams — libwebrtc clears the sender transaction id on each " +
			"setParameters, so overlapping applies (post-answer, post-connected, adapt loop) " +
			"produce 'getParameters() has never been called on this sender' (hosted box, 2026-08-17)")
	}
	if strings.Contains(src, "params.encodings = [{}]") {
		t.Error("encoder.js: must not synthesize encodings:[{}] before setParameters — that is the " +
			"InvalidStateError that the 1.0.10 skip exists to prevent")
	}
}

// encoderHarnessOptOutEnv makes the node requirement below skippable, but only
// DELIBERATELY. See TestEncoderJS_QualityAdaptLoop.
const encoderHarnessOptOutEnv = "OMNIPUS_SKIP_NODE_ENCODER_HARNESS"

// TestEncoderJS_QualityAdaptLoop executes the REAL encoder.js in a Node vm
// context against a fake RTCRtpSender, and drives the real
// stats -> decide -> setParameters wiring (not a re-implementation of the
// policy).
//
// It FAILS, rather than skips, when node is missing (round-2 finding F8).
// This is the only behavioural coverage the adaptation loop has — the guards
// above are text matches, and cannot tell a working loop from a broken one —
// so a silent skip meant that on a machine without node the loop had NO
// behavioural coverage at all AND the suite still reported green. That is the
// precise shape of failure this project keeps paying for: a gate that reports
// success while testing nothing. Every environment this suite is meant to run
// in has node: CI's Tests job installs it (setup-node, .github/workflows/
// pr.yml) because the same job builds the SPA, and so does the Fly CI worker
// image (deploy/ci-worker/Dockerfile).
//
// The opt-out exists for a contributor with a Go-only toolchain, and it is an
// env var they must type: an explicit, visible choice to run without this
// coverage, not a default that quietly happens to them.
func TestEncoderJS_QualityAdaptLoop(t *testing.T) {
	node, lookErr := exec.LookPath("node")
	if lookErr != nil {
		if os.Getenv(encoderHarnessOptOutEnv) != "" {
			t.Skipf("%s is set and node is not on PATH — SKIPPING the ONLY behavioural coverage of the "+
				"encoder's quality-adaptation loop. The content guards that still ran cannot detect a broken loop, "+
				"only a deleted one.", encoderHarnessOptOutEnv)
		}
		t.Fatalf("node is not on PATH, so the encoder quality-adaptation loop has NO behavioural coverage in this run. "+
			"Install node (CI and the Fly CI worker both have it), or set %s=1 to accept running without that coverage. "+
			"lookup error: %v", encoderHarnessOptOutEnv, lookErr)
	}

	dir := t.TempDir()
	encPath := filepath.Join(dir, "encoder.js")
	if writeErr := os.WriteFile(encPath, []byte(embeddedEncoderJS(t)), 0o644); writeErr != nil {
		t.Fatalf("write encoder.js: %v", writeErr)
	}
	harnessPath := filepath.Join(dir, "harness.cjs")
	if writeErr := os.WriteFile(harnessPath, []byte(qualityAdaptHarnessJS), 0o644); writeErr != nil {
		t.Fatalf("write harness: %v", writeErr)
	}

	out, runErr := exec.Command(node, harnessPath, encPath).CombinedOutput()
	if runErr != nil {
		t.Fatalf("node harness failed: %v\n%s", runErr, out)
	}
	// The harness prints one JSON line, prefixed, so any stray console noise
	// from encoder.js cannot corrupt the result.
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

func embeddedEncoderJS(t *testing.T) string {
	t.Helper()
	raw, err := embeddedExt.ReadFile(embeddedRoot + "/encoder.js")
	if err != nil {
		t.Fatalf("read embedded encoder.js: %v", err)
	}
	return string(raw)
}

// qualityAdaptHarnessJS loads the embedded encoder.js into a Node vm context
// with the minimal browser globals it touches at parse time, then drives
// window.__omnipusQualityAdapt.tick() against a fake PeerConnection whose
// sender reports scripted getStats() values. Every assertion is about what
// setParameters ACTUALLY received.
const qualityAdaptHarnessJS = `'use strict';
const fs = require('fs');
const vm = require('vm');

const encoderPath = process.argv[2];
const src = fs.readFileSync(encoderPath, 'utf8');

const results = [];
function check(name, ok, detail) { results.push({ name: name, ok: !!ok, detail: String(detail) }); }

const win = {};
// sentFrames collects everything the page pushes to the gateway over the
// ingest socket, so the F7 report can be asserted on from outside the page --
// which is the entire point of that fix.
const sentFrames = [];
const fakeWS = { readyState: 1, send: function (s) { sentFrames.push(JSON.parse(s)); } };
const sandbox = {
  window: win,
  console: { log: function () {}, warn: function () {}, error: function () {} },
  setTimeout: setTimeout, clearTimeout: clearTimeout,
  setInterval: setInterval, clearInterval: clearInterval,
  WebSocket: { OPEN: 1 },
  chrome: { runtime: { getManifest: function () { return { version: 'harness' }; } } },
};
sandbox.globalThis = sandbox;
vm.createContext(sandbox);
// No window.__omnipusCapture, so encoder.js does NOT self-start: no WS, no
// capture, no timers. Only the pure module scope is created.
vm.runInContext(src, sandbox, { filename: 'encoder.js' });

const adapt = win.__omnipusQualityAdapt;
if (!adapt) { console.log('OMNIPUS_RESULTS ' + JSON.stringify([{name:'surface', ok:false, detail:'window.__omnipusQualityAdapt missing'}])); process.exit(0); }
const K = adapt.constants;

// makePC builds a fake RTCPeerConnection exposing exactly the surface the
// loop uses. sample is mutable so a test can change conditions mid-run.
function makePC(sample, opts) {
  opts = opts || {};
  const applied = [];
  // A NEGOTIATED sender is the default: real Chrome populates ssrc once
  // negotiation completes, and encodingsNegotiated() keys off exactly that.
  // opts.placeholderEncodings reproduces Chrome's PRE-negotiation [{}],
  // which is the shape that made setParameters throw InvalidStateError.
  let params = { encodings: opts.emptyEncodings ? [] : (opts.placeholderEncodings ? [{}] : [{ ssrc: 424242 }]) };
  const sender = {
    track: { kind: 'video' },
    getStats: function () {
      return Promise.resolve(new Map([['out', {
        type: 'outbound-rtp', kind: 'video',
        framesPerSecond: sample.fps,
        qualityLimitationReason: sample.reason,
        frameWidth: 1266, frameHeight: 1372,
      }]]));
    },
    getParameters: function () { return JSON.parse(JSON.stringify(params)); },
    setParameters: function (p) {
      if (opts.failSetParameters) return Promise.reject(new Error('InvalidStateError (simulated)'));
      params = p;
      applied.push(p.encodings[0].scaleResolutionDownBy);
      return Promise.resolve();
    },
  };
  return {
    connectionState: 'connected',
    getSenders: function () { return [sender]; },
    applied: applied,
    currentParams: function () { return params; },
  };
}

async function ticks(pc, n) { for (let i = 0; i < n; i++) await adapt.tick(pc); }
// ticksAt drives the loop with a CONTROLLED clock, so the evidence-freshness
// rules can be exercised without a 60-second test.
async function ticksAt(pc, n, now) { for (let i = 0; i < n; i++) await adapt.tick(pc, now); }

(async function () {
  // --- 1. hosted Linux collapse: cpu-limited at 1 fps, full resolution ----
  adapt.reset();
  let sample = { fps: 1, reason: 'cpu' };
  let pc = makePC(sample);
  await ticks(pc, 24);
  check('linux_cpu_limited_steps_down',
    JSON.stringify(pc.applied) === JSON.stringify([1.5, 2]),
    'setParameters received ' + JSON.stringify(pc.applied) + ' (want [1.5,2]); final scale ' + adapt.scale());

  // --- 2. hard floor: it can never go past 2, however long it stays bad ---
  check('hard_floor_scale_2',
    adapt.scale() === 2 && pc.applied.every(function (s) { return s <= 2; }),
    'final scale ' + adapt.scale() + ', max applied ' + Math.max.apply(null, pc.applied));

  // --- 3. macOS/loopback parity: hardware encoder, never cpu-limited -----
  // The measured macOS figure is 13 fps at full resolution with the encoder
  // reporting no limitation. The loop must touch NOTHING.
  adapt.reset();
  sample = { fps: 13, reason: 'none' };
  pc = makePC(sample);
  await ticks(pc, 40);
  check('macos_loopback_inert',
    pc.applied.length === 0 && adapt.scale() === 1,
    'setParameters called ' + pc.applied.length + ' time(s), scale ' + adapt.scale() + ' (want 0 calls, scale 1)');

  // --- 4. a fast, unlimited encoder at full resolution stays put ---------
  adapt.reset();
  sample = { fps: 60, reason: 'none' };
  pc = makePC(sample);
  await ticks(pc, 40);
  check('healthy_encoder_never_upscales_past_1',
    pc.applied.length === 0 && adapt.scale() === 1,
    'setParameters called ' + pc.applied.length + ' time(s), scale ' + adapt.scale());

  // --- 5. a STATIC page encodes near 0 fps: that is not pressure ---------
  adapt.reset();
  sample = { fps: 0, reason: 'none' };
  pc = makePC(sample);
  await ticks(pc, 40);
  check('static_page_low_fps_is_not_pressure',
    pc.applied.length === 0 && adapt.scale() === 1,
    'setParameters called ' + pc.applied.length + ' time(s) on an idle page');

  // --- 6. recovery: pressure lifts, resolution is restored in steps ------
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24);
  const afterCollapse = pc.applied.slice();
  sample.fps = 30; sample.reason = 'none';
  await ticks(pc, 40);
  const restored = pc.applied.slice(afterCollapse.length);
  check('recovers_step_by_step',
    JSON.stringify(restored) === JSON.stringify([1.5, 1]) && adapt.scale() === 1,
    'restore sequence ' + JSON.stringify(restored) + ' (want [1.5,1]); final scale ' + adapt.scale());

  // --- 7. hysteresis: one good sample must not undo a step --------------
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24); // down to the floor
  const beforeFlap = pc.applied.length;
  for (let i = 0; i < 12; i++) {
    sample.fps = 30; sample.reason = 'none';
    await adapt.tick(pc);              // one good sample
    sample.fps = 1; sample.reason = 'cpu';
    await adapt.tick(pc);              // then bad again
  }
  check('single_good_sample_does_not_oscillate',
    pc.applied.length === beforeFlap && adapt.scale() === 2,
    'alternating good/bad samples produced ' + (pc.applied.length - beforeFlap) + ' extra step(s), scale ' + adapt.scale());

  // --- 8. hysteresis is asymmetric: recovery is slower than collapse -----
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  let downTicks = 0;
  while (pc.applied.length === 0 && downTicks < 50) { await adapt.tick(pc); downTicks++; }
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  const pc2 = makePC(sample);
  await ticks(pc2, 24);
  const n0 = pc2.applied.length;
  sample.fps = 30; sample.reason = 'none';
  let upTicks = 0;
  while (pc2.applied.length === n0 && upTicks < 50) { await adapt.tick(pc2); upTicks++; }
  check('recovery_slower_than_collapse',
    upTicks > downTicks && downTicks > 1,
    'first step down after ' + downTicks + ' samples, first step up after ' + upTicks + ' samples');

  // --- 9. a rejected setParameters must not be believed ------------------
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample, { failSetParameters: true });
  await ticks(pc, 24);
  check('failed_setParameters_reverts_state',
    adapt.scale() === 1 && String(win.__omnipusState.lastError || '').indexOf('qualityAdapt') === 0,
    'scale ' + adapt.scale() + ' (want 1), lastError=' + win.__omnipusState.lastError);

  // --- 10. the post-connected re-apply preserves the loop's decision -----
  // This is the wiring that makes the whole thing real: applyVideoSenderConstraints
  // runs again on every 'connected' transition, and a hardcoded 1 there would
  // silently revert the step the loop just took.
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24);
  const scaleAfterLoop = adapt.scale();
  vm.runInContext('applyVideoSenderConstraints(globalThis.__harnessPC, {context: "post-connected"})',
    Object.assign(sandbox, { __harnessPC: pc }));
  await new Promise(function (r) { setTimeout(r, 20); });
  const reapplied = pc.currentParams().encodings[0].scaleResolutionDownBy;
  check('post_connected_reapply_preserves_scale',
    reapplied === scaleAfterLoop && scaleAfterLoop === 2,
    'after re-apply scaleResolutionDownBy=' + reapplied + ' (loop had chosen ' + scaleAfterLoop + ')');

  // --- 11. the diagnostic surface is populated --------------------------
  
  // --- 1.0.10: empty encodings must not synthesize and setParameters --------
  adapt.reset();
  pc = makePC({ fps: 13, reason: 'none' }, { emptyEncodings: true });
  vm.runInContext('applyVideoSenderConstraints(globalThis.__harnessPC, {context: "post-connected"})',
    Object.assign(sandbox, { __harnessPC: pc }));
  await new Promise(function (r) { setTimeout(r, 20); });
  check('empty_encodings_skips_setParameters',
    pc.applied.length === 0,
    'setParameters called ' + pc.applied.length + ' time(s) on empty encodings (want 0)');

  // Chrome's PRE-negotiation placeholder is encodings:[{}] -- not empty, but
  // setParameters on it throws "getParameters() has never been called".
  adapt.reset();
  pc = makePC({ fps: 13, reason: 'none' }, { placeholderEncodings: true });
  vm.runInContext('applyVideoSenderConstraints(globalThis.__harnessPC, {context: "post-connected"})',
    Object.assign(sandbox, { __harnessPC: pc }));
  await new Promise(function (r) { setTimeout(r, 20); });
  check('placeholder_encodings_skip_setParameters',
    pc.applied.length === 0,
    'setParameters called ' + pc.applied.length + ' time(s) on Chrome pre-negotiation encodings:[{}] (want 0)');

check('state_surface_populated',
    win.__omnipusState.qualityAdapt && typeof win.__omnipusState.qualityAdapt.scale === 'number',
    JSON.stringify(win.__omnipusState.qualityAdapt));

  // --- 12. constants are the documented ones ----------------------------
  check('constants_pinned',
    K.maxScale === 2 && K.targetFps === 12 && K.restoreFps === 24 && K.upSamples > K.downSamples &&
      K.evidenceTtlMs > 0 && K.reportMinIntervalMs > 0,
    JSON.stringify(K));

  // --- 13. round-2 F2: a static page can NEVER deliver 24 fps ------------
  // The only restore path the round-1 loop had required reason !== 'cpu' AND
  // >= 24 fps. A page that has stopped moving encodes near 0 fps by
  // definition, so on the single most common thing this panel shows, a scale
  // learned during one busy moment could never be given back. The
  // evidence-TTL probe is what retires it.
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24);                       // collapse to the floor
  const collapsedAt = Date.now();
  sample.fps = 2; sample.reason = 'none';    // now the page goes static
  await ticksAt(pc, 6, collapsedAt + 1000);  // evidence still fresh: hold
  const beforeProbe = pc.applied.slice();
  await ticksAt(pc, 6, collapsedAt + 1000 + K.evidenceTtlMs);
  const afterFirstProbe = adapt.scale();
  await ticksAt(pc, 6, collapsedAt + 1000 + 2 * K.evidenceTtlMs);
  check('stale_evidence_is_retired_on_a_static_page',
    JSON.stringify(beforeProbe) === JSON.stringify([1.5, 2]) && afterFirstProbe === 1.5 && adapt.scale() === 1,
    'held at ' + JSON.stringify(beforeProbe) + ', after one TTL ' + afterFirstProbe + ', after two ' + adapt.scale());

  // --- 14. round-2 F2: fresh pressure is NOT retired ---------------------
  // The probe must not fight an encoder that is still saying 'cpu'. A
  // cpu-limited sample at a HEALTHY frame rate (the step worked) is exactly
  // that: the downscale is doing its job and must stay.
  adapt.reset();
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24);
  const t0 = Date.now();
  sample.fps = 20; sample.reason = 'cpu';    // still cpu-limited, but coping
  for (let i = 1; i <= 8; i++) await adapt.tick(pc, t0 + i * K.evidenceTtlMs);
  check('fresh_cpu_pressure_is_never_retired',
    adapt.scale() === 2,
    'scale ' + adapt.scale() + ' after 8 TTLs of cpu-limited-but-coping samples (want 2)');

  // --- 15. round-2 F2: the FIRST capture rebuild starts from full quality -
  // A boot-warmed capture (tools.browser.warm_capture_at_boot) adapts while
  // NOBODY is watching, during the busiest minute of the process's life. The
  // viewer's first open must not inherit that.
  adapt.reset();
  adapt.beginCycle();                        // the boot-warmed capture connects
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample);
  await ticks(pc, 24);
  const learnedUnwatched = adapt.scale();
  adapt.endCycle();                          // what teardownCapture calls
  check('first_capture_state_is_never_inherited',
    learnedUnwatched === 2 && adapt.scale() === 1 && adapt.cycles() === 1,
    'unwatched warm-up learned ' + learnedUnwatched + ', rebuild starts at ' + adapt.scale() +
      ' (cycle ' + adapt.cycles() + ')');

  // --- 16. ...but a later rebuild with FRESH evidence carries over --------
  // This is round 1's property, and it is the one that matters in use:
  // dragging the panel fires recaptures seconds apart, and re-collapsing from
  // 1 fps on every drag is what the carry-over exists to prevent.
  adapt.beginCycle();                        // the viewer's own capture connects
  sample.fps = 1; sample.reason = 'cpu';
  pc = makePC(sample);
  await ticks(pc, 24);
  adapt.endCycle();
  check('later_capture_with_fresh_evidence_carries_over',
    adapt.scale() === 2 && adapt.cycles() === 2,
    'scale after the second cycle ' + adapt.scale() + ' (want 2, i.e. carried)');

  // --- 17. the carry-over rule itself, exhaustively ----------------------
  const carryStale = adapt.carryOver({ index: 2, lastPressureAt: 1000 }, 3, 1000 + K.evidenceTtlMs);
  const carryFresh = adapt.carryOver({ index: 2, lastPressureAt: 1000 }, 3, 1000 + K.evidenceTtlMs - 1);
  const carryFirst = adapt.carryOver({ index: 2, lastPressureAt: 1000 }, 1, 1000);
  const carryNone = adapt.carryOver({ index: 2, lastPressureAt: 1000 }, 0, 1000);
  check('carry_over_rule',
    carryStale === 0 && carryFresh === 2 && carryFirst === 0 && carryNone === 0,
    'stale=' + carryStale + ' fresh=' + carryFresh + ' firstCapture=' + carryFirst + ' noCapture=' + carryNone);

  // --- 18. round-2 F7: an adaptation that fails to APPLY leaves the page --
  // record()/warn() reach console.log and nothing forwards it, so a rejected
  // setParameters used to be invisible to every layer outside this page.
  adapt.reset();
  sentFrames.length = 0;
  sandbox.__harnessWS = fakeWS;
  vm.runInContext('ws = globalThis.__harnessWS;', sandbox);
  sample = { fps: 1, reason: 'cpu' };
  pc = makePC(sample, { failSetParameters: true });
  await ticks(pc, 24);
  const reports = sentFrames.filter(function (f) { return f.action === 'ping' && f.reason; });
  check('failed_apply_is_reported_to_the_gateway',
    reports.length === 1 &&
      reports[0].type === 'browser_capture_control' &&
      reports[0].reason.indexOf('quality-adapt apply failed') === 0 &&
      reports[0].reason.length <= 512,
    // Exactly one, not one per 2s tick: the throttle is part of the fix, a
    // permanently-rejecting sender would otherwise flood a socket whose only
    // other traffic is a 15s heartbeat.
    'frames sent: ' + JSON.stringify(sentFrames));

  console.log('OMNIPUS_RESULTS ' + JSON.stringify(results));
})().catch(function (e) {
  results.push({ name: 'harness', ok: false, detail: 'threw: ' + (e && e.stack ? e.stack : String(e)) });
  console.log('OMNIPUS_RESULTS ' + JSON.stringify(results));
});
`

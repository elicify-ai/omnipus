// Omnipus capture extension encoder page.
//
// This page is opened directly by the gateway (via CDP,
// chrome-extension://<pinned-id>/encoder.html) once the extension has been
// loaded into a managed, headless Chrome instance. It self-consumes
// chrome.tabCapture on the ACTIVE tab (no separate consumer tab — the
// spike-proven simplest pattern, see docs/internal/design/wv1-spike-results.md
// Q2/Q3), negotiates a non-trickle WebRTC offer/answer with the gateway's
// loopback capture-ingest WS endpoint, and streams both audio+video tracks.
//
// Config is injected by the gateway BEFORE this script runs, via
// Page.addScriptToEvaluateOnNewDocument setting
// `window.__omnipusCapture = {token, ingestUrl}`. This is intentionally
// NEVER read from URL params or any other lower-trust channel — the
// loopback WS is not a trust boundary by itself, so the token is the only
// thing that authorizes this page to mint a capture session server-side.
//
// Wire frame shapes — pinned by the landed capture-ingest contract
// (contracts/components/schemas/BrowserCapture{Hello,Offer,Answer,Control}Frame.yaml,
// ADR-047 D1/D2/D6):
//   -> {type: 'browser_capture_hello',   token, ext_version}
//   -> {type: 'browser_capture_offer',   sdp}
//   <- {type: 'browser_capture_answer',  sdp}
//   <-  {type: 'browser_capture_control', action: recapture|shutdown, reason?}  (server -> client)
//   ->  {type: 'browser_capture_control', action: ping}                        (client -> server)
//
// browser_capture_hello is CLIENT -> SERVER ONLY — the gateway never sends
// one back. Per the schema doc: "the gateway audits any hello with a
// missing/invalid/expired token as a rejected ingest-auth attempt and
// closes the connection" — i.e. success is silent (the connection simply
// stays open and you proceed to the next frame); the only observable
// signal for a REJECTED hello is the WS closing. So this page sends hello
// then immediately proceeds to capture + offer, with no ack wait — the
// reconnect watchdog (a WS close before/without ever reaching 'connected')
// is what surfaces an auth rejection.
//
// browser_capture_control's `ping` is likewise CLIENT -> SERVER ONLY (this
// page's own periodic health/reconnect-watchdog beacon); the server-issued
// control actions are exactly `recapture` and `shutdown` — there is no
// `pong` action in the schema's enum.
'use strict';

const LOG_PREFIX = '[omnipus-capture]';

function log(...args) {
  console.log(LOG_PREFIX, ...args);
}

function warn(...args) {
  console.warn(LOG_PREFIX, ...args);
}

// window.__omnipusState is a debug/verification surface only — it is never
// part of the wire protocol. Verification (including this task's
// real-Chrome check) reads it back via CDP Runtime.evaluate.
window.__omnipusState = {
  status: 'idle', // idle -> connecting -> hello_sent -> capturing -> offering -> connected -> reconnecting -> error -> shutdown
  wsState: 'closed',
  iceState: null,
  connState: null,
  lastError: null,
  reconnectAttempts: 0,
  videoTracks: null,
  audioTracks: null,
  // senderConstraints is set only by a POST-negotiation
  // applyVideoSenderConstraints re-apply (see that function) whose
  // setParameters() call actually resolved -- i.e. it is the external,
  // outside-the-page proof (read via CDP Runtime.evaluate) that the
  // degradationPreference/maxBitrate/scaleResolutionDownBy encoding
  // constraints genuinely stuck on a settled sender, not just that this file
  // attempted to set them. Stays null until the first such success.
  senderConstraints: null,
  history: [],
};

function record(evt) {
  window.__omnipusState.history.push({ t: Date.now(), evt: String(evt) });
  if (window.__omnipusState.history.length > 300) window.__omnipusState.history.shift();
  log(evt);
}

function setStatus(s) {
  window.__omnipusState.status = s;
}

// ---- config ---------------------------------------------------------------

function readConfig() {
  const cfg = window.__omnipusCapture;
  if (!cfg || typeof cfg.token !== 'string' || typeof cfg.ingestUrl !== 'string') {
    throw new Error(
      'window.__omnipusCapture missing or malformed (expected {token, ingestUrl} injected by the gateway via Page.addScriptToEvaluateOnNewDocument)'
    );
  }
  return cfg;
}

function extVersion() {
  try {
    return chrome.runtime.getManifest().version;
  } catch (e) {
    return 'unknown';
  }
}

// ---- tab selection ----------------------------------------------------------

function isExtensionUrl(url) {
  return typeof url === 'string' && url.startsWith('chrome-extension://');
}

// findActiveTargetTab implements the targeting rule from the task spec:
// the ACTIVE tab in the last-focused window, falling back to the first
// non-extension tab if there is no such active tab (e.g. the extension's
// own page happens to be focused).
async function findActiveTargetTab() {
  const active = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
  const activeCandidate = active.find((t) => !isExtensionUrl(t.url));
  if (activeCandidate) return activeCandidate.id;

  record('findActiveTargetTab: no non-extension active tab, falling back to first non-extension tab');
  const all = await chrome.tabs.query({});
  const fallback = all.find((t) => !isExtensionUrl(t.url));
  if (!fallback) {
    throw new Error('no capturable tab found (only extension pages are open)');
  }
  return fallback.id;
}

// ---- capture ----------------------------------------------------------------

let currentStream = null;
let currentPC = null;
let ws = null;
let shuttingDown = false;
let lastGoodIceTime = Date.now();
let watchdogTimer = null;
let pingBeaconTimer = null;
let reconnectTimer = null;
let reconnectDelayMs = 1000;
const RECONNECT_MAX_DELAY_MS = 30000;
const ICE_BAD_GRACE_MS = 10000;
const PING_BEACON_INTERVAL_MS = 15000;

// DEFAULT_MAX_VIDEO_BITRATE_BPS (fix-wave finding 4, "overdrive"; revised
// per docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2):
// the tabCapture MediaStream has no bandwidth ceiling of its own, so an
// unconstrained VP8 encoder will burn as much CPU/bandwidth as the content
// demands -- a real contributor to pod-CPU-saturation/choppy-input symptoms
// under heavy (video-playing) pages, which is why a cap exists at all. The
// original 2 Mbps cap was set with that CPU-saturation risk in mind, but
// live UAT showed it was actually the PRESSURE that triggered the
// resolution collapse this file's degradationPreference fix (see
// applyVideoSenderConstraints) addresses: paired with 'balanced' degradation,
// 2 Mbps was tight enough that VP8 gave up resolution to stay under it,
// producing a 319x158 stream at panel-sized (~561x587) geometry -- both
// visibly blurry and, worse, wrong for input mapping. 6 Mbps gives VP8
// headroom to hold full panel-sized geometry without hitting the ceiling
// under normal browsing content, while 'maintain-resolution' now means any
// remaining pressure is spent on framerate instead of resolution.
// Overridable per-install via window.__omnipusCapture.maxVideoBitrate
// (bits/sec) for future config -- see the file header for the
// injection mechanism; unset/invalid falls back to this default.
const DEFAULT_MAX_VIDEO_BITRATE_BPS = 6000000;

// OFFER_ANSWER_TIMEOUT_MS (fix-wave finding 4, review-flagged gap): before
// this fix, a browser_capture_offer whose browser_capture_answer never
// arrived (dropped frame, gateway-side error the encoder never learns about
// any other way) left this page silently wedged in 'offering' state
// forever -- the WS itself stays open, so neither the ICE watchdog
// (startWatchdogOnce, which only fires once a PeerConnection ICE state
// exists) nor the reconnect-on-close path (scheduleReconnect) ever engages.
// Closing the WS on timeout routes through the SAME 'close' handler every
// other disconnect uses, so the existing backoff/reconnect logic below
// (scheduleReconnect) is what actually recovers -- no new recovery path.
const OFFER_ANSWER_TIMEOUT_MS = 10000;
let offerAnswerTimer = null;

// armOfferAnswerTimeout captures the CURRENT ws instance so a stale timer
// (one armed before a reconnect already replaced `ws` with a fresh
// connection) can never close a healthy, unrelated newer connection --
// without this guard, a timer that fires just as scheduleReconnect's own
// backoff already replaced `ws` would incorrectly kill the brand new
// connection instead of being the no-op it should be.
function armOfferAnswerTimeout() {
  clearOfferAnswerTimeout();
  const forWs = ws;
  offerAnswerTimer = setTimeout(() => {
    offerAnswerTimer = null;
    if (ws !== forWs) return; // superseded by a newer connection -- stale, ignore
    window.__omnipusState.lastError = 'offer-answer timeout: no browser_capture_answer received';
    record('offer-answer timeout: no browser_capture_answer within ' + OFFER_ANSWER_TIMEOUT_MS + 'ms, forcing reconnect');
    try {
      forWs.close();
    } catch (e) {
      /* ignore */
    }
  }, OFFER_ANSWER_TIMEOUT_MS);
}

function clearOfferAnswerTimeout() {
  if (offerAnswerTimer) {
    clearTimeout(offerAnswerTimer);
    offerAnswerTimer = null;
  }
}

// applyVideoSenderConstraints (fix-wave finding 4) caps the video sender's
// bitrate and sets encoding hints on the video track's RTCRtpSender. Errors
// are logged, not thrown -- a failed setParameters/contentHint assignment
// should degrade to "uncapped bitrate", not abort the whole capture/offer
// flow.
//
// Called THREE times per capture cycle (UAT v24 finding,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2,
// fix-wave follow-up): once from runCaptureAndOfferOnce right after
// pc.addTrack (context='pre-negotiation'), again once the
// browser_capture_answer has been applied via setRemoteDescription
// (context='post-answer'), and again on the PC's first transition to
// connectionState 'connected' (context='post-connected'). The
// pre-negotiation call is the one that shipped first and it is NOT enough
// on its own: RTCRtpSender.getParameters()/setParameters() is a
// transactional read-modify-write keyed to the sender's current transport
// state, and calling it before setLocalDescription/setRemoteDescription has
// completed negotiation commonly rejects with InvalidStateError or
// InvalidModificationError on a sender whose transport isn't settled yet.
// Live evidence: even after this function landed with
// degradationPreference='maintain-resolution' + scaleResolutionDownBy=1 + a
// 6 Mbps cap, UAT v24 still measured the delivered stream sitting at
// 228x246 for 60s before stepping to 307x328 (exactly tab/2) and holding --
// the resolution scaler was still active, meaning the pre-negotiation
// setParameters call had almost certainly been silently rejected the whole
// time (its .catch only warn()s, invisible in a headless run). Re-invoking
// this function (idempotent -- same params, same sender) once the
// transceiver has actually settled is what makes the constraints stick;
// the pre-negotiation call is kept anyway as a best-effort "in case this
// browser's sender accepts it early" no-op-on-failure attempt, now that
// failure is no longer invisible (see the lastError writes below).
//
// Each call site is naturally idempotent against re-invocation without any
// extra bookkeeping: the pre-negotiation and post-answer calls each fire at
// most once per capture cycle from their respective call sites, and the
// post-connected call is gated by the connectedConstraintsApplied flag
// newPeerConnection closes over -- so this never "stacks" repeated
// setParameters calls onto a single connectionstatechange listener.
function applyVideoSenderConstraints(pc, opts) {
  opts = opts || {};
  const context = opts.context || 'pre-negotiation';
  const recordSuccess = opts.recordSuccess === true;
  const logPrefix = 'applyVideoSenderConstraints[' + context + ']';

  const sender = pc.getSenders().find((s) => s.track && s.track.kind === 'video');
  if (!sender) {
    const msg = logPrefix + ': no RTCRtpSender found for the video track';
    warn(msg);
    window.__omnipusState.lastError = msg;
    return;
  }
  const videoTrack = sender.track;

  const cfg = window.__omnipusCapture || {};
  const maxBitrate =
    typeof cfg.maxVideoBitrate === 'number' && cfg.maxVideoBitrate > 0 ? cfg.maxVideoBitrate : DEFAULT_MAX_VIDEO_BITRATE_BPS;

  try {
    const params = sender.getParameters();
    if (!params.encodings || params.encodings.length === 0) {
      params.encodings = [{}];
    }
    params.encodings[0].maxBitrate = maxBitrate;
    // degradationPreference 'maintain-resolution' (previously 'balanced' --
    // see docs/internal/browser-viewport-input-rootcause-2026-07-31.md,
    // fault 2). The original rationale for 'balanced' was that the captured
    // tab can be anything from a text-heavy page to video playback, and
    // committing to either extreme permanently sacrifices the other content
    // type. That reasoning undersold the cost: live UAT measured the
    // delivered stream at 319x158 on a shared-cpu-2x box -- 'balanced' plus
    // the (then 2 Mbps) bitrate cap let VP8 trade resolution away under CPU
    // pressure, and it kept doing so far below anything a viewer would
    // accept. That downscale is (a) the reported blur -- 319x158 upscaled
    // ~2x into a ~561x587 panel -- and (b) was ORIGINALLY thought to be
    // load-bearing for the SPA's input coordinate mapping too, on the
    // assumption that capture pixels track the page's CSS viewport 1:1
    // (wave-plan key-decision-8). That input-correctness argument no longer
    // holds this fix up: the same fix-wave that raised this bitrate ceiling
    // also made the server independently rescale every pointer event from
    // whatever capture-frame size the client actually reports into the
    // tab's real CSS pixel space -- see pkg/tools/browser/live.go's
    // dispatchInput (which now rescales x/y per-event via
    // rescaleToCSSViewport/rescaleInputCoords using the client's reported
    // CaptureWidth/CaptureHeight, not an assumed 1:1). So a resolution
    // collapse here is no longer a click-goes-nowhere bug -- it is now
    // purely a sharpness/UX regression, handled as a second, independent
    // line of defense. 'maintain-resolution' is kept because blurry video
    // is still worth avoiding, and because avoiding a collapse in the first
    // place is simpler than trusting every input-consuming surface to
    // rescale correctly, not because input correctness depends on it.
    // Paired with the raised bitrate ceiling below (6 Mbps) to give VP8
    // headroom before it needs to degrade at all.
    params.degradationPreference = 'maintain-resolution';
    // Belt-and-braces against any lingering per-encoding down-scale --
    // degradationPreference governs the encoder's *runtime* trade-off, but
    // scaleResolutionDownBy is a separate, persistent per-encoding factor;
    // pin it to 1 so nothing above this layer is quietly resizing the
    // output out from under the resolution guarantee just established.
    params.encodings[0].scaleResolutionDownBy = 1;
    sender
      .setParameters(params)
      .then(() => {
        record(logPrefix + ': setParameters applied');
        if (recordSuccess) {
          // The exit-proof probe (CDP Runtime.evaluate) reads this back to
          // verify the constraints ACTUALLY stuck on a settled sender, not
          // merely that this file attempted to set them -- see the
          // window.__omnipusState.senderConstraints doc comment above.
          window.__omnipusState.senderConstraints = {
            degradationPreference: params.degradationPreference,
            maxBitrate: maxBitrate,
            scaleResolutionDownBy: params.encodings[0].scaleResolutionDownBy,
            appliedAt: Date.now(),
            context: context,
          };
        }
      })
      .catch((e) => {
        const msg = logPrefix + ': setParameters failed: ' + String(e);
        warn(msg, e);
        window.__omnipusState.lastError = msg;
      });
  } catch (e) {
    const msg = logPrefix + ': getParameters/setParameters failed: ' + String(e);
    warn(msg, e);
    window.__omnipusState.lastError = msg;
  }

  // contentHint 'detail' (REVERSED from 'motion', 2026-07-31 live evidence):
  // 'motion' tells libwebrtc this is camera-like video, which ENABLES the
  // quality scaler -- the encoder is allowed to reduce RESOLUTION under
  // CPU/bandwidth pressure. Measured on UAT v24/v25: the delivered stream sat
  // pinned at ~tab/2.7 (228px wide against a 615px tab) even with
  // degradationPreference 'maintain-resolution' + scaleResolutionDownBy 1
  // applied post-negotiation and a 4Mbps start-bitrate hint -- the scaler
  // kept winning, and 'motion' is what licenses it. 'detail' marks the track
  // as screen content whose per-frame legibility matters: libwebrtc disables
  // resolution scaling and sheds FRAMERATE under pressure instead. That is
  // the right trade here -- a text page at full resolution and 10fps is
  // usable; at 228px wide and 30fps it is not, and the original 'motion'
  // rationale (video-playback smoothness) predates the server-side input
  // rescale (live.go rescaleInputCoords), which now keeps clicks accurate
  // regardless. A per-page-adaptive hint remains possible future work.
  // Re-set on every call -- setting an unchanged value is a harmless no-op,
  // so this needs no context guard.
  try {
    videoTrack.contentHint = 'detail';
  } catch (e) {
    const msg = logPrefix + ': setting contentHint failed: ' + String(e);
    warn(msg, e);
    window.__omnipusState.lastError = msg;
  }
}

// mungeVideoStartBitrate appends libwebrtc's VP8 bitrate-hint fmtp
// parameters (x-google-start-bitrate/min-bitrate/max-bitrate, kbps) to the
// video m-section's VP8 fmtp line of a freshly-created offer, BEFORE it is
// handed to setLocalDescription.
//
// Live evidence (UAT v24, 2026-07-31,
// docs/internal/browser-viewport-input-rootcause-2026-07-31.md fault 2,
// fix-wave follow-up): even with the applyVideoSenderConstraints re-apply
// fix above landed, the delivered stream still sat at 228x246 for a full
// 60s before stepping to 307x328 (exactly tab/2, tab is 615x657) and
// holding there -- a textbook conservative bandwidth-estimation cold-start
// ramp, not the resolution-collapse-under-pressure symptom that fix
// addresses. This encoder's PeerConnection talks to the gateway's pion
// ingest over LOOPBACK -- there is no real, lossy network path to probe
// caution against, so spending 60+ seconds ramping up is pure waste on this
// deployment topology. x-google-start-bitrate tells libwebrtc's VP8 encoder
// to skip the slow-start climb and begin at the given rate; the paired
// min/max hints bound the adaptive range it can move within afterward --
// the RTCRtpSender.maxBitrate set in applyVideoSenderConstraints remains
// the authoritative hard ceiling regardless.
//
// These are libwebrtc-specific SDP fmtp hints (Chrome's own dialect) --
// harmless everywhere else, since a non-libwebrtc VP8 implementation on the
// other end simply ignores fmtp parameters it doesn't recognize rather than
// rejecting them.
function mungeVideoStartBitrate(sdp) {
  const START_KBPS = 4000;
  const MIN_KBPS = 1000;
  const MAX_KBPS = 6000;
  const HINTS = 'x-google-start-bitrate=' + START_KBPS + ';x-google-min-bitrate=' + MIN_KBPS + ';x-google-max-bitrate=' + MAX_KBPS;

  const lines = sdp.split('\r\n');

  // Payload type numbers are only unique WITHIN an m-section, not across
  // the whole SDP, so the video m-section's boundaries must be located
  // first -- otherwise a VP8 payload type that happens to collide with an
  // unrelated audio payload type number could get the wrong line munged.
  let videoStart = -1;
  let videoEnd = lines.length;
  for (let i = 0; i < lines.length; i++) {
    if (videoStart === -1 && lines[i].indexOf('m=video') === 0) {
      videoStart = i;
      continue;
    }
    if (videoStart !== -1 && lines[i].indexOf('m=') === 0) {
      videoEnd = i;
      break;
    }
  }
  if (videoStart === -1) {
    warn('mungeVideoStartBitrate: no m=video section found, leaving SDP untouched');
    return sdp;
  }

  // Find the VP8 payload type via its rtpmap line within the video section
  // only.
  const rtpmapRe = /^a=rtpmap:(\d+) VP8\/90000/i;
  let vp8Pt = null;
  let rtpmapIdx = -1;
  for (let i = videoStart; i < videoEnd; i++) {
    const m = lines[i].match(rtpmapRe);
    if (m) {
      vp8Pt = m[1];
      rtpmapIdx = i;
      break;
    }
  }
  if (!vp8Pt) {
    warn('mungeVideoStartBitrate: no VP8 payload type found in offer, leaving SDP untouched');
    return sdp;
  }

  // Append to an existing a=fmtp line for this payload type if present,
  // otherwise insert a fresh one directly after the rtpmap line.
  const fmtpPrefix = 'a=fmtp:' + vp8Pt + ' ';
  let fmtpIdx = -1;
  for (let i = videoStart; i < videoEnd; i++) {
    if (lines[i].indexOf(fmtpPrefix) === 0) {
      fmtpIdx = i;
      break;
    }
  }

  if (fmtpIdx !== -1) {
    lines[fmtpIdx] = lines[fmtpIdx] + ';' + HINTS;
  } else {
    lines.splice(rtpmapIdx + 1, 0, fmtpPrefix + HINTS);
  }

  return lines.join('\r\n');
}

function teardownCapture() {
  clearOfferAnswerTimeout();
  try {
    if (currentPC) currentPC.close();
  } catch (e) {
    warn('teardownCapture: pc.close() failed', e);
  }
  try {
    if (currentStream) currentStream.getTracks().forEach((t) => t.stop());
  } catch (e) {
    warn('teardownCapture: track stop failed', e);
  }
  currentPC = null;
  currentStream = null;
}

// waitIceGatheringComplete blocks until the offer is fully gathered
// (non-trickle ICE, per wave-plan decision #6), with a safety timeout so a
// stalled ICE gatherer can't hang the encoder forever.
function waitIceGatheringComplete(pc) {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === 'complete') {
      resolve();
      return;
    }
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        clearTimeout(timer);
        resolve();
      }
    };
    pc.addEventListener('icegatheringstatechange', check);
    const timer = setTimeout(() => {
      pc.removeEventListener('icegatheringstatechange', check);
      resolve();
    }, 10000);
  });
}

async function captureActiveTabStream() {
  const tabId = await findActiveTargetTab();
  record('captureActiveTabStream: targetTabId=' + tabId);

  const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tabId });
  record('captureActiveTabStream: got streamId');

  // W3 e2e finding: WITHOUT explicit size constraints, tabCapture delivers
  // its default 4:3 (800x600) frame and LETTERBOXES the (16:9, 1280x720)
  // tab viewport into it — black bars top/bottom, and the viewer's
  // videoWidth/Height no longer matches the page's CSS pixel space, which
  // breaks the wave-plan key-decision-8 assumption ("tab capture delivers
  // device pixels of the tab viewport, page_scale 1.0") that the SPA's
  // click/coordinate mapping relies on. Pin min==max to the captured tab's
  // own viewport size (chrome.tabs.get(...).width/height, CSS px — DPR is 1
  // in the managed headless Chrome) so the capture surface IS the viewport,
  // 1:1, no bars. Fallback 1280x720 matches the coordinator's
  // --window-size default.
  let capW = 1280;
  let capH = 720;
  try {
    const tab = await chrome.tabs.get(tabId);
    if (tab && tab.width && tab.height) {
      capW = tab.width;
      capH = tab.height;
    }
  } catch (e) {
    warn('captureActiveTabStream: chrome.tabs.get failed, using default capture size', e);
  }
  record('captureActiveTabStream: capture size ' + capW + 'x' + capH);

  // Self-consume: no processing constraints needed — tabCapture's defaults
  // are clean (AGC/EC/NS default OFF), unlike getDisplayMedia. See
  // wv1-spike-results.md Q2.
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
    video: {
      mandatory: {
        chromeMediaSource: 'tab',
        chromeMediaSourceId: streamId,
        minWidth: capW,
        minHeight: capH,
        maxWidth: capW,
        maxHeight: capH,
      },
    },
  });

  window.__omnipusState.videoTracks = stream.getVideoTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  window.__omnipusState.audioTracks = stream.getAudioTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  record(
    'captureActiveTabStream: MediaStream video=' +
      stream.getVideoTracks().length +
      ' audio=' +
      stream.getAudioTracks().length
  );
  return stream;
}

// DEFAULT_STUN_SERVER is the fallback used only when the gateway injecting
// this page's config predates the `stunServer` key entirely (back-compat).
const DEFAULT_STUN_SERVER = 'stun:stun.l.google.com:19302';

// resolveIceServers implements window.__omnipusCapture.stunServer's
// tri-state contract (fix-wave, LOW -- previously this hardcoded the Google
// public STUN server unconditionally, ignoring operator config):
//   - a non-empty string  -> use exactly that STUN server (operator config)
//   - an empty string (''), i.e. the key IS present but deliberately blank
//     -> host-candidates-only, no STUN server at all (iceServers: [])
//   - the key absent entirely (an older gateway build that predates this
//     config, or a verification harness that injects window.__omnipusCapture
//     without it) -> fall back to DEFAULT_STUN_SERVER, matching this file's
//     pre-fix-wave behavior exactly (back-compat).
function resolveIceServers() {
  const cfg = window.__omnipusCapture || {};
  if (typeof cfg.stunServer === 'string') {
    return cfg.stunServer === '' ? [] : [{ urls: cfg.stunServer }];
  }
  return [{ urls: DEFAULT_STUN_SERVER }];
}

function newPeerConnection() {
  const pc = new RTCPeerConnection({ iceServers: resolveIceServers() });
  // connectedConstraintsApplied is closed over per-PC (a fresh PC is created
  // for every capture/recapture cycle, see runCaptureAndOfferOnce), so this
  // guards only the FIRST 'connected' transition of THIS pc -- re-applying
  // applyVideoSenderConstraints on every later connectionstatechange
  // fluctuation (e.g. a transient disconnected->connected blip) would be
  // harmless (setParameters is idempotent) but pointless, so it's skipped.
  let connectedConstraintsApplied = false;
  pc.oniceconnectionstatechange = () => {
    window.__omnipusState.iceState = pc.iceConnectionState;
    record('ice state -> ' + pc.iceConnectionState);
    if (pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') {
      lastGoodIceTime = Date.now();
    }
  };
  pc.onconnectionstatechange = () => {
    window.__omnipusState.connState = pc.connectionState;
    record('connection state -> ' + pc.connectionState);
    // Second post-negotiation re-apply point (see applyVideoSenderConstraints's
    // doc comment) -- by 'connected' the transceiver is fully settled, so
    // this is the most reliable point for setParameters to actually stick.
    if (pc.connectionState === 'connected' && !connectedConstraintsApplied) {
      connectedConstraintsApplied = true;
      applyVideoSenderConstraints(pc, { context: 'post-connected', recordSuccess: true });
    }
  };
  return pc;
}

// runCaptureAndOffer performs (or re-performs, for recapture) the full
// capture -> offer flow: acquire the active tab's MediaStream, build a
// fresh non-trickle offer, and send it as a browser_capture_offer frame.
// Tears down any previous PC/stream first, so it is safe to call both for
// the initial connect and for a recapture control message — matching
// wv1-spike-results.md Q3's proven "new PC with fresh SSRCs replaces the
// old one" recovery pattern rather than incremental same-PC renegotiation.
// captureInFlight serialises runCaptureAndOffer (2026-07-31, found by review
// of the adaptive-viewport feature). This function has two awaits before it
// assigns currentPC/currentStream, and it had NO in-flight guard. That was
// practically unreachable while the only trigger was an active-tab switch (a
// rare, effectively serialized event) — but the viewport feature added a
// second, client-driven, HIGH-FREQUENCY trigger: every panel resize sends
// browser_viewport, and the gateway answers with a recapture. A CaptureSession
// is shared per AGENT, so two viewers of the same tab each push their own
// geometry with no cross-viewer coordination.
//
// Two overlapping calls race the same chrome.tabCapture pipeline for one tab.
// Chrome allows only one active tabCapture stream per tab, so the loser's
// getUserMedia throws — and handleControlFrame's catch closes the WHOLE ingest
// WebSocket, tearing down a session that was otherwise fine. Even without a
// throw, the loser's PeerConnection and MediaStreamTrack are orphaned, because
// teardownCapture only ever closes whatever the globals currently point at.
//
// Coalesce rather than queue: if a recapture arrives while one is running, set
// a rerun flag and let the in-flight call loop once more when it finishes. The
// geometry we want is always the LATEST one, so collapsing N pending
// recaptures into one extra pass is both correct and cheaper.
let captureInFlight = false;
let captureRerunRequested = false;

async function runCaptureAndOffer() {
  if (captureInFlight) {
    captureRerunRequested = true;
    record('runCaptureAndOffer: already in flight, coalescing into a rerun');
    return;
  }
  captureInFlight = true;
  try {
    do {
      captureRerunRequested = false;
      await runCaptureAndOfferOnce();
    } while (captureRerunRequested);
  } finally {
    captureInFlight = false;
  }
}

async function runCaptureAndOfferOnce() {
  teardownCapture();
  setStatus('capturing');

  const stream = await captureActiveTabStream();
  currentStream = stream;

  setStatus('offering');
  const pc = newPeerConnection();
  currentPC = pc;
  stream.getTracks().forEach((t) => pc.addTrack(t, stream));

  // Fix-wave finding 4: cap bitrate + set encoding hints on the video
  // sender now that addTrack has created it. Video-only (audio/Opus has no
  // equivalent overdrive risk here). This pre-negotiation call is
  // best-effort -- see applyVideoSenderConstraints's doc comment for why
  // the post-answer/post-connected re-applies below are the ones that
  // actually matter.
  const videoTrack = stream.getVideoTracks()[0];
  if (videoTrack) {
    applyVideoSenderConstraints(pc, { context: 'pre-negotiation' });
  }

  const offer = await pc.createOffer();
  offer.sdp = mungeVideoStartBitrate(offer.sdp);
  await pc.setLocalDescription(offer);
  await waitIceGatheringComplete(pc);

  record('runCaptureAndOffer: sending browser_capture_offer');
  sendFrame({ type: 'browser_capture_offer', sdp: pc.localDescription.sdp });
  armOfferAnswerTimeout();
}

// ---- signaling ----------------------------------------------------------------

function sendFrame(frame) {
  if (!ws || ws.readyState !== WebSocket.OPEN) {
    warn('sendFrame: WS not open, dropping frame', frame.type);
    return;
  }
  ws.send(JSON.stringify(frame));
}

// handleControlFrame handles the two SERVER -> CLIENT control actions
// (recapture, shutdown). `ping` is this page's own CLIENT -> SERVER health
// beacon (see startPingBeacon) — the gateway never sends ping to us, and
// there is no `pong` action in the schema, so neither is handled as an
// inbound case here.
async function handleControlFrame(msg) {
  const action = msg.action;
  record('control frame: action=' + action + (msg.reason ? ' reason=' + msg.reason : ''));

  if (action === 'recapture') {
    const forWs = ws;
    try {
      await runCaptureAndOffer();
    } catch (e) {
      window.__omnipusState.lastError = String(e);
      record('recapture FAILED: ' + e);
      // fix-wave (HIGH): without closing the socket here, a failed
      // recapture left the ping beacon running forever with no way for the
      // gateway to ever learn capture died -- the panel would freeze
      // permanently (never recovers, never retries). Closing routes through
      // the SAME 'close' handler every other disconnect uses, so the
      // existing scheduleReconnect backoff/reconnect loop -- the documented
      // recovery path -- is what actually recovers; no new recovery path is
      // introduced. Guard against closing a newer socket, matching
      // armOfferAnswerTimeout's forWs pattern above, in case a reconnect
      // already replaced `ws` while this recapture attempt was in flight.
      if (ws === forWs) {
        try {
          forWs.close();
        } catch (closeErr) {
          /* ignore */
        }
      }
    }
    return;
  }

  if (action === 'shutdown') {
    shuttingDown = true;
    setStatus('shutdown');
    stopWatchdog();
    stopPingBeacon();
    teardownCapture();
    try {
      ws.close();
    } catch (e) {
      /* ignore */
    }
    return;
  }

  warn('unexpected control action from server, ignoring:', action);
}

async function handleWsMessage(raw) {
  let msg;
  try {
    msg = JSON.parse(raw);
  } catch (e) {
    warn('bad frame JSON, ignoring:', e);
    return;
  }

  switch (msg.type) {
    case 'browser_capture_answer':
      // Clear the offer-answer timeout as soon as a response arrives at
      // all -- even if setRemoteDescription below then fails, that's a
      // different (already-logged) failure class, not the "silently
      // wedged, no server response" case armOfferAnswerTimeout guards.
      clearOfferAnswerTimeout();
      if (!currentPC) {
        warn('browser_capture_answer received with no active PeerConnection, ignoring');
        return;
      }
      try {
        await currentPC.setRemoteDescription({ type: 'answer', sdp: msg.sdp });
        setStatus('connected');
        lastGoodIceTime = Date.now();
        record('applied browser_capture_answer, PC connecting');
        // First post-negotiation re-apply point (see
        // applyVideoSenderConstraints's doc comment): the remote answer is
        // now applied, so the transceiver's direction/codecs are settled
        // even though ICE/DTLS may still be finishing up. A second
        // re-apply follows at pc.connectionState === 'connected'
        // (newPeerConnection's onconnectionstatechange) for whichever
        // browser needs the transport fully up before setParameters sticks.
        applyVideoSenderConstraints(currentPC, { context: 'post-answer', recordSuccess: true });
      } catch (e) {
        window.__omnipusState.lastError = String(e);
        record('setRemoteDescription(answer) FAILED: ' + e);
      }
      break;

    case 'browser_capture_control':
      await handleControlFrame(msg);
      break;

    case 'error':
      // ErrorFrame (contracts/components/schemas/ErrorFrame.yaml) — server
      // -> client. Surface it for debugging. NOTE this is NOT what an
      // invalid hello token produces — a rejected hello closes the
      // connection with NO frame at all (see this file's header doc
      // comment: "success is silent ... the only observable signal for a
      // REJECTED hello is the WS closing"). The one actual producer of this
      // frame is a rejected browser_capture_offer: the gateway sends this
      // frame, THEN closes the connection.
      window.__omnipusState.lastError = msg.message || 'error frame with no message';
      record('server error frame: ' + window.__omnipusState.lastError);
      break;

    default:
      warn('unknown frame type, ignoring:', msg.type);
  }
}

function connectOnce(cfg) {
  setStatus('connecting');
  window.__omnipusState.wsState = 'connecting';
  ws = new WebSocket(cfg.ingestUrl);

  ws.addEventListener('open', async () => {
    window.__omnipusState.wsState = 'open';
    reconnectDelayMs = 1000; // reset backoff on a clean connect
    record('WS open, sending hello');
    setStatus('hello_sent');
    sendFrame({ type: 'browser_capture_hello', token: cfg.token, ext_version: extVersion() });
    startWatchdogOnce();
    startPingBeacon();
    const forWs = ws;

    // browser_capture_hello is client -> server only (no ack frame) — a
    // rejected token surfaces as the gateway closing the connection, not a
    // nack. Proceed straight to capture + offer; the reconnect watchdog
    // handles the rejection case via the resulting WS close.
    try {
      await runCaptureAndOffer();
    } catch (e) {
      window.__omnipusState.lastError = String(e);
      setStatus('error');
      record('initial capture FAILED: ' + e);
      // fix-wave (HIGH): without closing the socket here, an initial capture
      // failure (no capturable tab, getUserMedia denied, etc.) left the page
      // silently wedged in 'error' forever -- the ping beacon kept running
      // and the gateway had no way to learn capture never started. Closing
      // engages the SAME scheduleReconnect backoff/reconnect loop as every
      // other disconnect (the documented recovery path) -- a fresh connect
      // re-attempts capture. Guard against closing a newer socket, matching
      // armOfferAnswerTimeout's forWs pattern above.
      if (ws === forWs) {
        try {
          forWs.close();
        } catch (closeErr) {
          /* ignore */
        }
      }
    }
  });

  ws.addEventListener('message', (ev) => {
    handleWsMessage(typeof ev.data === 'string' ? ev.data : '');
  });

  ws.addEventListener('close', () => {
    window.__omnipusState.wsState = 'closed';
    record('WS closed');
    stopPingBeacon();
    if (!shuttingDown) scheduleReconnect(cfg);
  });

  ws.addEventListener('error', (e) => {
    warn('WS error', e && e.message ? e.message : e);
  });
}

// scheduleReconnect drives the "reconnect watchdog ... full restart loop
// with backoff" requirement for a dropped WS (as opposed to a bad-ICE
// restart, which forces the WS closed and lands here too).
function scheduleReconnect(cfg) {
  if (shuttingDown || reconnectTimer) return;
  setStatus('reconnecting');
  window.__omnipusState.reconnectAttempts += 1;
  record('scheduling reconnect in ' + reconnectDelayMs + 'ms');
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    teardownCapture();
    connectOnce(cfg);
  }, reconnectDelayMs);
  reconnectDelayMs = Math.min(reconnectDelayMs * 2, RECONNECT_MAX_DELAY_MS);
}

// ---- watchdog -------------------------------------------------------------

function startWatchdogOnce() {
  if (watchdogTimer) return;
  watchdogTimer = setInterval(() => {
    if (shuttingDown || !currentPC) return;
    const s = currentPC.iceConnectionState;
    const bad = s === 'failed' || s === 'disconnected' || s === 'closed';
    if (bad && Date.now() - lastGoodIceTime > ICE_BAD_GRACE_MS) {
      record('watchdog: ICE ' + s + ' for >' + ICE_BAD_GRACE_MS + 'ms, forcing full restart');
      lastGoodIceTime = Date.now(); // avoid a hot loop while the restart runs
      try {
        ws.close(); // the close handler drives the actual reconnect
      } catch (e) {
        /* ignore */
      }
    }
  }, 2000);
}

function stopWatchdog() {
  if (watchdogTimer) {
    clearInterval(watchdogTimer);
    watchdogTimer = null;
  }
}

// startPingBeacon sends this page's own CLIENT -> SERVER health/liveness
// beacon (browser_capture_control action=ping) on a fixed interval — the
// "encoder page's periodic health beacon / reconnect-watchdog signal" the
// schema describes. Distinct from the ICE-state watchdog above: this beacon
// exists so the gateway can detect a hung-but-still-WS-open encoder even
// when no ICE state change would otherwise reveal it.
function startPingBeacon() {
  if (pingBeaconTimer) return;
  pingBeaconTimer = setInterval(() => {
    if (shuttingDown) return;
    sendFrame({ type: 'browser_capture_control', action: 'ping' });
  }, PING_BEACON_INTERVAL_MS);
}

function stopPingBeacon() {
  if (pingBeaconTimer) {
    clearInterval(pingBeaconTimer);
    pingBeaconTimer = null;
  }
}

// ---- entry point ------------------------------------------------------------

// window.__omnipusStart is the single entry point. In production, config
// is present at parse time (injected via addScriptToEvaluateOnNewDocument
// before this script tag runs) so this file self-starts below. It is also
// exposed for explicit invocation — e.g. a verification harness that
// injects window.__omnipusCapture AFTER navigation, mirroring the spike's
// startEncoding() call pattern (spikes/wv1-webrtc/q4-bidir/encoder/run.js).
window.__omnipusStart = async function () {
  try {
    const cfg = readConfig();
    connectOnce(cfg);
    return { ok: true };
  } catch (e) {
    window.__omnipusState.lastError = String(e);
    setStatus('error');
    record('start FAILED: ' + e);
    return { ok: false, error: String(e) };
  }
};

if (window.__omnipusCapture) {
  window.__omnipusStart();
} else {
  record('waiting for window.__omnipusCapture to be set (call window.__omnipusStart() once ready)');
}

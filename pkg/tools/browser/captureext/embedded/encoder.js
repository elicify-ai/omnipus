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

// DEFAULT_MAX_VIDEO_BITRATE_BPS (fix-wave finding 4, "overdrive"): the
// tabCapture MediaStream has no bandwidth ceiling of its own, so an
// unconstrained VP8 encoder will burn as much CPU/bandwidth as the content
// demands -- a major contributor to the reported pod-CPU-saturation/choppy-
// input UAT symptom under heavy (video-playing) pages. 2 Mbps is generous
// for a 1280x720 browsing UI (comfortably covers text and moving video
// alike) while giving the encoder a real ceiling to degrade against.
// Overridable per-install via window.__omnipusCapture.maxVideoBitrate
// (bits/sec) for future config -- see the file header for the
// injection mechanism; unset/invalid falls back to this default.
const DEFAULT_MAX_VIDEO_BITRATE_BPS = 2000000;

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
// bitrate and sets encoding hints AFTER pc.addTrack has created its
// RTCRtpSender for videoTrack. Errors are logged, not thrown -- a failed
// setParameters/contentHint assignment should degrade to "uncapped
// bitrate", not abort the whole capture/offer flow.
function applyVideoSenderConstraints(pc, videoTrack) {
  const sender = pc.getSenders().find((s) => s.track === videoTrack);
  if (!sender) {
    warn('applyVideoSenderConstraints: no RTCRtpSender found for the video track');
    return;
  }

  const cfg = window.__omnipusCapture || {};
  const maxBitrate =
    typeof cfg.maxVideoBitrate === 'number' && cfg.maxVideoBitrate > 0 ? cfg.maxVideoBitrate : DEFAULT_MAX_VIDEO_BITRATE_BPS;

  try {
    const params = sender.getParameters();
    if (!params.encodings || params.encodings.length === 0) {
      params.encodings = [{}];
    }
    params.encodings[0].maxBitrate = maxBitrate;
    // degradationPreference 'balanced' (not 'maintain-framerate' or
    // 'maintain-resolution'): the captured tab can be anything from a
    // text-heavy page (wants resolution to stay legible) to video playback
    // (wants framerate to stay smooth) -- committing to either extreme
    // permanently sacrifices the other content type. 'balanced' lets the
    // encoder trade resolution/framerate against CURRENT conditions instead
    // of a fixed bias, which pairs with this bitrate cap and the gateway's
    // screencast-pause fix (ADR-047 fix-wave finding 3, which removes the
    // competing CDP JPEG screencast's CPU draw while WebRTC is active) to
    // keep the CPU/bandwidth budget under control without a permanent
    // quality cliff for whichever content type ISN'T currently on screen.
    params.degradationPreference = 'balanced';
    sender.setParameters(params).catch((e) => {
      warn('applyVideoSenderConstraints: setParameters failed', e);
    });
  } catch (e) {
    warn('applyVideoSenderConstraints: getParameters/setParameters failed', e);
  }

  // contentHint 'motion' (not 'detail'): asks the VP8 encoder to favor
  // smooth motion over per-frame sharpness -- directly addresses this
  // fix-wave's reported freeze/choppiness symptoms on real video content.
  // Tradeoff: text-heavy (non-video) pages may render very slightly softer
  // under 'motion' than 'detail' would produce. Accepted for now given the
  // UAT content was video playback; a future per-page-adaptive hint (e.g.
  // detecting an actively-playing <video> element vs a static page) is
  // possible but out of scope for this fix.
  try {
    videoTrack.contentHint = 'motion';
  } catch (e) {
    warn('applyVideoSenderConstraints: setting contentHint failed', e);
  }
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
async function runCaptureAndOffer() {
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
  // equivalent overdrive risk here).
  const videoTrack = stream.getVideoTracks()[0];
  if (videoTrack) {
    applyVideoSenderConstraints(pc, videoTrack);
  }

  const offer = await pc.createOffer();
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

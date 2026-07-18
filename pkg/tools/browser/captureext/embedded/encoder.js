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

function teardownCapture() {
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

  // Self-consume: no processing constraints needed — tabCapture's defaults
  // are clean (AGC/EC/NS default OFF), unlike getDisplayMedia. See
  // wv1-spike-results.md Q2.
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
    video: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
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

function newPeerConnection() {
  const pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] });
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

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await waitIceGatheringComplete(pc);

  record('runCaptureAndOffer: sending browser_capture_offer');
  sendFrame({ type: 'browser_capture_offer', sdp: pc.localDescription.sdp });
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
    try {
      await runCaptureAndOffer();
    } catch (e) {
      window.__omnipusState.lastError = String(e);
      record('recapture FAILED: ' + e);
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
      // -> client, does NOT terminate the connection. Surface it for
      // debugging; the gateway is expected to close the connection
      // separately if the error was fatal (e.g. an invalid hello token).
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

// window.__omnipusMeasureAudioRMS is a debug/verification helper (like
// window.__omnipusState above) — it is never used by the production
// signaling flow. It samples the currently-captured audio track's RMS
// level over `durationMs` milliseconds, for external verification (CDP
// Runtime.evaluate) that tabCapture is delivering real, non-silent audio
// samples rather than silence or a stalled track.
//
// Uses AnalyserNode.getFloatTimeDomainData polling (NOT a
// ScriptProcessorNode wired to ac.destination) — this is the pattern
// wv1-spike-results.md's Q2 spike proved works for a live tabCapture
// MediaStreamTrack. A ScriptProcessorNode requires an active render pull
// reaching ac.destination to receive non-silent buffers; in a headless
// context whose AudioContext starts 'suspended', that pull can end up
// delivering the callback on schedule with all-zero buffers. AnalyserNode
// polling has no such dependency on the destination graph.
//
// KNOWN LIMITATION (found during W1-E real-Chrome verification, not in the
// original spike): once currentStream's audio track has been added to an
// RTCPeerConnection via pc.addTrack() (which runCaptureAndOffer does
// immediately after capture), a NEW MediaStreamSource wrapping that same
// track reads back all-zero samples here, even though the track is
// genuinely live and the PC forwards real RTP once negotiated (proven by
// wv1-spike-results.md Q3/Q4 measuring RMS on the FAR end after a real
// answer+ICE connect). So this helper only reads real levels when called
// BEFORE the capture flow reaches pc.addTrack() — i.e. there is no window
// to call it usefully after __omnipusStart() has already run to
// completion. It remains useful for a caller that captures independently
// (see the verification harness's "raw pre-PC audio RMS probe" for the
// pattern) or for future debugging hooks with an earlier call site.
window.__omnipusMeasureAudioRMS = async function (durationMs) {
  if (!currentStream || currentStream.getAudioTracks().length === 0) {
    throw new Error('no active audio track to measure');
  }
  const audioCtx = new (window.AudioContext || window.webkitAudioContext)();
  await audioCtx.resume();
  try {
    const source = audioCtx.createMediaStreamSource(new MediaStream(currentStream.getAudioTracks()));
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 2048;
    source.connect(analyser);

    const buf = new Float32Array(analyser.fftSize);
    const samples = [];
    const pollIntervalMs = 100;
    const iterations = Math.max(1, Math.round(durationMs / pollIntervalMs));
    for (let i = 0; i < iterations; i++) {
      await new Promise((r) => setTimeout(r, pollIntervalMs));
      analyser.getFloatTimeDomainData(buf);
      let sumSquares = 0;
      for (let j = 0; j < buf.length; j++) sumSquares += buf[j] * buf[j];
      samples.push(Math.sqrt(sumSquares / buf.length));
    }

    const mean = samples.reduce((a, b) => a + b, 0) / samples.length;
    const max = Math.max(...samples);
    return { rms: mean, rmsMax: max, samples, sampleCount: samples.length };
  } finally {
    try {
      audioCtx.close();
    } catch (e) {
      /* ignore */
    }
  }
};

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

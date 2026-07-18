// Q3 encoder extension page. Extension PAGE context (not the service
// worker) -> full chrome.* access, trivially CDP-evaluable (same pattern
// Q2 proved in ext/invoke.js's doCaptureSelfConsume()).
//
// Flow: find the /metronome tab by URL/title -> chrome.tabCapture
// self-consume (no consumerTabId, capture directly in THIS extension page)
// -> getUserMedia(tab) -> RTCPeerConnection with both tracks added ->
// offer/answer against ws1spike3's POST /ingest.
'use strict';

window.__state = { status: 'idle', history: [] };

function record(evt) {
  window.__state.history.push({ t: Date.now(), evt: String(evt) });
  if (window.__state.history.length > 200) window.__state.history.shift();
}

let currentPC = null;
let currentStream = null;
let lastGoodTime = Date.now();
let watchdogStarted = false;

async function findMetronomeTab() {
  const tabs = await chrome.tabs.query({});
  const t = tabs.find((tab) => (tab.url || '').includes('/metronome') || (tab.title || '').includes('WV1 Metronome'));
  if (!t) throw new Error('metronome tab not found (open http://localhost:8080/metronome first): tabs=' + JSON.stringify(tabs.map((x) => ({ id: x.id, url: x.url, title: x.title }))));
  return t.id;
}

function waitGathering(pc) {
  return new Promise((resolve) => {
    if (pc.iceGatheringState === 'complete') { resolve(); return; }
    const check = () => {
      if (pc.iceGatheringState === 'complete') {
        pc.removeEventListener('icegatheringstatechange', check);
        clearTimeout(timer);
        resolve();
      }
    };
    pc.addEventListener('icegatheringstatechange', check);
    const timer = setTimeout(resolve, 10000);
  });
}

function teardown() {
  try { if (currentPC) currentPC.close(); } catch (e) { /* ignore */ }
  try { if (currentStream) currentStream.getTracks().forEach((t) => t.stop()); } catch (e) { /* ignore */ }
  currentPC = null;
  currentStream = null;
}

async function connectOnce() {
  record('connectOnce: finding metronome tab');
  const tabId = await findMetronomeTab();
  record('connectOnce: metronome tabId=' + tabId);

  const streamId = await chrome.tabCapture.getMediaStreamId({ targetTabId: tabId });
  record('connectOnce: got streamId');
  const stream = await navigator.mediaDevices.getUserMedia({
    audio: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
    video: { mandatory: { chromeMediaSource: 'tab', chromeMediaSourceId: streamId } },
  });
  currentStream = stream;
  window.__state.videoTracks = stream.getVideoTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  window.__state.audioTracks = stream.getAudioTracks().map((t) => ({ label: t.label, settings: t.getSettings() }));
  record('connectOnce: got MediaStream video=' + stream.getVideoTracks().length + ' audio=' + stream.getAudioTracks().length);

  const pc = new RTCPeerConnection({ iceServers: [{ urls: 'stun:stun.l.google.com:19302' }] });
  currentPC = pc;
  stream.getTracks().forEach((t) => pc.addTrack(t, stream));

  pc.oniceconnectionstatechange = () => {
    window.__state.iceState = pc.iceConnectionState;
    record('ice ' + pc.iceConnectionState);
    if (pc.iceConnectionState === 'connected' || pc.iceConnectionState === 'completed') lastGoodTime = Date.now();
  };
  pc.onconnectionstatechange = () => { window.__state.connState = pc.connectionState; record('conn ' + pc.connectionState); };

  const offer = await pc.createOffer();
  await pc.setLocalDescription(offer);
  await waitGathering(pc);
  record('connectOnce: local gathering complete, POSTing /ingest');

  const res = await fetch('http://localhost:8080/ingest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ sdp: pc.localDescription.sdp, type: pc.localDescription.type }),
  });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(`ingest POST failed: HTTP ${res.status}: ${body}`);
  }
  const answer = await res.json();
  await pc.setRemoteDescription(answer);
  window.__state.status = 'connected';
  record('connectOnce: ingest connected');
  lastGoodTime = Date.now();
  startWatchdogOnce();
}

// Simple reconnect-safety watchdog (spike-grade, per task guidance): if ICE
// sits in failed/disconnected/closed for more than 10s, tear down and
// reconnect from scratch (fresh tabCapture + fresh RTCPeerConnection +
// fresh /ingest POST).
function startWatchdogOnce() {
  if (watchdogStarted) return;
  watchdogStarted = true;
  setInterval(async () => {
    if (!currentPC) return;
    const s = currentPC.iceConnectionState;
    const bad = s === 'failed' || s === 'disconnected' || s === 'closed';
    if (bad && Date.now() - lastGoodTime > 10000) {
      record('watchdog: reconnecting after ' + s + ' for >10s');
      window.__state.status = 'reconnecting';
      teardown();
      try {
        await connectOnce();
      } catch (e) {
        window.__state.status = 'error';
        window.__state.lastError = String(e);
        record('watchdog reconnect FAILED: ' + e);
        lastGoodTime = Date.now(); // avoid a hot retry loop; try again in >=10s
      }
    }
  }, 2000);
}

window.startEncoding = async function () {
  window.__state.status = 'starting';
  try {
    await connectOnce();
    return { ok: true };
  } catch (e) {
    window.__state.status = 'error';
    window.__state.lastError = String(e);
    record('startEncoding FAILED: ' + e);
    return { ok: false, error: String(e) };
  }
};

// T3 — tabCapture EXTENSION PATH on new-headless full Chrome.
// Loads the MV3 extension via --load-extension (Chrome for Testing keeps this flag),
// then tries to obtain a stream id via chrome.tabCapture.getMediaStreamId({targetTabId, consumerTabId})
// and consume it in the controller page via getUserMedia(chromeMediaSource:'tab').
//
// Invocation strategies for the "extension must be invoked / activeTab" gate:
//   A) direct: CDP Runtime.evaluate `doCapture()` on the extension service worker target
//   B) allowlist: relaunch with --allowlisted-extension-id=<id> then repeat A
//   C) hotkey: CDP Input.dispatchKeyEvent Ctrl+Shift+Y (_execute_action) on the active tab
'use strict';
const path = require('path');
const { CDP, sleep } = require('./cdp');
const { launch, kill } = require('./launch');

const EXT_DIR = path.join(__dirname, 'ext');

async function findSW(cdp, timeoutMs = 10000) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeoutMs) {
    const { targetInfos } = await cdp.send('Target.getTargets');
    const sw = targetInfos.find(t => (t.type === 'service_worker' || t.type === 'worker') && t.url.startsWith('chrome-extension://'));
    if (sw) return sw;
    await sleep(300);
  }
  return null;
}

async function pollResult(cdp, sessionId, timeoutMs = 20000) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeoutMs) {
    const r = await cdp.eval(sessionId, 'JSON.parse(JSON.stringify(window.__result))');
    if (r.state === 'done' || r.state === 'error') return r;
    await sleep(500);
  }
  const last = await cdp.eval(sessionId, 'JSON.parse(JSON.stringify(window.__result))');
  last.timedOut = true;
  return last;
}

function summarize(tag, capOut, r) {
  console.log(`\n=== T3 [${tag}] SW doCapture -> ${JSON.stringify(capOut)}`);
  if (!r) { console.log(`VERDICT[${tag}]: no consumption result`); return; }
  console.log(JSON.stringify(r, null, 1));
  const a = r.audio, v = r.video;
  console.log(`VERDICT[${tag}]: state=${r.state}${r.timedOut ? ' TIMEOUT' : ''} ` +
    `videoTracks=${(r.videoTracks || []).length} audioTracks=${(r.audioTracks || []).length} ` +
    (v ? `frames=${v.frames} fps=${v.fps.toFixed(1)} ` : 'video=none ') +
    (a ? `rmsMean=${a.rmsMean} rmsMax=${a.rmsMax} nonzero=${a.nonzeroCount}/32` : 'audio=none') +
    (r.error ? ` error=${r.error}` : ''));
}

async function runScenario(tag, extraFlags) {
  const { child, port } = launch([`--load-extension=${EXT_DIR}`, ...extraFlags], `t3-${tag}`);
  let extId = null;
  try {
    const cdp = await CDP.connect(port);
    const sw = await findSW(cdp);
    if (!sw) { console.log(`\n=== T3 [${tag}] extension service worker NOT found (extension did not load)`); return { extId: null }; }
    extId = new URL(sw.url).host;
    console.log(`\n=== T3 [${tag}] extension loaded id=${extId} swTarget=${sw.targetId}`);
    const { sessionId: swSession } = await cdp.send('Target.attachToTarget', { targetId: sw.targetId, flatten: true });
    await cdp.send('Runtime.enable', {}, swSession);

    await cdp.newPage('http://127.0.0.1:8090/tone.html');
    await sleep(800);
    const { sessionId: ctlSession } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
    await sleep(500);

    // Strategy A: call doCapture() directly on the SW
    let capOut;
    try {
      capOut = await cdp.eval(swSession, 'doCapture()');
    } catch (e) {
      capOut = { harnessError: e.message };
    }

    let r = null;
    if (capOut && capOut.ok) {
      r = await pollResult(cdp, ctlSession);
    } else {
      // Strategy C: hotkey _execute_action (Ctrl+Shift+Y) on the controller (active) tab
      console.log(`[${tag}] direct SW call failed; trying hotkey _execute_action`);
      for (const type of ['keyDown', 'keyUp']) {
        await cdp.send('Input.dispatchKeyEvent', {
          type, modifiers: 6, // Ctrl(2)+Shift(8)? NOTE: Ctrl=2, Shift=8 -> 10; 6 = Ctrl+Alt. fix below
        }, ctlSession).catch(() => {});
      }
      // proper: Ctrl=2, Shift=8 => 10, key 'Y' code 'KeyY' keyCode 89
      for (const type of ['rawKeyDown', 'keyUp']) {
        await cdp.send('Input.dispatchKeyEvent', {
          type, modifiers: 10, key: 'Y', code: 'KeyY', windowsVirtualKeyCode: 89, nativeVirtualKeyCode: 89,
        }, ctlSession).catch((e) => console.log(`[${tag}] hotkey dispatch err: ${e.message}`));
      }
      await sleep(1500);
      const last = await cdp.eval(swSession, 'globalThis.__last || null').catch(() => null);
      console.log(`[${tag}] after hotkey, sw __last =`, JSON.stringify(last));
      if (last && last.ok) r = await pollResult(cdp, ctlSession);
    }
    summarize(tag, capOut, r);
    cdp.close();
    return { extId };
  } catch (e) {
    console.log(`\n=== T3 [${tag}] HARNESS FAILURE: ${e.message}`);
    return { extId };
  } finally {
    await kill(child);
  }
}

(async () => {
  // Scenario 1: plain --load-extension, direct SW invocation
  const { extId } = await runScenario('direct', []);
  // Scenario 2: allowlisted extension id (id is stable for a fixed unpacked path)
  if (extId) {
    await runScenario('allowlisted', [`--allowlisted-extension-id=${extId}`]);
  } else {
    console.log('skip allowlisted scenario — no extension id from scenario 1');
  }
})();

// T4 — NAV SURVIVAL: does a tabCapture stream keep flowing when the captured tab
// navigates? Capture the tone tab (440Hz+WAV, expected RMS ~0.31), measure, then
// Page.navigate the tone tab to tone.html?freq=880&gain=0.5&wav=0 (pure sine,
// expected RMS = 0.5/sqrt(2) ~ 0.354) and re-measure the SAME stream.
'use strict';
const path = require('path');
const { PipeCDP, sleep } = require('./cdp');
const { launchPipe, kill } = require('./launch');

const EXT_DIR = path.join(__dirname, 'ext');
const KNOWN_ID = 'gdogapnbcanodbmgbhchhhdnohhlgljf';

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

function brief(r) {
  const a = r.audio, v = r.video;
  return `state=${r.state}${r.timedOut ? ' TIMEOUT' : ''} ` +
    (v ? `frames=${v.frames} fps=${v.fps.toFixed(1)} ` : 'video=none ') +
    (a ? `rmsMean=${a.rmsMean} rmsMax=${a.rmsMax} nonzero=${a.nonzeroCount}/32 ` : 'audio=none ') +
    `tracks(v/a)=${(r.videoTracks || []).length}/${(r.audioTracks || []).length}` +
    (r.error ? ` error=${r.error}` : '');
}

(async () => {
  const { child } = launchPipe([`--allowlisted-extension-id=${KNOWN_ID}`], 't4-nav');
  try {
    const cdp = new PipeCDP(child);
    await cdp.send('Browser.getVersion');
    const { id: extId } = await cdp.send('Extensions.loadUnpacked', { path: EXT_DIR });
    console.log(`ext id=${extId}`);

    const tone = await cdp.newPage('http://127.0.0.1:8090/tone.html');
    await sleep(800);
    const { sessionId: ctlSession } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
    await sleep(300);
    const { sessionId: extSession } = await cdp.newPage(`chrome-extension://${extId}/invoke.html`);
    await sleep(500);

    const capOut = await cdp.eval(extSession, 'doCapture()', { userGesture: true });
    console.log(`doCapture -> ok=${capOut.ok}${capOut.error ? ' err=' + capOut.error : ''}`);
    if (!capOut.ok) throw new Error('capture failed');

    const before = await pollResult(cdp, ctlSession);
    console.log(`\nBEFORE nav: ${brief(before)}`);
    console.log(`BEFORE rms: ${JSON.stringify(before.audio && before.audio.rmsSamples)}`);

    // Navigate the captured tab to the new signature
    await cdp.send('Page.navigate', { url: 'http://127.0.0.1:8090/tone.html?freq=880&gain=0.5&wav=0' }, tone.sessionId);
    await sleep(1500);

    await cdp.eval(ctlSession, 'window.remeasure(); "fired"');
    const after = await pollResult(cdp, ctlSession);
    console.log(`\nAFTER nav: ${brief(after)}`);
    console.log(`AFTER rms: ${JSON.stringify(after.audio && after.audio.rmsSamples)}`);

    const trackState = await cdp.eval(ctlSession,
      'window.__stream.getTracks().map(t => t.kind + ":" + t.readyState + ":muted=" + t.muted).join(", ")');
    console.log(`track states after nav: ${trackState}`);

    const expBefore = 0.313, expAfter = 0.354;
    console.log(`\nEXPECTED rmsMean before ~${expBefore} (440Hz g=.25 + wav) / after ~${expAfter} (880Hz g=.5 pure)`);
    cdp.close();
  } catch (e) {
    console.log(`T4 HARNESS FAILURE: ${e.message}`);
  } finally {
    await kill(child);
  }
})();

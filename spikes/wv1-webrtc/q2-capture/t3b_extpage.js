// T3b — tabCapture via an extension PAGE (chrome-extension://<id>/invoke.html opened
// as a tab). Avoids MV3 service-worker eval flakiness. Two scenarios: plain and
// --allowlisted-extension-id. Also probes the SW context for diagnostics.
'use strict';
const path = require('path');
const { CDP, sleep } = require('./cdp');
const { launch, kill } = require('./launch');

const EXT_DIR = path.join(__dirname, 'ext');

async function findExtId(cdp, timeoutMs = 10000) {
  const t0 = Date.now();
  while (Date.now() - t0 < timeoutMs) {
    const { targetInfos } = await cdp.send('Target.getTargets');
    const t = targetInfos.find(x => x.url.startsWith('chrome-extension://'));
    if (t) return { id: new URL(t.url).host, target: t };
    await sleep(300);
  }
  return { id: null };
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

function summarize(tag, r) {
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
  const { child, port } = launch([`--load-extension=${EXT_DIR}`, ...extraFlags], `t3b-${tag}`);
  let extId = null;
  try {
    const cdp = await CDP.connect(port);
    const found = await findExtId(cdp);
    extId = found.id;
    if (!extId) { console.log(`\n=== T3b [${tag}] extension NOT found in targets`); return { extId: null }; }
    console.log(`\n=== T3b [${tag}] ext id=${extId} (via ${found.target.type} ${found.target.url})`);

    // SW diagnostics if present
    const { targetInfos } = await cdp.send('Target.getTargets');
    const sw = targetInfos.find(t => t.type === 'service_worker' && t.url.includes(extId));
    if (sw) {
      try {
        const { sessionId: swS } = await cdp.send('Target.attachToTarget', { targetId: sw.targetId, flatten: true });
        const diag = await cdp.eval(swS, '(typeof doCapture) + "|" + (typeof globalThis.doCapture) + "|" + (typeof chrome) + "|" + (typeof chrome.tabCapture)').catch(e => 'diagfail: ' + e.message);
        console.log(`[${tag}] SW diag (doCapture|globalThis.doCapture|chrome|tabCapture): ${diag}`);
      } catch (e) { console.log(`[${tag}] SW attach fail: ${e.message}`); }
    } else {
      console.log(`[${tag}] no SW target visible (may be idle)`);
    }

    await cdp.newPage('http://127.0.0.1:8090/tone.html');
    await sleep(800);
    const { sessionId: ctlSession } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
    await sleep(300);

    // Open the extension page and drive it
    const { sessionId: extSession } = await cdp.newPage(`chrome-extension://${extId}/invoke.html`);
    await sleep(500);
    let capOut;
    try {
      capOut = await cdp.eval(extSession, 'doCapture()', { userGesture: true });
    } catch (e) {
      capOut = { harnessError: e.message };
    }
    console.log(`[${tag}] ext-page doCapture -> ${JSON.stringify(capOut).slice(0, 400)}`);

    let r = null;
    if (capOut && capOut.ok) r = await pollResult(cdp, ctlSession);
    summarize(tag, r);

    // Bonus: self-consume inside the extension page (offscreen-doc pattern proxy)
    let selfOut;
    try {
      selfOut = await cdp.eval(extSession, 'doCaptureSelfConsume()', { userGesture: true });
    } catch (e) {
      selfOut = { harnessError: e.message };
    }
    console.log(`[${tag}] ext-page SELF-consume -> ${JSON.stringify(selfOut).slice(0, 700)}`);

    cdp.close();
    return { extId };
  } catch (e) {
    console.log(`\n=== T3b [${tag}] HARNESS FAILURE: ${e.message}`);
    return { extId };
  } finally {
    await kill(child);
  }
}

(async () => {
  const { extId } = await runScenario('direct', []);
  if (extId) await runScenario('allowlisted', [`--allowlisted-extension-id=${extId}`]);
})();

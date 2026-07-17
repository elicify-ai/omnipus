// T3c — tabCapture extension loaded the SANCTIONED post-137 way:
// --remote-debugging-pipe + --enable-unsafe-extension-debugging + CDP Extensions.loadUnpacked.
// Then drive chrome-extension://<id>/invoke.html (extension page) to get a stream id
// by TAB ID and hand it to the controller page for consumption + measurement.
// Scenario 2 adds --allowlisted-extension-id=<id> in case tabCapture's
// "extension must be invoked" gate bites.
'use strict';
const path = require('path');
const { PipeCDP, sleep } = require('./cdp');
const { launchPipe, kill } = require('./launch');

const EXT_DIR = path.join(__dirname, 'ext');

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
  const { child } = launchPipe(extraFlags, `t3c-${tag}`);
  let extId = null;
  try {
    const cdp = new PipeCDP(child);
    // Sanity: browser responds over the pipe
    const ver = await cdp.send('Browser.getVersion');
    console.log(`\n=== T3c [${tag}] browser=${ver.product}`);

    const loaded = await cdp.send('Extensions.loadUnpacked', { path: EXT_DIR });
    extId = loaded.id;
    console.log(`[${tag}] Extensions.loadUnpacked -> id=${extId}`);

    await cdp.newPage('http://127.0.0.1:8090/tone.html');
    await sleep(800);
    const { sessionId: ctlSession } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
    await sleep(300);
    const { sessionId: extSession } = await cdp.newPage(`chrome-extension://${extId}/invoke.html`);
    await sleep(500);

    let capOut;
    try {
      capOut = await cdp.eval(extSession, 'doCapture()', { userGesture: true });
    } catch (e) {
      capOut = { harnessError: e.message };
    }
    console.log(`[${tag}] ext-page doCapture -> ${JSON.stringify(capOut).slice(0, 500)}`);

    let r = null;
    if (capOut && capOut.ok) r = await pollResult(cdp, ctlSession);
    summarize(tag, r);

    let selfOut;
    try {
      selfOut = await cdp.eval(extSession, 'doCaptureSelfConsume()', { userGesture: true });
    } catch (e) {
      selfOut = { harnessError: e.message };
    }
    console.log(`[${tag}] ext-page SELF-consume -> ${JSON.stringify(selfOut).slice(0, 900)}`);

    cdp.close();
    return { extId, capOut };
  } catch (e) {
    console.log(`\n=== T3c [${tag}] HARNESS FAILURE: ${e.message}`);
    return { extId };
  } finally {
    await kill(child);
  }
}

// Extension id is deterministic for a fixed unpacked path.
const KNOWN_ID = 'gdogapnbcanodbmgbhchhhdnohhlgljf';

(async () => {
  if (process.argv[2] === 'only-allowlist') {
    await runScenario('allowlisted', [`--allowlisted-extension-id=${KNOWN_ID}`]);
    return;
  }
  const { extId, capOut } = await runScenario('direct', []);
  // Only bother with the allowlist scenario if the direct one hit an invocation gate
  const invocationGated = capOut && !capOut.ok && /invok|activeTab|gesture/i.test(capOut.error || capOut.harnessError || '');
  if (extId && (invocationGated || process.argv[2] === 'force-allowlist')) {
    await runScenario('allowlisted', [`--allowlisted-extension-id=${extId}`]);
  } else if (extId) {
    console.log(`\n(no invocation gate hit — skipping allowlist scenario)`);
  }
})();

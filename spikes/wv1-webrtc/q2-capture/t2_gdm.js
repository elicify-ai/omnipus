// T2 — getDisplayMedia PATH on new-headless full Chrome.
// Variants:
//   self    : --auto-accept-this-tab-capture + preferCurrentTab (controller captures itself; tone injected inline)
//   bytitle : --auto-select-tab-capture-source-by-title=TONETAB (controller captures the tone tab — the real topology)
//   fakeui  : --use-fake-ui-for-media-stream added on top of bytitle
// Evidence: track labels/settings, rVFC frame count over 3s, audio RMS over 3.2s.
'use strict';
const { CDP, sleep } = require('./cdp');
const { launch, kill } = require('./launch');

const INJECT_TONE = `
  (() => {
    const ctx = new AudioContext();
    const osc = ctx.createOscillator(); osc.frequency.value = 440;
    const g = ctx.createGain(); g.gain.value = 0.25;
    osc.connect(g); g.connect(ctx.destination); osc.start();
    ctx.resume();
    window.__injCtx = ctx;
    return 'tone-started';
  })()
`;

async function pollResult(cdp, sessionId, timeoutMs = 25000) {
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
  console.log(`\n=== T2 [${tag}] RESULT ===`);
  console.log(JSON.stringify(r, null, 1));
  const a = r.audio, v = r.video;
  console.log(`VERDICT[${tag}]: state=${r.state}${r.timedOut ? ' TIMEOUT' : ''} ` +
    `videoTracks=${(r.videoTracks || []).length} audioTracks=${(r.audioTracks || []).length} ` +
    (v ? `frames=${v.frames} fps=${v.fps.toFixed(1)} ` : 'video=none ') +
    (a ? `rmsMean=${a.rmsMean} rmsMax=${a.rmsMax} nonzero=${a.nonzeroCount}/32` : 'audio=none') +
    (r.error ? ` error=${r.error}` : ''));
}

const VARIANTS = [
  {
    tag: 'self',
    flags: ['--auto-accept-this-tab-capture'],
    run: async (cdp) => {
      const { sessionId } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
      await sleep(500);
      console.log('inject tone:', await cdp.eval(sessionId, INJECT_TONE, { userGesture: true }));
      await cdp.eval(sessionId, 'window.startGDM({preferCurrentTab:true}); "fired"', { userGesture: true });
      return pollResult(cdp, sessionId);
    },
  },
  {
    tag: 'bytitle',
    flags: ['--auto-select-tab-capture-source-by-title=TONETAB'],
    run: async (cdp) => {
      await cdp.newPage('http://127.0.0.1:8090/tone.html');
      await sleep(800);
      const { sessionId } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
      await sleep(500);
      await cdp.eval(sessionId, 'window.startGDM({}); "fired"', { userGesture: true });
      return pollResult(cdp, sessionId);
    },
  },
  {
    tag: 'bytitle-raw',
    flags: ['--auto-select-tab-capture-source-by-title=TONETAB'],
    run: async (cdp) => {
      await cdp.newPage('http://127.0.0.1:8090/tone.html');
      await sleep(800);
      const { sessionId } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
      await sleep(500);
      await cdp.eval(sessionId, 'window.startGDM({rawAudio:true}); "fired"', { userGesture: true });
      return pollResult(cdp, sessionId);
    },
  },
  {
    tag: 'fakeui',
    flags: ['--auto-select-tab-capture-source-by-title=TONETAB', '--use-fake-ui-for-media-stream'],
    run: async (cdp) => {
      await cdp.newPage('http://127.0.0.1:8090/tone.html');
      await sleep(800);
      const { sessionId } = await cdp.newPage('http://127.0.0.1:8090/controller.html');
      await sleep(500);
      await cdp.eval(sessionId, 'window.startGDM({}); "fired"', { userGesture: true });
      return pollResult(cdp, sessionId);
    },
  },
];

(async () => {
  const only = process.argv[2];
  for (const v of VARIANTS) {
    if (only && v.tag !== only) continue;
    const { child, port } = launch(v.flags, `t2-${v.tag}`);
    try {
      const cdp = await CDP.connect(port);
      const r = await v.run(cdp);
      summarize(v.tag, r);
      cdp.close();
    } catch (e) {
      console.log(`\n=== T2 [${v.tag}] HARNESS FAILURE: ${e.message}`);
    } finally {
      await kill(child);
    }
  }
})();

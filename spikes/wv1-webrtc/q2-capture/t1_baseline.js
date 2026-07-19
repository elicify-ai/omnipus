// T1 — AUDIO BASELINE: does the tone page render audio at all in new-headless
// (no audio device, no PulseAudio)? Variants: no audio flags / --disable-audio-output / --mute-audio.
// Evidence: AudioContext.state, currentTime advancing, self-analyser RMS on the
// oscillator graph, <audio loop> element progress.
'use strict';
const { CDP, sleep } = require('./cdp');
const { launch, kill } = require('./launch');

const VARIANTS = [
  { tag: 'noflags', flags: [] },
  { tag: 'disable-audio-output', flags: ['--disable-audio-output'] },
  { tag: 'mute-audio', flags: ['--mute-audio'] },
];

(async () => {
  for (const v of VARIANTS) {
    const { child, port } = launch(v.flags, `t1-${v.tag}`);
    try {
      const cdp = await CDP.connect(port);
      const { sessionId } = await cdp.newPage('http://127.0.0.1:8090/tone.html');
      await sleep(1000);
      const s1 = await cdp.eval(sessionId, 'window.__tone()');
      await sleep(3000);
      const s2 = await cdp.eval(sessionId, 'window.__tone()');
      console.log(`\n=== T1 [${v.tag}] ===`);
      console.log('t+1s :', JSON.stringify(s1));
      console.log('t+4s :', JSON.stringify(s2));
      console.log(`VERDICT[${v.tag}]: ctxState=${s2.ctxState} ctxTimeAdvanced=${(s2.ctxTime - s1.ctxTime).toFixed(2)}s selfRms=${s2.selfRms.toFixed(4)} audioElAdvanced=${(s2.audioElTime - s1.audioElTime).toFixed(2)}s vis=${s2.vis}`);
      cdp.close();
    } catch (e) {
      console.log(`\n=== T1 [${v.tag}] FAILED: ${e.message}`);
    } finally {
      await kill(child);
    }
  }
})();

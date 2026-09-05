#!/usr/bin/env node
// Full byte-level replay of a Chrome-captured stream inside Firefox and WebKit.
// Stronger than isTypeSupported: proves the actual bytes decode in another engine.
// usage: node crossengine.js <run>
'use strict';
const { firefox, webkit, chromium } = require('playwright');
const fs = require('fs'), path = require('path');
const run = process.argv[2] || 'dash720';
const PORT = Number(process.env.PORT || 19732);

(async () => {
  const out = {};
  for (const [name, launcher] of [['chromium', chromium], ['firefox', firefox], ['webkit', webkit]]) {
    let b;
    try { b = await launcher.launch(); } catch (e) { console.log(`${name}: LAUNCH FAILED`); continue; }
    const pg = await (await b.newContext()).newPage();
    const logs = [];
    pg.on('console', m => logs.push(m.text().slice(0, 200)));
    pg.on('pageerror', e => logs.push('PAGEERROR ' + e.message.slice(0, 200)));
    try {
      await pg.goto(`http://127.0.0.1:${PORT}/viewer.html?run=${run}`, { timeout: 60000 });
      await pg.waitForFunction('window.__done === true', null, { timeout: 180000 });
    } catch (e) { logs.push('TIMEOUT/ERR ' + e.message.split('\n')[0]); }
    const s = await pg.evaluate(() => window.__stats || {}).catch(() => ({}));
    const err = await pg.evaluate(() => window.__err).catch(() => null);
    await pg.screenshot({ path: path.join(__dirname, 'out', run, `crossengine-${name}.png`) }).catch(() => {});
    out[name] = { stats: s, err, logs: logs.slice(-8) };
    console.log(`\n=== ${name} / run=${run} ===`);
    console.log(`  supported   : ${JSON.stringify(s.supported)}`);
    console.log(`  appended    : ${s.appended} appends, ${s.bytes} bytes`);
    console.log(`  firstErr    : ${s.firstErr || 'none'}`);
    console.log(`  video       : ${s.videoWidth}x${s.videoHeight}  readyState=${s.readyState}  err=${s.error}`);
    console.log(`  playback    : wall=${s.wallElapsed}s media=${s.mediaElapsed}s rate=${s.playbackRate}`);
    console.log(`  framesDecoded=${s.framesDecoded}  audioBytesDecoded=${s.audioBytesDecoded}  audioMaxRms=${s.audioMaxRms}`);
    if (err) console.log(`  __err: ${String(err).split('\n')[0]}`);
    await b.close();
  }
  fs.writeFileSync(path.join(__dirname, 'out', run, 'crossengine.json'), JSON.stringify(out, null, 1));
  console.log(`\n-> out/${run}/crossengine.json`);
})();

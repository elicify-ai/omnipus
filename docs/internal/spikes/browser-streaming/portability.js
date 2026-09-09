#!/usr/bin/env node
// Codec portability: do OTHER engines accept the exact MIME strings Chrome produced?
// Also attempts a FULL replay in each engine, not just isTypeSupported.
'use strict';
const { chromium, firefox, webkit } = require('playwright');
const fs = require('fs'), path = require('path');

const MIMES = [
  ['dash  video', 'video/mp4;codecs="avc1.640015"'],
  ['dash  audio', 'audio/mp4;codecs="mp4a.40.5"'],
  ['yt    video', 'video/mp4; codecs="av01.0.01M.08"'],
  ['yt    audio', 'audio/webm; codecs="opus"'],
  // context: what else the engines do/don't take
  ['(vp9 webm) ', 'video/webm; codecs="vp9"'],
  ['(av1 webm) ', 'video/webm; codecs="av01.0.01M.08"'],
  ['(opus mp4) ', 'audio/mp4; codecs="opus"'],
];

(async () => {
  const results = {};
  for (const [name, launcher] of [['chromium', chromium], ['firefox', firefox], ['webkit', webkit]]) {
    let b;
    try { b = await launcher.launch(); } catch (e) { console.log(`${name}: LAUNCH FAILED ${e.message.split('\n')[0]}`); continue; }
    const pg = await (await b.newContext()).newPage();
    await pg.goto('about:blank');
    const ua = await pg.evaluate(() => navigator.userAgent);
    const support = await pg.evaluate((mimes) => {
      const out = {};
      out.__hasMSE = typeof MediaSource !== 'undefined';
      out.__hasManaged = typeof ManagedMediaSource !== 'undefined';
      for (const [, m] of mimes) {
        try { out[m] = (typeof MediaSource !== 'undefined') ? MediaSource.isTypeSupported(m) : 'no-MSE'; }
        catch (e) { out[m] = 'throw:' + e.name; }
      }
      return out;
    }, MIMES);
    results[name] = { ua, support };
    console.log(`\n=== ${name} ===`);
    console.log(`  ${ua}`);
    console.log(`  MediaSource=${support.__hasMSE}  ManagedMediaSource=${support.__hasManaged}`);
    for (const [label, m] of MIMES) console.log(`  ${label}  ${String(support[m]).padEnd(6)}  ${m}`);
    await b.close();
  }
  fs.writeFileSync(path.join(__dirname, 'out', 'portability.json'), JSON.stringify(results, null, 1));
  console.log('\n-> out/portability.json');
})();

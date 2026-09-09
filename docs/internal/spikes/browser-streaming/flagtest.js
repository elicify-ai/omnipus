#!/usr/bin/env node
// Does a Chrome launch flag turn AV1 off? Launch with the given extra flags,
// ask the renderer what it supports, print, exit. usage: node flagtest.js [flags...]
'use strict';
const { spawn } = require('child_process');
const fs = require('fs'), os = require('os'), path = require('path');
const CHROME = '/Users/danielpiatkowski/.omnipus/browser/chromium/151.0.7922.77/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const PORT = 19735;
const extra = process.argv.slice(2);
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'flagtest-'));
const args = [`--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--headless=new', ...extra, 'about:blank'];
const ch = spawn(CHROME, args, { stdio: ['ignore', 'ignore', 'pipe'] });
console.log('CHROME_PID=' + ch.pid, 'flags=' + JSON.stringify(extra));
const sleep = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  let ver = null;
  for (let i = 0; i < 120; i++) { try { ver = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json(); break; } catch (e) { await sleep(150); } }
  if (!ver) { console.log('chrome never came up'); process.kill(ch.pid, 'SIGKILL'); process.exit(1); }
  const tabs = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const t = tabs.find(x => x.type === 'page');
  const ws = new WebSocket(t.webSocketDebuggerUrl);
  await new Promise(r => ws.addEventListener('open', r));
  const expr = `JSON.stringify({
    av1_lo: MediaSource.isTypeSupported('video/mp4; codecs="av01.0.01M.08"'),
    av1_hi: MediaSource.isTypeSupported('video/mp4; codecs="av01.0.05M.08"'),
    vp9_webm: MediaSource.isTypeSupported('video/webm; codecs="vp9"'),
    vp9_mp4: MediaSource.isTypeSupported('video/mp4; codecs="vp09.00.10.08"'),
    h264: MediaSource.isTypeSupported('video/mp4; codecs="avc1.42E01E"'),
    cpt_av1: document.createElement('video').canPlayType('video/mp4; codecs="av01.0.01M.08"')})`;
  ws.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression: expr, returnByValue: true } }));
  const res = await new Promise(r => ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id === 1) r(m); }));
  console.log('  ', res.result && res.result.result && res.result.result.value);
  ws.close(); process.kill(ch.pid, 'SIGKILL'); process.exit(0);
})().catch(e => { console.log('ERR', e.message); try { process.kill(ch.pid, 'SIGKILL'); } catch (x) {} process.exit(1); });

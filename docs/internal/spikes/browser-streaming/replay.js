#!/usr/bin/env node
// Viewer side: open viewer.html in a SECOND browser, wait for __done, dump stats + screenshot.
// usage: node replay.js <run> [--from N] [--browser chrome|firefox|webkit] [--port N]
'use strict';
const { spawn } = require('child_process');
const fs = require('fs'), path = require('path'), os = require('os');

const CHROME = '/Users/danielpiatkowski/.omnipus/browser/chromium/151.0.7922.77/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const CDP_PORT = Number(process.env.VIEWER_CDP_PORT || 19733);
const HTTP = Number(process.env.PORT || 19732);

const run = process.argv[2] || 'run';
const fromIdx = process.argv.indexOf('--from');
const from = fromIdx > -1 ? process.argv[fromIdx + 1] : null;
const url = `http://127.0.0.1:${HTTP}/viewer.html?run=${run}` + (from ? `&from=${from}` : "") + (process.argv.includes("--badinit") ? "&badinit=1" : "");

const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'msview-'));
const args = ['--headless=new', `--remote-debugging-port=${CDP_PORT}`, `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--autoplay-policy=no-user-gesture-required',
  '--window-size=1280,900', 'about:blank'];

const chrome = spawn(CHROME, args, { stdio: ['ignore', 'ignore', 'pipe'] });
console.error(`[view] VIEWER_CHROME_PID=${chrome.pid}  url=${url}`);
const sleep = ms => new Promise(r => setTimeout(r, ms));

class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pend = new Map(); this.h = [];
    ws.addEventListener('message', e => { const m = JSON.parse(e.data);
      if (m.id != null && this.pend.has(m.id)) { const { res, rej } = this.pend.get(m.id); this.pend.delete(m.id);
        m.error ? rej(new Error(m.error.message)) : res(m.result); } else if (m.method) this.h.forEach(f => f(m)); }); }
  send(method, params = {}, sessionId) { const id = ++this.id;
    return new Promise((res, rej) => { this.pend.set(id, { res, rej });
      this.ws.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }));
      setTimeout(() => { if (this.pend.has(id)) { this.pend.delete(id); rej(new Error('timeout ' + method)); } }, 120000); }); }
  on(f) { this.h.push(f); }
}

(async () => {
  let ver = null;
  for (let i = 0; i < 100; i++) { try { ver = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/version`)).json(); break; } catch (e) { await sleep(150); } }
  if (!ver) { console.error('[view] no chrome'); process.exit(1); }
  const ws = new WebSocket(ver.webSocketDebuggerUrl);
  await new Promise((r, j) => { ws.addEventListener('open', r); ws.addEventListener('error', j); });
  const cdp = new CDP(ws);
  const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
  await cdp.send('Runtime.enable', {}, sessionId);
  await cdp.send('Page.enable', {}, sessionId);
  cdp.on(m => { if (m.method === 'Runtime.consoleAPICalled' && m.sessionId === sessionId) {
    const s = (m.params.args || []).map(a => a.value != null ? a.value : a.description).join(' ');
    if (s) console.error('[page] ' + s.slice(0, 400)); } });
  await cdp.send('Page.navigate', { url }, sessionId);

  let stats = null;
  for (let i = 0; i < 300; i++) {
    await sleep(1000);
    const r = await cdp.send('Runtime.evaluate', { expression: '({d:window.__done,e:window.__err,s:window.__stats})', returnByValue: true }, sessionId).catch(() => null);
    const v = r && r.result && r.result.value;
    if (v && v.d) { stats = v; break; }
  }
  const outDir = path.join(__dirname, 'out', run);
  if (stats) {
    const name = from ? `viewer-stats-from${from}.json` : 'viewer-stats.json';
    fs.writeFileSync(path.join(outDir, name), JSON.stringify(stats, null, 1));
    console.error('\n[view] STATS ' + JSON.stringify(stats.s, null, 1));
    if (stats.e) console.error('[view] ERR ' + stats.e);
  } else console.error('[view] timed out waiting for __done');

  const shot = await cdp.send('Page.captureScreenshot', { format: 'png' }, sessionId).catch(() => null);
  if (shot) { const f = path.join(outDir, from ? `viewer-from${from}.png` : 'viewer.png');
    fs.writeFileSync(f, Buffer.from(shot.data, 'base64')); console.error('[view] screenshot -> ' + f); }

  try { ws.close(); } catch (e) {}
  try { process.kill(chrome.pid, 'SIGKILL'); } catch (e) {}
  process.exit(stats && stats.s && !stats.s.firstErr ? 0 : 1);
})().catch(e => { console.error('[view] FATAL', e); try { process.kill(chrome.pid, 'SIGKILL'); } catch (x) {} process.exit(1); });

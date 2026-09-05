#!/usr/bin/env node
// Dump YouTube's own adaptiveFormats (itag, codec, resolution, declared bitrate)
// so AV1 vs VP9 can be compared at the SAME resolution -- unconfounded by ABR.
'use strict';
const { spawn } = require('child_process');
const fs = require('fs'), os = require('os'), path = require('path');
const CHROME = '/Users/danielpiatkowski/.omnipus/browser/chromium/151.0.7922.77/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const PORT = 19736;
const URL_ = process.argv[2] || 'https://www.youtube.com/watch?v=aqz-KE-bpKQ';
const profile = fs.mkdtempSync(path.join(os.tmpdir(), 'ytfmt-'));
const ch = spawn(CHROME, [`--remote-debugging-port=${PORT}`, `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--headless=new', '--mute-audio',
  '--autoplay-policy=no-user-gesture-required', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] });
console.error('CHROME_PID=' + ch.pid);
const sleep = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  let ver = null;
  for (let i = 0; i < 150; i++) { try { ver = await (await fetch(`http://127.0.0.1:${PORT}/json/version`)).json(); break; } catch (e) { await sleep(150); } }
  const tabs = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json();
  const t = tabs.find(x => x.type === 'page');
  const ws = new WebSocket(t.webSocketDebuggerUrl, { maxPayload: 256 * 1024 * 1024 });
  await new Promise(r => ws.addEventListener('open', r));
  let id = 0; const pend = new Map();
  ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id); } });
  const send = (method, params) => new Promise(r => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
  await send('Page.enable', {});
  await send('Page.navigate', { url: URL_ });
  await sleep(12000);
  const expr = `(()=>{ let d=null;
    try{ d = window.ytInitialPlayerResponse; }catch(e){}
    if(!d){ try{ const p=document.querySelector('#movie_player'); d=p&&p.getPlayerResponse&&p.getPlayerResponse(); }catch(e){} }
    if(!d||!d.streamingData) return JSON.stringify({err:'no streamingData', keys:Object.keys(window).filter(k=>/^yt/i.test(k)).slice(0,20)});
    const f=(d.streamingData.adaptiveFormats||[]).map(x=>({itag:x.itag,mime:x.mimeType,br:x.bitrate,abr:x.averageBitrate,w:x.width,h:x.height,fps:x.fps,len:x.contentLength,q:x.qualityLabel}));
    return JSON.stringify({title:(d.videoDetails||{}).title, n:f.length, f:f});})()`;
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true });
  const val = r.result && r.result.result && r.result.result.value;
  fs.writeFileSync('out/ytformats.json', val || '{}');
  console.log(val ? val.slice(0, 200) + '...' : 'EMPTY');
  ws.close(); process.kill(ch.pid, 'SIGKILL'); process.exit(0);
})().catch(e => { console.error('ERR', e.message); try { process.kill(ch.pid, 'SIGKILL'); } catch (x) {} process.exit(1); });

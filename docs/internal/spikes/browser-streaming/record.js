#!/usr/bin/env node
// Source-side recorder: launch Chrome, inject MSE shim before page scripts,
// capture every appendBuffer, write chunks + manifest to disk.
//
// usage: node record.js <url> <seconds> <outdir> [--headful] [--profile DIR]
'use strict';
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const CHROME = '/Users/danielpiatkowski/.omnipus/browser/chromium/151.0.7922.77/chrome-mac-x64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing';
const CDP_PORT = Number(process.env.CDP_PORT || 19731);

const url = process.argv[2];
const secs = Number(process.argv[3] || 20);
const outDir = path.resolve(process.argv[4] || 'out/run');
const headful = process.argv.includes('--headful');
const profArg = process.argv.indexOf('--profile');
const profile = profArg > -1 ? process.argv[profArg + 1] : fs.mkdtempSync(path.join(os.tmpdir(), 'msespike-'));

if (!url) { console.error('usage: record.js <url> <seconds> <outdir>'); process.exit(2); }

fs.mkdirSync(path.join(outDir, 'chunks'), { recursive: true });
let shimSrc = fs.readFileSync(path.join(__dirname, 'shim.js'), 'utf8');

// --- optional codec gate -------------------------------------------------
// --block <regex>  makes matching codecs look unsupported to the page, so an
// adaptive player renegotiates. --hard-block also throws from addSourceBuffer.
const blockArg = process.argv.indexOf('--block');
const BLOCK_RE = blockArg > -1 ? process.argv[blockArg + 1] : null;
const HARD_BLOCK = process.argv.includes('--hard-block');
if (BLOCK_RE) {
  const gate = fs.readFileSync(path.join(__dirname, 'codecgate.js'), 'utf8')
    .replaceAll('__BLOCK_RE__', `new RegExp(${JSON.stringify(BLOCK_RE)}, 'i')`)
    .replaceAll('__HARD_BLOCK__', HARD_BLOCK ? 'true' : 'false');
  shimSrc = gate + '\n' + shimSrc;
  console.error(`[rec] codec gate ON: /${BLOCK_RE}/i hard=${HARD_BLOCK}`);
}

const args = [
  `--remote-debugging-port=${CDP_PORT}`,
  `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check',
  '--autoplay-policy=no-user-gesture-required',
  '--disable-features=Translate,MediaRouter',
  '--mute-audio',
  '--window-size=1280,720',
];
if (!headful) args.push('--headless=new');
args.push('about:blank');

console.error(`[rec] launching chrome (headless=${!headful}) profile=${profile}`);
const chrome = spawn(CHROME, args, { stdio: ['ignore', 'pipe', 'pipe'] });
console.error(`[rec] CHROME_PID=${chrome.pid}`);
fs.writeFileSync(path.join(outDir, 'chrome.pid'), String(chrome.pid));
let chromeErr = '';
chrome.stderr.on('data', d => { chromeErr += d.toString().slice(0, 2000); });

const sleep = ms => new Promise(r => setTimeout(r, ms));

async function getJSON(p) {
  const r = await fetch(`http://127.0.0.1:${CDP_PORT}${p}`);
  return r.json();
}

// ---- minimal CDP client over the browser websocket, flattened sessions ----
class CDP {
  constructor(ws) { this.ws = ws; this.id = 0; this.pend = new Map(); this.handlers = [];
    ws.addEventListener('message', e => {
      const m = JSON.parse(e.data);
      if (m.id != null && this.pend.has(m.id)) {
        const { res, rej } = this.pend.get(m.id); this.pend.delete(m.id);
        m.error ? rej(new Error(m.error.message + ' ' + JSON.stringify(m.error.data || ''))) : res(m.result);
      } else if (m.method) { for (const h of this.handlers) h(m); }
    });
  }
  send(method, params = {}, sessionId) {
    const id = ++this.id;
    return new Promise((res, rej) => {
      this.pend.set(id, { res, rej });
      this.ws.send(JSON.stringify(sessionId ? { id, method, params, sessionId } : { id, method, params }));
      setTimeout(() => { if (this.pend.has(id)) { this.pend.delete(id); rej(new Error('timeout ' + method)); } }, 30000);
    });
  }
  on(fn) { this.handlers.push(fn); }
}

// ---- collected state ----
const events = [];          // ordered, data-free event log
const partial = new Map();  // id -> [parts]
const chunkMeta = new Map();// id -> {bytes,file}
let dataBytesTotal = 0;
const workerSessions = new Set();   // sessions needing debugger-pause injection
const workerInjected = new Set();
const START = Date.now();

function handlePayload(str, sessionId) {
  let m; try { m = JSON.parse(str); } catch (e) { return; }
  if (m.ev === 'data') {
    let slot = partial.get(m.id);
    if (!slot) { slot = new Array(m.parts).fill(null); partial.set(m.id, slot); }
    slot[m.part] = m.b64;
    if (slot.every(x => x !== null)) {
      const buf = Buffer.from(slot.join(''), 'base64');
      partial.delete(m.id);
      const file = `chunks/${String(m.id).padStart(5, '0')}.bin`;
      fs.writeFileSync(path.join(outDir, file), buf);
      chunkMeta.set(m.id, { bytes: buf.length, file });
      dataBytesTotal += buf.length;
    }
    return;
  }
  m.wall = Date.now() - START;
  m.session = sessionId;
  events.push(m);
  if (m.ev !== 'append') console.error(`[ev] ${m.ev} ${JSON.stringify(m).slice(0, 220)}`);
}

async function setupTarget(cdp, sessionId, type) {
  await cdp.send('Runtime.addBinding', { name: '__mseSend' }, sessionId).catch(() => {});
  if (type && type !== 'page' && type !== 'iframe') {
    // Workers have no Page domain, so addScriptToEvaluateOnNewDocument does not exist.
    // And a target halted by waitForDebuggerOnStart cannot service Runtime.evaluate --
    // it deadlocks. The working sequence is: arm an instrumentation breakpoint on the
    // first script, release the start-halt, then inject from inside the resulting pause.
    await cdp.send('Debugger.enable', {}, sessionId).catch(() => {});
    await cdp.send('Debugger.setInstrumentationBreakpoint', { instrumentation: 'beforeScriptExecution' }, sessionId).catch(() => {});
    workerSessions.add(sessionId);
    console.error(`[rec] armed instrumentation breakpoint on ${type} target`);
  } else {
    await cdp.send('Page.addScriptToEvaluateOnNewDocument', { source: shimSrc, runImmediately: true }, sessionId).catch(e => console.error('[rec] addScript:', e.message));
    await cdp.send('Page.enable', {}, sessionId).catch(() => {});
  }
  await cdp.send('Runtime.enable', {}, sessionId).catch(() => {});
  await cdp.send('Target.setAutoAttach', { autoAttach: true, waitForDebuggerOnStart: true, flatten: true }, sessionId).catch(() => {});
}

(async () => {
  // wait for CDP
  let ver = null;
  for (let i = 0; i < 100; i++) {
    try { ver = await getJSON('/json/version'); break; } catch (e) { await sleep(150); }
  }
  if (!ver) { console.error('[rec] chrome never came up:', chromeErr); process.exit(1); }
  console.error('[rec] connected:', ver['Browser']);

  const ws = new WebSocket(ver.webSocketDebuggerUrl, { maxPayload: 512 * 1024 * 1024 });
  await new Promise((res, rej) => { ws.addEventListener('open', res); ws.addEventListener('error', rej); });
  const cdp = new CDP(ws);

  cdp.on(async (m) => {
    if (m.method === 'Runtime.bindingCalled' && m.params.name === '__mseSend') {
      handlePayload(m.params.payload, m.sessionId);
    } else if (m.method === 'Debugger.paused' && workerSessions.has(m.sessionId)) {
      const sid = m.sessionId;
      if (!workerInjected.has(sid)) {
        workerInjected.add(sid);
        const cf = (m.params.callFrames || [])[0];
        let ok = null;
        if (cf) ok = await cdp.send('Debugger.evaluateOnCallFrame', { callFrameId: cf.callFrameId, expression: shimSrc }, sid).catch(e => { console.error('[rec] evaluateOnCallFrame:', e.message); return null; });
        if (!ok) ok = await cdp.send('Runtime.evaluate', { expression: shimSrc }, sid).catch(e => { console.error('[rec] worker Runtime.evaluate:', e.message); return null; });
        console.error(`[rec] worker shim injection ${ok ? 'OK' : 'FAILED'} (paused at first script)`);
        await cdp.send('Debugger.setInstrumentationBreakpoint', { instrumentation: 'beforeScriptExecution' }, sid).catch(() => {});
      }
      await cdp.send('Debugger.resume', {}, sid).catch(() => {});
    } else if (m.method === 'Target.attachedToTarget') {
      const sid = m.params.sessionId, ti = m.params.targetInfo;
      console.error(`[rec] attached ${ti.type} ${String(ti.url).slice(0, 80)}`);
      await setupTarget(cdp, sid, ti.type);
      await cdp.send('Runtime.runIfWaitingForDebugger', {}, sid).catch(() => {});
    }
  });

  // create the page target and attach
  const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
  const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
  await setupTarget(cdp, sessionId, 'page');

  console.error(`[rec] navigating -> ${url}`);
  const navT = Date.now();
  await cdp.send('Page.navigate', { url }, sessionId);

  // sample playback state once a second
  const samples = [];
  const probe = `(()=>{const v=document.querySelector('video');if(!v)return{no:1};
    const b=[];for(let i=0;i<v.buffered.length;i++)b.push([+v.buffered.start(i).toFixed(2),+v.buffered.end(i).toFixed(2)]);
    return{ct:+v.currentTime.toFixed(3),rs:v.readyState,ns:v.networkState,pa:v.paused,w:v.videoWidth,h:v.videoHeight,
      dur:v.duration,buf:b,dropped:(v.getVideoPlaybackQuality?v.getVideoPlaybackQuality().droppedVideoFrames:null),
      totalFrames:(v.getVideoPlaybackQuality?v.getVideoPlaybackQuality().totalVideoFrames:null),
      abytes:v.webkitAudioDecodedByteCount,vbytes:v.webkitVideoDecodedByteCount};})()`;

  const deadline = Date.now() + secs * 1000;
  while (Date.now() < deadline) {
    await sleep(1000);
    try {
      const r = await cdp.send('Runtime.evaluate', { expression: probe, returnByValue: true, awaitPromise: false }, sessionId);
      const v = r.result && r.result.value;
      if (v) { v.wall = Date.now() - START; samples.push(v);
        if (!v.no) console.error(`[src] t=${v.ct}s rs=${v.rs} ${v.w}x${v.h} buf=${JSON.stringify(v.buf)} aBytes=${v.abytes} appends=${events.filter(e=>e.ev==='append').length} MB=${(dataBytesTotal/1048576).toFixed(2)}`);
      }
    } catch (e) { console.error('[rec] probe:', e.message); }
  }

  // let straggler binding messages land
  await sleep(1500);

  const appends = events.filter(e => e.ev === 'append');
  for (const a of appends) { const cm = chunkMeta.get(a.id); if (cm) a.file = cm.file; }
  const missing = appends.filter(a => !a.file);

  const manifest = {
    url, secs, recordedAt: new Date().toISOString(),
    blockRe: BLOCK_RE, hardBlock: HARD_BLOCK,
    loadavg: os.loadavg(), cpus: os.cpus().length,
    navToEnd_ms: Date.now() - navT,
    totalAppends: appends.length,
    totalBytes: dataBytesTotal,
    missingChunkData: missing.length,
    events, samples,
  };
  fs.writeFileSync(path.join(outDir, 'manifest.json'), JSON.stringify(manifest, null, 1));
  console.error(`\n[rec] DONE appends=${appends.length} bytes=${dataBytesTotal} (${(dataBytesTotal/1048576).toFixed(2)} MB) missing=${missing.length}`);
  console.error(`[rec] manifest -> ${path.join(outDir, 'manifest.json')}`);

  try { ws.close(); } catch (e) {}
  try { process.kill(chrome.pid, 'SIGTERM'); } catch (e) {}
  await sleep(500);
  try { process.kill(chrome.pid, 'SIGKILL'); } catch (e) {}
  process.exit(0);
})().catch(async e => {
  console.error('[rec] FATAL', e);
  try { process.kill(chrome.pid, 'SIGKILL'); } catch (e2) {}
  process.exit(1);
});

// Q4 encoder orchestrator (Node). Launches managed Chrome 151 over the CDP
// pipe transport, loads the encoder extension via Extensions.loadUnpacked
// (the sanctioned post-137 path -- --load-extension is dead in Chrome 151,
// per wv1-spike-results.md Q2), opens the /metronome capture+drive target
// tab, opens the extension's encoder.html page, and invokes startEncoding()
// there to establish the WebRTC ingest connection to wv1spike4's /ingest.
//
// Q4 ADDITION: after the metronome tab is attached, this process also
// connects OUT to ws://127.0.0.1:8080/inputbridge (Go relay's localhost-only
// WS server) and answers input-dispatch commands by translating them to
// CDP Input.dispatchMouseEvent/dispatchKeyEvent (inputdispatch.js) on the
// metronome tab's OWN session -- the same pipe transport this process
// already holds for the encoder-extension flow. This is spike-only
// scaffolding: the localhost WS hop is sub-ms and exists only because
// Extensions.loadUnpacked demands pipe-mode debugging, which only a
// process holding the child's stdio can provide; the real build has Go own
// the pipe directly via chromedp and dispatch CDP itself, no Node/WS hop.
//
// Two-phase launch: extension ids are deterministic per unpacked path, but
// only KNOWN after a load -- so phase 1 launches throwaway Chrome just to
// learn the id, then phase 2 launches the real encoder Chrome with
// --allowlisted-extension-id=<id> baked in from the start (mandatory per
// the Q2 recipe: without it getMediaStreamId hits the activeTab/gesture
// gate).
'use strict';
const path = require('path');
const { PipeCDP, sleep } = require('./cdp');
const { launchPipe, kill } = require('./launch');
const { dispatchMouse, dispatchKey, evaluateOnTab } = require('./inputdispatch');

const EXT_DIR = path.join(__dirname, 'ext');
const METRONOME_URL = 'http://localhost:8080/metronome';
const INPUT_BRIDGE_URL = 'ws://127.0.0.1:8080/inputbridge';

async function loadExtensionLearnId() {
  const { child } = launchPipe([], 'learn-id');
  try {
    const cdp = new PipeCDP(child);
    await cdp.send('Browser.getVersion');
    const loaded = await cdp.send('Extensions.loadUnpacked', { path: EXT_DIR });
    return loaded.id;
  } finally {
    await kill(child);
  }
}

// connectInputBridge opens (and, on drop, reconnects) a WS client to the Go
// relay's /inputbridge. Each inbound command is {id, method, params}; each
// reply is {id, ok, result} or {id, ok:false, error}. method routes to the
// matching CDP call against the metronome tab's session -- "mouse" ->
// Input.dispatchMouseEvent, "key" -> Input.dispatchKeyEvent, "evaluate" ->
// Runtime.evaluate (verification only, e.g. reading window.__events back).
function connectInputBridge(cdp, metronomeSession) {
  let ws;
  let closedByUs = false;

  function connect() {
    ws = new WebSocket(INPUT_BRIDGE_URL);
    ws.addEventListener('open', () => {
      console.log('[inputbridge] connected to Go relay');
    });
    ws.addEventListener('message', async (ev) => {
      let cmd;
      try {
        cmd = JSON.parse(typeof ev.data === 'string' ? ev.data : ev.data.toString());
      } catch (e) {
        console.log('[inputbridge] bad command JSON:', e.message);
        return;
      }
      const { id, method, params } = cmd;
      try {
        let result;
        if (method === 'mouse') {
          result = await dispatchMouse(cdp, metronomeSession, params);
        } else if (method === 'key') {
          result = await dispatchKey(cdp, metronomeSession, params);
        } else if (method === 'evaluate') {
          result = await evaluateOnTab(cdp, metronomeSession, params.expression);
        } else {
          throw new Error('unknown bridge method: ' + method);
        }
        ws.send(JSON.stringify({ id, ok: true, result }));
      } catch (e) {
        ws.send(JSON.stringify({ id, ok: false, error: String((e && e.message) || e) }));
      }
    });
    ws.addEventListener('close', () => {
      if (closedByUs) return;
      console.log('[inputbridge] disconnected, reconnecting in 1s...');
      setTimeout(connect, 1000);
    });
    ws.addEventListener('error', (e) => {
      console.log('[inputbridge] socket error:', e.message || e);
    });
  }
  connect();

  return {
    close() {
      closedByUs = true;
      try {
        ws.close();
      } catch (e) {
        /* ignore */
      }
    },
  };
}

async function main() {
  console.log('[run] Phase 1: learning deterministic extension id for', EXT_DIR);
  const learnedId = await loadExtensionLearnId();
  console.log(`[run] extension id = ${learnedId}`);

  console.log('[run] Phase 2: launching encoder Chrome with --allowlisted-extension-id=' + learnedId);
  const { child } = launchPipe([`--allowlisted-extension-id=${learnedId}`], 'encoder');
  const cdp = new PipeCDP(child);
  const ver = await cdp.send('Browser.getVersion');
  console.log(`[run] browser=${ver.product}`);

  const loaded = await cdp.send('Extensions.loadUnpacked', { path: EXT_DIR });
  console.log(`[run] Extensions.loadUnpacked -> id=${loaded.id}`);
  if (loaded.id !== learnedId) {
    console.log(`[run] WARNING: extension id changed between launches (${learnedId} -> ${loaded.id}); allowlist flag will NOT match this session`);
  }

  console.log('[run] opening metronome tab:', METRONOME_URL);
  const { sessionId: metronomeSession } = await cdp.newPage(METRONOME_URL);
  await sleep(1000);

  console.log('[run] connecting input bridge to', INPUT_BRIDGE_URL);
  connectInputBridge(cdp, metronomeSession);

  console.log('[run] opening encoder extension page...');
  const { sessionId: encSession } = await cdp.newPage(`chrome-extension://${loaded.id}/encoder.html`);
  await sleep(500);

  console.log('[run] invoking startEncoding()...');
  let result;
  try {
    result = await cdp.eval(encSession, 'startEncoding()', { userGesture: true });
  } catch (e) {
    result = { ok: false, harnessError: e.message };
  }
  console.log('[run] startEncoding result:', JSON.stringify(result));

  if (!result || !result.ok) {
    console.log('[run] FAILED to establish ingest connection (see error above). Chrome PID=' + child.pid + ' left running for post-mortem CDP inspection.');
    process.exitCode = 1;
  } else {
    console.log('[run] SUCCESS: ingest connection established. Encoder Chrome PID=' + child.pid);
  }

  // Keep this Node process (and therefore the pipe fds tying Chrome's
  // lifetime to it) alive, and periodically print encoder state so
  // `run.out` / process logs double as evidence of steady-state operation.
  setInterval(async () => {
    try {
      const state = await cdp.eval(encSession, 'JSON.parse(JSON.stringify(window.__state))');
      console.log(`[run] encoder state: status=${state.status} ice=${state.iceState} conn=${state.connState} lastError=${state.lastError || ''}`);
    } catch (e) {
      console.log('[run] state poll failed:', e.message);
    }
  }, 15000);

  const shutdown = async () => {
    console.log('[run] shutting down, killing Chrome pid=' + child.pid);
    await kill(child);
    process.exit(0);
  };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
}

main().catch((e) => {
  console.error('[run] FATAL', e);
  process.exit(1);
});

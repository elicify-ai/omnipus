// Zero-dep raw CDP client using Node 22's built-in WebSocket (undici).
// Flat session mode: attach with flatten:true, route by sessionId.
'use strict';

class CDP {
  constructor(ws) {
    this.ws = ws;
    this.id = 0;
    this.pending = new Map();
    this.eventHandlers = []; // {method, sessionId, fn}
    ws.addEventListener('message', (ev) => {
      const msg = JSON.parse(typeof ev.data === 'string' ? ev.data : ev.data.toString());
      if (msg.id !== undefined && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(`CDP ${msg.error.code}: ${msg.error.message}`));
        else resolve(msg.result);
      } else if (msg.method) {
        for (const h of this.eventHandlers) {
          if (h.method === msg.method && (h.sessionId === undefined || h.sessionId === msg.sessionId)) {
            h.fn(msg.params, msg.sessionId);
          }
        }
      }
    });
  }

  static async connect(port, retries = 50) {
    let ver;
    for (let i = 0; ; i++) {
      try {
        ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
        break;
      } catch (e) {
        if (i >= retries) throw new Error(`devtools port ${port} never came up: ${e}`);
        await sleep(200);
      }
    }
    const ws = new WebSocket(ver.webSocketDebuggerUrl);
    await new Promise((res, rej) => { ws.addEventListener('open', res, { once: true }); ws.addEventListener('error', rej, { once: true }); });
    const c = new CDP(ws);
    c.browserVersion = ver;
    return c;
  }

  send(method, params = {}, sessionId = undefined) {
    const id = ++this.id;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.ws.send(JSON.stringify(msg));
    });
  }

  on(method, fn, sessionId) { this.eventHandlers.push({ method, fn, sessionId }); }

  // Create a page target in its OWN WINDOW (Chrome 151 hidden-tab zero-frames bug)
  // and attach; returns sessionId.
  async newPage(url, { background = false } = {}) {
    const { targetId } = await this.send('Target.createTarget', { url, newWindow: true, background });
    const { sessionId } = await this.send('Target.attachToTarget', { targetId, flatten: true });
    await this.send('Runtime.enable', {}, sessionId);
    await this.send('Page.enable', {}, sessionId);
    return { targetId, sessionId };
  }

  // Evaluate with await + JSON round-trip. userGesture grants transient activation.
  async eval(sessionId, expr, { userGesture = false } = {}) {
    const r = await this.send('Runtime.evaluate', {
      expression: expr,
      awaitPromise: true,
      returnByValue: true,
      userGesture,
    }, sessionId);
    if (r.exceptionDetails) {
      const d = r.exceptionDetails;
      throw new Error(`page exception: ${d.text} ${d.exception ? d.exception.description || d.exception.value : ''}`);
    }
    return r.result.value;
  }

  close() { try { this.ws.close(); } catch {} }
}

function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

// CDP over --remote-debugging-pipe: child fd3 = we write, fd4 = we read,
// messages NUL-delimited JSON (same framing puppeteer's PipeTransport uses).
// Same API surface as CDP (send/on/eval/newPage).
class PipeCDP {
  constructor(child) {
    this.child = child;
    this.w = child.stdio[3];
    this.r = child.stdio[4];
    this.id = 0;
    this.pending = new Map();
    this.eventHandlers = [];
    let buf = '';
    this.r.setEncoding('utf8');
    this.r.on('data', (chunk) => {
      buf += chunk;
      let i;
      while ((i = buf.indexOf('\0')) >= 0) {
        const raw = buf.slice(0, i);
        buf = buf.slice(i + 1);
        if (!raw) continue;
        let msg;
        try { msg = JSON.parse(raw); } catch { continue; }
        if (msg.id !== undefined && this.pending.has(msg.id)) {
          const { resolve, reject } = this.pending.get(msg.id);
          this.pending.delete(msg.id);
          if (msg.error) reject(new Error(`CDP ${msg.error.code}: ${msg.error.message}`));
          else resolve(msg.result);
        } else if (msg.method) {
          for (const h of this.eventHandlers) {
            if (h.method === msg.method && (h.sessionId === undefined || h.sessionId === msg.sessionId)) {
              h.fn(msg.params, msg.sessionId);
            }
          }
        }
      }
    });
  }
  send(method, params = {}, sessionId = undefined) {
    const id = ++this.id;
    const msg = { id, method, params };
    if (sessionId) msg.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.w.write(JSON.stringify(msg) + '\0');
    });
  }
  close() { /* pipe dies with the child */ }
}
PipeCDP.prototype.on = CDP.prototype.on;
PipeCDP.prototype.newPage = CDP.prototype.newPage;
PipeCDP.prototype.eval = CDP.prototype.eval;

module.exports = { CDP, PipeCDP, sleep };

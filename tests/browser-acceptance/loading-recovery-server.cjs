// Loopback-only UAT fixture. Publish /fixture through the existing preview route.
// Usage: node loading-recovery-server.cjs [port]; copy input-connection-fixture.html alongside it.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const port = Number(process.argv[2] || 18996);
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw Error('Unprivileged fixture port required');
const fixture = fs.readFileSync(path.join(__dirname, 'input-connection-fixture.html'), 'utf8');
const pending = new Map();
const HOLD_MS = 60000;
const LATE_COMMIT_MS = 25000;
const server = http.createServer((req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1');
  const json = (status, value) => { res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); res.end(JSON.stringify(value)); };
  if (req.method !== 'GET') return json(405, { error: 'GET required' });
  if (url.pathname === '/health') return json(200, { ready: true, holdMs: HOLD_MS, lateCommitMs: LATE_COMMIT_MS, ttlMs: 900000 });
  const id = url.searchParams.get('request');
  if (!/^[a-f0-9]{24}$/.test(id || '')) return json(400, { error: '24-character request identity required' });
  if (url.pathname === '/status') {
    const record = pending.get(id);
    return json(200, record ? { phase: record.phase, ageMs: Date.now() - record.at } : { phase: 'not-started', ageMs: 0 });
  }
  const mode = url.searchParams.get('mode') || 'stop';
  if (!['stop', 'late'].includes(mode)) return json(400, { error: 'Unknown loading mode' });
  const nonce = Number(url.searchParams.get('nonce'));
  if (!Number.isInteger(nonce) || nonce < 1 || nonce > 65534) return json(400, { error: 'Nonce between 1 and 65534 required' });
  if (url.pathname === '/fixture') {
    const form = `<form action="hang" method="GET"><input type="hidden" name="mode" value="${mode}"><input type="hidden" name="nonce" value="${nonce}"><input type="hidden" name="request" value="${id}"><button id="click" type="submit">Start pending page load</button></form>`;
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
    return res.end(fixture.replace('<button id="click">Count click</button>', form));
  }
  if (url.pathname !== '/hang') return json(404, { error: 'Unknown fixture route' });
  if (pending.has(id)) return json(409, { error: 'Request identity already used' });
  if (pending.size >= 32) return json(503, { error: 'Fixture request limit reached' });
  const record = { at: Date.now(), phase: 'pending' };
  pending.set(id, record);
  // No headers or body commit until the deadline; Stop should abort this response.
  const timer = setTimeout(() => {
    record.phase = 'completed';
    res.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8', 'Cache-Control': 'no-store' });
    res.end(fixture.replace("const nonce=Number(new URL(location.href).searchParams.get('nonce'));", `const nonce=${nonce + 1};`));
  }, mode === 'late' ? LATE_COMMIT_MS : HOLD_MS);
  res.once('close', () => {
    clearTimeout(timer);
    if (record.phase === 'pending') record.phase = 'canceled';
    setTimeout(() => pending.delete(id), 120000).unref();
  });
});
server.headersTimeout = 10000;
server.requestTimeout = 70000;
server.timeout = 70000;
server.listen(port, '127.0.0.1', () => console.log(JSON.stringify({ listening: `http://127.0.0.1:${port}`, fixture: '/fixture', holdMs: HOLD_MS })));
function shutdown() { server.closeAllConnections(); server.close(() => process.exit(0)); }
setTimeout(shutdown, 15 * 60 * 1000).unref();
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, shutdown);

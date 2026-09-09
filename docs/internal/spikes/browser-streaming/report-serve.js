#!/usr/bin/env node
// Static server + self-report sink for the real-Safari leg of the spike.
// Safari cannot be driven without "Allow Remote Automation", so the viewer page
// POSTs its own measurements here and we read them off disk.
// Port 19742 (NOT 10994 -- the live gateway is untouched).
'use strict';
const http = require('http'), fs = require('fs'), path = require('path');
const ROOT = __dirname, PORT = Number(process.env.PORT || 19742);
const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.json': 'application/json',
                '.bin': 'application/octet-stream', '.png': 'image/png' };
const REPORTS = path.join(ROOT, 'out', 'reports');
fs.mkdirSync(REPORTS, { recursive: true });

http.createServer((req, res) => {
  const p = decodeURIComponent(req.url.split('?')[0]);

  if (req.method === 'POST' && p === '/report') {
    let body = '';
    req.on('data', d => { body += d; if (body.length > 8e6) req.destroy(); });
    req.on('end', () => {
      let name = 'report';
      try { const j = JSON.parse(body); name = String(j.name || 'report').replace(/[^a-z0-9._-]/gi, '_'); } catch (e) {}
      const stamp = new Date().toISOString().replace(/[:.]/g, '-');
      const file = path.join(REPORTS, `${name}-${stamp}.json`);
      fs.writeFileSync(file, body);
      console.log(`[report] ${file}  (${body.length} bytes)  ua=${(req.headers['user-agent']||'').slice(0,60)}`);
      res.writeHead(200, { 'content-type': 'application/json', 'access-control-allow-origin': '*' });
      res.end('{"ok":true}');
    });
    return;
  }
  if (req.method === 'OPTIONS') {
    res.writeHead(204, { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': 'POST,GET,OPTIONS' });
    res.end(); return;
  }

  const f = p.startsWith('/out/') ? path.join(ROOT, p) : path.join(ROOT, 'pages', p === '/' ? 'viewer.html' : p);
  if (!f.startsWith(ROOT)) { res.writeHead(403).end(); return; }
  fs.readFile(f, (e, d) => {
    if (e) { res.writeHead(404).end('nope: ' + p); return; }
    res.writeHead(200, { 'content-type': TYPES[path.extname(f)] || 'application/octet-stream', 'access-control-allow-origin': '*' });
    res.end(d);
  });
}).listen(PORT, '127.0.0.1', () => console.log(`report-serve http://127.0.0.1:${PORT}/ pid=${process.pid}`));

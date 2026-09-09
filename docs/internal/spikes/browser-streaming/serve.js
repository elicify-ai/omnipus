#!/usr/bin/env node
// dumb static server for the spike. serves /pages/* at / and /out/* .
'use strict';
const http = require('http'), fs = require('fs'), path = require('path');
const ROOT = __dirname, PORT = Number(process.env.PORT || 19732);
const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.json': 'application/json', '.bin': 'application/octet-stream', '.png': 'image/png' };
http.createServer((req, res) => {
  let p = decodeURIComponent(req.url.split('?')[0]);
  let f = p.startsWith('/out/') ? path.join(ROOT, p) : path.join(ROOT, 'pages', p === '/' ? 'viewer.html' : p);
  if (!f.startsWith(ROOT)) { res.writeHead(403).end(); return; }
  fs.readFile(f, (e, d) => {
    if (e) { res.writeHead(404).end('nope: ' + p); return; }
    res.writeHead(200, { 'content-type': TYPES[path.extname(f)] || 'application/octet-stream', 'access-control-allow-origin': '*' });
    res.end(d);
  });
}).listen(PORT, '127.0.0.1', () => console.log(`serve http://127.0.0.1:${PORT}/ pid=${process.pid}`));

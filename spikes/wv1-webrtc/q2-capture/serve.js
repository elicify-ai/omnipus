// Tiny static server for spike pages. Localhost only, port 8090 (8080 is taken by another spike).
const http = require('http');
const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, 'pages');
const MIME = { '.html': 'text/html', '.js': 'text/javascript', '.wav': 'audio/wav' };

http.createServer((req, res) => {
  const p = path.join(ROOT, path.normalize(req.url.split('?')[0]).replace(/^(\.\.[/\\])+/, ''));
  fs.readFile(p, (err, data) => {
    if (err) { res.writeHead(404); res.end('nope'); return; }
    res.writeHead(200, { 'Content-Type': MIME[path.extname(p)] || 'application/octet-stream' });
    res.end(data);
  });
}).listen(8090, '127.0.0.1', () => console.log('serving on http://127.0.0.1:8090'));

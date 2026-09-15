#!/usr/bin/env python3
"""CSP experiment harness.

Two servers:
  MAIN (port A) serves the fixture bundle under a chosen CSP policy, and sets a
        cookie so cookie-readability can be tested for real.
  EXT  (port B) is a DIFFERENT ORIGIN standing in for "the internet". Any request
        arriving here means the page achieved egress.

Ground truth = which paths each server actually received. Policy names are passed
as the first path segment: /p/<policy>/  -> the bundle under that policy.
"""
import http.server, socketserver, threading, json, sys, os, urllib.parse

FIX = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'fixture')
HITS_MAIN, HITS_EXT = [], []
EXT_PORT = int(sys.argv[2]) if len(sys.argv) > 2 else 8811
MAIN_PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 8810

CT = {'.html':'text/html; charset=utf-8', '.css':'text/css', '.js':'application/javascript',
      '.wav':'audio/wav', '.pdf':'application/pdf', '.woff2':'font/woff2', '.png':'image/png'}

SELF = f"http://127.0.0.1:{MAIN_PORT}"

# The candidate policies. 'sandbox' with no allow-same-origin => opaque origin.
POLICIES = {
  # 1. ADR's literal intent: sandbox + 'self'. Tests whether 'self' still matches
  #    when the document's own origin is opaque.
  "self": "sandbox allow-scripts; default-src 'none'; script-src 'self' 'unsafe-inline'; "
          "style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; "
          "media-src 'self'; frame-src 'self'; connect-src 'none'; form-action 'none'; "
          "base-uri 'none'; object-src 'none'",
  # 2. Same, but naming the serving origin EXPLICITLY instead of 'self'.
  "origin": f"sandbox allow-scripts; default-src 'none'; script-src {SELF} 'unsafe-inline'; "
            f"style-src {SELF} 'unsafe-inline'; img-src {SELF} data: blob:; font-src {SELF}; "
            f"media-src {SELF}; frame-src {SELF}; connect-src 'none'; form-action 'none'; "
            f"base-uri 'none'; object-src 'none'",
  # 3. No sandbox directive at all (baseline: what does 'self' do normally?).
  "nosandbox": "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "
               "img-src 'self' data: blob:; font-src 'self'; media-src 'self'; frame-src 'self'; "
               "connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'",
  # 4. Sandbox with NO csp source restrictions (isolation only, no egress control).
  "sandboxonly": "sandbox allow-scripts",
  # 5. No CSP at all (control: proves the probes actually fire when unrestricted).
  "none": None,
}

class Base(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *a): pass

class Main(Base):
    def do_GET(self):
        u = urllib.parse.urlparse(self.path); p = u.path
        HITS_MAIN.append(p)
        if p == '/__hits':
            body = json.dumps({'main': HITS_MAIN, 'ext': HITS_EXT}).encode()
            self.send_response(200); self.send_header('Content-Type','application/json')
            self.send_header('Content-Length', str(len(body))); self.end_headers()
            self.wfile.write(body); return
        if p == '/__reset':
            HITS_MAIN.clear(); HITS_EXT.clear()
            self.send_response(200); self.end_headers(); self.wfile.write(b'ok'); return
        policy = None; name = 'none'
        if p.startswith('/p/'):
            parts = p.split('/', 3)
            name = parts[2]; policy = POLICIES.get(name)
            rest = '/' + (parts[3] if len(parts) > 3 else '')
            p = '/f/index.html' if rest in ('/', '') else rest
        if p.startswith('/f/'):
            fn = os.path.join(FIX, os.path.basename(p))
            if not os.path.exists(fn):
                self.send_response(404); self.end_headers(); self.wfile.write(b'x'); return
            data = open(fn,'rb').read()
            if fn.endswith('index.html'):
                data = data.replace(b'__EXTPORT__', str(EXT_PORT).encode())
            ext = os.path.splitext(fn)[1]
            self.send_response(200)
            self.send_header('Content-Type', CT.get(ext,'application/octet-stream'))
            self.send_header('X-Content-Type-Options','nosniff')
            self.send_header('Content-Length', str(len(data)))
            self.send_header('Set-Cookie','omnipus_probe=SECRET; Path=/; SameSite=Strict')
            if policy: self.send_header('Content-Security-Policy', policy)
            self.end_headers(); self.wfile.write(data); return
        self.send_response(404); self.end_headers(); self.wfile.write(b'x')

class Ext(Base):
    def do_GET(self):
        HITS_EXT.append(urllib.parse.urlparse(self.path).path)
        self.send_response(200); self.send_header('Content-Type','text/plain')
        self.send_header('Access-Control-Allow-Origin','*')
        self.send_header('Content-Length','2'); self.end_headers(); self.wfile.write(b'ok')
    do_POST = do_GET

class TS(socketserver.ThreadingTCPServer): allow_reuse_address = True

if __name__ == '__main__':
    threading.Thread(target=lambda: TS(('127.0.0.1', EXT_PORT), Ext).serve_forever(), daemon=True).start()
    print(f"MAIN http://127.0.0.1:{MAIN_PORT}  EXT http://127.0.0.1:{EXT_PORT}", flush=True)
    print("policies: " + ", ".join(POLICIES), flush=True)
    TS(('127.0.0.1', MAIN_PORT), Main).serve_forever()

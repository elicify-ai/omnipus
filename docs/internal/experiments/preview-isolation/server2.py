#!/usr/bin/env python3
"""Follow-up experiment: per-format isolation, font CORS fix, type confusion.

Routes
  /p/<policy>/            -> the HTML bundle under <policy>          (active-content test)
  /f/<file>               -> a fixture asset under the same rules
  /pdf/<policy>/doc.pdf   -> a REAL pdf, top-level, under <policy>   (PDF render test)
  /pdf/<policy>/evil.pdf  -> an HTML doc NAMED .pdf, top-level       (TYPE CONFUSION test)
  /__hits /__reset

Content-Type is ALWAYS derived from the file extension and never sniffed;
X-Content-Type-Options: nosniff is always sent. That pair is the control under test.
"""
import http.server, socketserver, threading, json, sys, os, urllib.parse
FIX=os.path.join(os.path.dirname(os.path.abspath(__file__)),'fixture')
MAIN=int(sys.argv[1]) if len(sys.argv)>1 else 8910
EXT =int(sys.argv[2]) if len(sys.argv)>2 else 8911
HITS_MAIN,HITS_EXT=[],[]
SELF=f"http://127.0.0.1:{MAIN}"
CT={'.html':'text/html; charset=utf-8','.css':'text/css','.js':'application/javascript',
    '.ttf':'font/ttf','.pdf':'application/pdf','.wav':'audio/wav','.png':'image/png'}
SRC=("default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; "
     "img-src 'self' data: blob:; font-src 'self'; media-src 'self'; frame-src 'self'; "
     "connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'")
POLICIES={
  # ACTIVE: what HTML ships with (proven in experiment 1)
  "active":      "sandbox allow-scripts; "+SRC,
  # ACTIVE + the proposed font fix (ACAO on font responses)
  "active-cors": "sandbox allow-scripts; "+SRC,
  # PASSIVE: what PDF/media ship with — NO sandbox
  "passive":     SRC,
  # control
  "none":        None,
}
CORS_FONT={"active-cors"}
class Base(http.server.SimpleHTTPRequestHandler):
    def log_message(self,*a): pass
class Main(Base):
    def _serve(self,fn,policy,name):
        if not os.path.exists(fn):
            self.send_response(404); self.end_headers(); self.wfile.write(b'x'); return
        data=open(fn,'rb').read().replace(b'__EXTPORT__',str(EXT).encode())
        ext=os.path.splitext(fn)[1].lower()
        self.send_response(200)
        self.send_header('Content-Type',CT.get(ext,'application/octet-stream'))  # extension, never sniffed
        self.send_header('X-Content-Type-Options','nosniff')                     # never override
        self.send_header('Content-Disposition','inline')
        self.send_header('Content-Length',str(len(data)))
        self.send_header('Set-Cookie','omnipus_probe=SECRET; Path=/; SameSite=Strict')
        if ext in ('.ttf','.woff2') and name in CORS_FONT:
            self.send_header('Access-Control-Allow-Origin','*')                  # the proposed fix
        if policy: self.send_header('Content-Security-Policy',policy)
        self.end_headers(); self.wfile.write(data)
    def do_GET(self):
        p=urllib.parse.urlparse(self.path).path; HITS_MAIN.append(p)
        if p=='/__hits':
            b=json.dumps({'main':HITS_MAIN,'ext':HITS_EXT}).encode()
            self.send_response(200); self.send_header('Content-Type','application/json')
            self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b); return
        if p=='/__reset':
            HITS_MAIN.clear(); HITS_EXT.clear(); self.send_response(200); self.end_headers(); self.wfile.write(b'ok'); return
        parts=[x for x in p.split('/') if x]
        if parts and parts[0]=='pdf' and len(parts)>=3:
            name=parts[1]; self._serve(os.path.join(FIX,os.path.basename(parts[2])),POLICIES.get(name),name); return
        if parts and parts[0]=='p':
            name=parts[1] if len(parts)>1 else 'none'
            rest=parts[2] if len(parts)>2 else 'index.html'
            self._serve(os.path.join(FIX,'index.html' if rest in ('','index.html') else os.path.basename(rest)),
                        POLICIES.get(name),name); return
        if parts and parts[0]=='f':
            # assets inherit the active-cors font header when asked for via ?c=1
            q=urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
            name='active-cors' if q.get('c') else 'active'
            self._serve(os.path.join(FIX,os.path.basename(parts[1])),None,name); return
        self.send_response(404); self.end_headers(); self.wfile.write(b'x')
class Ext(Base):
    def do_GET(self):
        HITS_EXT.append(urllib.parse.urlparse(self.path).path)
        self.send_response(200); self.send_header('Access-Control-Allow-Origin','*')
        self.send_header('Content-Length','2'); self.end_headers(); self.wfile.write(b'ok')
    do_POST=do_GET
class TS(socketserver.ThreadingTCPServer): allow_reuse_address=True
if __name__=='__main__':
    threading.Thread(target=lambda: TS(('127.0.0.1',EXT),Ext).serve_forever(),daemon=True).start()
    print(f"MAIN {SELF}  EXT http://127.0.0.1:{EXT}",flush=True)
    print("policies: "+", ".join(POLICIES),flush=True)
    TS(('127.0.0.1',MAIN),Main).serve_forever()

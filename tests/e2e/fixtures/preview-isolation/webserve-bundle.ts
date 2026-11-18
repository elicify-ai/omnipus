/**
 * webserve-bundle.ts — fixture content and pure URL helpers for
 * tests/e2e/preview-isolation-webserve.spec.ts (ADR-094 orders 24, 30-32).
 *
 * Kept free of any Playwright runtime import so the vitest companion
 * (webserve-bundle.test.ts) exercises the REAL helpers the browser suite uses,
 * not a copy.
 *
 * The bundle is what an agent-built app looks like to the Mode 2 CSP
 * (docs/internal/specs/adr-094-preview-isolation-spec.md, US-3 AC1 and the
 * "Mode 2 response headers" template, where ${PREFIX} is
 * /preview/{agent}/{token}/ WITH its trailing slash): a linked stylesheet, a
 * module script, and a relative fetch('./data.json') — each a CHILD of the
 * prefix. A real agent turn writes these files with the real file tool and
 * calls the real serve_web (its model edge scripted by mint-provider.ts); the
 * test never registers a preview by any other path (the review's "keep the
 * real product mint path").
 *
 * Every file is one line of plain ASCII with no backslashes.
 */

/** The stylesheet's observable effect on #probe (computed style). */
export const PROBE_LETTER_SPACING = '7px'
/** The value the module script writes on window.__e2eModuleRan. */
export const MODULE_RAN_VALUE = 'yes'
/** data.json's `ok` field. */
export const DATA_OK_VALUE = 'yes'
/** The text frame the dev fixture's WebSocket upstream sends on connect. */
export const WS_HELLO = 'ok'

export type BundleFiles = Readonly<Record<string, string>>

/** Static bundle: served by the gateway's static handler (serve_web path). */
export function staticBundleFiles(dirName: string): BundleFiles {
  return {
    'index.html': `<!doctype html><title>${dirName}</title><p id="probe">${dirName}-ready</p>`,
    'style.css': `#probe{letter-spacing:${PROBE_LETTER_SPACING}}`,
    'app.js': `window.__e2eModuleRan='${MODULE_RAN_VALUE}';`,
    'data.json': `{"ok":"${DATA_OK_VALUE}"}`,
  }
}

/**
 * Dev-server upstream for serve_web's dev mode: serves the bundle files and
 * answers a WebSocket upgrade on any path with a handshake plus one text
 * frame (WS_HELLO). That makes the HMR row a FUNCTIONAL WebSocket check
 * through the dev proxy, not only a "no CSP violation" probe against an
 * upstream that cannot speak WebSocket (review F4).
 *
 * Plain Node (http, fs, path, crypto) — no package install. CRLF is built with
 * String.fromCharCode so the file carries no backslash escapes.
 */
export const DEV_SERVER_JS = [
  "const h=require('http'),f=require('fs'),p=require('path'),c=require('crypto');",
  'const E=String.fromCharCode(13,10);',
  "const T={'.html':'text/html','.css':'text/css','.js':'text/javascript','.json':'application/json'};",
  "const s=h.createServer(function(q,r){let n=q.url.split('?')[0];if(n.endsWith('/'))n=n+'index.html';" +
    "const fp=p.join(__dirname,p.normalize(n));f.readFile(fp,function(e,d){if(e){r.writeHead(404);r.end('missing');return}" +
    "r.writeHead(200,{'Content-Type':T[p.extname(fp)]||'text/plain'});r.end(d)})});",
  "s.on('upgrade',function(q,k){const a=c.createHash('sha1').update(q.headers['sec-websocket-key']+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');" +
    "k.write('HTTP/1.1 101 Switching Protocols'+E+'Upgrade: websocket'+E+'Connection: Upgrade'+E+'Sec-WebSocket-Accept: '+a+E+E);" +
    `k.write(Buffer.from([129,${WS_HELLO.length},${[...WS_HELLO].map((ch) => ch.charCodeAt(0)).join(',')}]))});`,
  's.listen(Number(process.env.PORT));',
].join('\n')

/** Dev bundle: the static files plus the Node upstream serve_web starts. */
export function devBundleFiles(dirName: string): BundleFiles {
  return {
    ...staticBundleFiles(dirName),
    'server.js': DEV_SERVER_JS,
    'package.json': '{"scripts":{"dev":"node server.js"}}',
  }
}

/**
 * Directory form of a Mode 2 preview URL: always ends in "/", matching the
 * spec's ${PREFIX}. Throws on anything that is not a /preview/{agent}/{token}
 * URL, so a malformed mint fails loudly at the call site.
 */
export function previewPrefixUrl(mode2Url: string): URL {
  const u = new URL(mode2Url)
  const m = /^\/preview\/[^/]+\/[^/]+\/?/.exec(u.pathname)
  if (!m) throw new Error(`not a /preview/{agent}/{token}/ URL: ${mode2Url}`)
  const prefix = m[0].endsWith('/') ? m[0] : `${m[0]}/`
  return new URL(prefix, u.origin)
}

/** A child URL INSIDE the prefix directory (never a sibling token). */
export function childOfPrefix(mode2Url: string, child: string): string {
  if (child.startsWith('/') || child.includes('..')) {
    throw new Error(`child must be a relative name inside the prefix: ${child}`)
  }
  return new URL(child, previewPrefixUrl(mode2Url)).href
}

/**
 * An out-of-prefix sibling under the same agent: same agent segment, a
 * different token that merely starts with the real one. A string-prefix
 * match would wrongly allow it; the spec's directory source must not.
 */
export function siblingTokenUrl(mode2Url: string, child: string): string {
  const prefix = previewPrefixUrl(mode2Url)
  const trimmed = prefix.pathname.replace(/\/$/, '')
  return new URL(`${trimmed}-sibling/${child}`, prefix.origin).href
}

/**
 * A Mode 1 isolated URL valid for the given SPA origin: one lower-case label
 * plus ".localhost" on the SAME port as the SPA (src/lib/preview-url.ts::
 * validateIsolatedUrl requires port equality; a portless URL only for port
 * 80). Derived from the real test origin, never a fixed port.
 */
export function isolatedUrlFor(spaOrigin: string, label: string): string {
  if (!/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label)) {
    throw new Error(`invalid Mode 1 label: ${label}`)
  }
  const origin = new URL(spaOrigin)
  const port = origin.port === '' ? (origin.protocol === 'https:' ? '443' : '80') : origin.port
  return port === '80' ? `http://${label}.localhost/` : `http://${label}.localhost:${port}/`
}

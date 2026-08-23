/**
 * harness.ts — the observation apparatus for ADR-067's preview-isolation E2E
 * suite. No assertions live here; only the things that watch.
 *
 * WHY THIS EXISTS SEPARATELY FROM THE SPEC FILE. The whole suite rests on one
 * claim: "nothing reached the second origin". That claim is only worth anything
 * if the second origin can be shown to notice things when they DO reach it, and
 * if the exact same observation apparatus is used for the product run and for
 * the deliberately-broken runs. Sharing one module is what makes the mutation
 * tests in preview-isolation.spec.ts a real proof rather than a parallel
 * implementation that might notice different things.
 *
 * GROUND TRUTH IS SERVER-SIDE (ADR-067 §13.1 test 11, experiment §0). What
 * arrives at a server was allowed out; what never arrives was blocked. In-page
 * JavaScript reports and browser console text are corroborating only — the
 * experiment measured that CSP violation wording differs on every engine
 * (experiment §4.4), so a string match silently stops matching on a new
 * browser version and the test goes quietly green.
 */
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import { fileURLToPath } from 'node:url';
import type { BrowserContext, Page } from '@playwright/test';

const HERE = path.dirname(fileURLToPath(import.meta.url));

/** The bundle the hostile fixture lives in, as copied into a workspace. */
export const BUNDLE_DIR_NAME = 'e2e-preview-isolation';
/** Source of the fixture files (this directory's own `bundle/`). */
export const BUNDLE_SOURCE_DIR = path.join(HERE, 'bundle');

/**
 * The seven egress vectors, and the second-origin path each one aims at.
 *
 * These are the MEASURED cases, taken from the committed experiment harness
 * (docs/internal/experiments/preview-isolation/fixture/index.html + server.py),
 * not re-derived here. `popup` matches a prefix because the fixture attempts it
 * twice — once on load and once from a real click — so that a browser's own
 * popup blocker cannot make the positive control pass for the wrong reason.
 */
export const EGRESS_VECTORS = [
  { name: 'image', pathPrefix: '/x/img' },
  { name: 'fetch', pathPrefix: '/x/fetch' },
  { name: 'beacon', pathPrefix: '/x/beacon' },
  { name: 'websocket', pathPrefix: '/x/ws' },
  { name: 'iframe', pathPrefix: '/x/iframe' },
  { name: 'form', pathPrefix: '/x/form' },
  { name: 'popup', pathPrefix: '/x/popup' },
] as const;

export type EgressVectorName = (typeof EGRESS_VECTORS)[number]['name'];

export interface RecordedHit {
  /** Request path including query string, exactly as it arrived. */
  path: string;
  method: string;
  /** 'upgrade' is a WebSocket handshake — Node surfaces those on a separate event. */
  kind: 'request' | 'upgrade';
  /** Cookie header as received, or undefined when the request carried none. */
  cookie?: string;
}

export interface RecordingOrigin {
  origin: string;
  port: number;
  hits: RecordedHit[];
  reset(): void;
  close(): Promise<void>;
}

/**
 * Which of the seven vectors reached this origin.
 *
 * Prefix matching, because a form GET submit appends its own query string and a
 * popup may arrive under either popup path.
 */
export function vectorsReaching(hits: RecordedHit[]): EgressVectorName[] {
  const seen = new Set<EgressVectorName>();
  for (const hit of hits) {
    for (const vector of EGRESS_VECTORS) {
      if (hit.path.startsWith(vector.pathPrefix)) seen.add(vector.name);
    }
  }
  return [...seen];
}

/**
 * The SECOND ORIGIN — "the internet".
 *
 * A different host:port from the gateway, so it is a different origin for both
 * the same-origin policy and for CSP's `'self'`. It answers everything with
 * `Access-Control-Allow-Origin: *`, exactly as the experiment's EXT server did:
 * a request that the policy permits must SUCCEED here, so that a failure can
 * only ever mean the policy stopped it.
 */
export async function startExternalOrigin(): Promise<RecordingOrigin> {
  return startRecordingServer((req, res, hits) => {
    hits.push({
      path: req.url ?? '',
      method: req.method ?? 'GET',
      kind: 'request',
      cookie: req.headers.cookie,
    });
    res.writeHead(200, {
      'Content-Type': 'text/plain',
      'Access-Control-Allow-Origin': '*',
      'Content-Length': '2',
    });
    res.end('ok');
  });
}

/** The five candidate policies, keyed exactly as the experiment keyed them. */
export type MutantPolicyName = 'shipped' | 'nosandbox' | 'sandboxonly' | 'none';

/**
 * Split a live policy string into its sandbox half and its source-directive
 * half, so a mutant can be built by DELETING one of them.
 *
 * Derived from the string the gateway actually sent, never re-typed here. If
 * the shipped policy is edited, the mutants follow it automatically — a
 * hand-copied mutant would keep testing the policy we used to have.
 */
export function splitPolicy(policy: string): { sandbox: string; sources: string } {
  const directives = policy.split(';').map((d) => d.trim()).filter(Boolean);
  const sandbox = directives.filter((d) => d === 'sandbox' || d.startsWith('sandbox '));
  const sources = directives.filter((d) => !(d === 'sandbox' || d.startsWith('sandbox ')));
  return { sandbox: sandbox.join('; '), sources: sources.join('; ') };
}

/**
 * Build the mutation table from the live shipped policy.
 *
 * `nosandbox`   — the source directives alone. The experiment measured
 *                 window.open still reaching the second origin under this.
 * `sandboxonly` — the sandbox directive alone. The experiment measured five of
 *                 seven vectors escaping under this.
 * `none`        — no policy at all. The control that must show all seven, or
 *                 no other row means anything (experiment §6).
 */
export function mutantPolicies(shipped: string): Record<MutantPolicyName, string | null> {
  const { sandbox, sources } = splitPolicy(shipped);
  return {
    shipped,
    nosandbox: sources,
    sandboxonly: sandbox,
    none: null,
  };
}

export interface MutantOrigin extends RecordingOrigin {
  /** URL of the hostile document served under the named policy. */
  documentURL(policy: MutantPolicyName, file?: string): string;
  /** The policy strings this origin is serving, for reporting in failures. */
  policies: Record<MutantPolicyName, string | null>;
}

const CONTENT_TYPES: Record<string, string> = {
  '.html': 'text/html; charset=utf-8',
  '.css': 'text/css',
  '.js': 'application/javascript',
  '.png': 'image/png',
};

/**
 * A third origin that serves the SAME fixture bytes under a CHOSEN policy.
 *
 * This is the mutation apparatus. The product cannot be asked to serve a broken
 * policy — pkg/gateway is not this suite's to edit, and a test that edits the
 * thing it is testing proves nothing anyway — so the mutants are served here,
 * from the same files, and driven through the same assertions.
 *
 * It sets a `SameSite=Strict` cookie on every response for the same reason
 * server.py did: cookie readability is only a meaningful assertion when there
 * is a cookie to read, and the `nosandbox` mutant must be seen to read it back.
 */
export async function startMutantOrigin(
  policies: Record<MutantPolicyName, string | null>,
  extPort: number,
): Promise<MutantOrigin> {
  const server = await startRecordingServer((req, res, hits) => {
    const url = req.url ?? '/';
    hits.push({
      path: url,
      method: req.method ?? 'GET',
      kind: 'request',
      cookie: req.headers.cookie,
    });

    const [rawPath] = url.split('?');
    const parts = rawPath.split('/').filter(Boolean);

    // /m/<policy>/<file>
    if (parts[0] === 'm' && parts.length >= 2) {
      const policyName = parts[1] as MutantPolicyName;
      const file = parts[2] || 'index.html';
      const filePath = path.join(BUNDLE_SOURCE_DIR, path.basename(file));
      if (!fs.existsSync(filePath)) {
        res.writeHead(404, { 'Content-Type': 'text/plain' });
        res.end('no such fixture file');
        return;
      }
      let body = fs.readFileSync(filePath);
      if (filePath.endsWith('.html')) {
        body = Buffer.from(body.toString('utf8').replaceAll('__EXTPORT__', String(extPort)));
      }
      const headers: Record<string, string> = {
        'Content-Type': CONTENT_TYPES[path.extname(filePath)] ?? 'application/octet-stream',
        'X-Content-Type-Options': 'nosniff',
        'Content-Length': String(body.length),
        'Set-Cookie': 'omnipus_probe=SECRET; Path=/; SameSite=Strict',
      };
      const policy = policies[policyName];
      if (policy) headers['Content-Security-Policy'] = policy;
      res.writeHead(200, headers);
      res.end(body);
      return;
    }

    // Everything else — including the fixture's absolute /api/v1/state probes —
    // is recorded and answered, so an ALLOWED same-origin request is seen to
    // succeed here rather than merely being seen to leave.
    res.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Length': '2' });
    res.end('ok');
  });

  return {
    ...server,
    policies,
    documentURL(policy: MutantPolicyName, file = 'index.html') {
      return `${server.origin}/m/${policy}/${file}`;
    },
  };
}

async function startRecordingServer(
  handle: (req: http.IncomingMessage, res: http.ServerResponse, hits: RecordedHit[]) => void,
): Promise<RecordingOrigin> {
  const hits: RecordedHit[] = [];
  const server = http.createServer((req, res) => handle(req, res, hits));

  // A WebSocket handshake never reaches the ordinary request handler — Node
  // routes it to 'upgrade'. Omitting this listener would silently lose one of
  // the seven vectors, and losing it looks exactly like blocking it.
  server.on('upgrade', (req, socket) => {
    hits.push({
      path: req.url ?? '',
      method: req.method ?? 'GET',
      kind: 'upgrade',
      cookie: req.headers.cookie,
    });
    socket.destroy();
  });

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;

  return {
    origin: `http://127.0.0.1:${port}`,
    port,
    hits,
    reset() { hits.length = 0; },
    close() {
      return new Promise<void>((resolve) => {
        server.closeAllConnections?.();
        server.close(() => resolve());
      });
    },
  };
}

/**
 * Copy the hostile bundle into a workspace's work tree, substituting the second
 * origin's port.
 *
 * Written straight to disk rather than through an upload endpoint on purpose:
 * what is under test is the SERVING path, and a write path that does not exist
 * yet (stage 3) must not be able to block a stage-1 security test.
 */
export function installBundle(workRoot: string, extPort: number): string {
  const dest = path.join(workRoot, BUNDLE_DIR_NAME);
  fs.rmSync(dest, { recursive: true, force: true });
  fs.mkdirSync(dest, { recursive: true });
  for (const entry of fs.readdirSync(BUNDLE_SOURCE_DIR)) {
    const from = path.join(BUNDLE_SOURCE_DIR, entry);
    const to = path.join(dest, entry);
    if (entry.endsWith('.html')) {
      fs.writeFileSync(to, fs.readFileSync(from, 'utf8').replaceAll('__EXTPORT__', String(extPort)));
    } else {
      fs.copyFileSync(from, to);
    }
  }
  return dest;
}

/** Where the gateway keeps a workspace's work tree (pkg/library/root.go). */
export function workspaceWorkRoot(omnipusHome: string, workspaceID: string): string {
  return path.join(omnipusHome, 'workspaces', workspaceID, 'work');
}

export interface BrowserRequestRecord {
  url: string;
  method: string;
  resourceType: string;
  /** Cookie header the browser actually attached, once `settle()` has resolved. */
  cookie?: string;
  /**
   * Whether the on-the-wire header set was actually retrieved.
   *
   * Load-bearing, not bookkeeping. `cookie` is `undefined` both when the browser
   * attached no cookie and when the headers could not be read at all — and a
   * CSP-blocked request's headers sometimes never resolve. Without this flag,
   * "the preview sent no session cookie" would be indistinguishable from "we
   * never found out", which is the whole failure mode FR-006a warns about.
   */
  headersRead: boolean;
  status?: number;
  failed?: boolean;
}

export interface BrowserRequestLog {
  records: BrowserRequestRecord[];
  /** Await before asserting on `cookie`: header retrieval is asynchronous. */
  settle(): Promise<void>;
  matching(predicate: (r: BrowserRequestRecord) => boolean): BrowserRequestRecord[];
}

/**
 * Record every request the browser context issues, with the headers it actually
 * attached.
 *
 * This is the ONLY oracle available for requests aimed at the gateway itself:
 * the gateway is a Go process this suite may not instrument. It is used for two
 * things and nothing else — proving a request LEFT (arrival at the gateway is
 * then confirmed by the response it got back), and reading the Cookie header
 * the browser attached to it.
 *
 * That second use is why every test using it carries a POSITIVE CONTROL in
 * which a cookie IS expected: "no Cookie header" is otherwise indistinguishable
 * from "this oracle cannot see cookies at all", which is precisely the shape of
 * false-green ADR-067 keeps warning about.
 *
 * ⚠️ ENGINE LIMIT, MEASURED 2026-08-23 — READ BEFORE USING THIS FOR A NEGATIVE.
 * WebKit reports NONE of a sandboxed iframe's requests here: not the document,
 * not its subresources, not a nested frame's. The same page loaded TOP-LEVEL is
 * reported normally, and Chromium reports both. So any assertion of the form
 * "no request to X was seen from inside a preview frame" is AUTOMATICALLY TRUE
 * on WebKit and proves nothing there. Assert negatives about an embedded
 * preview through the frame's own DOM (Playwright reaches a frame over the
 * browser protocol, which is origin-independent), or move the case to
 * top-level, where every engine reports.
 */
export function recordBrowserRequests(context: BrowserContext): BrowserRequestLog {
  const records: BrowserRequestRecord[] = [];
  const pending: Promise<unknown>[] = [];

  context.on('request', (req) => {
    const record: BrowserRequestRecord = {
      url: req.url(),
      method: req.method(),
      resourceType: req.resourceType(),
      headersRead: false,
    };
    records.push(record);
    // BOUNDED. `allHeaders()` on a request the browser refused can stay pending
    // for the life of the page — measured: an unbounded `Promise.all` over these
    // hangs `settle()` and the test dies on its own timeout with no clue why.
    // A record whose headers never arrive keeps `headersRead: false`, so a
    // caller can refuse to draw a conclusion from it rather than silently
    // drawing the wrong one.
    pending.push(
      Promise.race([
        req.allHeaders()
          .then((headers) => { record.cookie = headers['cookie']; record.headersRead = true; })
          .catch(() => { /* request gone before headers resolved */ }),
        new Promise<void>((resolve) => { setTimeout(resolve, 5_000).unref?.(); }),
      ]),
    );
  });
  context.on('response', (res) => {
    const record = records.find((r) => r.url === res.url() && r.status === undefined);
    if (record) record.status = res.status();
  });
  context.on('requestfailed', (req) => {
    const record = records.find((r) => r.url === req.url() && r.status === undefined);
    if (record) record.failed = true;
  });

  return {
    records,
    async settle() { await Promise.all(pending); },
    matching(predicate) { return records.filter(predicate); },
  };
}

/** Mint a preview token through the real authenticated endpoint (FR-003f). */
export async function mintPreviewToken(
  page: Page,
  workspaceID: string,
  relPath: string,
  scope: 'file' | 'bundle',
  entryPath?: string,
): Promise<{ token: string; url: string; scopeRoot: string }> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf');
  const response = await page.request.post('/api/v1/library/preview-token', {
    headers: {
      'Content-Type': 'application/json',
      ...(csrf ? { 'X-CSRF-Token': csrf.value } : {}),
    },
    data: {
      workspace_id: workspaceID,
      path: relPath,
      scope,
      ...(entryPath ? { entry_path: entryPath } : {}),
    },
  });
  if (!response.ok()) {
    throw new Error(
      `mint preview token failed: ${response.status()} ${await response.text()}`,
    );
  }
  const body = await response.json() as { token: string; url: string; scope_root: string };
  return { token: body.token, url: body.url, scopeRoot: body.scope_root };
}

/**
 * Embed a preview exactly the way the product must (§10.6, FR-005b).
 *
 * `<iframe src="<token URL>">` — NEVER srcdoc, which resolves relative URLs
 * against the embedder (so no bundle subresource would load at all) and has no
 * response to carry the isolation policy. Plus the three attributes FR-005b
 * names: `sandbox="allow-scripts"`, `referrerpolicy="no-referrer"` (the token
 * is IN the URL and must not leave in a Referer) and an empty `allow=""`.
 */
export async function embedPreview(page: Page, tokenURL: string): Promise<void> {
  await page.evaluate((src) => {
    document.querySelectorAll('#e2e-preview-frame').forEach((n) => n.remove());
    const frame = document.createElement('iframe');
    frame.id = 'e2e-preview-frame';
    frame.setAttribute('src', src);
    frame.setAttribute('sandbox', 'allow-scripts');
    frame.setAttribute('referrerpolicy', 'no-referrer');
    frame.setAttribute('allow', '');
    frame.setAttribute('width', '800');
    frame.setAttribute('height', '600');
    document.body.appendChild(frame);
  }, tokenURL);
}

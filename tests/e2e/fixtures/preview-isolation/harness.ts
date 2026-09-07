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

/**
 * The candidate policies. The first four are keyed exactly as the experiment
 * keyed them; `wrongsource` is this suite's own addition — see
 * `repointSourcesToDeadOrigin` for what it is for.
 */
export type MutantPolicyName =
  | 'shipped'
  | 'nosandbox'
  | 'sandboxonly'
  | 'wrongsource'
  | 'none';

/** A directive is the sandbox one, or it is a source directive. Nothing else. */
function isSandboxDirective(directive: string): boolean {
  return directive === 'sandbox' || directive.startsWith('sandbox ');
}

/** Split a policy header into trimmed, non-empty directives. */
export function policyDirectives(policy: string): string[] {
  return policy.split(';').map((d) => d.trim()).filter(Boolean);
}

/**
 * Split a live policy string into its sandbox half and its source-directive
 * half, so a mutant can be built by DELETING one of them.
 *
 * Derived from the string the gateway actually sent, never re-typed here. If
 * the shipped policy is edited, the mutants follow it automatically — a
 * hand-copied mutant would keep testing the policy we used to have.
 *
 * ⚠️ THIS SPLIT KNOWS NOTHING ABOUT `'self'`, DELIBERATELY. It partitions on
 * the DIRECTIVE NAME — `sandbox` versus everything else — so it kept working
 * unchanged when §10.3 ADDED the gateway's origins alongside `'self'`
 * (2026-08-23), and would keep working if either form went away. Any future
 * edit that starts matching on a source EXPRESSION
 * rather than a directive name re-introduces exactly the coupling this comment
 * exists to forbid: the mutants would then follow the keyword rather than the
 * policy, and would quietly stop mutating the day the keyword changed again.
 */
export function splitPolicy(policy: string): { sandbox: string; sources: string } {
  const directives = policyDirectives(policy);
  const sandbox = directives.filter((d) => isSandboxDirective(d));
  const sources = directives.filter((d) => !isSandboxDirective(d));
  return { sandbox: sandbox.join('; '), sources: sources.join('; ') };
}

/**
 * The source expressions of one directive, or `null` when the policy has no
 * such directive at all.
 *
 * `null` and `[]` are kept distinct on purpose: "the policy does not mention
 * script-src" and "script-src permits nothing" are different findings, and a
 * caller that cannot tell them apart writes the vacuous version of both.
 */
export function directiveSources(policy: string, name: string): string[] | null {
  for (const directive of policyDirectives(policy)) {
    const parts = directive.split(/\s+/);
    if (parts[0] === name) return parts.slice(1);
  }
  return null;
}

/** Trailing-slash-insensitive origin comparison — `http://h:1/` is `http://h:1`. */
function sameOrigin(a: string, b: string): boolean {
  return a.replace(/\/+$/, '') === b.replace(/\/+$/, '');
}

/**
 * Whether `directive` names `origin` EXPLICITLY, as a host source.
 *
 * ⚠️ `'self'` DOES NOT COUNT HERE, AND THAT IS THE ENTIRE POINT. An earlier
 * version of this helper accepted either form. Under §10.3 as it now stands
 * that made it permanently true and therefore worthless: the policy retains
 * `'self'` on every directive, so "does this permit the browser's origin?"
 * answers yes before the explicit half is even looked at.
 *
 * The two halves do different jobs and only one of them can be checked this
 * way (§10.3, 2026-08-23):
 *
 *   `'self'`            resolves to the document's own origin, so it matches
 *                       whatever spelling the reader typed. On Chromium and
 *                       Firefox it carries the whole load and no origin string
 *                       can break it.
 *   the explicit origins is what WebKit needs, because WebKit does not resolve
 *                       `'self'` at all inside a frame carrying FR-005b's
 *                       `sandbox` ATTRIBUTE (CSP3 §2.2.2 says self-origin comes
 *                       from the RESPONSE URL; WebKit reads it off the
 *                       document's opaque origin instead — WebKit bug 316847,
 *                       fixed upstream 315247@main but not in any shipping
 *                       Safari). An explicit host source is matched by string,
 *                       so it works where `'self'` does not — and only for the
 *                       exact spellings named.
 *
 * So a policy that names no origin the browser is on still renders correctly on
 * two engines out of three, and silently renders blank on the third. That is
 * the failure this helper exists to make loud, and accepting `'self'` as
 * sufficient would hide it exactly where it hides in production.
 */
export function directiveNamesOriginExplicitly(
  policy: string,
  name: string,
  origin: string,
): boolean {
  const sources = directiveSources(policy, name);
  if (sources === null) return false;
  return sources.some((s) => s !== "'self'" && sameOrigin(s, origin));
}

/**
 * The host sources of a directive — every source expression that is not a
 * keyword or a bare scheme. Used only to put the real list in a failure
 * message, so an operator sees what the policy DID name next to what it
 * needed to name.
 */
export function directiveHostSources(policy: string, name: string): string[] {
  return (directiveSources(policy, name) ?? []).filter(
    (s) => /^[a-z][a-z0-9+.-]*:\/\//i.test(s),
  );
}

/**
 * An origin that is guaranteed not to be serving anything: port 1 on loopback.
 *
 * Used only to build the `wrongsource` mutant. It must not be a port this
 * suite ever listens on, or the mutant would accidentally permit the real
 * fixture and stop mutating.
 */
export const DEAD_ORIGIN = 'http://127.0.0.1:1';

/**
 * Rewrite every ORIGIN-BEARING source expression in a policy to `DEAD_ORIGIN`.
 *
 * WHY THIS EXISTS. `preview-isolation.spec.ts` asserts that a previewed
 * bundle's EXTERNAL `<script src>` and `<link rel=stylesheet>` really load
 * (FR-004, US-1 AS-4). That assertion is worth nothing unless it can be shown
 * to fail — and "the page rendered" is precisely the shape of claim that goes
 * quietly true when nobody checks the negative. This mutation is that check:
 * same bytes, same frame, same assertion, a policy whose source directives
 * name an origin the document is not on.
 *
 * MECHANICAL AND POLICY-SHAPE-AGNOSTIC. It rewrites `'self'` AND any explicit
 * `scheme://host[:port]`, and leaves `'none'`, `'unsafe-inline'`, `data:` and
 * `blob:` alone. So it produced a real mutant both before and after §10.3
 * swapped one form for the other, and it is not a second hand-copy of the
 * policy that would drift.
 *
 * The `sandbox` directive is passed through untouched: the mutant must keep the
 * origin opaque, or `#result` could not be read out of the frame and the
 * mutation would fail for the wrong reason.
 */
export function repointSourcesToDeadOrigin(policy: string): string {
  return policyDirectives(policy)
    .map((directive) => {
      if (isSandboxDirective(directive)) return directive;
      const [name, ...sources] = directive.split(/\s+/);
      const rewritten = sources.map((s) =>
        s === "'self'" || /^[a-z][a-z0-9+.-]*:\/\//i.test(s) ? DEAD_ORIGIN : s,
      );
      return [name, ...new Set(rewritten)].join(' ');
    })
    .join('; ');
}

/**
 * Build the mutation table from the live shipped policy.
 *
 * `nosandbox`   — the source directives alone. The experiment measured
 *                 window.open still reaching the second origin under this.
 * `sandboxonly` — the sandbox directive alone. The experiment measured five of
 *                 seven vectors escaping under this.
 * `wrongsource` — every origin-bearing source repointed at a dead origin. Not
 *                 an experiment row: it is the RENDER assertions' mutation
 *                 proof, and the only mutant whose expected result is "the
 *                 bundle's own assets do NOT load".
 * `none`        — no policy at all. The control that must show all seven, or
 *                 no other row means anything (experiment §6).
 */
export function mutantPolicies(shipped: string): Record<MutantPolicyName, string | null> {
  const { sandbox, sources } = splitPolicy(shipped);
  return {
    shipped,
    nosandbox: sources,
    sandboxonly: sandbox,
    wrongsource: repointSourcesToDeadOrigin(shipped),
    none: null,
  };
}

export interface MutantOrigin extends RecordingOrigin {
  /** URL of the hostile document served under the named policy. */
  documentURL(policy: MutantPolicyName, file?: string): string;
  /**
   * A bare, policy-free HTML page on this origin, to be used as an EMBEDDER.
   *
   * The gateway's own SPA shell cannot play that role for a mutant: §10.7 gives
   * it `frame-src 'self'`, so an `<iframe>` pointed at this origin is refused
   * before any mutation is exercised. This page carries no policy at all, so
   * the only thing constraining the frame it holds is the frame's own `sandbox`
   * ATTRIBUTE — which is exactly the isolation half that has to be measured
   * standing alone.
   */
  embedderURL(): string;
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

    // /embedder — a policy-free host document. See MutantOrigin.embedderURL.
    if (parts[0] === 'embedder') {
      const body = Buffer.from(
        '<!doctype html><meta charset="utf-8"><title>mutant-embedder</title><body></body>',
      );
      res.writeHead(200, {
        'Content-Type': 'text/html; charset=utf-8',
        'Content-Length': String(body.length),
      });
      res.end(body);
      return;
    }

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
    embedderURL() {
      return `${server.origin}/embedder`;
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

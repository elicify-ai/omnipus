import { expect, type Page, type TestInfo } from '@playwright/test';
import { expectPainted, readPaintedRows } from './helper-notices-visible';
import { assertCountdownPaint } from './helper-notices-paint';

// not-wire-format: private delivery/DOM observations, never sent to the app.
type FirstCheck = { frame: Record<string, unknown> | null; source: string; receivedAtMs: number;
  parsedAtMs: number; checkedAtMs: number; rows: Record<string, string[]> };
type ReceiptState = { result: FirstCheck | null; arm: (sessionId: string, names: string[]) => void;
  capture: (frame: Record<string, unknown> | null, source: string, receivedAtMs: number, parsedAtMs: number) => FirstCheck };
declare global { interface Window { __hmCatchup: ReceiptState; } }

// Passive observers only: actual native sockets/workers, unchanged bytes,
// parsing, handlers and render scheduling. Socket receipt alone is insufficient
// when the app parses in a Worker then schedules its ordinary rAF batch.
function startCatchupRecorder(initialSession: string) {
  if (window !== window.top) return;
  let session = initialSession, names = ['Writer', 'Researcher'], pending = false;
  const rawReceipts = new Map<string, number>();
  const workers = new Set<Worker>();
  const key = (frame: Record<string, unknown>) => JSON.stringify([frame.session_id, frame.boot_id, frame.seq, frame.mode]);
  const state: ReceiptState = {
    result: null,
    arm(sessionId, targetNames) { session = sessionId; names = targetNames; pending = false; state.result = null; rawReceipts.clear(); },
    capture(frame, source, receivedAtMs, parsedAtMs) {
      if (state.result) return state.result;
      const nodes = [...document.querySelectorAll('span, p, div')];
      const rows = Object.fromEntries(names.map(name => {
        const literal = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        return [name, window.__hmReadPaintedRows(nodes, { prefix: `^${literal}: `, clause: '.*', minimal: true }).map(row => row.text)];
      }));
      state.result = { frame, source, receivedAtMs, parsedAtMs, checkedAtMs: performance.now(), rows };
      return state.result;
    },
  };
  window.__hmCatchup = state;
  const parsed = (frame: Record<string, unknown> | null | undefined, source: string) => {
    if (!frame || frame.type !== 'catch_up_complete' || frame.session_id !== session || pending || state.result) return;
    const receivedAtMs = rawReceipts.get(key(frame));
    if (receivedAtMs === undefined) return; // Never accept upstream observation or an old parser reply.
    pending = true;
    const parsedAtMs = performance.now();
    // The transparent onmessage wrapper observes AFTER the native application
    // callback returns and has queued its real render batch. A microtask
    // keeps our single check after that callback's own synchronous work.
    // Freeze once at the first following frame, never poll for empty rows.
    queueMicrotask(() => requestAnimationFrame(() => state.capture(frame, source, receivedAtMs, parsedAtMs)));
  };
  const afterMessage = (target: WebSocket | Worker, observe: (event: MessageEvent, receivedAtMs: number) => void) => {
    let owner = Object.getPrototypeOf(target), descriptor: PropertyDescriptor | undefined;
    while (owner && !descriptor) { descriptor = Object.getOwnPropertyDescriptor(owner, 'onmessage'); owner = Object.getPrototypeOf(owner); }
    if (!descriptor?.set) throw new Error('catch-up instrument: native onmessage setter unavailable');
    const nativeSetter = descriptor.set;
    let handler: ((event: MessageEvent) => unknown) | null = null;
    Object.defineProperty(target, 'onmessage', { configurable: true, get: () => handler, set(next) {
      handler = next;
      nativeSetter.call(target, typeof next !== 'function' ? next : function (event: MessageEvent) {
        const receivedAtMs = performance.now();
        const result = next.call(target, event);
        observe(event, receivedAtMs);
        return result;
      });
    } });
  };
  const NativeWorker = window.Worker;
  if (NativeWorker) {
    window.Worker = new Proxy(NativeWorker, { construct(target, args) {
      const worker = Reflect.construct(target, args) as Worker;
      // Only the existing WS parser worker can acknowledge parsed chat frames.
      if (String(args[0]).includes('ws-parser')) {
        workers.add(worker);
        worker.addEventListener('error', () => workers.delete(worker));
        afterMessage(worker, event => parsed(event.data?.frame, 'page-parser-worker'));
      }
      return worker;
    } });
    const terminate = NativeWorker.prototype.terminate;
    NativeWorker.prototype.terminate = function () { workers.delete(this); return terminate.call(this); };
  }
  const NativeSocket = window.WebSocket;
  window.WebSocket = new Proxy(NativeSocket, { construct(target, args) {
    const socket = Reflect.construct(target, args) as WebSocket;
    if (String(args[0]).includes('/api/v1/chat/ws')) afterMessage(socket, (event, receivedAtMs) => {
      if (!session || typeof event.data !== 'string') return;
      const frame = JSON.parse(event.data) as Record<string, unknown>;
      if (frame.type !== 'catch_up_complete' || frame.session_id !== session) return;
      rawReceipts.set(key(frame), receivedAtMs);
      if (workers.size === 0) parsed(frame, 'page-socket-inline-parser');
    });
    return socket;
  } });
}

export async function installNoticeCatchupRecorder(page: Page, initialSession = '') {
  await page.addInitScript({ content: `window.__hmReadPaintedRows = (${readPaintedRows.toString()}); (${startCatchupRecorder.toString()})(${JSON.stringify(initialSession)});` });
  return {
    async arm(sessionId: string) { await page.evaluate(id => window.__hmCatchup.arm(id, ['Writer', 'Researcher']), sessionId); },
    async first(info: TestInfo, label = 'reconnect') {
      let first: FirstCheck | null = null;
      // Poll ONLY availability of the immutable page observation, never its
      // content. A stale first row remains a failure even if it clears later.
      await expect.poll(async () => {
        first = await page.evaluate(() => window.__hmCatchup.result);
        return first !== null;
      }, { message: 'delivery control: page received catch-up and recorded its FIRST post-dispatch frame' }).toBe(true);
      const observed = first as unknown as FirstCheck;
      await info.attach(`helper-notice-first-post-catchup-${label}`, { body: JSON.stringify(observed, null, 2), contentType: 'application/json' });
      expect(observed.frame?.type, 'delivery control: actual page-received completion, not upstream frames').toBe('catch_up_complete');
      expect(observed.checkedAtMs, 'delivery control: first check follows actual page receipt/parse').toBeGreaterThanOrEqual(observed.parsedAtMs);
      return observed;
    },
  };
}

export function expectNoStaleRestartRows(first: FirstCheck) {
  expect(first.rows.Writer, 'restart BDD: no stale Writer at the first post-catch-up check').toEqual([]);
  expect(first.rows.Researcher, 'restart BDD: no stale Researcher at the first post-catch-up check').toEqual([]);
}

export async function checkCatchupInstrument(page: Page, info: TestInfo) {
  await installNoticeCatchupRecorder(page);
  await page.goto('data:text/html,<main><p id="row">Writer: stale waiting notice</p></main>');
  const observations = await page.evaluate(() => {
    const state = window.__hmCatchup;
    state.arm('instrument-only', ['Writer', 'Researcher']);
    // Direct private DOM control, no socket, worker reply or gateway frame.
    const stale = state.capture(null, 'instrument-only', 1, 2);
    document.querySelector('#row')!.textContent = '';
    const laterClear = state.capture(null, 'instrument-only', 1, 2);
    state.arm('instrument-only', ['Writer', 'Researcher']);
    const empty = state.capture(null, 'instrument-only', 3, 4);
    document.querySelector('#row')!.textContent = 'Writer: later notice';
    const laterNotice = state.capture(null, 'instrument-only', 3, 4);
    return { stale, laterClear, empty, laterNotice };
  });
  expect(observations.stale.rows.Writer).toEqual(['Writer: stale waiting notice']);
  expect(observations.laterClear, 'a later clear cannot replace the FIRST stale check').toEqual(observations.stale);
  expect(() => expectNoStaleRestartRows(observations.stale)).toThrow(/no stale Writer at the first post-catch-up check/);
  expectNoStaleRestartRows(observations.empty);
  expect(observations.laterNotice, 'a later resumed notice cannot change the FIRST empty check').toEqual(observations.empty);
  await page.setContent('<main data-active-session-id="expired-instrument"><div data-testid="provider-retry-indicator">Retrying now…</div></main>');
  await expectPainted(page.getByTestId('provider-retry-indicator'), 'Retrying now…');
  const expired = await page.evaluate(() => window.__hmPaint.snapshot());
  const target = { sessionId: 'expired-instrument', kind: 'own' as const, retryAt: new Date(expired.documentEpochMs - 1_000).toISOString() };
  expect(() => assertCountdownPaint(expired, target, 'instrument expired retry'))
    .toThrow(/first countdown restored and painted within 2 s of reload/);
  await info.attach('helper-notice-catchup-instrument', { body: JSON.stringify({ observations, expiredRetryRejected: true }), contentType: 'application/json' });
}

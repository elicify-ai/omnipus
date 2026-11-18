import { expect, type Page, type TestInfo } from '@playwright/test';
import { readPaintedRows } from './helper-notices-visible';

// not-wire-format: private page-side test observations, never gateway payloads.
export type CountdownTarget = { sessionId: string; kind: 'own' | 'named'; name?: string; ordinal?: number; retryAt: string };
type PaintRow = { sessionId: string; kind: 'own' | 'named'; name: string; ordinal: number; text: string;
  paintedAtMs: number; confirmedAtMs: number; paintedEpochMs: number; displayedSeconds: number };
type PaintSnapshot = { documentEpochMs: number; loadEventAtMs: number | null; rows: PaintRow[]; errors: string[]; deadlineObservedAtMs: number | null };
type PaintEvent = PaintSnapshot & { event: 'navigation' | 'first-countdown' | 'deadline' };
type PaintState = PaintSnapshot & { deadline: Promise<void>; snapshot: () => PaintSnapshot };

declare global {
  interface Window {
    __hmReadPaintedRows: typeof readPaintedRows;
    __hmPaint: PaintState;
    __hmPaintEvidence: (event: PaintEvent) => Promise<void>;
  }
}

// Serialized once as an init script, before app scripts on EVERY navigation.
// Sample visible text before a render, then use the NEXT rAF's page timestamp
// (documented previous-frame rendering end) for that first rendering opportunity.
// Keep callback-delivery time separate. This is not a hardware-display timestamp;
// never choose a later frame because its countdown happens to be correct.
function startPaintRecorder() {
  if (window !== window.top) return;
  const state: PaintState = {
    documentEpochMs: performance.timeOrigin, loadEventAtMs: null, rows: [], errors: [], deadlineObservedAtMs: null,
    deadline: Promise.resolve(), snapshot: () => ({ documentEpochMs: state.documentEpochMs,
      loadEventAtMs: state.loadEventAtMs, rows: [...state.rows], errors: [...state.errors], deadlineObservedAtMs: state.deadlineObservedAtMs }),
  };
  window.__hmPaint = state;
  const emit = (event: PaintEvent['event']) => {
    void window.__hmPaintEvidence({ ...state.snapshot(), event }).catch(error => { state.errors.push(String(error)); });
  };
  const seen = new Set<string>();
  let pending = false, dirty = false;
  const collect = () => {
    const found: Array<Omit<PaintRow, 'paintedAtMs' | 'confirmedAtMs' | 'paintedEpochMs' | 'displayedSeconds'>> = [];
    for (const surface of document.querySelectorAll('[data-active-session-id]')) {
      const sessionId = surface.getAttribute('data-active-session-id')!;
      const own = window.__hmReadPaintedRows([...surface.querySelectorAll('[data-testid="provider-retry-indicator"]')],
        { prefix: '^', clause: 'Retrying automatically in \\d+:\\d{2}', minimal: false });
      own.forEach((row, ordinal) => found.push({ sessionId, kind: 'own', name: '', ordinal, text: row.text }));
      const named = window.__hmReadPaintedRows([...surface.querySelectorAll('[data-testid="provider-helper-rows"] :is(span, p, div)')],
        { prefix: '^.+?: ', clause: 'Retrying automatically in \\d+:\\d{2}', minimal: true });
      const ordinals = new Map<string, number>();
      for (const row of named) {
        const name = row.text.slice(0, row.text.indexOf(': '));
        const ordinal = ordinals.get(name) ?? 0; ordinals.set(name, ordinal + 1);
        found.push({ sessionId, kind: 'named', name, ordinal, text: row.text });
      }
    }
    return found;
  };
  const schedule = () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => {
      dirty = false;
      const candidates = collect();
      requestAnimationFrame(paintedAtMs => {
        const confirmedAtMs = performance.now();
        for (const row of candidates) {
          const key = JSON.stringify([row.sessionId, row.kind, row.name, row.ordinal]);
          if (seen.has(key)) continue;
          const match = /Retrying automatically in (\d+):([0-5]\d)/.exec(row.text)!;
          state.rows.push({ ...row, paintedAtMs, confirmedAtMs, paintedEpochMs: performance.timeOrigin + paintedAtMs,
            displayedSeconds: Number(match[1]) * 60 + Number(match[2]) });
          seen.add(key); emit('first-countdown');
        }
        pending = false;
        // Continuously watch the strict initial2s rendering interval, including
        // CSS-only visibility changes; later DOM changes still earn a record.
        if (dirty || performance.now() < 2_000) schedule();
      });
    });
  };
  const changed = () => { dirty = true; schedule(); };
  new MutationObserver(changed).observe(document, { subtree: true, childList: true, characterData: true, attributes: true,
    attributeFilter: ['class', 'style', 'hidden', 'aria-hidden', 'inert', 'data-active-session-id'] });
  window.addEventListener('load', () => { state.loadEventAtMs = performance.now(); schedule(); }, { once: true });
  window.addEventListener('resize', changed);
  window.addEventListener('scroll', changed, true);
  window.addEventListener('transitionend', changed, true);
  window.addEventListener('animationend', changed, true);
  state.deadline = new Promise<void>(resolve => setTimeout(() => {
    state.deadlineObservedAtMs = performance.now(); emit('deadline'); resolve();
  }, Math.max(0, 2_000 - performance.now())));
  emit('navigation'); schedule();
}

export async function installNoticePaintRecorder(page: Page, info: TestInfo, audience: string) {
  const events: PaintEvent[] = [];
  await page.exposeBinding('__hmPaintEvidence', (_source, event: PaintEvent) => { events.push(event); });
  const content = `window.__hmReadPaintedRows = (${readPaintedRows.toString()}); (${startPaintRecorder.toString()})();`;
  await page.addInitScript({ content });
  return {
    async attach() {
      const current = await page.evaluate(() => window.__hmPaint.snapshot());
      const documents = [...new Set([...events.map(e => e.documentEpochMs), current.documentEpochMs])].map(documentEpochMs => {
        const snapshots = [...events.filter(e => e.documentEpochMs === documentEpochMs), current].filter(e => e.documentEpochMs === documentEpochMs);
        const rows = new Map<string, PaintRow>();
        for (const snapshot of snapshots) for (const row of snapshot.rows) rows.set(JSON.stringify([row.sessionId, row.kind, row.name, row.ordinal]), row);
        return { audience, documentEpochMs, loadEventAtMs: snapshots.at(-1)!.loadEventAtMs,
          deadlineObservedAtMs: snapshots.at(-1)!.deadlineObservedAtMs, firstCountdownPaints: [...rows.values()],
          errors: snapshots.flatMap(s => s.errors) };
      });
      await info.attach(`helper-notice-first-paints-${audience}`, { body: JSON.stringify({ schemaVersion: 1,
        clock: 'page performance.timeOrigin + next-rAF previous-frame rendering-end timestamp; callback delivery recorded separately',
        budgetBaseline: 'navigation/reload start; never a late test-runner readiness check', documents }, null, 2), contentType: 'application/json' });
      expect(documents.flatMap(d => d.errors), 'first-paint recorder has no hidden instrumentation errors').toEqual([]);
    },
  };
}

// Pure oracle arithmetic uses the immutable page observation and the real
// server retry deadline. Neither the poll's completion nor the runner's clock
// supplies the true remaining value. Exposed for instrument counter-controls.
export function countdownPaintVerdict(snapshot: PaintSnapshot, target: CountdownTarget) {
  const row = snapshot.rows.find(r => r.sessionId === target.sessionId && r.kind === target.kind &&
    r.name === (target.name ?? '') && r.ordinal === (target.ordinal ?? 0));
  const trueRemainingSeconds = row ? Math.max(0, (Date.parse(target.retryAt) - row.paintedEpochMs) / 1000) : null;
  return { row: row ?? null, trueRemainingSeconds, absoluteErrorSeconds: row && trueRemainingSeconds !== null ?
    Math.abs(row.displayedSeconds - trueRemainingSeconds) : null };
}

export async function expectRestoredCountdowns(page: Page, info: TestInfo, label: string, targets: CountdownTarget[]) {
  // The page's2s deadline starts at navigation, not when this call gets CPU.
  // Slow IPC may deliver this saved observation later; it grants no paint time.
  const snapshot = await page.evaluate(async () => { await window.__hmPaint.deadline; return window.__hmPaint.snapshot(); });
  const observations = targets.map(target => ({ target, ...countdownPaintVerdict(snapshot, target) }));
  await info.attach(`helper-notice-reload-paints-${label}`, { body: JSON.stringify({ schemaVersion: 1, label, snapshot, observations }, null, 2), contentType: 'application/json' });
  expect(snapshot.errors, 'first-paint recorder has no instrumentation errors').toEqual([]);
  for (const target of targets) assertCountdownPaint(snapshot, target, label);
}

export function assertCountdownPaint(snapshot: PaintSnapshot, target: CountdownTarget, label: string) {
  const { row, trueRemainingSeconds, absoluteErrorSeconds } = countdownPaintVerdict(snapshot, target);
  const who = `${label}/${target.kind}/${target.name ?? 'own'}/${target.ordinal ?? 0}`;
  expect(row, `FR-008 first countdown restored and painted within 2 s of reload: ${who}`).not.toBeNull();
  expect(row!.paintedAtMs, `FR-008 first countdown paint within 2 s of reload: ${who}`).toBeLessThanOrEqual(2_000);
  expect(row!.paintedAtMs, 'page paint timestamp is nonnegative').toBeGreaterThanOrEqual(0);
  expect(Number.isFinite(trueRemainingSeconds), 'true remaining derives from the real retry_at and page paint time').toBe(true);
  expect(absoluteErrorSeconds, `FR-008 countdown equals true remaining at first page paint ±1 s: ${who}`).toBeLessThanOrEqual(1);
}

export async function checkFirstPaintInstrument(page: Page, info: TestInfo) {
  const recorder = await installNoticePaintRecorder(page, info, 'instrument');
  // Only a private DOM instrument page. No gateway/model frame is fabricated.
  await page.goto('data:text/html,<main data-active-session-id="instrument"><div data-testid="provider-retry-indicator">openrouter is rate-limiting requests right now. Retrying automatically in 0:07 (attempt 2 of 6).<span hidden>0:10</span></div></main>');
  await expect.poll(() => page.evaluate(() => window.__hmPaint.rows.length), { timeout: 2_000 }).toBe(1);
  const first = await page.evaluate(() => window.__hmPaint.snapshot());
  await page.locator('[data-testid="provider-retry-indicator"]').evaluate(el => {
    el.textContent = 'openrouter is rate-limiting requests right now. Retrying automatically in 0:10 (attempt 2 of 6).';
  });
  const after = await page.evaluate(() => new Promise<PaintSnapshot>(resolve => {
    requestAnimationFrame(() => requestAnimationFrame(() => resolve(window.__hmPaint.snapshot())));
  }));
  expect(after.rows, 'instrument: a later correct countdown cannot replace the FIRST painted wrong countdown').toEqual(first.rows);
  const target: CountdownTarget = { sessionId: 'instrument', kind: 'own', retryAt: new Date(first.rows[0].paintedEpochMs + 10_000).toISOString() };
  expect(countdownPaintVerdict(first, target).absoluteErrorSeconds, 'instrument: a countdown off by3s is detected').toBeCloseTo(3, 2);
  expect(() => assertCountdownPaint(first, target, 'instrument +3s')).toThrow(/countdown equals true remaining at first page paint/);
  expect(() => assertCountdownPaint({ ...first, rows: [] }, target, 'instrument missing')).toThrow(/first countdown restored and painted/);
  // Whole-ms independent fixture data exercises the SAME product assertions,
  // not a duplicate <= expression or a weakened expected-error comparison.
  const exactTarget = { ...target, retryAt: new Date(110_000).toISOString() };
  for (const milliseconds of [1_999, 2_000, 2_001]) {
    const sample = { ...first, rows: [{ ...first.rows[0], paintedAtMs: milliseconds, paintedEpochMs: 100_000, displayedSeconds: 10 }] };
    if (milliseconds <= 2_000) assertCountdownPaint(sample, exactTarget, `instrument ${milliseconds}`);
    else expect(() => assertCountdownPaint(sample, exactTarget, 'instrument late')).toThrow(/first countdown paint within 2 s/);
  }
  for (const error of [0.999, 1, 1.001, 3]) {
    const sample = { ...first, rows: [{ ...first.rows[0], paintedAtMs: 100, paintedEpochMs: 100_000 + error * 1_000, displayedSeconds: 10 }] };
    if (error <= 1) assertCountdownPaint(sample, exactTarget, `instrument error ${error}`);
    else expect(() => assertCountdownPaint(sample, exactTarget, 'instrument wrong clock')).toThrow(/countdown equals true remaining at first page paint/);
  }
  expect(first.rows[0].paintedEpochMs, 'instrument: page epoch is its own time origin plus the recorded paint time')
    .toBe(first.documentEpochMs + first.rows[0].paintedAtMs);
  await recorder.attach();
}

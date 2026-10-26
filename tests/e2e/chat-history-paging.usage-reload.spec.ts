import { expect } from '@playwright/test';
import { test, type ReloadHarness } from './fixtures/chat-history-paging/reload-fixture';
import { USAGE_INPUT, NEXT_INPUT, EARLIER_TOKENS, CURRENT_TOKENS, SESSION_TOKENS, NEXT_TOKENS,
  NEXT_SESSION_TOKENS, COMPLETED_MARKER, CANCELLED_MARKER } from './fixtures/chat-history-paging/reload-provider';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });
test.setTimeout(240_000);

async function liveRecordedTotal(reload: ReloadHarness) {
  await reload.ready();
  await reload.baseline();
  await reload.send(USAGE_INPUT);
  await reload.completed(SESSION_TOKENS);
  expect(reload.provider.receipts.filter(receipt => receipt.stage === 'baseline' || receipt.stage === 'usage'),
    'PREREQUISITE: actual provider emitted D-U2 totals1200 and45123, not a mocked UI counter')
    .toEqual([{ stage: 'baseline', tokens: EARLIER_TOKENS }, { stage: 'usage', tokens: CURRENT_TOKENS }]);
  const live = await reload.counter(SESSION_TOKENS, 'PREREQUISITE: actual live46323 before reload');
  await reload.record('recorded-live-46323-before-reload');
  return live;
}

async function absoluteCarriers(reload: ReloadHarness) {
  const body = await reload.read(20);
  expect(body.session_tokens_total, 'D16: the actual bounded page carries recorded whole-session usage').toBe(SESSION_TOKENS);
  const state = [...reload.traffic.frames].reverse().find(frame => frame.type === 'session_state' && frame.session_id === reload.sessionId);
  expect(state, 'real attach current state exists').toBeDefined();
  expect(state?.type === 'session_state' ? state.session_tokens_total : undefined,
    'D16: real session-scoped attach carries the same absolute total').toBe(SESSION_TOKENS);
}

test('T41 B55 B56: provider-accounted46323 survives REAL reload and fresh route; next real700 advances once to47023', async ({ reload }) => {
  const live = await liveRecordedTotal(reload);
  const calls = reload.provider.receipts.length;
  const inputs = reload.traffic.sends.length;
  reload.assertion('T41 real browser reload preserves recorded46323');
  await reload.ready(reload.sessionId, true);
  const restored = await reload.counter(SESSION_TOKENS, 'T41/#1420: actual reload must show46323, not missing-hydration0');
  expect(restored, 'same raw accessible total AND displayed format as the proven live counter').toEqual(live);
  await reload.noReexecution(calls, inputs, 0);
  await absoluteCarriers(reload);
  await reload.record('same-recorded-46323-after-real-reload');

  // A hash-only root round trip can leave the same chat mounted. Leave via
  // a real settled screen before requiring fresh addressed attach readiness;
  // no hand-built store, frame or cached footer is supplied to the application.
  await reload.page.goto(`${reload.gateway.baseURL}/#/skills`);
  await expect(reload.page.getByRole('heading', { name: 'Skills & Tools', exact: true }),
    'PREREQUISITE: the route really left chat before re-entering').toBeVisible();
  await expect(reload.page.locator(`[data-active-session-id="${reload.sessionId}"]`),
    'PREREQUISITE: a same-chat navigation no-op cannot masquerade as a fresh route').toHaveCount(0);
  await reload.ready();
  expect(await reload.counter(SESSION_TOKENS, 'T41: fresh addressed route preserves46323')).toEqual(live);
  await reload.noReexecution(calls, inputs, 0);
  await reload.send(NEXT_INPUT);
  await reload.completed(NEXT_SESSION_TOKENS);
  await reload.counter(NEXT_SESSION_TOKENS, 'D-U2: only the next genuinely accounted700 advances once to47023');
  expect(reload.provider.receipts.filter(receipt => receipt.stage === 'next')).toEqual([{ stage: 'next', tokens: NEXT_TOKENS }]);
  expect(reload.traffic.sends.map(frame => frame.content).filter(content => content === NEXT_INPUT)).toEqual([NEXT_INPUT]);
  await reload.record('next-real-turn-47023-exactly-once');
});

test('T41 B55: older page excluding accounted bodies keeps the SAME46323 after REAL reload', async ({ reload }) => {
  // These are ordinary real-writer prefix rows, never invented usage entries.
  await reload.seedOrdinary('d16-ordinary-prefix', 240);
  const live = await liveRecordedTotal(reload);
  const calls = reload.provider.receipts.length;
  const inputs = reload.traffic.sends.length;
  reload.assertion('T41 real reload and older reader use session usage, not selected-page sums');
  await reload.ready(reload.sessionId, true);
  const older = await reload.olderUntil(body => body.entries.length > 0 &&
    body.entries.every(entry => entry.id.startsWith('d16-ordinary-prefix-')));
  expect(older.page_info.mode, 'the actual older carrier was driven through the browser').toBe('older');
  expect(older.entries.every(entry => (entry.tokens ?? 0) === 0), 'positive control: selected old bodies cannot explain the recorded total').toBe(true);
  const restored = await reload.counter(SESSION_TOKENS, 'T41/#1420: reloaded older view keeps recorded46323 despite no accounted rows in that page');
  expect(restored).toEqual(live);
  expect(older.session_tokens_total, 'D16 absolute metadata is independent of the selected range').toBe(SESSION_TOKENS);
  await reload.noReexecution(calls, inputs, 0);
  await reload.record('same-recorded-46323-in-reloaded-older-reader');
});

test('T41 B55 independent control: genuinely unused sibling is0 before and after REAL reload, without provider or input', async ({ reload }) => {
  await reload.ready(reload.otherSessionId);
  expect((await reload.detail(reload.otherSessionId)).session.stats?.tokens_total, 'independent persisted zero, not absent accounting').toBe(0);
  await reload.counter(0, 'genuine unused session');
  reload.assertion('T41 true0 is not replaced by a nonzero fallback');
  await reload.ready(reload.otherSessionId, true);
  await reload.counter(0, 'genuine unused session after real reload');
  expect((await reload.detail(reload.otherSessionId)).session.stats?.tokens_total).toBe(0);
  expect(reload.provider.receipts, 'reload of unused session is not a model turn').toEqual([]);
  expect(reload.traffic.sends, 'no user input was invented').toEqual([]);
  expect(reload.executionCount(COMPLETED_MARKER)).toBe(0);
  expect(reload.executionCount(CANCELLED_MARKER)).toBe(0);
  await reload.record('true-zero-after-real-reload', reload.otherSessionId);
});

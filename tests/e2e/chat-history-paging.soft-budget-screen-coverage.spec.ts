import { expect } from '@playwright/test';
import { SessionHistoryResponse } from '../../src/lib/api/generated/schemas';
import { test, BUDGET, anchor, expectAnchor, geometry, historyScroller, holdPage, scrollToDistance, settled } from './fixtures/chat-history-paging/harness';
import { INLINE_RESULT_BYTES, inlineToolMessage, utcDay } from './fixtures/chat-history-paging/day-fixtures';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });

// T28; D-B1/B1b/B2/B3, re-derived per D2 A2.7. Real canonical writer,
// shortening and max-carrier projection; no mocked weights, styles or rows.
test('T28 B37:five inline50KiB results byte-cut at16 whole groups exactly at the80-row floor', async ({ history }) => {
  const entries = Array.from({ length: 100 }, (_, n) => inlineToolMessage('byte-cut', n, history.ids.agent.id));
  await history.seedDays([{ day: utcDay(0), entries }]);
  await history.authenticate();
  const response = await history.read({ limit: 100, min_rows: 80 });
  // D-B1/A2.7:5 calls/group =250KiB and5 rows.16×255KiB +8KiB
  // envelope headroom <4MiB;17×250KiB >4MiB, even before envelopes.
  expect(16 * 255 * 1024 + 8 * 1024).toBeLessThan(BUDGET);
  expect(17 * 5 * INLINE_RESULT_BYTES).toBeGreaterThan(BUDGET);
  expect(response.entries.map((entry) => entry.id)).toEqual(entries.slice(-16).map((entry) => entry.id));
  expect(response.page_info).toMatchObject({
    limit: 100, min_rows: 80, row_count: 80, source_group_count: 16,
    soft_byte_budget: BUDGET, stop_reason: 'byte_budget', byte_budget_exceeded: false,
  });
  expect(response.page_info.budget_override).toBeUndefined();
  expect(response.page_info.projected_bytes).toBeGreaterThanOrEqual(16 * 5 * INLINE_RESULT_BYTES);
  expect(response.page_info.projected_bytes).toBeLessThanOrEqual(BUDGET);
  expect(response.has_earlier).toBe(true);
  expect(response.next_before).toBe(response.entries[0].history_position);
  for (let n = 0; n < response.entries.length; n++) {
    const entry = response.entries[n];
    // Exact original calls include ids, statuses, parameters AND full results;
    // a shortened marker, dropped call or reordered result cannot pass.
    expect(entry.tool_calls).toEqual(entries[84 + n].tool_calls);
    for (const call of entry.tool_calls ?? []) expect(Buffer.byteLength(JSON.stringify(call.result))).toBe(INLINE_RESULT_BYTES);
    const encoded = Buffer.byteLength(JSON.stringify(entry));
    expect(encoded, 'five real inline results contribute at least250KiB').toBeGreaterThanOrEqual(5 * INLINE_RESULT_BYTES);
    expect(encoded, 'independent5KiB/group envelope headroom, not an observed byte count').toBeLessThanOrEqual(255 * 1024);
  }
});

test('T28 B37:inline50KiB results plus64KiB texts keep96 whole rows above5MiB instead of cutting the height floor', async ({ history }) => {
  // D-B1b/A2.7: one inline result + one unshortened64-KiB text =114KiB
  // and2 rows/owner.48 owners meet floor96;49 would be unnecessary.
  const entries = Array.from({ length: 80 }, (_, n) => inlineToolMessage('floor', n, history.ids.agent.id, 1, 'x'.repeat(65_536)));
  expect(48 * (INLINE_RESULT_BYTES + 65_536)).toBeGreaterThan(5 * 1024 * 1024);
  await history.seedDays([{ day: utcDay(0), entries }]);
  await history.authenticate();
  const response = await history.read({ limit: 100, min_rows: 96 });
  expect(response.entries.map((entry) => entry.id)).toEqual(entries.slice(-48).map((entry) => entry.id));
  expect(response.page_info).toMatchObject({
    limit: 100, min_rows: 96, row_count: 96, source_group_count: 48, soft_byte_budget: BUDGET,
    stop_reason: 'byte_budget', byte_budget_exceeded: true, budget_override: 'height_floor',
  });
  expect(response.page_info.projected_bytes).toBeGreaterThan(5 * 1024 * 1024);
  expect(response.has_earlier).toBe(true);
  expect(response.next_before).toBe(response.entries[0].history_position);
  expect(response.entries.flatMap((e) => e.tool_calls ?? []))
    .toEqual(entries.slice(-48).flatMap((e) => e.tool_calls ?? []));
  expect(response.entries.map((e) => e.content)).toEqual(entries.slice(-48).map((e) => e.content));
});

test('T28 B37 D-B1c:one101-row atomic owner keeps all100 inline results above4MiB', async ({ history }) => {
  // A2.7:100×51,200 inline bytes >4MiB but <8MiB;100 tool rows
  // plus one narration row exceed limit20 without splitting the owner.
  const old = inlineToolMessage('atomic', 0, history.ids.agent.id, 1);
  const owner = inlineToolMessage('atomic', 1, history.ids.agent.id, 100, 'one narration row');
  await history.seedDays([{ day: utcDay(0), entries: [old, owner] }]);
  await history.authenticate();
  const response = await history.read({ limit: 20, min_rows: 20 });
  expect(response.entries.map((entry) => entry.id)).toEqual([owner.id]);
  expect(response.entries[0].content).toBe(owner.content);
  expect(response.entries[0].tool_calls).toEqual(owner.tool_calls);
  expect(response.page_info).toMatchObject({ row_count: 101, source_group_count: 1,
    soft_byte_budget: BUDGET, byte_budget_exceeded: true, budget_override: 'atomic_group' });
  expect(response.page_info.projected_bytes).toBeGreaterThan(BUDGET);
  expect(response.page_info.projected_bytes).toBeLessThan(8 * 1024 * 1024);
  expect(response.has_earlier).toBe(true);
  expect(response.next_before).toBe(response.entries[0].history_position);
});

test('T28 B37 D-B1c:one oversized row still admits19 following rows to meet floor20', async ({ history }) => {
  // A2.1 leaves non-message system content whole. That gives a valid one-row
  // group >4MiB after shortening, without an over-bound ordinary text/result.
  const entries = Array.from({ length: 21 }, (_, n) => inlineToolMessage('atomic-floor', n, history.ids.agent.id, 0, `small-${n}`));
  entries[20] = { ...entries[20], type: 'system', role: 'system', content: 'x'.repeat(5 * 1024 * 1024) };
  await history.seedDays([{ day: utcDay(0), entries }]);
  await history.authenticate();
  const response = await history.read({ limit: 40, min_rows: 20 });
  expect(response.entries.map((entry) => entry.id)).toEqual(entries.slice(1).map((entry) => entry.id));
  expect(response.entries.map((entry) => entry.content)).toEqual(entries.slice(1).map((entry) => entry.content));
  expect(response.page_info).toMatchObject({ min_rows: 20, row_count: 20, source_group_count: 20,
    soft_byte_budget: BUDGET, stop_reason: 'byte_budget', byte_budget_exceeded: true, budget_override: 'height_floor' });
  expect(response.page_info.projected_bytes).toBeGreaterThan(5 * 1024 * 1024);
  expect(response.page_info.projected_bytes).toBeLessThan(8 * 1024 * 1024);
  expect(response.has_earlier).toBe(true);
  expect(response.next_before).toBe(response.entries[0].history_position);
});

test('T28 B38:normal40-row opening fits4MiB unchanged, with no byte override', async ({ history, page }) => {
  await history.seed({ label: 'normal', count: 240, shape: 'normal', bodyBytes: 96 });
  await history.authenticate();
  const response = await history.read({ limit: 40, min_rows: 20 });
  expect(response.entries.map((entry) => entry.id)).toEqual(Array.from({ length: 40 }, (_, n) => `normal-${String(200 + n).padStart(6, '0')}`));
  expect(response.page_info).toMatchObject({ source_group_count: 40, row_count: 40,
    stop_reason: 'row_target', soft_byte_budget: BUDGET, byte_budget_exceeded: false });
  expect(response.page_info.budget_override).toBeUndefined();
  expect(response.page_info.projected_bytes).toBeLessThan(40 * 8 * 1024 + 8 * 1024);
  await history.open();
  await expect.poll(async () => { const g = await geometry(page); return g.height / g.H; },
    { message: 'D5 real opening has at least approximately4 screens when retained content exists' }).toBeGreaterThanOrEqual(4);
  await history.record('normal-open-four-screens');
});

test('T28 B39:byte-shortened open and simultaneous2H observer top-up share ONE contiguous chain; prefetch adds2H', async ({ history, page }) => {
  // D5: after the default40-row snapshot, H/A around44 makes the opening
  // remainder3H-40A about92A, with floor2H/A about88. A top-up therefore
  // asks for82..100 rows:80 inline50-KiB rows fit4MiB;85 do not. The tall
  // real viewport supplies that geometry without fake styles/measurements.
  // Assert the actual byte stop below, so a row-cap-only fixture cannot pass.
  await page.setViewportSize({ width: 1280, height: 2450 });
  await history.seedDays([{ day: utcDay(0), entries: Array.from({ length: 260 }, (_, n) => inlineToolMessage('fill', n, history.ids.agent.id)) }]);
  await history.authenticate();
  await history.open();
  await expect.poll(async () => { const g = await geometry(page); return g.height / g.H; },
    // Founder amendment2026-10-30 (#1452), spec header and D5: opening3H;
    // a2H-only implementation still fails. Prefetch remains2H below.
    { message: 'byte cuts cannot declare opening ready with only2H content' }).toBeGreaterThanOrEqual(3);
  const snapshot = history.snapshot();
  expect(snapshot.page_info.soft_byte_budget).toBe(BUDGET);
  const openingPages = history.traffic.pages.filter((r) => r.status === 200).map((r) => ({ url: new URL(r.url), page: SessionHistoryResponse.parse(r.body) }));
  expect(openingPages.length, 'positive control: fixture actually forced at least one opening top-up').toBeGreaterThan(0);
  expect(openingPages.some((response) => response.page.page_info.stop_reason === 'byte_budget'),
    'positive control: opening really byte-cuts, not merely row-target/source-cap top-up').toBe(true);
  let expectedBoundary = snapshot.next_before;
  for (const response of openingPages) {
    expect(response.url.searchParams.get('before'), 'each physical chunk starts at the immediate prior lower edge').toBe(expectedBoundary);
    expectedBoundary = response.page.next_before;
  }
  expect(new Set(openingPages.map((p) => p.url.searchParams.get('before'))).size, 'observer and top-up cannot duplicate a boundary')
    .toBe(openingPages.length);
  expect(history.traffic.attaches.filter((f) => f.session_id === history.sessionId)).toHaveLength(1);
  const before = await geometry(page);
  const held = await holdPage(page, (url) => url.searchParams.has('before'));
  try {
    await scrollToDistance(page, 2 * before.H);
    await held.requested;
    const saved = await anchor(page);
    await historyScroller(page).evaluate((el) => { el.dispatchEvent(new Event('scroll')); });
    await settled(page);
    expect(held.requests).toHaveLength(1);
    held.release();
    await expect.poll(async () => (await geometry(page)).height - before.height,
      { message: 'logical prefetch acquires at least2H NEW real content, not merely2H total' }).toBeGreaterThanOrEqual(2 * before.H);
    await expectAnchor(page, saved);
    await expect(page.getByText('Loading earlier messages…', { exact: true })).toHaveCount(0);
    await history.record('budget-prefetch-two-new-screens');
  } finally { held.release(); await held.dispose(); }
});

import { expect } from '@playwright/test';
import { SessionHistoryResponse } from '../../src/lib/api/generated/schemas';
import { test, BUDGET, anchor, expectAnchor, geometry, historyScroller, holdPage, scrollToDistance, settled } from './fixtures/chat-history-paging/harness';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });

// T28; D-B1/B1b/B2/B3: independently authored ASCII result sizes. The fixture
// uses real tool projection. No mocked page weights, styles or virtual rows.
test('T28 B37:200KiB tool results byte-cut at20 whole groups after the20-row floor', async ({ history }) => {
  await history.seed({ label: 'byte-cut', count: 100, shape: 'tools', bodyBytes: 200 * 1024 });
  await history.authenticate();
  const response = await history.read({ limit: 100, min_rows: 20 });
  // D-B1:20×201KiB+8KiB <4MiB,21×200KiB >4MiB.
  expect(20 * 201 * 1024 + 8 * 1024).toBeLessThan(BUDGET);
  expect(21 * 200 * 1024).toBeGreaterThan(BUDGET);
  expect(response.entries.map((entry) => entry.id)).toEqual(Array.from({ length: 20 }, (_, n) => `byte-cut-${String(80 + n).padStart(6, '0')}`));
  expect(response.page_info).toMatchObject({
    limit: 100, min_rows: 20, row_count: 20, source_group_count: 20,
    soft_byte_budget: BUDGET, stop_reason: 'byte_budget', byte_budget_exceeded: false,
  });
  expect(response.page_info.budget_override).toBeUndefined();
  expect(response.page_info.projected_bytes).toBeGreaterThanOrEqual(20 * 200 * 1024);
  expect(response.page_info.projected_bytes).toBeLessThanOrEqual(BUDGET);
  expect(response.has_earlier).toBe(true);
  expect(response.next_before).toBe(response.entries[0].history_position);
  for (const entry of response.entries) {
    expect(entry.tool_calls?.map((call) => ({ id: call.id, status: call.status })))
      .toEqual([{ id: `call-${entry.id}`, status: 'success' }]);
    const encoded = Buffer.byteLength(JSON.stringify(entry));
    expect(encoded, 'fixture lower bound independently pins a real large projected group').toBeGreaterThanOrEqual(200 * 1024);
    expect(encoded, 'D-B1 fixture upper headroom, not an observed expected byte count').toBeLessThanOrEqual(201 * 1024);
  }
});

test('T28 B37:256KiB results keep20 whole rows above5MiB instead of cutting the height floor', async ({ history }) => {
  await history.seed({ label: 'floor', count: 40, shape: 'tools', bodyBytes: 256 * 1024 });
  await history.authenticate();
  const response = await history.read({ limit: 40, min_rows: 20 });
  expect(response.entries.map((entry) => entry.id)).toEqual(Array.from({ length: 20 }, (_, n) => `floor-${String(20 + n).padStart(6, '0')}`));
  expect(response.page_info).toMatchObject({
    min_rows: 20, row_count: 20, source_group_count: 20, soft_byte_budget: BUDGET,
    stop_reason: 'byte_budget', byte_budget_exceeded: true, budget_override: 'height_floor',
  });
  expect(response.page_info.projected_bytes).toBeGreaterThan(5 * 1024 * 1024);
  expect(response.next_before).toBe(response.entries[0].history_position);
  expect(response.entries.flatMap((e) => e.tool_calls ?? []).map((t) => t.id))
    .toEqual(response.entries.map((e) => `call-${e.id}`));
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
  await history.seed({ label: 'fill', count: 260, shape: 'tools', bodyBytes: 256 * 1024 });
  await history.authenticate();
  await history.open();
  await expect.poll(async () => { const g = await geometry(page); return g.height / g.H; },
    { message: 'byte cuts cannot declare opening ready with only2H content' }).toBeGreaterThanOrEqual(4);
  const snapshot = history.snapshot();
  expect(snapshot.page_info.soft_byte_budget).toBe(BUDGET);
  const openingPages = history.traffic.pages.filter((r) => r.status === 200).map((r) => ({ url: new URL(r.url), page: SessionHistoryResponse.parse(r.body) }));
  expect(openingPages.length, 'positive control: fixture actually forced at least one opening top-up').toBeGreaterThan(0);
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

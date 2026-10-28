import { createHash } from 'node:crypto';
import { expect } from '@playwright/test';
import { Message } from '../../src/lib/api/generated/schemas';
import { HistoryResultPreview } from '../../src/lib/api/generated/ws-schemas';
import { test, fixtureId, historyRow } from './fixtures/chat-history-paging/harness';
import { hasTrueSizeLabel } from '../chat-history-paging/size-label';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });

// T67/B71/B72: seeded display/read acceptance, NOT a live producer claim.
// Real normal append utility, gateway, embedded SPA and browser renderer.
test('T67: two2MB saved outputs open with a small preview page and one separate plain full-output read', async ({ history, page }, info) => {
  const label = 'd2-large-output';
  const payloadBytes = 2_000_000; // Corrected spec UAT setup: strictly over1MiB.
  await history.seed({ label, count: 2, shape: 'tools', bodyBytes: payloadBytes });
  const reads: string[] = [];
  const fullPrefix = `/api/v1/sessions/${history.sessionId}/messages/`;
  // Arm BEFORE navigation: an erroneous eager read must not escape observation.
  page.on('request', request => { if (request.method() === 'GET' && new URL(request.url()).pathname.startsWith(fullPrefix)) reads.push(request.url()); });
  await history.authenticate();
  await history.open();
  const snapshot = history.snapshot();
  const results = history.traffic.frames.filter(frame => frame.type === 'tool_call_result' && frame.history_origin);
  expect(results, 'both actual saved tool results, not an empty or live-only open').toHaveLength(2);
  const markers = results.map(frame => {
    if (frame.type !== 'tool_call_result') throw new Error('unreachable narrowed result fixture');
    return HistoryResultPreview.parse(frame.result);
  });
  for (let i = 0; i < markers.length; i++) {
    const expected = JSON.stringify({ content: fixtureId(label, i) + 'x'.repeat(payloadBytes) });
    expect(markers[i].original_size_bytes).toBe(Buffer.byteLength(expected));
    expect(markers[i].original_size_bytes).toBeGreaterThan(1_048_576);
    expect(markers[i].preview).toBe(expected.slice(0, 4096));
  }
  // Two4096-byte previews plus generated source/parameter/frame metadata fit
  // comfortably below64KiB; this is an authored-corpus bound, not observed data.
  expect(snapshot.page_info.projected_bytes).toBeLessThan(64 * 1024);
  expect(snapshot.page_info.byte_ceiling).toBe(8_388_608);
  const target = results[1];
  if (target.type !== 'tool_call_result' || !target.history_origin) throw new Error('BLOCKED: actual source-owned preview required by T67');
  const path = `/api/v1/sessions/${history.sessionId}/messages/${target.history_origin.position}`;
  expect(reads, 'collapsed previews perform no eager full-occurrence read on navigation').toEqual([]);
  const response = page.waitForResponse(r => new URL(r.url()).pathname === path && r.request().method() === 'GET');
  const row = historyRow(page, fixtureId(label, 1));
  expect(hasTrueSizeLabel(await row.innerText(), markers[1].original_size_bytes), 'collapsed actual browser row displays the true original size').toBe(true);
  // Dispatch clarification A: opening the actual disclosure fetches once.
  await row.locator('[data-history-tool-row] button[aria-expanded]').click();
  const fullResponse = await response;
  expect(fullResponse.status()).toBe(200);
  const full = Message.parse(await fullResponse.json());
  expect(full.history_position).toBe(target.history_origin.position);
  expect(full.content_original_size_bytes).toBeUndefined();
  expect(full.tool_calls).toHaveLength(1);
  const expectedValue = fixtureId(label, 1) + 'x'.repeat(payloadBytes);
  expect(full.tool_calls![0].result).toEqual({ content: expectedValue });
  const content = row.locator('pre, code, [role="region"]').filter({ hasText: 'x'.repeat(1000) }).last();
  await expect.poll(() => content.evaluate((element, value) => element.textContent?.includes(value), expectedValue),
    { message: 'full2MB body actually rendered, not just the preview or a successful HTTP receipt' }).toBe(true);
  expect(await content.evaluate(element => getComputedStyle(element).fontFamily), 'over1MiB uses plain monospace').toMatch(/mono/i);
  expect(await content.locator('.shiki, .token, [data-language], span[style*="color"]').count(), 'no syntax-highlighting token tree for a2MB result').toBe(0);
  expect(reads.map(url => new URL(url).pathname), 'one exact occurrence read, never a per-tool duplicate, eager read or automatic retry').toEqual([path]);
  await info.attach('large-output-receipt', { contentType: 'application/json', body: JSON.stringify({
    seeded: true, label, count: 2, requested_payload_bytes: payloadBytes,
    canonical_writer: 'UnifiedStore.AppendTranscriptAddressed', session_id: history.sessionId,
    page_projected_bytes: snapshot.page_info.projected_bytes, byte_ceiling: snapshot.page_info.byte_ceiling,
    original_size_bytes: markers[1].original_size_bytes, full_read_count: reads.length,
    full_json_sha256: createHash('sha256').update(JSON.stringify(full.tool_calls![0].result)).digest('hex'),
  }) });
  await history.record('large-output-opened');
});

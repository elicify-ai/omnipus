import { expect } from '@playwright/test';
import type { Message } from '../../src/lib/api/generated/openapi-types';
import { SessionHistoryResponse } from '../../src/lib/api/generated/schemas';
import { seedCanonicalLegacyTranscript } from './fixtures/chat-history';
import { observeAddressedAttachReadiness } from './fixtures/addressed-attach-readiness';
import { historyScroller, settled } from './fixtures/chat-history-paging/harness';
import { chatInput } from './fixtures/selectors';
import { createXssSession, expectEscapedPayload, HISTORY_XSS_PAYLOAD, monitorXssExecution, openXssSession, test } from './fixtures/xss-history';

// Security-lead F1: native live, actual REST older page and real document reload.
// Only external model HTTP is scripted; all product/render/persistence paths real.
test.use({ viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });
test.setTimeout(120_000); // Includes private gateway and canonical fsynced fixture setup.

const assistantRow = (page: Parameters<typeof chatInput>[0], id: string) => page.locator(`[data-message-id="${id}"]`);

test('XSS F1 live: exact hostile assistant text is safe during a genuinely held native turn', async ({ page, canonicalGateway, xssProvider }, info) => {
  const monitor = await monitorXssExecution(page);
  const session = await createXssSession(canonicalGateway);
  const traffic = await openXssSession(page, canonicalGateway, session.id);
  const instruction = 'Return the security acceptance text directly in this chat; do not call tools.';
  await chatInput(page).fill(instruction);
  await chatInput(page).press('Enter');
  await expect.poll(() => traffic.sends.filter(frame => frame.session_id === session.id && frame.content === instruction).length,
    { message: 'one actual authenticated input starts the real engine turn' }).toBe(1);
  await expect.poll(() => traffic.frames.filter(frame => frame.type === 'token' && frame.session_id === session.id && frame.content === HISTORY_XSS_PAYLOAD).length,
    { message: 'actual native live token carries the hostile payload, not seeded replay' }).toBe(1);
  expect(xssProvider.calls(), 'the real engine reached the external model once').toBe(1);
  expect(xssProvider.heldCount(), 'the actual model HTTP response is still streaming').toBe(1);
  await expect(page.getByTestId('stop-btn'), 'real held turn exposes Stop').toBeVisible();
  const live = page.locator('[data-message-id][data-message-role="assistant"]').filter({ has: page.getByText(HISTORY_XSS_PAYLOAD, { exact: true }) });
  await expectEscapedPayload(page, live, monitor, info, 'xss-live-while-streaming');
  xssProvider.finish();
  await expect.poll(() => traffic.frames.filter(frame => frame.type === 'done' && frame.session_id === session.id).length,
    { message: 'real live turn ends after the provider release' }).toBeGreaterThan(1);
  expect(traffic.failures).toEqual([]);
});

test('XSS F1 older: Show earlier retrieves the hostile source through a real REST older page before safety checks', async ({ page, canonicalGateway }, info) => {
  const monitor = await monitorXssExecution(page);
  const session = await createXssSession(canonicalGateway);
  const hostileId = 'xss-older-hostile';
  const timestamp = new Date().toISOString();
  const entries: Message[] = [
    { id: hostileId, type: 'message', role: 'assistant', content: HISTORY_XSS_PAYLOAD, agent_id: session.agent_id, timestamp },
    ...Array.from({ length: 239 }, (_, n): Message => ({ id: `xss-older-filler-${n}`, type: 'message', role: 'assistant',
      content: `Ordinary retained row ${n}`, agent_id: session.agent_id, timestamp })),
  ];
  await seedCanonicalLegacyTranscript(session.id, entries);
  const traffic = await openXssSession(page, canonicalGateway, session.id);
  expect(traffic.frames.filter(frame => frame.type === 'replay_message' && frame.session_id === session.id && frame.id === hostileId),
    'positive path control: hostile source is outside the bounded opening snapshot').toEqual([]);
  await expect(assistantRow(page, hostileId), 'older payload is not accidentally rendered from the latest page').toHaveCount(0);
  let olderPage: ReturnType<typeof SessionHistoryResponse.parse> | undefined;
  //240 groups / minimum20 requested groups + two boundary iterations.
  for (let boundary = 0; boundary < 14 && !olderPage; boundary++) {
    const before = traffic.pages.length;
    const earlier = page.getByRole('button', { name: 'Show earlier messages', exact: true });
    await expect(earlier, 'real explicit older control is reachable').toBeVisible();
    await earlier.evaluate(element => (element as HTMLElement).focus({ preventScroll: true }));
    await page.keyboard.press('Enter');
    await expect.poll(() => traffic.pages.slice(before).some(reply => new URL(reply.url).searchParams.has('before')),
      { message: 'Show earlier actually fetches its REST older carrier' }).toBe(true);
    for (const reply of traffic.pages.slice(before).filter(value => new URL(value.url).searchParams.has('before'))) {
      expect(reply.status, 'real older response succeeds, never a fake UI pass').toBe(200);
      const parsed = SessionHistoryResponse.parse(reply.body);
      expect(parsed.page_info.mode).toBe('older');
      if (parsed.entries.some(entry => entry.id === hostileId)) olderPage = parsed;
    }
    await settled(page);
  }
  expect(olderPage, 'captured actual REST page must contain the hostile source').toBeDefined();
  if (!olderPage) throw new Error('Hostile source never arrived in actual traffic.pages');
  expect(olderPage.entries.find(entry => entry.id === hostileId)?.content).toBe(HISTORY_XSS_PAYLOAD);
  expect(traffic.pages.some(reply => SessionHistoryResponse.safeParse(reply.body).success &&
    new URL(reply.url).searchParams.has('before')), 'paging proof uses the existing real traffic.pages ledger').toBe(true);
  await historyScroller(page).evaluate(element => { element.scrollTop = 0; element.dispatchEvent(new Event('scroll')); });
  await expectEscapedPayload(page, assistantRow(page, hostileId), monitor, info, 'xss-real-rest-older-page');
  expect(traffic.failures).toEqual([]);
});

test('XSS F1 reload: exact persisted hostile text stays escaped after a real document reload and fresh addressed catch-up', async ({ page, canonicalGateway, xssProvider }, info) => {
  const monitor = await monitorXssExecution(page);
  const session = await createXssSession(canonicalGateway);
  const hostileId = 'xss-reload-hostile';
  await seedCanonicalLegacyTranscript(session.id, [{ id: hostileId, type: 'message', role: 'assistant',
    content: HISTORY_XSS_PAYLOAD, agent_id: session.agent_id, timestamp: new Date().toISOString() } satisfies Message]);
  const traffic = await openXssSession(page, canonicalGateway, session.id);
  await expectEscapedPayload(page, assistantRow(page, hostileId), monitor, info, 'xss-before-real-reload');
  const before = traffic.frames.length;
  const observer = observeAddressedAttachReadiness(page);
  const ready = observer.arm(session.id);
  try {
    const [, proof] = await Promise.all([page.reload(), ready.wait()]);
    expect(proof.request.session_id).toBe(session.id);
    expect(proof.completion.session_id).toBe(session.id);
    expect(traffic.frames.slice(before).filter(frame => frame.type === 'catch_up_complete' && frame.session_id === session.id)).toHaveLength(1);
    await expectEscapedPayload(page, assistantRow(page, hostileId), monitor, info, 'xss-after-real-reload');
    expect(xssProvider.calls(), 'document reload is not a new model turn').toBe(0);
    expect(traffic.sends, 'document reload never resends a user input').toEqual([]);
    expect(traffic.failures).toEqual([]);
  } finally { ready.cancel(); observer.dispose(); }
});

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
  // The streaming bubble is attribute-less until the turn finalizes (first
  // real browser run: 34 locator resolutions to 0 against saved-row
  // attributes while the held stream was live). The uniquely addressable
  // live container is the exact-text element's parent; the hostile-render
  // surface itself is the exact-text element, so DOM safety checks scope
  // there — the message chrome's avatar/copy icons are not the render
  // surface and must not weaken the img/script oracle.
  const liveText = page.getByText(HISTORY_XSS_PAYLOAD, { exact: true });
  const live = liveText.locator('xpath=..');
  await expectEscapedPayload(page, live, monitor, info, 'xss-live-while-streaming', HISTORY_XSS_PAYLOAD, liveText);
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

test('XSS F3 tool result: the actual expanded saved tool output retains exact hostile text without executing it', async ({ page, canonicalGateway, xssProvider }, info) => {
  const monitor = await monitorXssExecution(page);
  const session = await createXssSession(canonicalGateway);
  const entryId = 'xss-tool-result-entry';
  const callId = 'xss-tool-result-call';
  // Single quotes keep the WHOLE hostile source verbatim inside visible JSON.
  // A saved ToolCall.result is an object, never an invented string wire shape.
  const toolPayload = "<script>window.__xss_fired=true;alert(1)</script><img src=x onerror='window.__xss_fired=true;alert(1)'>";
  const result = { content: toolPayload };
  const literalOutput = JSON.stringify(result, null, 2);
  expect(literalOutput, 'positive-control instrument preserves the full attack source, not a JSON-escaped approximation').toContain(toolPayload);
  await seedCanonicalLegacyTranscript(session.id, [{ id: entryId, type: 'tool_call', role: 'assistant',
    content: 'Saved tool result security probe.', agent_id: session.agent_id, timestamp: new Date().toISOString(),
    tool_calls: [{ id: callId, tool: 'grep', status: 'success', content_state: 'full',
      parameters: { pattern: 'hostile', path: 'xss-tool-result.txt' }, result }],
  } satisfies Message]);
  const traffic = await openXssSession(page, canonicalGateway, session.id);
  expect(traffic.frames.filter(frame => frame.type === 'tool_call_start' && frame.session_id === session.id && frame.call_id === callId),
    'real native replay contains the authored tool identity').toHaveLength(1);
  expect(traffic.frames.filter(frame => frame.type === 'tool_call_result' && frame.session_id === session.id && frame.call_id === callId)
    .map(frame => frame.type === 'tool_call_result' ? { tool: frame.tool, status: frame.status, result: frame.result } : undefined),
  'the actual native result carries exactly the hostile object, not ordinary text or a fabricated DOM pane')
    .toEqual([{ tool: 'grep', status: 'success', result }]);
  const badge = assistantRow(page, entryId).getByTestId('tool-call-badge');
  await expect(badge, 'the actual addressed saved tool is rendered once').toHaveCount(1);
  await expect(badge).toHaveAttribute('data-tool', 'grep');
  await badge.getByTestId('tool-call-toggle').click();
  const output = badge.getByRole('region', { name: 'Tool output', exact: true }).filter({ hasText: toolPayload });
  await expect(output, 'POSITIVE CONTROL: full tool result including the unmodified hostile source is exact visible literal text').toHaveText(literalOutput);
  await expect(output, 'POSITIVE CONTROL: the exact tool output is genuinely visible').toBeVisible();
  expect(await output.innerText(), 'complete visible tool output is byte-for-byte the authored JSON text').toBe(literalOutput);
  await expectEscapedPayload(page, badge, monitor, info, 'xss-actual-tool-result', literalOutput);
  expect(traffic.sends, 'opening the saved tool does not send a new input').toEqual([]);
  expect(xssProvider.calls(), 'saved tool visibility never reexecutes the tool or model').toBe(0);
  expect(traffic.failures).toEqual([]);
});

test('XSS F3 user message: one genuine native input and durable echo render the full hostile text safely in its exact user row', async ({ page, canonicalGateway, xssProvider }, info) => {
  const monitor = await monitorXssExecution(page);
  const session = await createXssSession(canonicalGateway);
  const traffic = await openXssSession(page, canonicalGateway, session.id);
  const doneBefore = traffic.frames.filter(frame => frame.type === 'done' && frame.session_id === session.id).length;
  await chatInput(page).fill(HISTORY_XSS_PAYLOAD);
  await chatInput(page).press('Enter');
  await expect.poll(() => traffic.sends.filter(frame => frame.session_id === session.id).length,
    { message: 'the real composer sends exactly one genuine hostile input' }).toBe(1);
  const sent = traffic.sends.find(frame => frame.session_id === session.id);
  if (!sent) throw new Error('PREREQUISITE: real user input was not observed');
  expect(sent.content, 'the real outbound input is the complete hostile source').toBe(HISTORY_XSS_PAYLOAD);
  await expect.poll(() => traffic.frames.filter(frame => frame.type === 'user_message' && frame.session_id === session.id).length,
    { message: 'the gateway durably echoes exactly one real user message, not an optimistic-only bubble' }).toBe(1);
  const echo = traffic.frames.find(frame => frame.type === 'user_message' && frame.session_id === session.id);
  if (echo?.type !== 'user_message') throw new Error('PREREQUISITE: actual user_message echo is missing');
  expect({ content: echo.content, client_message_id: echo.client_message_id }).toEqual({ content: HISTORY_XSS_PAYLOAD, client_message_id: sent.client_message_id });
  expect(echo.id, 'the real gateway supplies the addressed user row identity').not.toBe('');
  // Durability is already proven from the real frames above. The live-turn
  // thread renders both bubbles attribute-less even after done (first real
  // browser run: no data-message-id on the live list), so prove the server
  // id on the transcript replay path: finish the real turn, then re-address
  // the same session through a genuine document reload and require the row
  // the ACTUAL server message id selects to carry the exact hostile text.
  await expect.poll(() => xssProvider.heldCount(), { message: 'the genuine input reached the actual held model HTTP stream' }).toBe(1);
  expect(xssProvider.calls()).toBe(1);
  xssProvider.finish();
  await expect.poll(() => traffic.frames.filter(frame => frame.type === 'done' && frame.session_id === session.id).length,
    { message: 'the genuinely submitted turn finishes only after provider release' }).toBe(doneBefore + 1);
  // The live-turn thread renders both bubbles attribute-less even after done
  // (first real browser run), so prove the server id on the transcript replay
  // path: a genuine document reload (goto to the identical URL is a
  // same-document navigation and never re-attaches), then the row the ACTUAL
  // server message id selects must carry the exact hostile text safely.
  const framesBeforeReload = traffic.frames.length;
  const observer = observeAddressedAttachReadiness(page);
  const ready = observer.arm(session.id);
  try {
    const [, proof] = await Promise.all([page.reload(), ready.wait()]);
    expect(proof.request.session_id).toBe(session.id);
    expect(proof.completion.session_id).toBe(session.id);
    expect(traffic.frames.slice(framesBeforeReload).filter(frame => frame.type === 'catch_up_complete' && frame.session_id === session.id)).toHaveLength(1);
    const user = page.locator(`[data-message-role="user"][data-message-id="${echo.id}"]`);
    await expectEscapedPayload(page, user, monitor, info, 'xss-genuine-user-message');
    expect(xssProvider.calls(), 'the transcript replay never re-executes the model turn').toBe(1);
    expect(traffic.sends.filter(frame => frame.session_id === session.id), 're-addressing the session never resends the hostile input').toHaveLength(1);
    expect(traffic.failures).toEqual([]);
  } finally { ready.cancel(); observer.dispose(); }
});

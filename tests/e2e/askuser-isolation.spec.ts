import { type Page } from '@playwright/test';
import { SessionDetail } from '../../src/lib/api/generated/schemas';
import { WsFrame } from '../../src/lib/api/generated/ws-schemas';
import { test as base, expect } from './fixtures/console-errors';
import { cancelActiveSessionOnTeardown } from './fixtures/cancel-active-session';
import { GatewayProcess } from './fixtures/gateway-process';
import { freshAgentChat } from './fixtures/fresh-agent-chat';
import { chatInput, selectAgent, waitForConnected } from './fixtures/selectors';
import { ASK_USER_PROMPT, ASK_USER_QUESTION, FRESH_CHAT_PROMPT, FRESH_CHAT_REPLY, startAskUserProvider } from './fixtures/ask-user-provider';

type QuestionGateway = { gateway: GatewayProcess; provider: Awaited<ReturnType<typeof startAskUserProvider>> };
const test = base.extend<{ questionGateway: QuestionGateway; reaskAfterCancel: boolean }>({
  reaskAfterCancel: [false, { option: true }],
  questionGateway: async ({ browserName, reaskAfterCancel }, use, info) => {
    const provider = await startAskUserProvider({ reaskAfterCancel });
    let gateway: GatewayProcess | undefined;
    try {
      gateway = await GatewayProcess.start({ apiBase: provider.url, adminUsername: 'askuser-isolation' });
      await use({ gateway, provider });
    } finally {
      try {
        await info.attach('scripted-provider-evidence', { body: JSON.stringify({ browserName, ...provider.evidence }), contentType: 'application/json' });
      } finally {
        try { await gateway?.stop(); } finally { await provider.close(); }
      }
    }
  },
  // The automatic cancelOnTeardown depends on THIS page. Its cleanup therefore
  // runs before the page and private gateway fixtures are disposed, including
  // after an assertion failure. Never close the gateway in the test body.
  page: async ({ page, questionGateway }, use) => {
    await page.context().addCookies((await questionGateway.gateway.browserStorageState()).cookies);
    await use(page);
  },
});
test.use({ storageState: { cookies: [], origins: [] } });
test.describe.configure({ retries: 0 });
test.setTimeout(120_000); // Local boot, public API, two browser contexts; no paid-provider latency.

function observeQuestions(page: Page) {
  const received: WsFrame[] = [];
  const sent: WsFrame[] = [];
  page.on('websocket', socket => {
    if (!new URL(socket.url()).pathname.endsWith('/chat/ws')) return;
    socket.on('framereceived', ({ payload }) => received.push(WsFrame.parse(JSON.parse(payload.toString()))));
    socket.on('framesent', ({ payload }) => sent.push(WsFrame.parse(JSON.parse(payload.toString()))));
  });
  return { received, sent };
}

async function openMainAndAsk(page: Page, { gateway, provider }: QuestionGateway) {
  const traffic = observeQuestions(page);
  await page.goto(gateway.baseURL);
  await selectAgent(page, /Jim/i);
  await waitForConnected(page);
  const surface = page.locator('[data-active-session-id]');
  await expect(surface).toBeVisible();
  const sessionId = await surface.getAttribute('data-active-session-id');
  if (!sessionId) throw new Error('BLOCKED: selected main session has no identity — required by #1769');
  const response = await page.request.get(`${gateway.baseURL}/api/v1/sessions/${sessionId}`);
  expect(response.status(), 'read actual selected main session').toBe(200);
  const detail = SessionDetail.parse(await response.json());
  expect(detail.session.agent_id, 'pollution control really targets Jim').toBe('jim');
  expect(detail.session.type, 'pollution control really targets the shared main chat').toBe('main');
  expect(detail.history_state.status).toBe('available');
  expect(detail.messages, 'the private gateway starts with no earlier conversation').toEqual([]);
  await chatInput(page).fill(ASK_USER_PROMPT);
  await chatInput(page).press('Enter');
  await expect(page.getByTestId('ask-user-question-card'), '(a) real question card is open').toBeVisible();
  await expect(page.getByTestId('ask-user-question-card')).toContainText(ASK_USER_QUESTION);
  await expect(chatInput(page), '(a) a pending question locks its owning composer').toBeDisabled();
  await expect(page.getByTestId('ask-user-composer-note')).toHaveText('chat input is locked while questions are pending — Cancel to unlock');
  await expect(page.getByTestId('stop-btn'), 'the parked question outlives streaming; Stop alone is insufficient').not.toBeVisible();
  expect(provider.evidence.questionCalls, 'real model edge issued exactly one question tool call').toBe(1);
  expect(provider.evidence.errors).toEqual([]);
  const pending = traffic.received.find(frame => frame.type === 'ask_user_question' && frame.card.session_id === sessionId && frame.card.status === 'pending');
  if (!pending || pending.type !== 'ask_user_question') throw new Error('BLOCKED: real pending question wire receipt missing');
  return { sessionId, cardId: pending.card.card_id, traffic };
}

test('teardown cancels a pending question and a following browser can use the same Jim main session', async ({ page, browser, questionGateway }, info) => {
  const { gateway } = questionGateway;
  const { sessionId, cardId, traffic } = await openMainAndAsk(page, questionGateway);
  await info.attach('main-locked-before-teardown', { body: await page.screenshot(), contentType: 'image/png' });
  // Two steps rather than order-dependent tests: worker restarts/retries would
  // otherwise throw away the gateway between the poison and following test.
  // This is the SAME helper the inherited auto fixture invokes, not a copy.
  await test.step('run the real teardown helper', async () => {
    await cancelActiveSessionOnTeardown(page);
    await expect(page.getByTestId('ask-user-question-card'), '(b) teardown removed the open question').toHaveCount(0);
    await expect(chatInput(page), '(b) teardown unlocked the owning composer').toBeEnabled();
    expect(traffic.sent.filter(frame => frame.type === 'ask_user_answer')).toEqual([
      { type: 'ask_user_answer', card_id: cardId, session_id: sessionId, cancel: true },
    ]);
    await expect.poll(() => traffic.received.filter(frame => frame.type === 'ask_user_question' &&
      frame.card.card_id === cardId && frame.card.status === 'cancelled').length,
    { message: '(b) the server, not a local DOM change, acknowledged cancellation' }).toBe(1);
  });
  await test.step('a following browser selects the same agent main chat', async () => {
    const context = await browser.newContext({ storageState: await page.context().storageState() });
    try {
      const following = await context.newPage();
      await following.goto(gateway.baseURL);
      await selectAgent(following, /Jim/i);
      await expect(following.locator('[data-active-session-id]'), '(b) following test opened the SAME main session').toHaveAttribute('data-active-session-id', sessionId);
      await expect(following.getByTestId('ask-user-question-card'), '(b) cancelled question never replays as pending').toHaveCount(0);
      await expect(chatInput(following), '(b) following test composer is not locked').toBeEnabled();
      await waitForConnected(following);
      await chatInput(following).fill(FRESH_CHAT_PROMPT);
      await chatInput(following).press('Enter');
      await expect(following.getByText(FRESH_CHAT_REPLY, { exact: true }), '(b) following test can send, not just see an enabled input').toBeVisible();
      await info.attach('following-main-unlocked', { body: await following.screenshot(), contentType: 'image/png' });
    } finally { await context.close(); }
  });
  expect(questionGateway.provider.evidence.errors).toEqual([]);
});

test('a second test fresh Jim session stays usable while the first main session question is still pending', async ({ page, browser, questionGateway }, info) => {
  const { gateway, provider } = questionGateway;
  const { sessionId: pollutedId, cardId, traffic } = await openMainAndAsk(page, questionGateway);
  const context = await browser.newContext({ storageState: await page.context().storageState() });
  try {
    const following = await context.newPage();
    // Exact same API/deep-link helper as the changed live specs; it does not
    // wait on the polluted main composer's readiness before creating a chat.
    const fresh = await freshAgentChat(following, 'jim', gateway.baseURL);
    expect(fresh.sessionId, '(c) the second test never reuses the polluted main session').not.toBe(pollutedId);
    await expect(following.getByTestId('ask-user-question-card'), '(c) the fresh session never inherits another session question').toHaveCount(0);
    await expect(chatInput(following), '(c) fresh session is not locked while the original is pending').toBeEnabled();
    await chatInput(following).fill(FRESH_CHAT_PROMPT);
    await chatInput(following).press('Enter');
    await expect(following.getByText(FRESH_CHAT_REPLY, { exact: true }), '(c) the fresh session genuinely sends and receives').toBeVisible();
    await expect(page.getByTestId('ask-user-question-card'), '(c) positive control: the original question is STILL open').toBeVisible();
    await expect(chatInput(page), '(c) positive control: the original composer is STILL locked').toBeDisabled();
    expect(traffic.sent.filter(frame => frame.type === 'ask_user_answer'), '(c) isolation did not secretly cancel the original question').toEqual([]);
    expect(traffic.received.filter(frame => frame.type === 'ask_user_question' && frame.card.card_id === cardId && frame.card.status !== 'pending')).toEqual([]);
    expect(provider.evidence.questionCalls).toBe(1);
    expect(provider.evidence.errors).toEqual([]);
    await info.attach('second-fresh-session-unlocked', { body: await following.screenshot(), contentType: 'image/png' });
    await info.attach('first-session-still-locked', { body: await page.screenshot(), contentType: 'image/png' });
  } finally { await context.close(); }
  // Leave the original pending on purpose: inherited automatic teardown owns
  // its Cancel, and the private gateway is disposed only after that fixture.
});

test.describe('when the model re-asks after Cancel', () => {
  test.use({ reaskAfterCancel: true });

  test('teardown clears two distinct question cards after a re-ask and unlocks the composer', async ({ page, questionGateway }, info) => {
    const { provider } = questionGateway;
    const { sessionId, cardId, traffic } = await openMainAndAsk(page, questionGateway);
    await info.attach('reask-first-question-locked', { body: await page.screenshot(), contentType: 'image/png' });
    await cancelActiveSessionOnTeardown(page);
    expect(provider.evidence.questionCalls, 'F1: the model really asked again, not just a duplicate DOM control').toBe(2);
    await expect(page.getByTestId('ask-user-question-card'), 'F1: both the original and replacement cards were cleared').toHaveCount(0);
    await expect(chatInput(page), 'F1: two Cancels unlocked the owning composer').toBeEnabled();
    const questionIds = traffic.received.flatMap(frame => frame.type === 'ask_user_question' &&
      frame.card.session_id === sessionId && frame.card.status === 'pending' ? [frame.card.card_id] : []);
    expect(questionIds, 'F1: exactly two real pending-card receipts').toHaveLength(2);
    expect(questionIds[0], 'F1: the original question was not replaced in the evidence').toBe(cardId);
    expect(questionIds[1], 'F1: the successor has a distinct identity despite identical text').not.toBe(cardId);
    expect(traffic.sent.filter(frame => frame.type === 'ask_user_answer'), 'F1: one actual Cancel per distinct card').toEqual(
      questionIds.map(id => ({ type: 'ask_user_answer', card_id: id, session_id: sessionId, cancel: true })),
    );
    expect(traffic.received.flatMap(frame => frame.type === 'ask_user_question' && frame.card.session_id === sessionId ?
      [{ card_id: frame.card.card_id, status: frame.card.status }] : []), 'F1: server acknowledgement precedes each successor').toEqual([
      { card_id: cardId, status: 'pending' }, { card_id: cardId, status: 'cancelled' },
      { card_id: questionIds[1], status: 'pending' }, { card_id: questionIds[1], status: 'cancelled' },
    ]);
    await expect(page.getByText('Question cancelled.', { exact: true }), 'F1: the second Cancel resumed the real turn').toBeVisible();
    await chatInput(page).fill(FRESH_CHAT_PROMPT);
    await chatInput(page).press('Enter');
    await expect(page.getByText(FRESH_CHAT_REPLY, { exact: true }), 'F1: the unlocked composer can send and receive').toBeVisible();
    expect(provider.evidence.questionCalls).toBe(2);
    expect(provider.evidence.replies).toBe(2);
    expect(provider.evidence.errors).toEqual([]);
    await info.attach('reask-both-cleared-and-usable', { body: await page.screenshot(), contentType: 'image/png' });
  });
});

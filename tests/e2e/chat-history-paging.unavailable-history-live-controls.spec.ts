import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { request, expect } from '@playwright/test';
import { SessionDetail } from '../../src/lib/api/generated/schemas';
import { chatInput } from './fixtures/selectors';
import { TERMINAL_NARRATION } from './fixtures/terminal-outcome-provider';
import { terminalLogRows } from './fixtures/terminal-outcome-acceptance';
import { test, UNAVAILABLE, HistoryHarness } from './fixtures/chat-history-paging/harness';
import { dropSocket } from './fixtures/chat-history-paging/actions';
import { removeSessionHistoryIndex, corruptSessionHistoryIndex } from './fixtures/chat-history';
import { activatePendingControl, controlProvider, reconnectWithPending, sendControlInstruction,
  stopPendingControl, stopRunningControlTurn, waitForPendingControl } from './fixtures/chat-history-paging/control-actions';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 1000 } });
test.describe.configure({ retries: 0 });
test.setTimeout(120_000);

function canonicalRecords(history: HistoryHarness) {
  const root = path.join(history.gateway.homeDir, 'sessions', history.sessionId);
  const records: Record<string, unknown>[] = [];
  for (const name of fs.readdirSync(root).filter((p) => p === 'transcript.jsonl' || /^\d{4}-\d{2}-\d{2}.*\.jsonl$/.test(p))) {
    const raw = fs.readFileSync(path.join(root, name), 'utf8');
    for (const line of raw.slice(0, raw.lastIndexOf('\n') + 1).split('\n').filter(Boolean)) records.push(JSON.parse(line) as Record<string, unknown>);
  }
  return records;
}
async function unavailableFixture(history: HistoryHarness, fault: 'missing' | 'corrupt' = 'missing') {
  await history.seed({ label: 'saved-before-index-loss', count: 80 });
  expect(() => history.gateway.assertStoppedForFixture(), 'positive safety control: a running gateway cannot be mutated').toThrow('owned gateway has not actually exited');
  await history.gateway.kill9();
  history.gateway.assertStoppedForFixture();
  const file = path.join(history.gateway.homeDir, 'sessions', history.sessionId, 'transcript.jsonl');
  const before = createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  if (fault === 'missing') await removeSessionHistoryIndex(history.gateway, history.sessionId);
  else await corruptSessionHistoryIndex(history.gateway, history.sessionId);
  expect(createHash('sha256').update(fs.readFileSync(file)).digest('hex'), 'missing-index fixture never rewrites canonical chat bytes').toBe(before);
  await history.gateway.restart();
  await history.authenticate();
}

for (const fault of ['missing', 'corrupt'] as const) {
test(`T35 B31 B51: ${fault} index keeps metadata-only route and independent composer reachable`, async ({ history, page }) => {
  await unavailableFixture(history, fault);
  const messages = await page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.sessionId}/messages`);
  expect(messages.status(), 'D11 missing baseline is not empty/full-history success').toBe(500);
  expect(await messages.json()).toEqual({ error: UNAVAILABLE });
  const response = await page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.sessionId}`);
  expect(response.status()).toBe(200);
  const detail = SessionDetail.parse(await response.json());
  expect(detail.session.id).toBe(history.sessionId);
  expect(detail.history_state).toEqual({ status: 'unavailable', reason: fault === 'missing' ? 'index_missing' : 'index_inconsistent', message: UNAVAILABLE });
  expect([detail.messages, detail.page_info, detail.has_earlier, detail.has_later, detail.next_before, detail.next_after])
    .toEqual([undefined, undefined, undefined, undefined, undefined, undefined]);
  await history.open();
  const scoped = history.traffic.frames.filter((f) => f.type === 'session_state' && f.session_id === history.sessionId);
  expect(scoped.map((f) => f.type === 'session_state' ? f.history_state : undefined))
    .toContainEqual({ status: 'unavailable', reason: fault === 'missing' ? 'index_missing' : 'index_inconsistent', message: UNAVAILABLE });
  expect(history.traffic.frames.filter((f) => f.type === 'session_snapshot' && f.session_id === history.sessionId)).toEqual([]);
  const completed = history.traffic.frames.filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId);
  expect(completed.map((f) => f.type === 'catch_up_complete' ? f.mode : undefined)).toEqual(['live_only']);
  await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE })).toBeVisible();
  await expect(chatInput(page)).toBeEnabled();
  await expect(page.getByText(/preparing history/i)).toHaveCount(0);
  await expect(page.locator(`[data-active-session-id="${history.sessionId}"]`)).toBeVisible();
  await history.record(`metadata-only-route-${fault}`);
});
}

test('T35 B42 B43 B49: canonical send once and REAL running engine survive history Retry/reconnect; Stop actually ends that turn', async ({ history, page }) => {
  await unavailableFixture(history);
  await history.open();
  const error = page.getByRole('alert').filter({ hasText: UNAVAILABLE });
  await expect(error).toBeVisible();
  const instruction = 'HISTORY_UNAVAILABLE_SEND_ONCE: load read_file using ToolSearch.';
  await chatInput(page).fill(instruction);
  await chatInput(page).press('Enter');
  await expect(page.getByText(TERMINAL_NARRATION, { exact: true })).toBeVisible();
  const stop = page.getByTestId('stop-btn');
  await expect(stop).toBeEnabled();
  expect(history.provider.calls(), 'positive control: a real streaming provider call is held open').toBe(1);
  const token = history.traffic.frames.find((f) => f.type === 'token' && f.session_id === history.sessionId && f.content === TERMINAL_NARRATION);
  expect(token?.type).toBe('token');
  if (!token || token.type !== 'token' || !token.turn_id) throw new Error('BLOCKED: genuine active-turn identity is required by D-N2/B49');
  const turnId = token.turn_id;
  await expect.poll(() => canonicalRecords(history).filter((r) => r.role === 'user' &&
    (r.view_membership === 'chat' || r.view_membership === 'both') && r.content === instruction).length,
  { message: 'D1/D-N1 save is canonical and durable even though index publication is unavailable' }).toBe(1);
  const beforeRetry = history.traffic.frames.length;
  await error.getByRole('button', { name: 'Retry', exact: true }).click();
  await expect(stop, 'history Retry never hides/locks already authorized current Stop').toBeEnabled();
  await expect.poll(() => history.traffic.frames.slice(beforeRetry).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  const retryAttach = history.traffic.attaches.at(-1);
  expect(retryAttach?.since_seq, 'explicit history Retry asks for snapshot, never a journal-only claim').toBeUndefined();
  await expect(error).toBeVisible();
  await expect(stop).toBeEnabled();
  expect(terminalLogRows(history.logPath).filter((r) => r.event_kind === 'turn_end' && r.turn_id === turnId), 'lost history must not have already ended/relaunched the real turn').toEqual([]);
  const beforeReconnect = history.traffic.frames.length;
  await dropSocket(page);
  await expect.poll(() => history.traffic.frames.slice(beforeReconnect).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  await expect(error).toBeVisible();
  await expect(stop).toBeEnabled();
  const state = history.traffic.frames.slice(beforeReconnect).find((f) => f.type === 'session_state' && f.session_id === history.sessionId);
  expect(state?.type).toBe('session_state');
  if (state?.type === 'session_state') {
    expect(state.history_state?.status).toBe('unavailable');
    expect(state.active_turn?.turn_id, 'same actual turn, not a fake Stop-only UI').toBe(turnId);
  }
  await stop.click();
  await expect.poll(() => terminalLogRows(history.logPath).filter((r) => r.event_kind === 'turn_end' && r.turn_id === turnId).length,
    { message: 'Stop reaches the running engine, not merely an enabled button' }).toBe(1);
  await expect(stop).toHaveCount(0);
  await expect(error, 'Stop does not pretend the lost index was repaired').toBeVisible();
  const messages = await page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.sessionId}/messages`);
  expect(messages.status()).toBe(500);
  expect(await messages.json()).toEqual({ error: UNAVAILABLE });
  expect(history.traffic.sends.filter((f) => f.content === instruction), 'Retry/reconnect never resend accepted input').toHaveLength(1);
  expect(canonicalRecords(history).filter((r) => r.role === 'user' &&
    (r.view_membership === 'chat' || r.view_membership === 'both') && r.content === instruction)).toHaveLength(1);
  expect(history.provider.calls(), 'no history action restarts the provider turn').toBe(1);
  expect(history.provider.failures()).toEqual([]);
  expect(history.traffic.frames.filter((f) => f.type === 'session_snapshot' && f.session_id === history.sessionId)).toEqual([]);
  await history.record('unavailable-history-engine-stop', { turnId });
});

test('T35 B20: unauthorized read with an invalid paging limit refuses before revealing unavailable history', async ({ history, page }) => {
  await unavailableFixture(history);
  const anonymous = await request.newContext({ baseURL: history.gateway.baseURL, storageState: { cookies: [], origins: [] } });
  try {
    const baseline = await anonymous.get(`/api/v1/sessions/${history.sessionId}/messages`);
    expect([401, 403], 'positive auth instrument must really refuse this anonymous gateway').toContain(baseline.status());
    const malformed = await anonymous.get(`/api/v1/sessions/${history.sessionId}/messages?limit=19`);
    expect(malformed.status()).toBe(baseline.status());
    expect(await malformed.json(), 'actual access-first response, not paging/index detail').toEqual(await baseline.json());
  } finally { await anonymous.dispose(); }
  await history.open();
  await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE })).toBeVisible();
});

for (const kind of ['approval', 'question'] as const) {
  test.describe(`T35 genuine ${kind} under unavailable history`, () => {
    test.use({ historyControlKind: kind });

    test(`T35 B43 B49: real pending ${kind} survives reconnect, its action works, then Stop ends the real resumed turn`, async ({ history, page }) => {
      await unavailableFixture(history);
      await history.open();
      await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE }), 'US1.6: explicit unavailable-history error precedes the initial send').toBeVisible();
      await expect(chatInput(page), 'US1.6: the composer remains usable after the history error and before the genuine decision opens').toBeEnabled();
      await sendControlInstruction(page);
      const pending = await waitForPendingControl(history);
      const calls = controlProvider(history).calls();
      await reconnectWithPending(history, pending, true);
      expect(controlProvider(history).calls(), 'reconnect does not rerun the model/tool request').toBe(calls);
      await activatePendingControl(history, pending);
      const before = history.traffic.frames.length;
      await page.getByRole('alert').filter({ hasText: UNAVAILABLE }).getByRole('button', { name: 'Retry', exact: true }).click();
      await expect(page.getByTestId('stop-btn'), 'history Retry cannot disable an already authorized Stop').toBeEnabled();
      await expect.poll(() => history.traffic.frames.slice(before).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
      expect(history.traffic.attaches.at(-1)?.since_seq).toBeUndefined();
      await stopRunningControlTurn(history);
      await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE })).toBeVisible();
      expect(history.traffic.sends, 'control action/history Retry/reconnect never repost the saved instruction').toHaveLength(1);
      const response = await page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.sessionId}/messages`);
      expect(response.status()).toBe(500);
      expect(await response.json()).toEqual({ error: UNAVAILABLE });
      await history.record(`genuine-${kind}-action-and-engine-stop`);
    });

    test(`T35 B43 B49: Stop cancels the genuine pending ${kind}, without granting or answering it`, async ({ history, page }) => {
      await unavailableFixture(history);
      await history.open();
      await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE }), 'US1.6: explicit unavailable-history error precedes the initial send').toBeVisible();
      await expect(chatInput(page), 'US1.6: the composer remains usable after the history error and before the genuine decision opens').toBeEnabled();
      await sendControlInstruction(page);
      const pending = await waitForPendingControl(history);
      await reconnectWithPending(history, pending, true);
      await stopPendingControl(history, pending);
      await expect(page.getByRole('alert').filter({ hasText: UNAVAILABLE })).toBeVisible();
      if (pending.kind === 'approval') {
        expect(history.traffic.frames.filter((f) => f.type === 'tool_approval_resolved' && f.approval_id === pending.frame.approval_id && f.state === 'approved')).toEqual([]);
      } else {
        expect(history.traffic.frames.filter((f) => f.type === 'ask_user_question' && f.card.card_id === pending.frame.card.card_id && f.card.status === 'answered')).toEqual([]);
      }
      expect(history.traffic.sends).toHaveLength(1);
      expect(controlProvider(history).failures()).toEqual([]);
      await history.record(`genuine-${kind}-pending-stop`);
    });
  });
}

test('T35 B43 B49: genuine secret action and Stop require the external request_secret runtime', async () => {
  // Explicit squad/chief decision: do not guess another work unit's tool
  // arguments or fabricate a secret card. This dependency remains visible.
  throw new Error('BLOCKED: request_secret not implemented (WU-4/5) — T35/B43/B49 genuine secret action and Stop require that external runtime');
});

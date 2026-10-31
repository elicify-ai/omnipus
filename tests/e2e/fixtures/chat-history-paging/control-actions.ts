import { expect, type Page } from '@playwright/test';
import { SecretRequestState, SessionDetail } from '../../../../src/lib/api/generated/schemas';
import { WsFrame } from '../../../../src/lib/api/generated/ws-schemas';
import type { ClientFrame } from '../../../../src/lib/api/generated/asyncapi-types';
import { chatInput, waitForConnected } from '../selectors';
import { terminalLogRows } from '../terminal-outcome-acceptance';
import { dropSocket } from './actions';
import { HistoryHarness, type Traffic, UNAVAILABLE } from './harness';
import { CONTROL_COMMAND, CONTROL_QUESTION, CONTROL_RUNNING, CONTROL_SECRET_PURPOSE, CONTROL_SECRET_VARIABLE, type HistoryControlProvider } from './control-provider';

// not-wire-format: fixture state referencing generated, actually received frames.
export type PendingHistoryControl =
  | { kind: 'approval'; frame: Extract<WsFrame, { type: 'tool_approval_required' }> }
  | { kind: 'question'; frame: Extract<WsFrame, { type: 'ask_user_question' }> }
  | { kind: 'secret'; frame: Extract<WsFrame, { type: 'secret_request' }> };

// Harmless test input, never a real credential. FR-075: this may travel only in
// the authenticated card submit, never chat frames or model/tool arguments.
const SECRET_FIXTURE_VALUE = 'history-card-fixture-9af2-not-a-real-secret';

export function controlProvider(history: HistoryHarness): HistoryControlProvider {
  if (!('kind' in history.provider)) throw new Error('historyControlKind fixture option is required');
  return history.provider;
}
export async function sendControlInstruction(page: Page) {
  await chatInput(page).fill('Use the requested harmless tool. If my output format is unknown, ask me to choose Plain text or JSON.');
  await chatInput(page).press('Enter');
}
export async function waitForPendingControl(history: HistoryHarness): Promise<PendingHistoryControl> {
  const provider = controlProvider(history);
  if (provider.kind === 'stream') throw new Error('stream-only fixture has no pending control');
  await expect.poll(() => {
    if (provider.failures().length) throw new Error(provider.failures().join('\n'));
    return history.traffic.frames.some((f) => provider.kind === 'approval'
      ? f.type === 'tool_approval_required' && f.session_id === history.sessionId
      : provider.kind === 'secret' ? f.type === 'secret_request' && f.session_id === history.sessionId
      : f.type === 'ask_user_question' && f.card.session_id === history.sessionId);
  }, { message: 'real engine/tool registry must produce the actual pending control, not a scripted gateway frame' }).toBe(true);
  if (provider.kind === 'approval') {
    const frame = history.traffic.frames.find((f) => f.type === 'tool_approval_required' && f.session_id === history.sessionId);
    if (!frame || frame.type !== 'tool_approval_required') throw new Error('real approval frame missing');
    expect(frame.tool_name).toBe('bash');
    expect(frame.agent_id).toBe(history.ids.agent.id);
    expect(frame.args).toMatchObject({ command: CONTROL_COMMAND });
    return { kind: 'approval', frame };
  }
  if (provider.kind === 'secret') {
    const frame = history.traffic.frames.find((f) => f.type === 'secret_request' && f.session_id === history.sessionId);
    if (!frame || frame.type !== 'secret_request') throw new Error('real pending secret frame missing');
    expect(frame.acting_agent_id).toBe(history.ids.agent.id);
    expect(frame.purpose).toBe(CONTROL_SECRET_PURPOSE);
    expect(frame.recipient.kind).toBe('agent_private');
    expect(frame.owner, 'FR-065/FR-076: server derives the requesting agent and workspace').toEqual({
      workspace_id: history.ids.session.workspace_id, agent_id: history.ids.agent.id,
    });
    expect(frame.fields.map(field => field.label), 'one requested private variable, not a fabricated generic card').toEqual([CONTROL_SECRET_VARIABLE]);
    const pending = { kind: 'secret', frame } as const;
    const state = await readSecretState(history, pending);
    expect(state.state, 'the real registry still owns the pending request').toBe('pending');
    expect(state.reference_names, 'nothing stored before consent').toBeUndefined();
    return pending;
  }
  const frame = history.traffic.frames.find((f) => f.type === 'ask_user_question' && f.card.session_id === history.sessionId);
  if (!frame || frame.type !== 'ask_user_question') throw new Error('real pending question frame missing');
  expect(frame.card.status).toBe('pending');
  expect(frame.card.agent_id).toBe(history.ids.agent.id);
  expect(frame.card.questions.map((q) => ({ header: q.header, question: q.question, labels: q.options.map((o) => o.label) })))
    .toEqual([{ header: 'Format', question: CONTROL_QUESTION, labels: ['Plain text', 'JSON'] }]);
  return { kind: 'question', frame };
}
export function pendingAction(page: Page, pending: PendingHistoryControl) {
  if (pending.kind === 'secret') return secretCard(page, pending).getByTestId(`secret-request-field-${pending.frame.fields[0].key}`);
  return pending.kind === 'approval'
    ? page.getByRole('region', { name: 'Tool Approval Required', exact: true }).getByRole('button', { name: 'Approve Once', exact: true })
    : page.getByRole('group', { name: `Questions from ${pending.frame.card.agent_id}`, exact: true }).getByRole('button', { name: /^Plain text/ });
}
function secretCard(page: Page, pending: Extract<PendingHistoryControl, { kind: 'secret' }>) {
  return page.getByRole('region', { name: `Secret requested by ${pending.frame.acting_agent_name}`, exact: true })
    .filter({ hasText: CONTROL_SECRET_PURPOSE });
}
export async function readSecretState(history: HistoryHarness, pending: Extract<PendingHistoryControl, { kind: 'secret' }>) {
  const response = await history.page.request.get(`${history.gateway.baseURL}/api/v1/secret-requests/${pending.frame.request_id}`);
  expect(response.status(), 'genuine accessible secret request REST state').toBe(200);
  const state = SecretRequestState.parse(await response.json());
  expect(state.request_id, 'REST state belongs to the exact observed request').toBe(pending.frame.request_id);
  return state;
}
export async function assertRestoredControl(history: HistoryHarness, pending: PendingHistoryControl, start: number, unavailable: boolean) {
  const state = [...history.traffic.frames.slice(start)].reverse().find((f) => f.type === 'session_state' && f.session_id === history.sessionId);
  if (!state || state.type !== 'session_state') throw new Error('authoritative current session_state was not delivered');
  expect(state.history_state?.status).toBe(unavailable ? 'unavailable' : 'available');
  if (pending.kind === 'approval') {
    const restored = state.pending_approvals.filter((p) => p.session_id === history.sessionId);
    expect(restored.map((p) => p.approval_id)).toEqual([pending.frame.approval_id]);
    expect(restored[0].args).toEqual(pending.frame.args);
    expect(restored[0].turn_id).toBe(pending.frame.turn_id);
  } else if (pending.kind === 'question') {
    expect(state.pending_asks, 'pending questions remain a current-control owner, not lost with history').toBeDefined();
    if (!state.pending_asks) throw new Error('current pending_asks missing');
    expect(state.pending_asks.filter((q) => q.session_id === history.sessionId).map((q) => q.card_id)).toEqual([pending.frame.card.card_id]);
  } else {
    expect(state.secret_requests, 'K4/FR-074: pending secret remains current state despite unavailable history').toBeDefined();
    if (!state.secret_requests) throw new Error('current secret_requests missing');
    expect(state.secret_requests.filter(card => card.session_id === history.sessionId).map(card => ({
      request_id: card.request_id, state: card.state, fields: card.fields, owner: card.owner,
    }))).toEqual([{ request_id: pending.frame.request_id, state: 'pending', fields: pending.frame.fields, owner: pending.frame.owner }]);
    await expect(secretCard(history.page, pending)).toHaveAttribute('data-state', 'pending');
  }
  await expect(pendingAction(history.page, pending)).toBeVisible();
}
export async function reconnectWithPending(history: HistoryHarness, pending: PendingHistoryControl, unavailable: boolean) {
  const start = history.traffic.frames.length;
  if (unavailable) {
    await expect(history.page.getByRole('alert').filter({ hasText: UNAVAILABLE }), 'US1.6: history error precedes the initial decision focus check').toBeVisible();
  }
  await pendingAction(history.page, pending).focus();
  await dropSocket(history.page);
  await waitForConnected(history.page);
  await expect.poll(() => history.traffic.frames.slice(start).filter((f) => f.type === 'catch_up_complete' && f.session_id === history.sessionId).length).toBe(1);
  if (unavailable) {
    await expect(history.page.getByRole('alert').filter({ hasText: UNAVAILABLE }), 'US1.6: restored history error precedes restored-decision checks and action').toBeVisible();
  }
  await assertRestoredControl(history, pending, start, unavailable);
  if (unavailable) {
    await expect(pendingAction(history.page, pending), 'US1.6: the restored decision remains enabled after the history error').toBeEnabled();
  }
  await expect(pendingAction(history.page, pending), 'current focused decision survives catch-up').toBeFocused();
  if (unavailable) await expect(history.page.getByRole('alert').filter({ hasText: UNAVAILABLE })).toBeVisible();
}
export async function activatePendingControl(history: HistoryHarness, pending: PendingHistoryControl) {
  if (pending.kind === 'approval') {
    await pendingAction(history.page, pending).click();
    await expect.poll(() => history.traffic.frames.some((f) => f.type === 'tool_approval_resolved' && f.approval_id === pending.frame.approval_id && f.state === 'approved')).toBe(true);
    await expect.poll(() => terminalLogRows(history.logPath).filter((row) => row.event_kind === 'tool_exec_end' &&
      row.turn_id === pending.frame.turn_id && row.tool === 'bash' && row.is_error === false).length,
    { message: 'Approve Once actually executed the real harmless command successfully' }).toBe(1);
  } else if (pending.kind === 'secret') {
    // US-6 / FR-075: only the genuine secure-card REST submit carries the
    // test value. It is never sent as a chat message or tool argument.
    await pendingAction(history.page, pending).fill(SECRET_FIXTURE_VALUE);
    const submitted = history.page.waitForResponse(response => response.request().method() === 'POST' &&
      new URL(response.url()).pathname === `/api/v1/secret-requests/${pending.frame.request_id}/submit`);
    await secretCard(history.page, pending).getByTestId('secret-request-submit').click();
    const response = await submitted;
    expect(response.status(), 'the actual destination-bound secure-card submit succeeds').toBe(200);
    expect(response.request().postDataJSON()).toEqual({ values: { [pending.frame.fields[0].key]: SECRET_FIXTURE_VALUE } });
    const state = SecretRequestState.parse(await response.json());
    expect(state.request_id).toBe(pending.frame.request_id);
    expect(state.state).toBe('submitted');
    expect(state.reference_names, 'one actual stored private variable reference, never the value').toHaveLength(1);
    expect(JSON.stringify(state), 'value-free submit response').not.toContain(SECRET_FIXTURE_VALUE);
    await expect.poll(() => history.traffic.frames.filter(frame => frame.type === 'secret_request_resolved' &&
      frame.request_id === pending.frame.request_id && frame.state === 'submitted').length).toBe(1);
    await expect(secretCard(history.page, pending)).toHaveAttribute('data-state', 'submitted');
    await expect(secretCard(history.page, pending).getByTestId('secret-request-result')).toContainText('Saved');
    await expect(pendingAction(history.page, pending), 'terminal card clears the input').toHaveCount(0);
    expect(JSON.stringify(history.traffic.frames), 'the card value never enters the chat socket').not.toContain(SECRET_FIXTURE_VALUE);
    expect(JSON.stringify(history.traffic.sends), 'secret consent is not a new chat instruction').not.toContain(SECRET_FIXTURE_VALUE);
  } else {
    // Existing #1066 keyboard contract: Enter chooses/confirms the last
    // question immediately. No sleep/autosubmit deadline is the action oracle.
    await pendingAction(history.page, pending).press('Enter');
    await expect.poll(() => history.traffic.frames.some((f) => f.type === 'ask_user_question' && f.card.card_id === pending.frame.card.card_id && f.card.status === 'answered')).toBe(true);
    const answered = [...history.traffic.frames].reverse().find((f) => f.type === 'ask_user_question' && f.card.card_id === pending.frame.card.card_id && f.card.status === 'answered');
    if (!answered || answered.type !== 'ask_user_question') throw new Error('actual answered question outcome missing');
    expect(answered.card.answers?.map((answer) => ({ header: answer.header, selected: answer.selected, auto_default: answer.auto_default })))
      .toEqual([{ header: 'Format', selected: ['Plain text'], auto_default: false }]);
  }
  await expect(history.page.getByText(CONTROL_RUNNING, { exact: true })).toBeVisible();
}
export async function stopRunningControlTurn(history: HistoryHarness) {
  await stopHeldControlTurn(history);
  await assertSessionStopped(history);
  expect(controlProvider(history).failures()).toEqual([]);
}
/** The latest genuinely running control turn's observed identity, skipping already-stopped turns. */
function runningControlTurnId(history: HistoryHarness, stopped: readonly string[]) {
  const fact = [...history.traffic.frames].reverse().find((f) =>
    (f.type === 'token' && f.session_id === history.sessionId && f.content === CONTROL_RUNNING && f.turn_id !== undefined && !stopped.includes(f.turn_id)) ||
    (f.type === 'session_state' && f.session_id === history.sessionId && f.active_turn !== undefined && !stopped.includes(f.active_turn.turn_id)));
  return fact?.type === 'token' ? fact.turn_id : fact?.type === 'session_state' ? fact.active_turn?.turn_id : undefined;
}
/**
 * Stop ONE genuinely running, provider-held control turn through the visible
 * Stop button and prove that exact turn ended and its held model call was
 * cancelled. Lifecycle is the caller's oracle (assertSessionStopped).
 */
export async function stopHeldControlTurn(history: HistoryHarness, stopped: readonly string[] = []) {
  const provider = controlProvider(history);
  await expect.poll(() => provider.heldCount(), { message: 'positive control: an actual model HTTP response is held open' }).toBeGreaterThan(0);
  if (stopped.length) await expect.poll(() => runningControlTurnId(history, stopped) !== undefined, { message: 'a further genuinely running control turn is observed' }).toBe(true);
  const turnId = runningControlTurnId(history, stopped);
  if (!turnId) throw new Error('genuinely running provider turn has no authoritative observed identity');
  const before = provider.aborted();
  expect(terminalLogRows(history.logPath).filter((r) => r.event_kind === 'turn_end' && r.turn_id === turnId)).toEqual([]);
  await history.page.getByTestId('stop-btn').click();
  await expect.poll(() => terminalLogRows(history.logPath).filter((r) => r.event_kind === 'turn_end' && r.turn_id === turnId).length).toBe(1);
  await expect.poll(() => provider.aborted(), { message: 'Stop cancelled the actual held HTTP model call' }).toBe(before + 1);
  return turnId;
}
async function startRunningTurnWithPendingQuestion(history: HistoryHarness, pending: Extract<PendingHistoryControl, { kind: 'question' }>) {
  return startRunningTurnWithParkedControl(history, pending);
}
async function startRunningTurnWithParkedControl(history: HistoryHarness, pending: Exclude<PendingHistoryControl, { kind: 'approval' }>) {
  // Both tools park their original turn. B49 requires a genuinely RUNNING
  // turn while that same decision remains pending, not a fake Stop button.
  const provider = controlProvider(history);
  const tool = pending.kind === 'question' ? 'AskUserQuestion' : 'request_secret';
  const parkedEnds = () => terminalLogRows(history.logPath).filter(row => row.event_kind === 'tool_exec_end' && row.tool === tool);
  await expect.poll(parkedEnds, { message: `PREREQUISITE: actual ${pending.kind} tool execution is recorded exactly once` }).toHaveLength(1);
  const parkedTurnId = parkedEnds()[0].turn_id;
  if (typeof parkedTurnId !== 'string' || !parkedTurnId) throw new Error(`PREREQUISITE: genuine ${pending.kind} audit turn identity missing`);
  await expect.poll(() => terminalLogRows(history.logPath).filter(row => row.event_kind === 'turn_end' && row.turn_id === parkedTurnId).length,
    { message: `PREREQUISITE: the ${pending.kind} turn has actually parked before the independent producer input` }).toBe(1);
  const producer = await history.page.context().newPage(); // Reuse the same current cookie; never log in again.
  try {
  const viewer = new HistoryHarness(producer, history.gateway, history.ids, history.provider, history.info, history.logPath);
  await viewer.open(history.sessionId, { currentControlMayLockComposer: true });
  const callsBefore = provider.calls();
  const input = { type: 'message', session_id: history.sessionId, agent_id: history.ids.agent.id,
    client_message_id: `history-keep-${pending.kind}-running`,
    content: pending.kind === 'question'
      ? 'HISTORY_KEEP_QUESTION_PENDING: continue the draft without choosing an output format until I press Stop.'
      : 'HISTORY_KEEP_SECRET_PENDING: continue the draft without filling or cancelling the secret request until I press Stop.' } satisfies ClientFrame;
  const send = async (frame: ClientFrame) => {
    WsFrame.parse(frame);
    await producer.evaluate(payload => {
      const socket = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances?.find(candidate =>
        candidate.readyState === WebSocket.OPEN && new URL(candidate.url).pathname === '/api/v1/chat/ws');
      if (!socket) throw new Error('PREREQUISITE: auxiliary producer has no real authenticated chat socket');
      socket.send(JSON.stringify(payload));
    }, frame);
  };
  await send(input);
  await expect.poll(() => viewer.traffic.sends.filter(frame => frame.client_message_id === input.client_message_id).length,
    { message: 'PREREQUISITE: auxiliary input is separately observed exactly once, not hidden in the original send count' }).toBe(1);
  await expect.poll(() => viewer.traffic.frames.filter(frame => frame.type === 'user_message' &&
    frame.session_id === history.sessionId && frame.client_message_id === input.client_message_id).length,
    { message: 'PREREQUISITE: the actual gateway durably echoes the auxiliary input' }).toBe(1);
  await expect(history.page.getByText(CONTROL_RUNNING, { exact: true }), 'PREREQUISITE: a new genuine provider turn is running').toBeVisible();
  await expect.poll(() => provider.calls(), { message: 'one independently requested model turn, not a rerun of the question' }).toBe(callsBefore + 1);
  await expect.poll(() => provider.heldCount(), { message: 'the actual auxiliary model HTTP call is held open' }).toBe(1);
  const stateStart = viewer.traffic.frames.length;
  await send({ type: 'attach_session', session_id: history.sessionId });
  await expect.poll(() => viewer.traffic.frames.slice(stateStart).some(frame => frame.type === 'session_state' &&
    frame.session_id === history.sessionId && frame.active_turn !== undefined),
    { message: 'PREREQUISITE: fresh authoritative state must expose the genuinely running turn' }).toBe(true);
  const state = [...viewer.traffic.frames.slice(stateStart)].reverse().find(frame => frame.type === 'session_state' && frame.session_id === history.sessionId);
  if (state?.type !== 'session_state' || !state.active_turn) throw new Error('PREREQUISITE: active turn missing from authoritative current state');
  expect(state.active_turn.turn_id, 'the running input is not the already parked decision turn').not.toBe(parkedTurnId);
  if (pending.kind === 'question') {
    expect(state.pending_asks?.filter(card => card.card_id === pending.frame.card.card_id).map(card => ({ id: card.card_id, status: card.status })),
      'the original real question stays pending, not implicitly answered or cancelled by fixture setup')
      .toEqual([{ id: pending.frame.card.card_id, status: 'pending' }]);
  } else {
    expect(state.secret_requests?.filter(card => card.request_id === pending.frame.request_id).map(card => ({ id: card.request_id, state: card.state })),
      'the original real secret request remains pending while an actual independent engine turn runs')
      .toEqual([{ id: pending.frame.request_id, state: 'pending' }]);
  }
  expect(terminalLogRows(history.logPath).filter(row => row.event_kind === 'turn_end' && row.turn_id === state.active_turn!.turn_id),
    'the independently running turn has not already ended or parked').toEqual([]);
  await expect(pendingAction(history.page, pending)).toBeEnabled();
  await expect(history.page.getByTestId('stop-btn'), 'PREREQUISITE: real running engine exposes Stop while the old decision remains pending').toBeVisible();
  await history.record(`pending-${pending.kind}-with-independent-running-turn`, {
    auxiliaryInput: input, auxiliarySends: viewer.traffic.sends, activeTurn: state.active_turn,
    pendingCard: pending.kind === 'question' ? pending.frame.card.card_id : pending.frame.request_id,
  });
  return producer;
  } catch (error) {
    await producer.close();
    throw error;
  }
}

export async function stopPendingControl(history: HistoryHarness, pending: PendingHistoryControl) {
  const producer = pending.kind === 'question' ? await startRunningTurnWithPendingQuestion(history, pending)
    : pending.kind === 'secret' ? await startRunningTurnWithParkedControl(history, pending) : undefined;
  const before = controlProvider(history).calls();
  try {
    if (pending.kind === 'approval') {
      // Inline Cancel refuses only this tool; B49 is the actual current-turn Stop.
      if (!pending.frame.turn_id) throw new Error('PREREQUISITE: genuine approval has no observed owning turn identity');
      expect(terminalLogRows(history.logPath).filter(row => row.event_kind === 'turn_end' && row.turn_id === pending.frame.turn_id),
        'PREREQUISITE: the genuine approval is still holding this exact turn open').toEqual([]);
      const stop = history.page.getByTestId('stop-btn');
      await expect(stop).toBeEnabled();
      await stop.click();
      await expect.poll(() => history.traffic.frames.some((f) => f.type === 'tool_approval_resolved' && f.approval_id === pending.frame.approval_id && f.state === 'denied_cancel')).toBe(true);
      await expect.poll(() => terminalLogRows(history.logPath).filter(row => row.event_kind === 'turn_end' && row.turn_id === pending.frame.turn_id).length,
        { message: 'Stop ends the exact turn that owned the genuine pending approval' }).toBe(1);
    } else {
      await stopRunningControlTurn(history);
      if (pending.kind === 'question') {
        await expect.poll(() => history.traffic.frames.some((f) => f.type === 'ask_user_question' && f.card.card_id === pending.frame.card.card_id && f.card.status === 'cancelled')).toBe(true);
      } else {
        await expect.poll(() => history.traffic.frames.filter(frame => frame.type === 'secret_request_resolved' &&
          frame.request_id === pending.frame.request_id && frame.state === 'stopped').length).toBe(1);
        const state = await readSecretState(history, pending);
        expect(state.state, 'BDD-6.5: Stop terminates the exact unsubmitted card').toBe('stopped');
        expect(state.reference_names, 'nothing was stored by Stop').toBeUndefined();
        await expect(secretCard(history.page, pending)).toHaveAttribute('data-state', 'stopped');
        await expect(secretCard(history.page, pending).getByTestId('secret-request-result')).toHaveText('Stopped — nothing was stored.');
        await expect(pendingAction(history.page, pending)).toHaveCount(0);
      }
    }
    await assertSessionStopped(history);
    expect(controlProvider(history).calls(), 'Stop does not approve/answer/resume another model turn').toBe(before);
  } finally { if (producer) await producer.close(); }
}
export async function assertSessionStopped(history: HistoryHarness) {
  await expect.poll(async () => {
    const response = await history.page.request.get(`${history.gateway.baseURL}/api/v1/sessions/${history.sessionId}`);
    expect(response.status()).toBe(200);
    return SessionDetail.parse(await response.json()).session.lifecycle_state;
  }, { message: 'real server lifecycle, not a hidden Stop button, records stopped' }).toBe('stopped');
}
export async function floodRealJournal(producer: Page, traffic: Traffic, sessionId: string, agentId: string) {
  // ADR-066 bounds USER input at 64,000 Unicode codepoints, not UTF-8 bytes.
  // Five 60,000-codepoint non-emoji CJK messages fit that admission contract
  // while their four-byte encoding exceeds the unchanged real 1-MiB journal.
  // Actual authenticated socket sends, canonical echoes and bytes are required.
  for (let i = 0; i < 5; i++) {
    const content = `HISTORY_JOURNAL_FILL_${i} ` + '\u{20000}'.repeat(60_000);
    expect([...content].length, 'journal fixture fits the shipped 64,000-codepoint user admission bound').toBeLessThanOrEqual(64_000);
    const frame = { type: 'message', session_id: sessionId, agent_id: agentId,
      client_message_id: `history-journal-fill-${i}`, content } satisfies ClientFrame;
    const schemas = await import('../../../../src/lib/api/generated/ws-schemas');
    schemas.WsFrame.parse(frame);
    await producer.evaluate((payload) => {
      const socket = (window as unknown as { __ws_instances?: WebSocket[] }).__ws_instances?.find((s) => s.readyState === WebSocket.OPEN);
      if (!socket) throw new Error('journal producer has no real authenticated OPEN socket');
      socket.send(JSON.stringify(payload));
    }, frame);
    await expect.poll(() => traffic.frames.filter((f) => f.type === 'user_message' && f.session_id === sessionId && f.client_message_id === frame.client_message_id && f.seq !== undefined).length,
      { message: 'journal filler is canonically saved and broadcast by the real gateway' }).toBe(1);
  }
  const echoes = traffic.frames.filter((f) => f.type === 'user_message' && f.session_id === sessionId && f.content.startsWith('HISTORY_JOURNAL_FILL_') && f.seq !== undefined);
  expect(echoes).toHaveLength(5);
  expect(echoes.reduce((bytes, f) => bytes + Buffer.byteLength(JSON.stringify(f)), 0), 'actual published bytes exceed the1-MiB retention budget').toBeGreaterThan(1024 * 1024);
}

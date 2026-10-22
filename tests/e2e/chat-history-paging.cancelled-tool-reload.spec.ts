import { expect, type Page } from '@playwright/test';
import { test, type ReloadHarness } from './fixtures/chat-history-paging/reload-fixture';
import { CANCEL_INPUT, CANCELLED_ID, CANCELLED_COMMAND, COMPLETED_ID, COMPLETED_MARKER,
  CANCELLED_MARKER, RUNNING_MARKER, EARLIER_TOKENS } from './fixtures/chat-history-paging/reload-provider';

test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1280, height: 900 } });
test.describe.configure({ retries: 0 });
test.setTimeout(240_000);

const cancelledRow = (page: Page) => page.locator('[data-testid="tool-call-badge"][data-tool="bash"]')
  .filter({ has: page.getByText('Cancelled', { exact: true }) });

async function runAndStop(reload: ReloadHarness) {
  await reload.ready();
  await reload.baseline(); // Independent actual completed-call/known-usage control.
  await reload.send(CANCEL_INPUT);
  await expect.poll(() => reload.marker(RUNNING_MARKER), {
    message: 'PREREQUISITE: actual native child started and wrote its process receipt, not just a queued call badge',
  }).toBeDefined();
  expect(reload.executionCount(CANCELLED_MARKER), 'PREREQUISITE: exactly one actual started native command').toBe(1);
  expect(reload.nativeRunning(), 'PREREQUISITE: native process is genuinely running before Stop').toBe(true);
  const starts = reload.traffic.frames.filter(frame => frame.type === 'tool_call_start' && frame.call_id === CANCELLED_ID);
  expect(starts, 'PREREQUISITE: actual gateway start has the independently planned call identity').toHaveLength(1);
  expect(starts[0]).toMatchObject({ type: 'tool_call_start', session_id: reload.sessionId, tool: 'bash',
    call_id: CANCELLED_ID, params: { command: CANCELLED_COMMAND, timeout_seconds: 300 } });
  await expect(reload.page.getByTestId('stop-btn'), 'PREREQUISITE: genuine running turn exposes Stop').toBeVisible();
  await reload.page.getByTestId('stop-btn').click();
  await expect.poll(async () => (await reload.detail()).session.lifecycle_state,
    { message: 'PREREQUISITE: UI Stop actually ends the native engine turn' }).toBe('stopped');
  await expect.poll(() => reload.nativeRunning(), { message: 'PREREQUISITE: Stop terminated the actual native child, not only the badge' }).toBe(false);
  await expect(cancelledRow(reload.page), 'PREREQUISITE: the genuinely stopped tool is visibly Cancelled BEFORE reload').toHaveCount(1);
  await expect(cancelledRow(reload.page)).toBeVisible();
  await reload.counter(EARLIER_TOKENS, 'PREREQUISITE: recorded nonzero live counter before reload');
  expect(reload.provider.failures).toEqual([]);
  await reload.record('live-native-cancelled-before-reload');
}

async function sameCancelledCall(reload: ReloadHarness) {
  const row = cancelledRow(reload.page);
  await expect(row, 'T40/#1418: reload must retain the SAME one named Cancelled native tool row, not only interrupted text').toHaveCount(1);
  await expect(row).toBeVisible();
  await expect(row).toHaveAttribute('data-tool', 'bash');
  await row.getByTestId('tool-call-toggle').click();
  await expect(row.locator('code')).toHaveText('bash');
  expect(JSON.parse(await row.locator('pre').first().innerText()), 'D16: original parameters survive with the original call')
    .toEqual({ command: CANCELLED_COMMAND, timeout_seconds: 300 });
  await expect(reload.page.getByText('(interrupted)', { exact: false }), 'the interrupted assistant reply remains separate from its tool row').toBeVisible();
  await reload.counter(EARLIER_TOKENS, 'T40: same live recorded total after reload');
  const body = await reload.read(100);
  const calls = body.entries.flatMap(entry => entry.tool_calls ?? []);
  expect(calls.filter(call => call.id === CANCELLED_ID).map(call => ({ id: call.id, tool: call.tool, status: call.status, parameters: call.parameters })),
    'real REST projection joins the visible row to its original native call ID').toEqual([
    { id: CANCELLED_ID, tool: 'bash', status: 'cancelled', parameters: { command: CANCELLED_COMMAND, timeout_seconds: 300 } },
  ]);
  expect(calls.filter(call => call.id === COMPLETED_ID).map(call => call.status), 'D16: Stop cannot rewrite the earlier completed call').toEqual(['success']);
  const replayed = reload.traffic.frames.filter(frame => frame.type === 'tool_call_result' && frame.call_id === CANCELLED_ID);
  expect(replayed.some(frame => frame.type === 'tool_call_result' && frame.status === 'cancelled'),
    'real reload snapshot/replay preserves cancelled rather than binary error clamping').toBe(true);
}

test('T40 B52 B54: actual Stop-cancelled bash row survives REAL reload with its name, state, identity and completed control', async ({ reload }) => {
  await runAndStop(reload);
  const receipts = reload.provider.receipts.length;
  const inputs = reload.traffic.sends.length;
  reload.assertion('T40 real browser reload reconstructs the confirmed cancelled call');
  await reload.ready(reload.sessionId, true);
  await reload.noReexecution(receipts, inputs, 1);
  await sameCancelledCall(reload);
  expect(reload.executionCount(COMPLETED_MARKER)).toBe(1);
  await reload.record('same-cancelled-call-after-real-reload');
});

test('T40 B52 B53: real cancelled call survives browser reload and an older-page revisit beyond the bounded latest tail', async ({ reload }) => {
  await runAndStop(reload);
  const receipts = reload.provider.receipts.length;
  const inputs = reload.traffic.sends.length;
  await reload.seedOrdinary('d16-after-cancel', 240);
  await reload.ready();
  reload.assertion('T40 reload then genuine older-page reconstruction');
  await reload.ready(reload.sessionId, true);
  const body = await reload.olderUntil(page => page.entries.some(entry => entry.tool_calls?.some(call => call.id === COMPLETED_ID)));
  const cancelled = body.entries.flatMap(entry => entry.tool_calls ?? []).filter(call => call.id === CANCELLED_ID);
  expect(cancelled.map(call => ({ id: call.id, tool: call.tool, status: call.status })),
    'T40/#1418: the real older page containing the completed control must also retain the original cancelled call')
    .toEqual([{ id: CANCELLED_ID, tool: 'bash', status: 'cancelled' }]);
  await expect(cancelledRow(reload.page), 'T40/#1418: older reconstruction renders the same single Cancelled row').toHaveCount(1);
  await expect(cancelledRow(reload.page)).toBeVisible();
  await reload.counter(EARLIER_TOKENS, 'T40: older reader retains the live session-wide counter');
  await reload.noReexecution(receipts, inputs, 1);
  await reload.record('same-cancelled-call-in-reloaded-older-view');
});

// #1537 Q11 / FR-009: real gateway projection -> native reload -> painted rows.
// Script external model responses only. Never synthesize a projection/WS frame.
import { expect, type Page, type TestInfo } from '@playwright/test';
import { createNoticeScene, recordScene, type NoticeScene } from './helper-notices-harness';
import { paintedRows, expectPainted } from './helper-notices-visible';
import { waitForConnected } from './selectors';

export async function orderedHelperNames(page: Page, names: string[]) {
  const alternatives = names.map(name => name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('|');
  const rows = await paintedRows(page.getByTestId('provider-helper-rows').locator('span, p, div'), `^(?:${alternatives}): `, '.*', true);
  // locator order is document order, NOT the order in names or an alphabetic sort.
  return rows.map(row => row.text.slice(0, row.text.indexOf(': ')));
}

async function checkOrderInstrument(page: Page) {
  const probe = await page.context().newPage();
  try {
    await probe.setContent('<main data-testid="provider-helper-rows"><p id="a">A: real notice</p><p id="b">B: real notice</p><p id="c">C: real notice</p><p hidden>D: hidden notice</p><p aria-live="polite">D: announcement only</p></main>');
    expect(await orderedHelperNames(probe, ['A', 'B', 'C', 'D']), 'Q11 instrument: only painted rows, not hidden/announcement text').toEqual(['A', 'B', 'C']);
    await probe.locator('#a').evaluate(node => node.parentElement!.append(node));
    expect(await orderedHelperNames(probe, ['A', 'B', 'C', 'D']), 'Q11 instrument: a wrong actual DOM order is not silently sorted into the expected order').toEqual(['B', 'C', 'A']);
  } finally { await probe.close(); }
}

function notices(scene: NoticeScene, index: number) {
  return scene.traffic.frames.filter(frame => frame.session_id === scene.root.id &&
    frame.child_session_id === scene.children[index].id && ['provider_status', 'provider_retry'].includes(String(frame.type)));
}
function retryCount(scene: NoticeScene, index: number) {
  return notices(scene, index).filter(frame => frame.type === 'provider_retry').length;
}
async function releaseFirstRetry(scene: NoticeScene, index: number) {
  expect(retryCount(scene, index), `Q11 setup: helper ${index} has not retried before its failure is released`).toBe(0);
  scene.provider.fail[index].resolve();
  await expect.poll(() => retryCount(scene, index), { message: `Q11 actual addressed retry from helper ${index}`, timeout: 15_000 }).toBe(1);
  expect(scene.provider.evidence.requests.some(row => row.role === `helper-${index}` && row.status === 429), 'Q11 setup: a real fake-model 429 caused this retry').toBe(true);
}

export async function expectOrderedTrio(scene: NoticeScene, label: string) {
  const names = scene.agents.map(agent => agent.name);
  await expect.poll(() => orderedHelperNames(scene.page, names), { message: `${label}: exact visible identities AND DOM order, oldest three A/B/C`, timeout: 2_000 }).toEqual(names.slice(0, 3));
  if (names.length === 4) await expectPainted(scene.page.getByTestId('provider-helper-overflow-row'), '1 more helper has a model notice');
}

async function reloadPreservingOrder(scene: NoticeScene, info: TestInfo, label: string) {
  await expectOrderedTrio(scene, `Q11 ${label} LIVE`);
  const live = await orderedHelperNames(scene.page, scene.agents.map(agent => agent.name));
  await recordScene(scene, info, `${label}-live-order`);
  const fence = scene.traffic.frames.length;
  await scene.page.reload();
  await waitForConnected(scene.page);
  const receipt = await scene.catchupReceipt.first(info, `${label}-full-snapshot`);
  expect(receipt.frame?.session_id, 'Q11 reload belongs to the real main chat').toBe(scene.root.id);
  expect(receipt.frame?.mode, 'Q11 setup: test a FULL snapshot, not an unchanged live store/incremental replay').toBe('snapshot');
  const reload = scene.traffic.frames.slice(fence);
  expect(reload.filter(frame => frame.type === 'provider_retry' && frame.session_id === scene.root.id).length, 'Q11 positive control: the gateway actually replayed four helper retries').toBe(4);
  await info.attach(`helper-notice-order-${label}`, { body: JSON.stringify({ live,
    afterReload: await orderedHelperNames(scene.page, scene.agents.map(agent => agent.name)),
    replay: reload.filter(frame => frame.child_session_id).map(frame => ({ type: frame.type, state: frame.state,
      child: scene.children.findIndex(child => child.id === frame.child_session_id), turn: frame.turn_id })) }, null, 2), contentType: 'application/json' });
  await recordScene(scene, info, `${label}-reloaded-order`);
  await expectOrderedTrio(scene, `Q11 ${label} RELOAD`);
  expect(await orderedHelperNames(scene.page, scene.agents.map(agent => agent.name)), 'Q11 reload must equal the same real live sequence, not just contain the same names').toEqual(live);
}

export async function runLaterAOrderCase(page: Page, info: TestInfo, later: 'second retry' | 'silence then retry') {
  await checkOrderInstrument(page);
  const scene = await createNoticeScene(page, info, { script: 'retry', count: 4, helperNames: ['A', 'B', 'C', 'D'],
    helperScripts: [later === 'second retry' ? 'repeat' : 'retry-silence-retry', 'retry', 'retry', 'retry'],
    helperWaits: [1, 120, 120, 120], nativeNotices: true });
  try {
    scene.provider.parentFinish.resolve();
    await expect.poll(() => scene.provider.evidence.parentDone).toBe(true);
    for (let index = 0; index < 4; index++) await releaseFirstRetry(scene, index);
    await expectOrderedTrio(scene, `Q11 ${later} BEFORE A update`);
    scene.provider.helpers[0].retryWait = 120; // external next 429 only; leave the already-published 1s retry untouched.
    if (later === 'second retry') {
      scene.provider.answer[0].resolve();
      await expect.poll(() => scene.provider.evidence.toolResults.some(result => (result as { tool_call_id?: string }).tool_call_id === 'hm-hold-0'),
        { message: 'Q11 setup: A really answered and completed its correlated 30s tool before the second retry', timeout: 35_000 }).toBe(true);
    } else {
      await expect.poll(() => notices(scene, 0).some(frame => frame.type === 'provider_status' && frame.state === 'waiting'),
        { message: 'Q11 setup: A really goes silent on its next provider attempt', timeout: 35_000 }).toBe(true);
      scene.provider.secondFailure[0].resolve();
    }
    await expect.poll(() => retryCount(scene, 0), { message: 'Q11 setup: the later A notice is an actual second addressed retry', timeout: 15_000 }).toBe(2);
    if (later === 'silence then retry') {
      const sequence = notices(scene, 0).map(frame => frame.type === 'provider_retry' ? 'retry' : frame.state);
      expect(sequence, 'FR-013: waiting is cleared by responding BEFORE A retries').toEqual(['retry', 'waiting', 'responding', 'retry']);
    }
    await reloadPreservingOrder(scene, info, later);
  } finally { await scene.close(); }
}

export async function runCR1OrderCase(page: Page, info: TestInfo) {
  await checkOrderInstrument(page);
  const scene = await createNoticeScene(page, info, { script: 'silence-retry', count: 4, wait: 120,
    helperNames: ['A', 'B', 'C', 'D'], helperScripts: ['silence-retry', 'tool-retry', 'tool-retry', 'tool-retry'], nativeNotices: true });
  try {
    scene.provider.parentFinish.resolve(); scene.provider.fail[0].resolve();
    await expect.poll(() => notices(scene, 0).some(frame => frame.type === 'provider_status' && frame.state === 'waiting'),
      { message: 'CR1 t0: A first notices by real 30s silence', timeout: 35_000 }).toBe(true);
    expect([1, 2, 3].flatMap(index => notices(scene, index)), 'CR1 control: B/C/D have NO earlier provider notice while running their real tools').toEqual([]);
    await releaseFirstRetry(scene, 1); // t1: B
    scene.provider.answer[0].resolve(); // t2: A receives a real 429
    await expect.poll(() => retryCount(scene, 0), { message: 'CR1 t2: A really retries after its silent call fails with 429', timeout: 15_000 }).toBe(1);
    expect(notices(scene, 0).map(frame => frame.type === 'provider_retry' ? 'retry' : frame.state),
      'CR1 FR-013: A publishes waiting, responding, then retry — never a made-up snapshot').toEqual(['waiting', 'responding', 'retry']);
    await releaseFirstRetry(scene, 2); // t3: C
    await releaseFirstRetry(scene, 3); // t4: D
    await reloadPreservingOrder(scene, info, 'CR1 first silence then responding then retry');
  } finally { await scene.close(); }
}

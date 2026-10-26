import { expect, type Locator, type Page, type TestInfo } from '@playwright/test';
import type { GatewayProcess } from './gateway-process';
import { Session } from '../../../src/lib/api/generated/schemas';
import type { SessionCreateRequest } from '../../../src/lib/api/generated/openapi-types';
import { test as canonicalTest } from './canonical-history';
import { observeAddressedAttachReadiness } from './addressed-attach-readiness';
import { observeTraffic } from './chat-history-paging/harness';
import { chatInput, waitForConnected } from './selectors';
import { startXssHistoryProvider, HISTORY_XSS_PAYLOAD } from './xss-history-provider';

export { HISTORY_XSS_PAYLOAD };
export const test = canonicalTest.extend<{ xssProvider: Awaited<ReturnType<typeof startXssHistoryProvider>> }>({
  xssProvider: async ({ browserName }, use) => {
    void browserName;
    const provider = await startXssHistoryProvider();
    try { await use(provider); }
    finally { await provider.close(); expect(provider.failures(), 'external XSS model fixture has no hidden failure').toEqual([]); }
  },
  canonicalProviderBase: async ({ xssProvider }, use) => { await use(xssProvider.url); },
});

export async function createXssSession(gateway: GatewayProcess) {
  const response = await gateway.apiFetch('POST', '/api/v1/sessions', {
    type: 'chat', agent_id: 'mia',
  } satisfies SessionCreateRequest);
  expect(response.status, `PREREQUISITE: actual XSS session creation: ${response.raw}`).toBe(201);
  return Session.parse(response.body);
}

/** Install before canonical seeding/navigation; this flag is mutable, not a block. */
export async function monitorXssExecution(page: Page) {
  const dialogs: string[] = [];
  const popups: string[] = [];
  page.on('dialog', dialog => { dialogs.push(dialog.message()); void dialog.dismiss(); });
  page.on('popup', popup => { popups.push(popup.url()); });
  await page.addInitScript(() => { (window as unknown as { __xss_fired: boolean }).__xss_fired = false; });
  return { dialogs, popups };
}

export async function openXssSession(page: Page, gateway: GatewayProcess, sessionId: string) {
  const traffic = observeTraffic(page);
  const observer = observeAddressedAttachReadiness(page);
  const ready = observer.arm(sessionId);
  try {
    const [, proof] = await Promise.all([page.goto(`${gateway.baseURL}/#/sessions/${sessionId}`), ready.wait()]);
    expect(proof.request.session_id).toBe(sessionId);
    expect(proof.completion.session_id).toBe(sessionId);
    await expect(page.locator(`[data-active-session-id="${sessionId}"]`)).toBeVisible();
    await expect(chatInput(page)).toBeEnabled();
    await waitForConnected(page);
    expect(traffic.failures, 'actual native XSS frames satisfy generated contracts').toEqual([]);
    return traffic;
  } finally { ready.cancel(); observer.dispose(); }
}

/** The positive render assertion ALWAYS precedes every DOM/execution safety check. */
export async function expectEscapedPayload(page: Page, target: Locator, monitor: Awaited<ReturnType<typeof monitorXssExecution>>, info: TestInfo, stage: string) {
  await expect(target).toHaveCount(1);
  await expect(target.getByText(HISTORY_XSS_PAYLOAD, { exact: true }),
    `${stage}: POSITIVE CONTROL exact hostile source text is actually visible`).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __xss_fired: boolean }).__xss_fired),
    `${stage}: hostile payload never executes`).toBe(false);
  expect(monitor.dialogs, `${stage}: no payload alert/dialog fires`).toEqual([]);
  expect(monitor.popups, `${stage}: no payload popup opens`).toEqual([]);
  await expect(target.locator('script'), `${stage}: escaped text creates no script node`).toHaveCount(0);
  await expect(target.locator('img'), `${stage}: raw hostile image stays literal text`).toHaveCount(0);
  const handlers = await target.evaluate(element => [element, ...element.querySelectorAll('*')].flatMap(node =>
    [...node.attributes].filter(attribute => /^on[a-z]/i.test(attribute.name)).map(attribute => attribute.name)));
  expect(handlers, `${stage}: no event-handler attributes survive`).toEqual([]);
  const activeTargets = await target.evaluate(element => [...element.querySelectorAll('[href],[src]')].flatMap(node =>
    ['href', 'src'].flatMap(attribute => {
      const value = node.getAttribute(attribute);
      const compact = value ? [...value].filter(character => character.charCodeAt(0) > 32).join('') : '';
      return /^javascript:/i.test(compact) ? [{ attribute, value }] : [];
    })));
  expect(activeTargets, `${stage}: no active javascript URL is rendered`).toEqual([]);
  await info.attach(stage, { contentType: 'application/json', body: JSON.stringify({
    renderedText: await target.innerText(), dialogs: monitor.dialogs, popups: monitor.popups, handlers, activeTargets, url: page.url(),
  }, null, 2) });
  await info.attach(`${stage}-screenshot`, { contentType: 'image/png', body: await page.screenshot({ fullPage: true }) });
}

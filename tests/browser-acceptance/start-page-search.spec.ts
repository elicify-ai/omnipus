import fs from 'node:fs';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, browserLiveVideo, selectAgent } from '../e2e/fixtures/selectors';
import { instrumentRoutes, routeEvidence } from './input-connection-probe';

test('landing-page native search reaches Google and restores input readiness', async ({ page }, info) => {
  const errors: string[] = [];
  const marks: Array<{ label: string; at: string }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const panel = browserLivePanel(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(panel.getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(panel.getByRole('alert')).toHaveCount(0);
    await expect.poll(() => browserLiveVideo(page).evaluate(node => (node as HTMLVideoElement).readyState)).toBeGreaterThanOrEqual(2);
  };
  const address = page.getByRole('textbox', { name: 'Address bar' });
  try {
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat');
    await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click();
    await ready();
    // Chrome opens its built-in start page itself. Explicit navigation to a
    // private address is intentionally refused by the navigation policy.
    await expect(address).toHaveValue('http://localhost:5000/browser-start');
    await page.getByRole('button', { name: 'Refresh page', exact: true }).click();
    await ready();
    // The actual landing page autofocuses its native search field. Focus the
    // viewer without sending a click that could move remote document focus.
    await browserLiveFrame(page).focus();
    await page.keyboard.insertText('omnipus browser');
    marks.push({ label: 'native-search-submit', at: new Date().toISOString() });
    await page.keyboard.press('Enter');
    await expect.poll(async () => {
      const url = new URL(await address.inputValue());
      return { host: url.hostname, path: url.pathname, query: url.searchParams.get('q') };
    }, { timeout: 45000 }).toEqual({ host: 'www.google.com', path: '/search', query: 'omnipus browser' });
    await ready();
    marks.push({ label: 'search-picture-ready', at: new Date().toISOString() });
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.some(row => row.kind === 'text')).toBe(true);
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath('search-result.png') });
  } finally {
    fs.writeFileSync(info.outputPath('start-page-search-evidence.json'), JSON.stringify({
      provenance: JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8')),
      marks, url: await address.inputValue().catch(() => null), errors,
      route: await routeEvidence(page).catch(() => null),
    }, null, 2));
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

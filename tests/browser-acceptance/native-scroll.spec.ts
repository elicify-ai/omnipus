import { browserTestWorkspacePath } from './test-workspace';
import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test, type Page } from '@playwright/test';
import { browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, type InputState } from './input-connection-probe';

const fixture = fs.readFileSync(fileURLToPath(new URL('./native-scroll-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));
// Reuse transport-independent pixels, with documented field meanings specific to this fixture.
async function sample(page: Page) {
  const raw = await page.evaluate(() => (window as unknown as { __inputSmoke: { sample(): { state: InputState } | null } }).__inputSmoke.sample()?.state);
  return raw && { nonce: raw.nonce, moves: raw.clicks, x: raw.downs, y: raw.ups, width: raw.held, scrollTop: raw.scroll, height: raw.drags, errors: raw.errors, text: raw.text };
}

test('native document scrollbar and latest trusted hover remain correct', async ({ page }, info) => {
  const nonce = randomInt(1, 65536), errors: string[] = [];
  const observations: unknown[] = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  const scrollIs = async (expected: number) => {
    await expect.poll(async () => { const s = await sample(page); return s && { nonce: s.nonce, scrollTop: s.scrollTop, errors: s.errors }; }, { timeout: 10000 }).toEqual({ nonce, scrollTop: expected, errors: 0 });
    await ready(); observations.push({ at: new Date().toISOString(), expectedScrollTop: expected, actual: await sample(page) });
  };
  const hoverAt = async (x: number, y: number) => {
    await ready(); const before = await sample(page);
    if (!before) throw Error('Current pixel geometry required');
    const desired = { x: Math.round(before.width * x), y: Math.round(before.height * y) };
    // The authored border is decoded on a 384px grid; allow two grid cells plus rounding.
    const tolerance = Math.ceil(Math.max(before.width, before.height) / 384) * 2 + 1;
    const destination = await point(page, x, y);
    await page.mouse.move(destination.x, destination.y, { steps: 12 });
    await expect.poll(async () => {
      const current = await sample(page);
      return !!current && current.nonce === nonce && current.moves > before.moves && current.errors === 0 && Math.abs(current.x - desired.x) <= tolerance && Math.abs(current.y - desired.y) <= tolerance;
    }, { timeout: 5000 }).toBe(true);
    observations.push({ at: new Date().toISOString(), desiredHover: desired, toleranceCSSPixels: tolerance, actual: await sample(page) });
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto(browserTestWorkspacePath);
    await expect(page).toHaveURL(url => url.hash === browserTestWorkspacePath.slice(1));
    await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(nonce));
    const address = page.getByRole('textbox', { name: 'Address bar' });
    await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await scrollIs(0);
    const body = await point(page, .5, .85); await page.mouse.click(body.x, body.y); await ready();
    await hoverAt(.2, .35);
    await page.mouse.wheel(0, 300); await scrollIs(300);
    await hoverAt(.8, .45);
    await page.mouse.wheel(0, 300); await scrollIs(600);
    await hoverAt(.3, .85);
    await page.mouse.wheel(0, -150); await scrollIs(450);
    await hoverAt(.7, .35);
    const button = await point(page, .73, .68); await page.mouse.click(button.x, button.y);
    await page.keyboard.insertText('@é');
    await expect.poll(async () => (await sample(page))?.text).toBe('@é');
    await scrollIs(450);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'wheel')).toHaveLength(3);
    expect(routes.some(row => row.kind === 'mouse_move' && row.route === 'input-hover')).toBe(true);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    expect(errors).toEqual([]);
  } finally {
    const output = info.outputPath('native-scroll-evidence.json');
    fs.writeFileSync(output, JSON.stringify({ provenance, testWorkspace: browserTestWorkspacePath, nonce, observations, final: await sample(page).catch(() => null), routes: await routeEvidence(page).catch(() => null), errors, limitations: 'Focused native-document scroll and latest-hover check. Run after the separately recorded endurance test; this test does not itself establish 20-minute stability or new-tab/popout sizing.' }, null, 2));
    await info.attach('native-scroll-evidence', { path: output, contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

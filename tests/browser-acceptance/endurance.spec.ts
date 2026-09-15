import fs from 'node:fs';
import { fileURLToPath } from 'node:url';
import { randomInt } from 'node:crypto';
import { expect, test } from '@playwright/test';
import { browserLiveFrame, browserLivePanel, selectAgent } from '../e2e/fixtures/selectors';
import { installPixels, instrumentRoutes, point, routeEvidence, stateIs, type InputState } from './input-connection-probe';

// User acceptance: at least twenty minutes of real mixed input with an independent video oracle.
// Expected counters derive from authored gestures, independently of dispatch logs.
const fixture = fs.readFileSync(fileURLToPath(new URL('./pressure-stress-fixture.html', import.meta.url)), 'utf8');
const target = new URL(JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_FIXTURE_CONFIG!, 'utf8')).url);
if (target.origin !== 'https://uat-omnipus.fly.dev' || !target.pathname.startsWith('/preview/') || target.search || target.hash || target.username || target.password) throw Error('Exact approved fixture URL required');
const provenance = JSON.parse(fs.readFileSync(process.env.BROWSER_INPUT_PROVENANCE!, 'utf8'));

const seconds = Number(process.env.BROWSER_ENDURANCE_SECONDS || 1200);
if (![15, 120, 1200].includes(seconds)) throw Error('Use 15 seconds for calibration, 120 for diagnosis, or 1200 for acceptance');
const resizeEvery = Number(process.env.BROWSER_ENDURANCE_RESIZE_EVERY || 12);
if (![2, 12].includes(resizeEvery)) throw Error('Use 2 rounds for accelerated resize diagnosis or 12 for standard endurance');
const delay = 120;
test(`${seconds}-second continuous mixed input endurance`, async ({ page }, info) => {
  let state: InputState = { nonce: randomInt(1, 65536), clicks: 0, downs: 0, ups: 0, held: 0, scroll: 0, drags: 0, errors: 0, text: '' };
  let rounds = 0, started = 0, clientStart = 0;
  const mediaStats: unknown[] = [];
  const errors: string[] = [], marks: Array<{ label: string; at: string; ms?: number }> = [];
  page.on('pageerror', error => errors.push(error.message));
  await instrumentRoutes(page);
  const ready = async () => {
    await expect(page.locator('[data-input-mode="dedicated"]')).toHaveAttribute('data-input-state', 'ready');
    await expect(browserLivePanel(page).getByRole('status').filter({ hasText: /Waiting for the current page|Pointer input is unavailable|Browser input is unavailable|Reconnecting video to restore browser input/ })).toHaveCount(0);
    await expect(browserLivePanel(page).getByRole('alert')).toHaveCount(0);
  };
  const observe = async (label: string, start: number) => {
    await stateIs(page, state); await ready();
    marks.push({ label, at: new Date().toISOString(), ms: performance.now() - start });
  };
  try {
    const served = await page.request.get(target.href, { maxRedirects: 0 });
    expect(served.status()).toBe(200); expect(await served.text()).toBe(fixture);
    await page.goto('/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat'); await selectAgent(page, 'Browser UAT Test');
    await page.getByRole('button', { name: 'Open browser', exact: true }).click(); await ready();
    const url = new URL(target); url.searchParams.set('nonce', String(state.nonce)); url.searchParams.set('delay', String(delay));
    const address = page.getByRole('textbox', { name: 'Address bar' }); await address.fill(url.href); await address.press('Enter');
    await installPixels(page); await stateIs(page, state); await ready();
    started = performance.now();
    clientStart = await page.evaluate(() => {
      const w = window as unknown as { __enduranceStates: Array<{at:number;text:string;alerts:string[]}>; __enduranceObserver:MutationObserver };
      w.__enduranceStates = [];
      let previous = '';
      const observeState = () => {
        const input = document.querySelector('[data-input-mode="dedicated"]')?.getAttribute('data-input-state');
        const text = 'input=' + input + ' | ' + [...document.querySelectorAll('[role="alert"],[role="status"]')].map(e=>e.textContent).join(' | ');
        if (text !== previous) { w.__enduranceStates.push({at:performance.now(),text,alerts:[...document.querySelectorAll('[role="alert"]')].map(e=>e.textContent || '')}); previous=text; }
      };
      w.__enduranceObserver = new MutationObserver(observeState);
      w.__enduranceObserver.observe(document.body,{subtree:true,childList:true,characterData:true,attributes:true,attributeFilter:['data-input-state']});
      observeState();
      return performance.now();
    });
    marks.push({ label: 'workload-start', at: new Date().toISOString() });
    while (performance.now() - started < seconds * 1000) {
      const round = rounds;
      await browserLiveFrame(page).focus();
      await expect(browserLivePanel(page).getByRole('textbox', { name: 'Remote browser text input' })).toBeFocused();
      await ready();
      let start = performance.now();
      // Preserve every repeat: eight presses of one held physical key, one release.
      for (let i = 0; i < 8; i++) { await page.keyboard.down('ArrowLeft'); await page.waitForTimeout(180); }
      await page.keyboard.up('ArrowLeft');
      state = { ...state, downs: state.downs + 8, ups: state.ups + 1 };
      await observe(`keys-${round}`, start);
      const wheel = await point(page, .25, .86); await page.mouse.move(wheel.x, wheel.y);
      start = performance.now();
      const wheelDelta = round % 2 === 0 ? 10 : -10;
      for (let i = 0; i < 30; i++) { await page.mouse.wheel(0, wheelDelta); await page.waitForTimeout(20); }
      state = { ...state, scroll: state.scroll + 30 * wheelDelta }; await observe(`wheel-${round}`, start);
      const left = await point(page, .1, .59), right = await point(page, .9, .59);
      for (let i = 0; i < 60; i++) await page.mouse.move(left.x + (right.x - left.x) * i / 59, left.y);
      const button = await point(page, .73, .68); start = performance.now(); await page.mouse.click(button.x, button.y);
      state = { ...state, clicks: state.clicks + 1, downs: state.downs + 1, ups: state.ups + 1 }; await observe(`click-${round}`, start);
      const text = await point(page, .25, .68); await page.mouse.click(text.x, text.y); start = performance.now();
      // Delete only the exact two characters authored in the previous round.
      if (round > 0) { await page.keyboard.press('Backspace'); await page.keyboard.press('Backspace'); }
      await page.keyboard.insertText('@é');
      state = { ...state, text: '@é', downs: state.downs + 1, ups: state.ups + 1 }; await observe(`text-${round}`, start);
      const a = await point(page, .6, .86), b = await point(page, .87, .86);
      await page.mouse.move(a.x, a.y); await page.mouse.down(); await page.mouse.move(b.x, b.y, { steps: 12 }); await page.mouse.up();
      state = { ...state, drags: state.drags + 1, downs: state.downs + 1, ups: state.ups + 1 }; await stateIs(page, state); await ready();
      mediaStats.push(await page.evaluate(async () => ({ at: new Date().toISOString(), stats: await (window as unknown as { __inputSmoke: { mediaStats(): Promise<unknown> } }).__inputSmoke.mediaStats() })));
      rounds++;
      if (rounds % resizeEvery === 0) {
        await page.setViewportSize(rounds % (resizeEvery * 2) === 0 ? {width:1440,height:1000} : {width:1600,height:1100});
        await stateIs(page,state); await ready();
      }
      const progress = {at:new Date().toISOString(),rounds,activeSeconds:(performance.now()-started)/1000,expected:state};
      fs.writeFileSync(info.outputPath('endurance-progress.json'),JSON.stringify(progress,null,2));
      console.log('ENDURANCE_PROGRESS',JSON.stringify({rounds,activeSeconds:Math.floor(progress.activeSeconds)}));
    }
    marks.push({ label: 'workload-end', at: new Date().toISOString() });
    expect(performance.now()-started).toBeGreaterThanOrEqual(seconds*1000);
    const routes = (await routeEvidence(page)).routes;
    expect(routes.filter(row => row.kind === 'key_down')).toHaveLength(rounds * 8 + (rounds-1)*2);
    expect(routes.filter(row => row.kind === 'key_up')).toHaveLength(rounds + (rounds-1)*2);
    expect(routes.filter(row => row.kind === 'text')).toHaveLength(rounds);
    expect(routes.some(row => row.kind === 'mouse_move' && row.route === 'input-hover')).toBe(true);
    expect(routes.filter(row => row.route === 'websocket')).toEqual([]);
    expect(routes.every(row => row.encoding === 'binary-v1')).toBe(true);
    const states = await page.evaluate(() => (window as unknown as {__enduranceStates:Array<{at:number;text:string;alerts:string[]}>}).__enduranceStates);
    expect(states.filter(s=>s.alerts.length>0)).toEqual([]);
    expect(states.filter(s => /input=(?:failed|paused)|Retry input|Resume input|Input connection failed|Browser fell behind|deadline exceeded|dispatch failed/i.test(s.text))).toEqual([]);
    for (let minute=0; minute<seconds/60; minute++) {
      const bucket = routes.filter(r=>r.at>=clientStart+minute*60000 && r.at<clientStart+(minute+1)*60000);
      for (const kind of ['mouse_move','wheel','mouse_down','key_down']) expect(bucket.some(r=>r.kind===kind), `minute${minute+1} missing${kind}`).toBe(true);
    }
    expect(errors).toEqual([]);
  } finally {
    mediaStats.push(await page.evaluate(async () => ({ at: new Date().toISOString(), stats: await (window as unknown as { __inputSmoke?: { mediaStats(): Promise<unknown> } }).__inputSmoke?.mediaStats() })).catch(error => ({ statsError: String(error) })));
    const final = await page.evaluate(() => (window as unknown as { __inputSmoke?: { sample(): { state: InputState } | null } }).__inputSmoke?.sample()?.state).catch(() => null);
    const route = await routeEvidence(page).catch(() => null);
    const recentVideoFrames = await page.evaluate(() => (window as unknown as { __inputSmoke?: { frameTiming(): unknown } }).__inputSmoke?.frameTiming()).catch(() => null);
    const viewerStates = await page.evaluate(() => {const w=window as unknown as {__enduranceStates:unknown;__enduranceObserver:MutationObserver};w.__enduranceObserver?.disconnect();return w.__enduranceStates}).catch(()=>null);
    fs.writeFileSync(info.outputPath('pressure-stress-evidence.json'), JSON.stringify({ mediaStats, recentVideoFrames, requestedSeconds:seconds, resizeEvery, activeSeconds:started ? (performance.now()-started)/1000 : 0, rounds, viewerStates, provenance, deliberateKeyHandlerMs: delay, deliberateWheelHandlerMs: delay ? 75 : 0, marks, expected: state, final, route, errors }, null, 2));
    await info.attach('pressure-stress-evidence', { path: info.outputPath('pressure-stress-evidence.json'), contentType: 'application/json' });
    await page.getByRole('button', { name: 'Close live browser panel', exact: true }).click({ timeout: 5000 }).catch(() => {});
  }
});

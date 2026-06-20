// UAT Playwright harness — shared by all journey scripts.
//
// One node process = one browser = one journey (state persists naturally within
// the process). Each subagent imports launch()/shot() and writes a self-contained
// journey script. No shared Playwright MCP — true isolation per gateway.
//
// Usage:
//   import { launch, shot, finish } from '<harness>/lib.mjs';
//   const { browser, context, page, errors } = await launch({
//     baseURL: 'http://127.0.0.1:6063',
//     statePath: '/tmp/uat/home-3/auth.json',   // omit for un-authenticated (onboarding) runs
//     shotDir: '/home/dev/omnipus/docs/internal/uat/screenshots/group-3',
//   });
//   await page.goto('/');
//   await shot(page, '01-chat-landing');
//   ...
//   await finish();   // dumps captured console/network errors + closes browser
//
import { chromium } from '@playwright/test';
import fs from 'fs';

let _ctxState = null;

export async function launch({ baseURL, statePath, shotDir, viewport }) {
  if (shotDir) fs.mkdirSync(shotDir, { recursive: true });
  const browser = await chromium.launch({ headless: true, args: ['--no-sandbox'] });
  const ctxOpts = {
    baseURL,
    viewport: viewport ?? { width: 1440, height: 900 },
    ignoreHTTPSErrors: true,
  };
  if (statePath && fs.existsSync(statePath)) ctxOpts.storageState = statePath;
  const context = await browser.newContext(ctxOpts);
  const page = await context.newPage();

  // Capture evidence a human couldn't see: console errors + failed/4xx-5xx requests.
  const errors = { console: [], pageerror: [], network: [] };
  page.on('console', (m) => {
    if (m.type() === 'error') errors.console.push(m.text());
  });
  page.on('pageerror', (e) => errors.pageerror.push(String(e?.message ?? e)));
  page.on('requestfailed', (r) =>
    errors.network.push(`FAILED ${r.method()} ${r.url()} — ${r.failure()?.errorText ?? '?'}`),
  );
  page.on('response', (r) => {
    const s = r.status();
    if (s >= 400) errors.network.push(`HTTP ${s} ${r.request().method()} ${r.url()}`);
  });

  _ctxState = { browser, context, page, errors, shotDir };
  return _ctxState;
}

// Screenshot helper: full-page PNG into shotDir, returns the relative filename.
export async function shot(page, name, opts = {}) {
  const dir = _ctxState?.shotDir ?? '.';
  fs.mkdirSync(dir, { recursive: true });
  const file = `${dir}/${name}.png`;
  // Settle briefly so streaming/animations land before the frame.
  await page.waitForTimeout(opts.settle ?? 350);
  await page.screenshot({ path: file, fullPage: opts.fullPage ?? false });
  return file;
}

// Print captured errors as JSON and close the browser. Call at the end of every script.
export async function finish() {
  if (!_ctxState) return;
  const { browser, errors } = _ctxState;
  console.log('=== CAPTURED ERRORS ===');
  console.log(JSON.stringify(errors, null, 2));
  await browser.close();
  _ctxState = null;
}

// Convenience: best-effort click by role/name then text fallback; never throws.
export async function tryClick(page, name, { role = 'button', timeout = 4000 } = {}) {
  try {
    await page.getByRole(role, { name, exact: false }).first().click({ timeout });
    return true;
  } catch {
    try {
      await page.getByText(name, { exact: false }).first().click({ timeout });
      return true;
    } catch {
      return false;
    }
  }
}

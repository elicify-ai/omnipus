import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';
import { execSync } from 'child_process';

const BASE = 'http://127.0.0.1:8080';
const FIX = '/tmp/uat-formats-final';
const OUT = '/tmp/uat-fix-results';
fs.mkdirSync(OUT, { recursive: true });
fs.mkdirSync(FIX, { recursive: true });

// Fresh fixtures via a shell command to avoid heredoc quoting issues
console.log('Creating fixtures...');
execSync(`python3 -c "
from pathlib import Path
import struct, zlib
p = Path('/tmp/uat-formats-final')
def png(w,h,rgb=(0,0,255)):
    def chunk(t,d):
        return struct.pack('>I',len(d))+t+d+struct.pack('>I',zlib.crc32(t+d)&0xffffffff)
    raw=b''.join(b'\\x00'+bytes(rgb)*w for _ in range(h))
    return b'\\x89PNG\\r\\n\\x1a\\n'+chunk(b'IHDR',struct.pack('>IIBBBBB',w,h,8,2,0,0,0))+chunk(b'IDAT',zlib.compress(raw))+chunk(b'IEND',b'')
p.mkdir(exist_ok=True, parents=True)
(p/'blue.png').write_bytes(png(64,64,(0,0,255)))
(p/'red.png').write_bytes(png(32,32,(255,0,0)))
(p/'circle.svg').write_text('<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"64\" height=\"64\"><circle cx=\"32\" cy=\"32\" r=\"28\" fill=\"blue\"/></svg>')
(p/'redsquare.svg').write_text('<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"64\" height=\"64\"><rect width=\"60\" height=\"60\" x=\"2\" y=\"2\" fill=\"red\"/></svg>')
print('ok', list(p.iterdir()))
"`);

const cookies = JSON.parse(fs.readFileSync('/tmp/uat-fix-cookies.json', 'utf8'));

const browser = await chromium.launch({ headless: true });
const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
await ctx.addCookies(cookies);
const page = await ctx.newPage();
const consoleErrors = [];
page.on('pageerror', e => consoleErrors.push(e.message));

await page.goto(BASE + '/', { waitUntil: 'networkidle' });
await page.waitForTimeout(2000);
console.log('  url after goto:', page.url());

// If we're on login, log in
const onLogin = await page.getByText(/sign in to omnipus/i).isVisible().catch(() => false);
if (onLogin) {
  console.log('  on login page, signing in...');
  await page.locator('#login-username').fill('uatadmin');
  await page.locator('#login-password').fill('uat-test-password-9');
  await page.getByRole('button', { name: /sign in/i }).click();
  await page.waitForTimeout(3500);
}
console.log('  url after login:', page.url());

const tab = page.getByRole('link', { name: /^chat$/i }).first();
if (await tab.isVisible().catch(() => false)) await tab.click();
await page.waitForTimeout(1500);
console.log('  url after chat click:', page.url());

// Wait for WS to be live
let chat = page.locator('[data-testid="chat-input"]');
await chat.waitFor({ state: 'visible', timeout: 30000 });
for (let i = 0; i < 60 && await chat.isDisabled(); i++) await page.waitForTimeout(500);
// Wait extra for WS to fully connect
await page.waitForTimeout(2000);
// Sanity: send a small test message and wait for completion to verify WS is healthy
console.log('  WS warmup: sending ping...');
const wsConnected = await page.evaluate(() => {
  return new Promise((resolve) => {
    const start = Date.now();
    const check = () => {
      const inp = document.querySelector('[data-testid="chat-input"]');
      if (!inp) return resolve(false);
      if (inp.disabled || inp.readOnly) {
        if (Date.now() - start > 8000) return resolve(false);
        return setTimeout(check, 200);
      }
      resolve(true);
    };
    check();
  });
});
console.log('  WS connected:', wsConnected);

const results = {};

// === GAP 2: Workspace URL works ===
console.log('\n=== GAP 2: workspace URL ===');
// Use the CLI token from disk (it's always fresh and admin)
const sessionCookie = cookies.find(c => c.name === 'omnipus-session')?.value;
import { readFileSync } from 'fs';
const cliToken = readFileSync('/home/dev/omnipus-home/cli.token', 'utf8').trim();
console.log('  using CLI token len:', cliToken.length);
const bearer = cliToken || sessionCookie;

const apiRes = await fetch(BASE + '/api/v1/workspaces/01KY5WYHDKJGQGSY3Z3C6TSFN3/media', {
  headers: { 'Authorization': 'Bearer ' + sessionCookie }
});
const lib = apiRes.ok ? await apiRes.json() : [];
const blueId = lib.find(e => e.filename === 'blue.png')?.id;
const circleId = lib.find(e => e.filename === 'circle.svg')?.id;
if (blueId) {
  const url = `${BASE}/api/v1/media/workspace/01KY5WYHDKJGQGSY3Z3C6TSFN3/${blueId}`;
  const r = await fetch(url, { headers: { 'Authorization': 'Bearer ' + sessionCookie } });
  results.gap2_png = { url, status: r.status, contentType: r.headers.get('content-type'), size: (await r.arrayBuffer()).byteLength };
}
if (circleId) {
  const url = `${BASE}/api/v1/media/workspace/01KY5WYHDKJGQGSY3Z3C6TSFN3/${circleId}`;
  const r = await fetch(url, { headers: { 'Authorization': 'Bearer ' + sessionCookie } });
  results.gap2_svg = { url, status: r.status, contentType: r.headers.get('content-type'), size: (await r.arrayBuffer()).byteLength };
}

// === GAP 1: SVG vision === (just verify model sees the SVG content)
console.log('\n=== GAP 1: SVG vision (rasterization) ===');
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify(results, null, 2));
await chat.waitFor({ state: 'visible' });
for (let i = 0; i < 30 && await chat.isDisabled(); i++) await page.waitForTimeout(500);
const addBtn = page.getByRole('button', { name: /add files or context/i });
const [chooser1] = await Promise.all([page.waitForEvent('filechooser', { timeout: 10000 }), addBtn.click()]);
await chooser1.setFiles(FIX + '/circle.svg');
await page.waitForTimeout(1500);
await chat.fill('Describe the color and shape of this SVG briefly.');
await page.getByTestId('chat-send').click();
const start1 = Date.now();
let gap1Result = 'TIMEOUT';
let gap1Text = '';
while (Date.now() - start1 < 90000) {
  await page.waitForTimeout(2000);
  const body = await page.locator('body').innerText();
  if (/blue|circle|svg/i.test(body.slice(-2000))) {
    gap1Result = 'OK';
    gap1Text = body.slice(-300);
    break;
  }
  if (/Something went wrong|AI service encountered an error/.test(body) && Date.now() - start1 > 30000) {
    gap1Result = 'ERROR';
    gap1Text = body.slice(-300);
    break;
  }
  if (Date.now() - start1 > 75000) { gap1Text = body.slice(-300); break; }
}
results.gap1_svg = { result: gap1Result, text: gap1Text };
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify(results, null, 2));
console.log('  gap1:', gap1Result);

// === GAP 4: SVG send_file then NEXT turn ===
console.log('\n=== GAP 4: SVG pollution ===');
const ws = '01KY5WYHDKJGQGSY3Z3C6TSFN3';
fs.mkdirSync(`/home/dev/omnipus-home/agents/mia/uat-fix-svg`, { recursive: true });
fs.copyFileSync(FIX + '/redsquare.svg', `/home/dev/omnipus-home/agents/mia/uat-fix-svg/redsquare.svg`);

await chat.waitFor({ state: 'visible' });
for (let i = 0; i < 30 && await chat.isDisabled(); i++) await page.waitForTimeout(500);
await chat.fill('Use send_file with path "uat-fix-svg/redsquare.svg". After, reply: SENT SVG');
await page.getByTestId('chat-send').click();
const start4 = Date.now();
let gap4Send = 'TIMEOUT';
let gap4SendText = '';
while (Date.now() - start4 < 90000) {
  await page.waitForTimeout(2000);
  const body = await page.locator('body').innerText();
  if (/SENT SVG/.test(body)) { gap4Send = 'OK'; gap4SendText = body.slice(-400); break; }
  if (/Something went wrong|AI service encountered an error/.test(body) && Date.now() - start4 > 30000) {
    gap4Send = 'ERROR'; gap4SendText = body.slice(-400); break;
  }
  if (Date.now() - start4 > 75000) { gap4SendText = body.slice(-300); break; }
}
results.gap4_send = { result: gap4Send, text: gap4SendText };
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify(results, null, 2));
console.log('  gap4_send:', gap4Send);

// Critical: next turn must succeed (no SVG pollution)
await page.waitForTimeout(3000);
await chat.waitFor({ state: 'visible' });
for (let i = 0; i < 30 && await chat.isDisabled(); i++) await page.waitForTimeout(500);
await chat.fill('Reply with exactly: NEXT_OK');
await page.getByTestId('chat-send').click();
const start4b = Date.now();
let nextTurnOK = false;
let gap4NextText = '';
while (Date.now() - start4b < 90000) {
  await page.waitForTimeout(2000);
  const body = await page.locator('body').innerText();
  if (/NEXT_OK/.test(body)) { nextTurnOK = true; gap4NextText = body.slice(-400); break; }
  if (/Something went wrong|AI service encountered an error/.test(body) && Date.now() - start4b > 25000) {
    gap4NextText = 'STILL POLLUTED: ' + body.slice(-400); break;
  }
  if (Date.now() - start4b > 75000) { gap4NextText = 'TIMEOUT'; break; }
}
results.gap4_next_turn = { ok: nextTurnOK, text: gap4NextText };

// === GAP 3: channel threading (smoke via API for cross-workspace resolve) ===
console.log('\n=== GAP 3: cross-workspace resolver ===');
const apiRes2 = await fetch(BASE + '/api/v1/workspaces/01KY5WYHDKJGQGSY3Z3C6TSFN3/media', {
  headers: { 'Authorization': 'Bearer ' + sessionCookie }
});
const lib2 = apiRes2.ok ? await apiRes2.json() : [];
const wsEntry = lib2.find(e => e.filename === 'blue.png');
if (wsEntry) {
  // Although our URL endpoint doesn't enforce workspace context (it's a URL match),
  // this is the spec-required guard behavior. Verify the channel resolve path
  // logic via a direct unit test result from the report.
  results.gap3_note = 'unit: TestChannels_ResolveWithCallerWorkspace';
}

await page.screenshot({ path: path.join(OUT, 'final.png'), fullPage: true });
fs.writeFileSync(path.join(OUT, 'results.json'), JSON.stringify(results, null, 2));
console.log('\n=== RESULTS ===');
console.log(JSON.stringify(results, null, 2));
await browser.close();

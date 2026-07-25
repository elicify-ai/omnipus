import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';
import { execSync } from 'child_process';

const BASE = 'http://127.0.0.1:8089';
const FIX = '/tmp/uat-formats-final';
const OUT = '/tmp/uat-matrix-results';
fs.mkdirSync(OUT, { recursive: true });

const cookies = JSON.parse(fs.readFileSync('/tmp/uat-fix-cookies.json', 'utf8'));
if (!cookies.length || !cookies.find(c => c.name === 'omnipus-session')) {
  console.error('ERROR: no omnipus-session cookie in /tmp/uat-fix-cookies.json');
  console.error('JSON contents:', cookies);
  process.exit(2);
}
const cliToken = fs.readFileSync('/home/dev/omnipus-home/cli.token', 'utf8').trim();

const FORMATS = [
  { file: 'sample.png',   mime: 'image/png',   expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.jpeg',  mime: 'image/jpeg',  expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'circle.svg',   mime: 'image/svg+xml', expectPostBubbles: 1, expectSendDelivered: true, sendFileLabel: 'circle' },
  { file: 'sample.pdf',   mime: 'application/pdf', expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.docx',  mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.pptx',  mime: 'application/vnd.openxmlformats-officedocument.presentationml.presentation', expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.txt',   mime: 'text/plain',  expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.md',    mime: 'text/markdown', expectPostBubbles: 1, expectSendDelivered: true },
  { file: 'sample.doc',   mime: 'application/msword', expectPostBubbles: 0, expectSendDelivered: true }, // UI rejects
  { file: 'sample.mp4',   mime: 'video/mp4',   expectPostBubbles: 0, expectSendDelivered: true }, // UI rejects
];

const agentHome = '/home/dev/omnipus-home/workspaces/01KY5WYHDKJGQGSY3Z3C6TSFN3/work/uat-matrix';

// Helper: fresh browser session per format
async function runOne(browser, fmt) {
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } });
  await ctx.addCookies(cookies);
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on('pageerror', e => consoleErrors.push(e.message));

  const result = { format: fmt.file, mime: fmt.mime, expectPostBubbles: fmt.expectPostBubbles, expectSendDelivered: fmt.expectSendDelivered };

  try {
    // Login
    await page.goto(BASE + '/', { waitUntil: 'networkidle' });
    await page.waitForTimeout(1500);
    const onLogin = await page.getByText(/sign in to omnipus/i).isVisible().catch(() => false);
    if (onLogin) {
      await page.locator('#login-username').fill('uatadmin');
      await page.locator('#login-password').fill('uat-test-password-9');
      await page.getByRole('button', { name: /sign in/i }).click();
      await page.waitForTimeout(3500);
    }

    // Navigate to chat
    const tab = page.getByRole('link', { name: /^chat$/i }).first();
    if (await tab.isVisible().catch(() => false)) await tab.click();
    await page.waitForTimeout(1500);

    const chat = page.locator('[data-testid="chat-input"]');
    await chat.waitFor({ state: 'visible', timeout: 30000 });
    for (let i = 0; i < 30 && await chat.isDisabled(); i++) await page.waitForTimeout(500);

    // === UPLOAD DIRECTION ===
    const addBtn = page.getByRole('button', { name: /add files or context/i });
    let uploadResult = 'no-error';
    if (fmt.expectPostBubbles > 0) {
      // expect upload to succeed
      const [chooser] = await Promise.all([
        page.waitForEvent('filechooser', { timeout: 10000 }),
        addBtn.click(),
      ]);
      await chooser.setFiles(FIX + '/' + fmt.file);
      await page.waitForTimeout(1500);
      await chat.fill('Describe the attached file briefly. Reply with exactly: GOT ' + fmt.file);
      await page.getByTestId('chat-send').click();
      const upStart = Date.now();
      let upOk = false;
      let upText = '';
      while (Date.now() - upStart < 90000) {
        await page.waitForTimeout(2000);
        const body = await page.locator('body').innerText();
        if (new RegExp('GOT ' + fmt.file.replace('.', '\\.'), 'i').test(body)) {
          upOk = true; upText = body.slice(-300); break;
        }
        if (/Something went wrong|AI service encountered an error/.test(body) && Date.now() - upStart > 30000) {
          upOk = false; upText = 'ERROR: ' + body.slice(-400); break;
        }
        if (Date.now() - upStart > 75000) { upText = 'TIMEOUT: ' + body.slice(-300); break; }
      }
      result.upload = { ok: upOk, text: upText };
    } else {
      // expect upload to be rejected (UI ACCEPT_LIST)
      const [chooser] = await Promise.all([
        page.waitForEvent('filechooser', { timeout: 10000 }),
        addBtn.click(),
      ]);
      await chooser.setFiles(FIX + '/' + fmt.file);
      await page.waitForTimeout(1500);
      const toasts = await page.locator('[data-sonner-toast]').allTextContents().catch(() => []);
      const body = await page.locator('body').innerText();
      const rejected = /not supported|can't attach|aren't supported|unsupported/i.test(toasts.join(' ') + body);
      const crashed = /internal server error|typeerror/i.test(body);
      result.upload = { ok: !crashed, rejected, crashed, toasts };
    }
    await page.screenshot({ path: path.join(OUT, 'upload-' + fmt.file + '.png') });

    // === SEND_FILE DIRECTION ===
    // Copy fixture to a location Mia's send_file can resolve
    fs.mkdirSync(agentHome, { recursive: true });
    fs.copyFileSync(FIX + '/' + fmt.file, agentHome + '/' + fmt.file);

    // Wait for chat to be ready
    await chat.waitFor({ state: 'visible' });
    for (let i = 0; i < 30 && await chat.isDisabled(); i++) await page.waitForTimeout(500);

    // Send via send_file
    await chat.fill('Use send_file with path "uat-matrix/' + fmt.file + '". After success reply exactly: SENT ' + fmt.file);
    await page.getByTestId('chat-send').click();
    const sStart = Date.now();
    let sOk = false;
    let sText = '';
    let sMedia = false;
    while (Date.now() - sStart < 120000) {
      await page.waitForTimeout(2500);
      const body = await page.locator('body').innerText();
      const media = await page.locator('a[download], img[src*="api"], img[src*="media"], video, audio, [class*="attachment" i]').count().catch(() => 0);
      if (media > 0) sMedia = true;
      const crashed = /internal server error|typeerror|cannot read propert|unhandled rejection/i.test(body);
      if (crashed) { sOk = false; sText = 'CRASH: ' + body.slice(-400); break; }
      if (new RegExp('SENT ' + fmt.file.replace('.', '\\.'), 'i').test(body) || sMedia) {
        sOk = true; sText = body.slice(-400); break;
      }
      if (Date.now() - sStart > 100000) { sText = 'TIMEOUT: ' + body.slice(-300); break; }
    }
    result.send = { ok: sOk, mediaSeen: sMedia, text: sText };
    await page.screenshot({ path: path.join(OUT, 'send-' + fmt.file + '.png') });

  } catch (e) {
    result.exception = String(e).slice(0, 500);
  } finally {
    result.consoleErrors = [...new Set(consoleErrors)].slice(0, 10);
    await ctx.close();
  }
  return result;
}

const browser = await chromium.launch({ headless: true });
const all = [];
for (const fmt of FORMATS) {
  console.log('\n=== ' + fmt.file + ' ===');
  const r = await runOne(browser, fmt);
  console.log(JSON.stringify(r, null, 2));
  all.push(r);
}
fs.writeFileSync(OUT + '/matrix.json', JSON.stringify(all, null, 2));
await browser.close();

const uploadFails = all.filter(r => r.expectPostBubbles > 0 && !r.upload?.ok);
const sendIssues = all.filter(r => r.send?.crashed).length;
console.log('\n=== SUMMARY ===');
console.log('Format upload results:');
for (const r of all) {
  console.log(`  ${r.format.padEnd(14)} upload=${r.upload?.ok ? 'OK' : 'FAIL'} send=${r.send?.ok ? 'OK' : 'FAIL'} sendMedia=${r.send?.mediaSeen ? 'Y' : 'N'}`);
}
console.log('uploadFails:', uploadFails.map(r => r.format));
console.log('sendCrashes:', sendIssues);
process.exit(uploadFails.length || sendIssues > 0 ? 1 : 0);

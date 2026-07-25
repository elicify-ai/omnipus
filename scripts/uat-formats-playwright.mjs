/**
 * Multi-format UAT: UI upload + chat + send_file for ADR-051.
 * Run: node /tmp/uat-formats-playwright.mjs
 */
import { chromium } from 'playwright';
import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const BASE = process.env.UAT_BASE || 'http://127.0.0.1:8080';
const FIX = '/tmp/uat-formats';
const OUT = '/tmp/uat-formats-results';
fs.mkdirSync(OUT, { recursive: true });

const FORMATS = [
  { file: 'sample.png', mime: 'image/png', expectUiAccept: true },
  { file: 'sample.jpeg', mime: 'image/jpeg', expectUiAccept: true },
  { file: 'sample.svg', mime: 'image/svg+xml', expectUiAccept: true },
  { file: 'sample.pdf', mime: 'application/pdf', expectUiAccept: true },
  { file: 'sample.docx', mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document', expectUiAccept: true },
  { file: 'sample.pptx', mime: 'application/vnd.openxmlformats-officedocument.presentationml.presentation', expectUiAccept: true },
  { file: 'sample.txt', mime: 'text/plain', expectUiAccept: true },
  { file: 'sample.md', mime: 'text/markdown', expectUiAccept: true },
  { file: 'sample.doc', mime: 'application/msword', expectUiAccept: false }, // not in ACCEPT_LIST
  { file: 'sample.mp4', mime: 'video/mp4', expectUiAccept: false }, // video not in ACCEPT_LIST
];

function loadCookies() {
  const raw = fs.readFileSync('/tmp/uat-cookies.txt', 'utf8');
  const cookies = [];
  for (const line of raw.split('\n')) {
    if (!line || line.startsWith('#')) continue;
    const parts = line.split('\t');
    if (parts.length < 7) continue;
    cookies.push({
      name: parts[5],
      value: parts[6],
      domain: '127.0.0.1',
      path: parts[2] || '/',
      httpOnly: parts[0].includes('HttpOnly') || parts[5] === 'omnipus-session',
      secure: false,
      sameSite: 'Strict',
    });
  }
  return cookies;
}

async function collectToasts(page) {
  // Common toast selectors in this SPA
  const texts = await page.locator('[data-sonner-toast], [role="status"], .toast, [class*="toast"]').allTextContents().catch(() => []);
  return texts.map((t) => t.trim()).filter(Boolean);
}

async function pageErrors(page, bag) {
  page.on('pageerror', (e) => bag.push(`pageerror: ${e.message}`));
  page.on('console', (msg) => {
    if (msg.type() === 'error') bag.push(`console.error: ${msg.text()}`);
  });
}

async function openChat(page) {
  await page.goto(`${BASE}/`, { waitUntil: 'networkidle', timeout: 60000 });
  await page.waitForTimeout(1500);
  // Dismiss onboarding if still showing
  const skip = page.getByRole('button', { name: /skip|continue|get started|start/i }).first();
  if (await skip.isVisible({ timeout: 2000 }).catch(() => false)) {
    await skip.click().catch(() => {});
    await page.waitForTimeout(500);
  }
  // Navigate to chat if needed
  const chatLink = page.getByRole('link', { name: /chat/i }).first();
  if (await chatLink.isVisible({ timeout: 2000 }).catch(() => false)) {
    await chatLink.click().catch(() => {});
  }
  await page.goto(`${BASE}/chat`, { waitUntil: 'networkidle', timeout: 60000 }).catch(() => {});
  await page.waitForTimeout(2000);
  // Composer textarea
  const composer = page.locator('textarea, [contenteditable="true"], [data-placeholder]').first();
  await composer.waitFor({ timeout: 30000 });
  return composer;
}

async function findFileInput(page) {
  // Hidden file input from AssistantUI AddAttachment
  const inputs = page.locator('input[type="file"]');
  const n = await inputs.count();
  for (let i = 0; i < n; i++) {
    const el = inputs.nth(i);
    const accept = (await el.getAttribute('accept')) || '';
    if (accept.includes('image') || accept.includes('pdf') || accept.includes('.md')) {
      return el;
    }
  }
  return inputs.first();
}

async function uploadAndSend(page, composer, fixturePath, prompt) {
  const errors = [];
  const fileInput = await findFileInput(page);
  await fileInput.setInputFiles(fixturePath);
  await page.waitForTimeout(800);
  // Type prompt
  await composer.click();
  await composer.fill(prompt);
  await page.waitForTimeout(300);
  // Send
  const sendBtn = page.getByRole('button', { name: /send/i }).first();
  if (await sendBtn.isEnabled().catch(() => false)) {
    await sendBtn.click();
  } else {
    await composer.press('Enter');
  }
  // Wait for turn to settle (done / assistant text / error toast)
  await page.waitForTimeout(2500);
  // Wait up to 90s for assistant response or error
  const start = Date.now();
  let assistantText = '';
  while (Date.now() - start < 90000) {
    const toasts = await collectToasts(page);
    const bodyText = await page.locator('main, [data-testid="chat"], body').first().innerText().catch(() => '');
    if (toasts.some((t) => /error|failed|not supported|can't attach|cannot/i.test(t))) {
      return { ok: false, phase: 'toast', toasts, assistantText: bodyText.slice(-500), errors };
    }
    // Look for recent assistant bubble growth
    const bubbles = page.locator('[data-role="assistant"], .aui-assistant-message, [class*="assistant"]');
    const count = await bubbles.count().catch(() => 0);
    if (count > 0) {
      assistantText = (await bubbles.last().innerText().catch(() => '')) || '';
      // Wait until streaming likely done (stable text)
      await page.waitForTimeout(2000);
      const again = (await bubbles.last().innerText().catch(() => '')) || '';
      if (again === assistantText && assistantText.length > 5) {
        return { ok: true, phase: 'assistant', toasts, assistantText: assistantText.slice(0, 800), errors };
      }
      assistantText = again;
    }
    // Generic error banners
    if (/something went wrong|internal server error|request failed/i.test(bodyText)) {
      return { ok: false, phase: 'banner', toasts, assistantText: bodyText.slice(-500), errors };
    }
    await page.waitForTimeout(1500);
  }
  return { ok: false, phase: 'timeout', toasts: await collectToasts(page), assistantText, errors };
}

async function main() {
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({
    baseURL: BASE,
    viewport: { width: 1400, height: 900 },
  });
  await context.addCookies(loadCookies());
  const page = await context.newPage();
  const consoleErrors = [];
  await pageErrors(page, consoleErrors);

  const results = { uploadChat: [], sendFile: [], meta: { base: BASE, started: new Date().toISOString() } };

  // Screenshot landing
  try {
    const composer = await openChat(page);
    await page.screenshot({ path: path.join(OUT, '00-chat-open.png'), fullPage: true });

    // --- Phase A: UI upload + chat per format ---
    for (const fmt of FORMATS) {
      const fp = path.join(FIX, fmt.file);
      const rec = {
        format: fmt.file,
        mime: fmt.mime,
        expectUiAccept: fmt.expectUiAccept,
        uploadChat: null,
      };
      console.log(`\n=== UPLOAD+CHAT ${fmt.file} ===`);
      try {
        // Fresh navigation each time to avoid composer state bleed
        await page.goto(`${BASE}/chat`, { waitUntil: 'domcontentloaded', timeout: 60000 });
        await page.waitForTimeout(1500);
        const c = page.locator('textarea, [contenteditable="true"]').first();
        await c.waitFor({ timeout: 20000 });

        if (!fmt.expectUiAccept) {
          // Expect client-side rejection
          const fileInput = await findFileInput(page);
          // Listen for dialogs/toasts
          await fileInput.setInputFiles(fp);
          await page.waitForTimeout(1000);
          const toasts = await collectToasts(page);
          const body = await page.innerText('body');
          const rejected =
            toasts.some((t) => /not supported|can't attach|aren't supported/i.test(t)) ||
            /not supported|can't attach|aren't supported/i.test(body);
          rec.uploadChat = {
            ok: rejected, // success = correctly rejected without crash
            phase: rejected ? 'ui-reject' : 'unexpected-accept',
            toasts,
            note: 'UI ACCEPT_LIST should reject this type without crashing',
          };
          await page.screenshot({ path: path.join(OUT, `upload-${fmt.file}.png`) });
          console.log(JSON.stringify(rec.uploadChat));
          results.uploadChat.push(rec);
          continue;
        }

        const prompt = `I attached ${fmt.file}. Reply with exactly: OK ${fmt.file} and one short sentence about what you received.`;
        const r = await uploadAndSend(page, c, fp, prompt);
        rec.uploadChat = r;
        // Fail if generic crash errors
        const bad =
          /internal server error|typeerror|cannot read propert|unhandled|stack trace/i.test(
            (r.assistantText || '') + (r.toasts || []).join(' '),
          );
        if (bad) rec.uploadChat.ok = false;
        await page.screenshot({ path: path.join(OUT, `upload-${fmt.file}.png`) });
        console.log(JSON.stringify({ ok: r.ok, phase: r.phase, text: (r.assistantText || '').slice(0, 120), toasts: r.toasts }));
      } catch (e) {
        rec.uploadChat = { ok: false, phase: 'exception', error: String(e) };
        console.log('EXC', e);
        await page.screenshot({ path: path.join(OUT, `upload-${fmt.file}-err.png`) }).catch(() => {});
      }
      results.uploadChat.push(rec);
    }

    // --- Phase B: send_file via LLM for each accepted format ---
    // Place files into a workspace-accessible path via API upload first, then ask agent to send_file.
    // Better: write into agent workspace via REST if available, or use bash for jim.
    // Use chat: "Use send_file tool with path ..." after putting files in workspace via upload+offload or direct write.

    // Put fixtures into workspace media library and also copy paths into work via API isn't easy.
    // Use Jim with bash to copy fixtures - but sandbox may block.
    // Simplest path: upload each file, then ask model to send_file using the offloaded work path if any.
    // Actually send_file needs a local path in workspace. Write files via multipart to a known place?

    // Use CLI one-shot with jim to write files? Or REST write_file tool via chat.
    // Direct approach: copy fixtures into OMNIPUS_HOME workspace work dir for the default workspace.
    const ws = '01KY5WYHDKJGQGSY3Z3C6TSFN3';
    const workDir = `/home/dev/omnipus-home/workspaces/${ws}/work/uat-send`;
    fs.mkdirSync(workDir, { recursive: true });
    for (const fmt of FORMATS) {
      fs.copyFileSync(path.join(FIX, fmt.file), path.join(workDir, fmt.file));
    }

    for (const fmt of FORMATS) {
      // Only test send_file for types we care about delivering
      if (fmt.file === 'sample.doc') continue; // optional
      const rec = { format: fmt.file, sendFile: null };
      console.log(`\n=== SEND_FILE ${fmt.file} ===`);
      try {
        await page.goto(`${BASE}/chat`, { waitUntil: 'domcontentloaded', timeout: 60000 });
        await page.waitForTimeout(1200);
        const c = page.locator('textarea, [contenteditable="true"]').first();
        await c.waitFor({ timeout: 20000 });
        const relPath = `work/uat-send/${fmt.file}`;
        const prompt = `Call the send_file tool exactly once with path "${relPath}". After the tool result, reply with exactly: SENT ${fmt.file}`;
        await c.click();
        await c.fill(prompt);
        const sendBtn = page.getByRole('button', { name: /send/i }).first();
        if (await sendBtn.isEnabled().catch(() => false)) await sendBtn.click();
        else await c.press('Enter');

        const start = Date.now();
        let ok = false;
        let detail = '';
        let mediaSeen = false;
        while (Date.now() - start < 120000) {
          await page.waitForTimeout(2000);
          const body = await page.locator('body').innerText();
          const toasts = await collectToasts(page);
          // Media attachment cards / download links / images
          const mediaCount = await page.locator('a[download], img[src*="media"], [class*="Media"], [data-testid*="media"], audio, video').count().catch(() => 0);
          if (mediaCount > 0) mediaSeen = true;
          if (/SENT sample\.|SENT sample/i.test(body) || mediaSeen) {
            ok = true;
            detail = body.slice(-600);
            break;
          }
          if (/internal server error|typeerror|unhandled|crash/i.test(body + toasts.join(' '))) {
            ok = false;
            detail = body.slice(-600) + ' TOASTS:' + toasts.join('|');
            break;
          }
          // Tool error visible but not crash
          if (/send_file|failed to|error/i.test(body) && /SENT /i.test(body) === false && Date.now() - start > 45000) {
            // keep waiting a bit more
          }
        }
        if (!detail) detail = (await page.locator('body').innerText()).slice(-600);
        const toasts = await collectToasts(page);
        const crashed = /internal server error|typeerror|cannot read propert|unhandled rejection/i.test(detail + toasts.join(' '));
        rec.sendFile = {
          ok: ok && !crashed,
          mediaSeen,
          crashed,
          toasts,
          detail: detail.slice(0, 500),
          consoleErrors: consoleErrors.slice(-5),
        };
        await page.screenshot({ path: path.join(OUT, `sendfile-${fmt.file}.png`) });
        console.log(JSON.stringify(rec.sendFile));
      } catch (e) {
        rec.sendFile = { ok: false, error: String(e) };
        console.log('EXC', e);
      }
      results.sendFile.push(rec);
    }
  } catch (e) {
    results.fatal = String(e);
    console.error('FATAL', e);
    await page.screenshot({ path: path.join(OUT, 'fatal.png') }).catch(() => {});
  }

  results.meta.finished = new Date().toISOString();
  results.meta.consoleErrors = consoleErrors;
  fs.writeFileSync(path.join(OUT, 'summary.json'), JSON.stringify(results, null, 2));
  console.log('\n=== SUMMARY WRITTEN ===', path.join(OUT, 'summary.json'));
  await browser.close();

  // Exit non-zero if any expected-accept upload failed or any send_file crashed
  const uploadFails = results.uploadChat.filter((r) => r.expectUiAccept && r.uploadChat && !r.uploadChat.ok);
  const sendCrashes = results.sendFile.filter((r) => r.sendFile && (r.sendFile.crashed || r.sendFile.ok === false));
  console.log('uploadFails', uploadFails.map((r) => r.format));
  console.log('sendIssues', sendCrashes.map((r) => r.format));
  process.exit(uploadFails.length || sendCrashes.some((r) => r.sendFile?.crashed) ? 1 : 0);
}

main().catch((e) => {
  console.error(e);
  process.exit(2);
});

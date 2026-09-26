/**
 * provider-retry.spec.ts — provider-messages spec RED test, TDD row 20:
 *
 *   "E2E: injected 429 (`retry-after: 3` for suite speed; 120 s formatting
 *    proven at component level) → countdown → terminal line; sentinel scan
 *    over the whole WS log | E2E | A-1, A-5, DG-1, DG-2 | Playwright, mock
 *    provider; MIN-005 applied"
 *
 * Oracles come from the SPEC ONLY:
 *   - MIN-005: the injected 429 carries `retry-after: 3` (suite speed); the
 *     120 s formatting is component-level (ProviderRetryIndicator.test.tsx).
 *   - D3 row 1 template (§6): "«Provider» is busy. Retrying automatically in
 *     mm:ss (attempt n of max)." — asserted as the countdown line for
 *     attempt 2 of 3 and then attempt 3 of 3 (two 429s before the answer,
 *     per the mock's 429/429/200 script; C-8: attempt numbers are the call
 *     ABOUT to be made).
 *   - A-5: the terminal state is the assistant's answer replacing the
 *     indicator (no success-coloured "cleared" state in e2e either).
 *   - Scan 1 (DG-1/DG-2, MAJ-105): the raw provider body — here carrying the
 *     sentinel in the 429 body — must appear NOWHERE: not in any WS frame of
 *     any type, not in the non-Verbose DOM. The e2e is scan 2's surface
 *     (rows 14/15 own scan 1's unit halves; this file re-proves the whole
 *     log end to end).
 *   - C-2/MAJ-015: the provider_retry frame carries named fields only — no
 *     `error` key. Asserted on the wire frame the SPA actually received.
 *
 * AUTHORED, NOT EXECUTED (Wave 2 RED constraint): this spec runs in Wave 3's
 * CI e2e gate. It is red by construction against today's binary — no code
 * path emits a provider_retry frame, so every frame assertion below fails
 * until GREEN lands. It self-hosts its own gateway + provider so it cannot
 * disturb the other specs on its shard.
 *
 * Isolation model:
 *   - fixtures/gateway-process.ts::GatewayProcess starts an ISOLATED gateway
 *     (own port, own fresh OMNIPUS_HOME) onboarded with the provider's
 *     `api_base` pointed at the mock (rest_onboarding.go persists
 *     provider.api_base; the onboarding key probe prefers it), so no real
 *     OpenRouter key is exercised and the shared port-6060 gateway is never
 *     reconfigured. Loopback provider bases are a supported path
 *     (pkg/providers/validate.go::isLoopbackBaseURL — Ollama/LM Studio).
 *   - The mock provider is a node:http server speaking just enough
 *     OpenAI-compatible protocol: non-streaming POST /chat/completions
 *     (the onboarding/PUT validation probe) answers 200 honestly; streaming
 *     POST /chat/completions (the real turn) follows the 429/429/200 script.
 *     The discriminator is the wire fact `"stream":true` — validation probes
 *     (pkg/providers/validate.go::probeCompletion) are never streaming.
 */

import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import {
  chatInput,
  waitForConnected,
  startNewChat,
  assistantMessages,
} from './fixtures/selectors';
import { GatewayProcess } from './fixtures/gateway-process';
import http from 'node:http';

test.setTimeout(240_000);

// The sentinel the mock embeds in its 429 BODY (the "raw provider body" the
// spec's sentinel scans hunt for — MAJ-105).
const SENTINEL = 'SENTINEL-PM-E2E-9K4Z';
// The fixed content of the mock's final (successful) completion.
const ANSWER = 'Mock provider says hello.';
const MODEL = 'mock-model-a';

interface MockProvider {
  url: string;
  close: () => Promise<void>;
  streamingChatCalls: () => number;
}

// ── The mock provider ───────────────────────────────────────────────────────

function startMockProvider(): Promise<MockProvider> {
  return new Promise((resolve, reject) => {
    let streamingCalls = 0;

    function sseChunk(delta: Record<string, unknown>, finish: string | null): string {
      return (
        'data: ' +
        JSON.stringify({
          id: 'chatcmpl-pm-mock',
          object: 'chat.completion.chunk',
          created: Math.floor(Date.now() / 1000),
          model: MODEL,
          choices: [{ index: 0, delta, finish_reason: finish }],
        }) +
        '\n\n'
      );
    }

    const server = http.createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on('data', (c: Buffer) => chunks.push(c));
      req.on('end', () => {
        const body = Buffer.concat(chunks).toString('utf8');

        if (req.method === 'GET' && req.url?.startsWith('/models')) {
          res.writeHead(200, { 'Content-Type': 'application/json' });
          res.end(
            JSON.stringify({
              object: 'list',
              data: [{ id: MODEL, object: 'model' }],
            }),
          );
          return;
        }

        if (req.method === 'POST' && req.url?.includes('/chat/completions')) {
          const streaming = body.includes('"stream":true') || body.includes('"stream": true');

          if (!streaming) {
            // Validation probe (onboarding / PUT): answer honestly so the
            // install comes up healthy — never part of the retry script.
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(
              JSON.stringify({
                id: 'chatcmpl-pm-probe',
                object: 'chat.completion',
                created: Math.floor(Date.now() / 1000),
                model: MODEL,
                choices: [
                  {
                    index: 0,
                    message: { role: 'assistant', content: 'probe ok' },
                    finish_reason: 'stop',
                  },
                ],
                usage: {
                  prompt_tokens: 1,
                  completion_tokens: 2,
                  total_tokens: 3,
                },
              }),
            );
            return;
          }

          // The real chat turn: 429 twice (each with retry-after: 3, MIN-005
          // and the sentinel in the raw body), then the answer.
          streamingCalls += 1;
          if (streamingCalls <= 2) {
            res.writeHead(429, {
              'Content-Type': 'application/json',
              'retry-after': '3',
            });
            res.end(
              JSON.stringify({
                error: {
                  type: 'rate_limit_error',
                  message: 'rate limited by mock provider ' + SENTINEL,
                },
              }),
            );
            return;
          }

          res.writeHead(200, { 'Content-Type': 'text/event-stream' });
          res.end(
            sseChunk({ role: 'assistant', content: '' }, null) +
              sseChunk({ content: ANSWER }, null) +
              sseChunk({}, 'stop') +
              'data: [DONE]\n\n',
          );
          return;
        }

        res.writeHead(404, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: { message: 'mock provider: no such route' } }));
      });
    });

    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      if (!addr || typeof addr === 'string') {
        reject(new Error('mock provider: no ephemeral port'));
        return;
      }
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        streamingChatCalls: () => streamingCalls,
        close: () =>
          new Promise<void>((resolveClose, rejectClose) => {
            server.close((err) => (err ? rejectClose(err) : resolveClose()));
          }),
      });
    });
  });
}

// ── Page-side helpers ───────────────────────────────────────────────────────

/** Login inline against the isolated gateway (GatewayProcess onboarded via
 * REST; this browser context is fresh). Mirrors catchup-scenarios.spec.ts. */
async function login(page: Page, gw: GatewayProcess): Promise<void> {
  await page.goto(gw.baseURL);
  await expect(page.locator('#login-username')).toBeVisible({ timeout: 15_000 });
  await page.locator('#login-username').pressSequentially(gw.adminUsername);
  await page.locator('#login-password').pressSequentially(gw.adminPassword);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page).not.toHaveURL(/\/#\/login/, { timeout: 15_000 });
}

// ── The row-20 scenario ─────────────────────────────────────────────────────

test.describe('provider-messages row 20 (A-1, A-5, DG-1, DG-2, MIN-005)', () => {
  test('injected 429 with retry-after: 3 shows the countdown, then the answer; the sentinel appears nowhere', async ({
    page,
  }) => {
    const mock = await startMockProvider();
    let gw: GatewayProcess | null = null;
    try {
      gw = await GatewayProcess.start({
        apiBase: mock.url,
        model: MODEL,
        adminPassword: 'admin1234',
      });

      // ── WS log capture (the sentinel scan's surface) ──
      const frames: string[] = [];
      page.on('websocket', (ws) => {
        ws.on('framereceived', (data) => {
          const p = data.payload;
          frames.push(typeof p === 'string' ? p : p.toString('utf8'));
        });
      });

      await login(page, gw);

      // ── DOM timeline recorder: snapshots body text every 200 ms so a
      // brief indicator state (a 3 s window) is never missed by
      // assertion-polling alone. ──
      await page.evaluate(() => {
        const w = window as unknown as { __pmTexts?: string[] };
        w.__pmTexts = [];
        window.setInterval(() => {
          const w2 = window as unknown as { __pmTexts?: string[] };
          w2.__pmTexts!.push(document.body ? document.body.innerText : '');
        }, 200);
      });

      // ── Drive the turn ──
      const input = chatInput(page);
      await expect(input).toBeVisible({ timeout: 15_000 });
      await waitForConnected(page);
      await startNewChat(page);
      await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });
      await input.fill('Say hello.');
      await input.press('Enter');

      // ── A-1: the countdown line, D3 template, attempt 2 of 3 ──
      await expect(
        page.getByText(/is busy\. Retrying automatically in \d+:\d{2} \(attempt 2 of 3\)\./),
      ).toBeVisible({ timeout: 20_000 });

      // ── A-5: the terminal answer replaces the indicator ──
      await expect(page.getByText(ANSWER)).toBeVisible({ timeout: 30_000 });

      // ── Assert the recorded timeline (brief states included) ──
      const texts: string[] = await page.evaluate(
        () => (window as unknown as { __pmTexts?: string[] }).__pmTexts ?? [],
      );
      const timeline = texts.join('\n');
      // First wait: attempt 2 of 3 ...
      expect(timeline).toContain('Retrying automatically in');
      expect(timeline).toContain('(attempt 2 of 3)');
      // ... second wait: attempt 3 of 3 (the second 429's frame) ...
      expect(timeline).toContain('(attempt 3 of 3)');

      // ── Row 14's wire contract, re-proven at the SPA boundary ──
      const retryFrames = frames
        .map((f) => {
          try {
            return JSON.parse(f) as Record<string, unknown>;
          } catch {
            return null;
          }
        })
        .filter((f): f is Record<string, unknown> => f !== null && f['type'] === 'provider_retry');
      expect(
        retryFrames.length,
        'the SPA must have received at least one provider_retry frame over the wire',
      ).toBeGreaterThanOrEqual(1);

      const first = retryFrames[0];
      // C-8 / MIN-005 / MIN-104: the call ABOUT to be made, the wire
      // retry-after, the chain max.
      expect(first['attempt']).toBe(2);
      expect(first['retry_after_seconds']).toBe(3);
      expect(first['max_attempts']).toBe(3);
      for (const field of ['session_id', 'turn_id', 'provider', 'model', 'sent_at', 'retry_at']) {
        expect(typeof first[field], `retry frame field ${field}`).toBe('string');
        expect(first[field], `retry frame field ${field} non-empty`).not.toBe('');
      }
      // C-2/MAJ-015: named fields only — no `error` key of any kind.
      expect(Object.keys(first)).not.toContain('error');

      // ── Scan 1, whole-log edition: the sentinel appears in NO frame ──
      const wholeLog = frames.join('\n');
      expect(wholeLog).not.toContain(SENTINEL);

      // ── Scan 2 / DG-2: the sentinel never reaches the non-Verbose DOM ──
      const bodyText = await page.locator('body').innerText();
      expect(bodyText).not.toContain(SENTINEL);

      // ── DG-1: Verbose chat is off by default → no disclosure at all ──
      expect(await page.getByText('Technical details').count()).toBe(0);
    } finally {
      if (gw) await gw.stop();
      await mock.close();
    }
  });
});

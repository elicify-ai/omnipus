import { test as base, expect } from '@playwright/test';

const CONSOLE_ERROR_ALLOWLIST: RegExp[] = [
  /WebSocket.*reconnect/i,
  /hydration/i,
  /HMR/i,
  /manifest\.json.*404/i,
];

export const test = base.extend<{ consoleErrors: string[]; cancelOnTeardown: void }>({
  consoleErrors: async ({ page }, use) => {
    const errors: string[] = [];
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return;
      const text = msg.text();
      if (CONSOLE_ERROR_ALLOWLIST.some((re) => re.test(text))) return;
      errors.push(text);
    });
    await use(errors);
    expect(errors, 'unexpected console errors').toEqual([]);
  },
  // Auto-applied teardown: if the test leaves a streaming turn behind, click the
  // Stop button before the page closes. Without this, the backend agent loop
  // keeps running against the closed WebSocket, the gateway emits "no active
  // connection for chat ... send failed" until the LLM finishes naturally, and
  // the next test inherits a backlog of orphaned agent loops queued against the
  // same OpenRouter rate limit. That backlog is the documented cause of the
  // Group-A flakiness in playwright.config.ts (subagent×5, handoff b, T24,
  // T26 — each takes >40s under suite load even though it passes in 5-25s
  // standalone). See https://github.com/elicify-ai/omnipus/issues/180 for the
  // permanent server-side fix (cancel-turn on WS close).
  cancelOnTeardown: [
    async ({ page }, use) => {
      await use();
      try {
        // The Stop button only renders while isStreaming is true on the active
        // session. isVisible has a 0ms default — no extra wait if no turn is
        // running. The locator wait below honors a small budget so a turn that
        // just started has a chance to land in the DOM.
        const stopBtn = page.locator('[data-testid="stop-btn"]');
        if (await stopBtn.isVisible().catch(() => false)) {
          await stopBtn.click({ timeout: 2_000 }).catch(() => undefined);
          // Brief settling window so the cancel frame is actually sent over the
          // WebSocket before Playwright tears the context down.
          await page.waitForTimeout(250).catch(() => undefined);
        }
      } catch {
        // Best effort: any failure here (page closing, navigation in flight,
        // store not initialised) is harmless — we only want to cancel when
        // there is a clear streaming turn to cancel.
      }
    },
    { auto: true },
  ],
});

export { expect } from '@playwright/test';

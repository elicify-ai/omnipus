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
  // Auto-applied teardown: simulate a well-behaved user clicking Stop before
  // leaving. The gateway, by design, keeps an agent loop running after its
  // WebSocket closes — agents may be doing long background work the user wants
  // to come back to, headless channels (Slack/Telegram/…) have no WS at all,
  // and the since-cursor replay path expects a turn to complete regardless of
  // who's currently watching. Cancel only fires on explicit user action.
  //
  // That contract means a Playwright test that walks away mid-turn leaves a
  // real agent loop behind, exactly as a real user would. Across the suite
  // those loops queue against the same OpenRouter rate-limit window and starve
  // later tests' tool calls — the documented Group-A variance in
  // playwright.config.ts (subagent×5, handoff b, T24, T26 each takes >40s
  // under suite load even though they pass in 5-25s standalone). The fix is
  // for each test to act like a user: when it's done, click Stop. This
  // fixture is that action, applied uniformly.
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

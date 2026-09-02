import { test as base, expect } from '@playwright/test';

const CONSOLE_ERROR_ALLOWLIST: RegExp[] = [
  /WebSocket.*reconnect/i,
  /hydration/i,
  /HMR/i,
  /manifest\.json.*404/i,
];

export const test = base.extend<{ consoleErrors: string[]; cancelOnTeardown: void }>({
  // AUTO-APPLIED. Without `auto: true` this fixture was dead code in almost
  // every spec that imported it.
  //
  // A Playwright fixture is LAZY: its listener attaches, and its teardown
  // assertion runs, only for a test that names it in its destructured
  // arguments. 33 spec files import this module's `test` export; 3 mention
  // `consoleErrors`. For the other 30 the gate reported green having watched
  // nothing at all.
  //
  // Found by a UAT tester in 2026-09, who proved it rather than inferred it:
  // a test using this `test` export, destructuring only `{ page }`, navigating
  // to #/settings and waiting, PASSED while the page emitted five
  // "Failed to load resource … 503" console errors. "Zero console errors" is a
  // stated pass criterion of the UAT plan, and the suite that appeared to
  // enforce it did not.
  //
  // Its sibling below was declared with `{ auto: true }` from the start, which
  // is what makes the omission here a slip rather than a design choice.
  consoleErrors: [
    async ({ page }, use, testInfo) => {
      const errors: string[] = [];
      page.on('console', (msg) => {
        if (msg.type() !== 'error') return;
        const text = msg.text();
        if (CONSOLE_ERROR_ALLOWLIST.some((re) => re.test(text))) return;
        errors.push(text);
      });
      await use(errors);

      // A test whose PURPOSE is to provoke an HTTP error will log console
      // errors by design — asserting zero there would be asserting the test
      // does not do its job. Such a test opts out explicitly, per test, with:
      //
      //   test.info().annotations.push({ type: 'expects-console-errors',
      //     description: 'why' })
      //
      // Opting out per test rather than widening CONSOLE_ERROR_ALLOWLIST is
      // deliberate: an allow-list entry for, say, 405 would blind EVERY test in
      // the suite to a real 405, which is how a gate quietly stops gating.
      const optedOut = testInfo.annotations.some(
        (a) => a.type === 'expects-console-errors',
      );
      if (optedOut) return;

      expect(errors, 'unexpected console errors').toEqual([]);
    },
    { auto: true },
  ],
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

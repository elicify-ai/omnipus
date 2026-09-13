/**
 * browser-control-handover.spec.ts — ADR-085 BROWSER-FR-041/FR-042/FR-043a,
 * wave B8 (the render half of FR-042 and the SPA idempotency half of
 * FR-044 are unit-tested in chat.browser-handover-notice.test.ts and
 * ChatScreen.browser-handover-notice.test.tsx; this spec is the end-to-end
 * proof that a REAL take-control gesture against a REAL running gateway
 * produces the waiting notice in the REAL chat thread, and that it survives
 * a reload via replay (FR-043a)).
 *
 * Drives the real embedded binary against a live model, same idiom as
 * browser-live-video.spec.ts and media.spec.ts (Jim, browser_navigate to
 * https://example.com — media.spec.ts's own established external-navigation
 * target). Once the agent has a live browser tab, the test opens the panel
 * and clicks into the live frame — BrowserLiveView's takeWheelIfNeeded, the
 * SAME gesture uat-browser-panel.spec.ts's UAT-14 uses ("Step 1... clicking
 * the frame IS that gesture") — to take the wheel, then asserts:
 *
 *   1. `data-testid="browser-handover-notice"` appears live in the chat
 *      thread (BROWSER-FR-041/042 — this repo's shipped `isGoalAck`/
 *      `goal-ack-line` two-site discriminator pattern, copied for this
 *      notice per C-90).
 *   2. Its text says the browser was taken over and that a message returns
 *      it — content is asserted loosely (key phrases), never the schema's
 *      example string verbatim: FR-041 states a REQUIREMENT on the copy's
 *      meaning, not a fixed wire value.
 *   3. The SAME notice survives a page reload — FR-043a: replay must emit
 *      the identical frame type/discriminator as the live path, not a
 *      generic system banner.
 *
 * Per the ADR-085 D-G operator decision (2026-09-11): taking the wheel does
 * NOT cancel, park, or require the agent's turn to finish first — a human
 * can take control at any point in a live session. This spec therefore does
 * not wait for the agent's turn to fully finish before taking control.
 *
 * This spec issues no `POST /api/v1/auth/login` (scripts/check-e2e-login-
 * crosstalk.sh) — page.goto('/') alone, relying on the shared session
 * cookie global-setup.ts already established, exactly like every other
 * real-model spec in this suite (e.g. goal-work-first.spec.ts).
 */

import { expect, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import {
  chatInput,
  waitForConnected,
  startNewChat,
  selectAgent,
  assistantMessages,
  watchLiveButton,
  browserLivePanel,
  browserLiveFrame,
} from './fixtures/selectors';

const stopButton = (page: Page) => page.locator('[data-testid="stop-btn"]');

const handoverNotice = (page: Page) => page.locator('[data-testid="browser-handover-notice"]');

/**
 * Ends the current turn without racing a live model's wall-clock — copies
 * goal-work-first.spec.ts's endTurnDeterministically exactly: click Stop if
 * it is still showing, then wait for streaming to be over. This spec's
 * assertions never depend on the turn having fully finished (D-G: a
 * take-control never needs the turn to be over), so this is only cleanup
 * between steps, never a wait for the model's own reply.
 */
async function endTurnDeterministically(page: Page): Promise<void> {
  const stop = stopButton(page);
  if (await stop.isVisible().catch(() => false)) {
    await stop.click().catch(() => {
      /* already settled between the check and the click — nothing to stop */
    });
  }
  await expect(stop).toBeHidden({ timeout: 30_000 });
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

test(
  'taking the wheel emits a live waiting notice in the thread, which survives a reload',
  async ({ page }) => {
    // Worst-case budget: connect + new-chat + agent-select + a browser_navigate
    // turn (real LLM) + opening the live panel/waiting for the first frame +
    // taking control + the notice arriving + a reload + replay hydration.
    test.setTimeout(300_000);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 15_000 });
    await waitForConnected(page);

    // Jim — the generalist doer with browser_navigate allow-policied
    // (pkg/coreagent/core.go); Mia's "guide" persona routes browser work
    // away rather than executing it (media.spec.ts, browser-live-video.spec.ts
    // rely on the same choice).
    await startNewChat(page);
    await selectAgent(page, /Jim/i);
    await expect(assistantMessages(page)).toHaveCount(0, { timeout: 10_000 });

    await test.step('drive the agent to open a live browser tab', async () => {
      await input.fill(
        'Use the browser tools to navigate to https://example.com. Call browser_navigate ' +
          'yourself — do not delegate this, and do not call any other browser tool.',
      );
      await input.press('Enter');

      // The turn starting is enough — per D-G, taking control does not need
      // the turn to be finished. Confirms the send actually landed before
      // waiting on the tool-call row below.
      await expect(stopButton(page)).toBeVisible({ timeout: 30_000 });
    });

    let panel: ReturnType<typeof browserLivePanel>;
    await test.step('open the live view via "Watch live"', async () => {
      const btn = watchLiveButton(page);
      await expect(btn).toBeVisible({ timeout: 120_000 });
      await btn.click();
      panel = browserLivePanel(page);
      await expect(panel).toBeVisible({ timeout: 15_000 });
    });

    await test.step('take the wheel by clicking into the live frame', async () => {
      const frame = browserLiveFrame(page);
      await expect(frame).toBeVisible({ timeout: 90_000 });
      const box = await frame.boundingBox();
      if (!box) throw new Error('the live frame has no bounding box');
      // Click empty space away from any link the agent's own navigation
      // might have landed on (mirrors UAT-14's "well below the link list").
      await page.mouse.click(box.x + box.width / 2, box.y + box.height * 0.9);
    });

    await test.step(
      'the browser-handover waiting notice appears live in the chat thread (BROWSER-FR-041/042)',
      async () => {
        const notice = handoverNotice(page);
        await expect(
          notice,
          'no element with data-testid="browser-handover-notice" appeared in the thread within ' +
            'the timeout — BROWSER-FR-041/042 requires the take to emit a live waiting-surface line',
        ).toBeVisible({ timeout: 60_000 });
        const text = (await notice.textContent()) ?? '';
        // FR-041: the copy must say the agent stopped driving and that
        // sending a message returns it — content requirement, not a fixed
        // wire string (the schema's own example is illustrative only).
        expect(text.toLowerCase()).toMatch(/browser|control|driving/);
        expect(text.toLowerCase()).toMatch(/message/);
      },
    );

    await endTurnDeterministically(page);

    await test.step('the SAME notice survives a reload via replay (FR-043a)', async () => {
      await page.reload();
      await waitForConnected(page);
      // FR-043a: replay must emit the identical frame TYPE (discriminated
      // by the persisted system_subtype, never by content prefix-matching)
      // — the observable proof from the SPA side is the same testid
      // reappearing, not a generic system banner with similar-looking text.
      await expect(
        handoverNotice(page),
        'the browser-handover notice did not reappear after reload — FR-043a requires replay to ' +
          'emit the SAME frame type as the live path, not a generic system banner',
      ).toBeVisible({ timeout: 30_000 });
    });
  },
);

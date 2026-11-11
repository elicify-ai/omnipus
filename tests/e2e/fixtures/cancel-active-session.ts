import { randomUUID } from 'node:crypto';
import type { Page } from '@playwright/test';
import { chatInput } from './selectors';

// Shared with fixture units so the existing click/card/Stop budgets have one name.
export const CANCEL_TEARDOWN_TIMEOUTS = { click: 2_000, cardHidden: 5_000, composer: 1_000, settle: 250 } as const;
export const MAX_QUESTION_CANCEL_ROUNDS = 3;

/** User actions shared by automatic teardown and the #1769 browser regression.
 * A parked question survives the streaming turn, so Stop alone is not cleanup. */
export async function cancelActiveSessionOnTeardown(page: Page): Promise<void> {
  if (page.isClosed()) return;
  const questionCancel = page.getByTestId('ask-user-cancel').first();
  let rounds = 0;
  while (await questionCancel.isVisible()) {
    if (rounds >= MAX_QUESTION_CANCEL_ROUNDS) {
      throw new Error(`cancelOnTeardown: the model asked again ${rounds} times after Cancel (maximum ${MAX_QUESTION_CANCEL_ROUNDS} cancellation rounds)`);
    }
    // The product exposes no card-id attribute. Mark this actual DOM root only;
    // never retarget the disappearance wait to a successor with identical text.
    const marker = randomUUID();
    const card = questionCancel.locator('xpath=ancestor::*[@data-testid="ask-user-question-card"][1]');
    await card.evaluate((element, identity) => element.setAttribute('data-e2e-teardown-card', identity), marker,
      { timeout: CANCEL_TEARDOWN_TIMEOUTS.click });
    // Intentionally OUTSIDE the inherited best-effort Stop catch (#1661).
    // A failed click or a card that stays open is not a tolerated re-ask.
    await questionCancel.click({ timeout: CANCEL_TEARDOWN_TIMEOUTS.click });
    await page.locator(`[data-e2e-teardown-card="${marker}"]`).waitFor({ state: 'hidden', timeout: CANCEL_TEARDOWN_TIMEOUTS.cardHidden });
    rounds++;
    // Let the resumed turn hand off to an immediate successor before inspecting
    // the controls again. The old card's disappearance, not this pause, is proof.
    await page.waitForTimeout(CANCEL_TEARDOWN_TIMEOUTS.settle);
  }
  if (rounds > 0) {
    const enabled = await chatInput(page).isEnabled({ timeout: CANCEL_TEARDOWN_TIMEOUTS.composer }).catch((cause: unknown) => {
      throw new Error('cancelOnTeardown: active session composer is missing after question Cancel', { cause });
    });
    if (!enabled) {
      throw new Error('cancelOnTeardown: question Cancel did not unlock the active session composer');
    }
  }
  try {
    const stopBtn = page.locator('[data-testid="stop-btn"]');
    if (await stopBtn.isVisible().catch(() => false)) {
      // Preserve the existing #1661 Stop behaviour. Question cleanup stays
      // outside this best-effort catch so attempted Cancel failures propagate.
      await stopBtn.click({ timeout: CANCEL_TEARDOWN_TIMEOUTS.click }).catch((err: unknown) => {
        console.warn(`[cancelOnTeardown] Stop click failed: ${err instanceof Error ? err.message : String(err)}`);
      });
      await page.waitForTimeout(CANCEL_TEARDOWN_TIMEOUTS.settle).catch(() => undefined);
    }
  } catch (err) {
    console.warn(`[cancelOnTeardown] teardown cancel did not run: ${err instanceof Error ? err.message : String(err)}`);
  }
}

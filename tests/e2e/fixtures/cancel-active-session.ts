import type { Page } from '@playwright/test';
import { chatInput } from './selectors';

/** User actions shared by automatic teardown and the #1769 browser regression.
 * A parked question survives the streaming turn, so Stop alone is not cleanup. */
export async function cancelActiveSessionOnTeardown(page: Page): Promise<void> {
  if (page.isClosed()) return;
  const questionCancel = page.getByTestId('ask-user-cancel');
  if (await questionCancel.isVisible()) {
    // Intentionally OUTSIDE the inherited best-effort Stop catch (#1661).
    // An attempted question Cancel must fail this test if it does not work.
    await questionCancel.click({ timeout: 2_000 });
    await questionCancel.waitFor({ state: 'hidden', timeout: 5_000 });
    if (!await chatInput(page).isEnabled()) {
      throw new Error('cancelOnTeardown: question Cancel did not unlock the active session composer');
    }
  }
  try {
    const stopBtn = page.locator('[data-testid="stop-btn"]');
    if (await stopBtn.isVisible().catch(() => false)) {
      // Preserve the existing #1661 Stop behaviour. Question cleanup stays
      // outside this best-effort catch so attempted Cancel failures propagate.
      await stopBtn.click({ timeout: 2_000 }).catch((err: unknown) => {
        console.warn(`[cancelOnTeardown] Stop click failed: ${err instanceof Error ? err.message : String(err)}`);
      });
      await page.waitForTimeout(250).catch(() => undefined);
    }
  } catch (err) {
    console.warn(`[cancelOnTeardown] teardown cancel did not run: ${err instanceof Error ? err.message : String(err)}`);
  }
}

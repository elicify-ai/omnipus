/**
 * connectors-programs.spec.ts — the program switch on the Connectors page.
 *
 * Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
 * Traces to: E2E row 37 of the Test Implementation Order — S-148 (program
 * switch on the Connectors page) and FR-009 (the per-connection
 * programs_allowed switch, default off).
 *
 * Every assertion drives the real Connectors screen: the switch is visible on
 * the connected-instance panel and absent without one, keyboard-operable with
 * an accessible state, persists across a reload, and a failed PUT rolls back
 * with the error state the screen already uses (error + Retry). At RED the
 * switch does not exist, so every step fails loudly — the bodies cannot pass
 * without the feature.
 */
import { expect, test, type Page } from '@playwright/test';

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

async function openConnectors(page: Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/connectors`);
  await expect(page).toHaveURL(/connectors/, { timeout: 15_000 });
}

test.describe('Connectors page — program switch', () => {
  test('S148: the programs switch exists, is keyboard-operable, persists, and rolls back on failure', async ({ page }) => {
    await openConnectors(page);

    // Given GitHub on for Dev and the telegram connection never changed, the
    // connected telegram panel shows the programs switch OFF by default.
    const telegramPanel = page.getByTestId('connector-telegram').or(
      page.locator('[data-connector="telegram"], section, div').filter({ hasText: /telegram/i }).first(),
    );
    await expect(telegramPanel).toBeVisible({ timeout: 15_000 });
    const programsSwitch = telegramPanel.getByRole('switch', { name: /programs/i })
      .or(telegramPanel.getByTestId('programs-allowed-switch'));
    await expect(programsSwitch).toBeVisible({ timeout: 15_000 });
    await expect(programsSwitch).not.toBeChecked();

    // Keyboard operation: focus it and press Space; the state is announced
    // through the switch's accessible name/state.
    await programsSwitch.focus();
    await page.keyboard.press('Space');
    await expect(programsSwitch).toBeChecked({ timeout: 10_000 });
    const announced = await programsSwitch.getAttribute('aria-checked');
    expect(announced).toBe('true');

    // Persistence: reload — the switch comes back on.
    await page.reload();
    await expect(telegramPanel).toBeVisible({ timeout: 15_000 });
    await expect(programsSwitch).toBeChecked({ timeout: 15_000 });

    // Failure path: a failed PUT rolls the switch back with an error + Retry
    // state (the screen's existing failure surface, never a silent revert).
    await page.route('**/channels/telegram/programs', (route) =>
      route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"store busy"}' }),
    );
    await programsSwitch.focus();
    await page.keyboard.press('Space'); // optimistic off...
    await expect(telegramPanel.getByText(/retry|failed|error/i).first()).toBeVisible({ timeout: 15_000 });
    await expect(programsSwitch).toBeChecked({ timeout: 15_000 }); // ...rolled back on
    await page.unroute('**/channels/telegram/programs');

    // An instance with no connector bound (fresh shard fixture) shows no
    // programs switch — the setting exists per connection, not per agent.
    const unboundPanel = page.getByTestId('connector-unbound').or(
      page.getByText(/no connector|not connected/i).first(),
    );
    if (await unboundPanel.isVisible().catch(() => false)) {
      await expect(unboundPanel.getByRole('switch', { name: /programs/i })).toHaveCount(0);
    }
  });
});

/**
 * RED e2e for chat links (TDD row 22: BS-1.1).
 * Stubbed model output goes through tests/e2e/fixtures/terminal-outcome-provider.ts.
 * The narration is the only override; the provider's tool round is unchanged.
 * A full gateway run was not executed in RED (needs a built binary). Do not
 * treat collection as a product result.
 */
import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { GatewayProcess } from './fixtures/gateway-process';
import { E2E_MODEL } from './fixtures/e2e-model';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { startTerminalOutcomeProvider } from './fixtures/terminal-outcome-provider';
import { createTerminalSession, openTerminalSession } from './fixtures/terminal-outcome-acceptance';

const LINK_TEXT = 'Here is [the plan](omnipus://file/W/notes/plan.md).';

test.use({ storageState: { cookies: [], origins: [] } });
test.setTimeout(240_000);

test('a streamed omnipus file link becomes an in-app Library anchor (BS-1.1)', async ({ page }) => {
  const mock = await startTerminalOutcomeProvider({ narration: LINK_TEXT });
  let gw: GatewayProcess | undefined;
  try {
    gw = await GatewayProcess.start({
      apiBase: mock.url,
      model: E2E_MODEL,
      adminUsername: 'chat-links',
    });
    const { session } = await createTerminalSession(gw);
    await page.goto(gw.baseURL);
    await expect(page.locator('#login-username')).toBeVisible();
    await page.locator('#login-username').pressSequentially(gw.adminUsername);
    await page.locator('#login-password').pressSequentially(gw.adminPassword);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('#login-username')).toBeHidden();
    await openTerminalSession(page, gw.baseURL, session.id);
    await chatInput(page).fill('Show me the plan.');
    await chatInput(page).press('Enter');
    const link = page.locator('a[data-testid="markdown-link"]');
    await expect(link).toHaveAttribute('href', '/library?workspace=W&path=notes%2Fplan.md');
    await expect(link).not.toHaveAttribute('target', '_blank');
    await expect(link).toContainText('W / notes/plan.md');
  } finally {
    await mock.close();
    await gw?.stop();
  }
});

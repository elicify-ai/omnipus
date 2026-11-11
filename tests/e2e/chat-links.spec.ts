/**
 * RED e2e for chat links (TDD row 22: BS-1.1).
 * Stubbed model output goes through tests/e2e/fixtures/terminal-outcome-provider.ts.
 * The narration is the only override; the provider's tool round is unchanged.
 * A full gateway run was not executed here (needs a built binary). The click
 * and the preview assertion are UNVERIFIED until CI runs this spec.
 */
import fs from 'node:fs';
import path from 'node:path';
import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { GatewayProcess } from './fixtures/gateway-process';
import { E2E_MODEL } from './fixtures/e2e-model';
import { chatInput } from './fixtures/selectors';
import { startTerminalOutcomeProvider } from './fixtures/terminal-outcome-provider';
import { createTerminalSession, openTerminalSession } from './fixtures/terminal-outcome-acceptance';

const PLAN_BODY = 'chat-links e2e plan body';

test.use({ storageState: { cookies: [], origins: [] } });
test.setTimeout(240_000);

test('a streamed omnipus file link opens that file in the Library (BS-1.1)', async ({ page }) => {
  const mock = await startTerminalOutcomeProvider();
  let gw: GatewayProcess | undefined;
  try {
    gw = await GatewayProcess.start({
      apiBase: mock.url,
      model: E2E_MODEL,
      adminUsername: 'chat-links',
    });
    const { session } = await createTerminalSession(gw);
    const workspaceId = session.workspace_id;
    if (!workspaceId) throw new Error('the chat session has no workspace, so the Library link has nowhere to open');
    const filePath = path.join(gw.homeDir, 'workspaces', workspaceId, 'work', 'notes', 'plan.md');
    fs.mkdirSync(path.dirname(filePath), { recursive: true });
    fs.writeFileSync(filePath, PLAN_BODY);
    const href = `/#/library?${new URLSearchParams({ workspace: workspaceId, path: 'notes/plan.md' }).toString()}`;
    mock.setNarration(`Here is [the plan](omnipus://file/${workspaceId}/notes/plan.md).`);
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
    // Hash history (src/main.tsx createHashHistory). The spec's page is
    // /library?workspace=&path=; this app addresses it as /#/library?...
    await expect(link).toHaveAttribute('href', href);
    await expect(link).not.toHaveAttribute('target', '_blank');
    await expect(link).toContainText(`${workspaceId} / notes/plan.md`);
    await link.click();
    await expect(page.getByTestId('library-preview-title')).toHaveText('plan.md', { timeout: 20_000 });
    await expect(page.getByTestId('library-preview-pane')).toContainText(PLAN_BODY);
  } finally {
    await mock.close();
    await gw?.stop();
  }
});

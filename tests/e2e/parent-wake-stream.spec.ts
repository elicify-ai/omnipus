/**
 * Parent-wake visibility — Test 26.
 *
 * Oracle: docs/internal/specs/parent-wake-visibility-spec.md
 *   BDD "Open parent chat shows the woken reply live"
 *   BDD "Reload shows the reply the live chat showed"
 *   BDD "Two browsers share one wake turn"
 *   FR-003. Issue #1317.
 *
 * Two browsers on one parent session. The scripted provider holds the wake
 * stream after the first partial token and before completion. Both browsers
 * show that token. A session in another workspace does not. Reload then shows
 * the finished reply.
 *
 * Not run locally: this spec needs the gateway binary and runs in CI.
 * Step 4 items 2–3 are deferred to CHECK.
 */
import { expect, type BrowserContext, type Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { GatewayProcess } from './fixtures/gateway-process';
import { E2E_MODEL } from './fixtures/e2e-model';
import { chatInput, waitForConnected } from './fixtures/selectors';
import { openTerminalSession } from './fixtures/terminal-outcome-acceptance';
import { assertSessionHistoryExcludes } from './fixtures/session-history';
import {
  PARENT_WAKE_PARTIAL,
  PARENT_WAKE_TASK,
  startParentWakeProvider,
} from './fixtures/parent-wake-provider';
import { Agent, Workspace, Session } from '../../src/lib/api/generated/schemas';
import type { AgentCreateRequestMain, WorkspaceCreateRequest, SessionCreateRequest } from '../../src/lib/api/generated/openapi-types';

test.use({ storageState: { cookies: [], origins: [] } });
test.setTimeout(240_000);

test('a woken parent reply streams to both browsers without a reload', async ({ page, browser }) => {
  const provider = await startParentWakeProvider();
  let gw: GatewayProcess | undefined;
  let second: BrowserContext | undefined;
  try {
    gw = await GatewayProcess.start({
      apiBase: provider.url,
      model: E2E_MODEL,
      adminUsername: 'parent-wake-stream',
    });
    const agentResult = await gw.apiFetch('POST', '/api/v1/agents', {
      type: 'Main',
      name: 'Parent wake',
      soul: 'When asked, delegate the task to yourself with the delegate tool and do not poll for the result.',
      provider: 'openrouter',
      model: E2E_MODEL,
    } satisfies AgentCreateRequestMain);
    expect(agentResult.ok, agentResult.raw).toBe(true);
    const agent = Agent.parse(agentResult.body);
    provider.setAgentId(agent.id);

    const workspaceA = await gw.apiFetch('POST', '/api/v1/workspaces', {
      name: 'Parent wake A',
      core_team: [agent.id],
    } satisfies WorkspaceCreateRequest);
    expect(workspaceA.ok, workspaceA.raw).toBe(true);
    const workspaceB = await gw.apiFetch('POST', '/api/v1/workspaces', {
      name: 'Parent wake B',
      core_team: [agent.id],
    } satisfies WorkspaceCreateRequest);
    expect(workspaceB.ok, workspaceB.raw).toBe(true);
    const parentResult = await gw.apiFetch('POST', '/api/v1/sessions', {
      type: 'chat',
      agent_id: agent.id,
      workspace_id: Workspace.parse(workspaceA.body).id,
    } satisfies SessionCreateRequest);
    expect(parentResult.ok, parentResult.raw).toBe(true);
    const parent = Session.parse(parentResult.body);
    const otherResult = await gw.apiFetch('POST', '/api/v1/sessions', {
      type: 'chat',
      agent_id: agent.id,
      workspace_id: Workspace.parse(workspaceB.body).id,
    } satisfies SessionCreateRequest);
    expect(otherResult.ok, otherResult.raw).toBe(true);
    const other = Session.parse(otherResult.body);

    await login(page, gw);
    await openTerminalSession(page, gw.baseURL, parent.id);
    const state = await page.context().storageState();
    second = await browser.newContext({ storageState: state });
    const pageB = await second.newPage();
    await openTerminalSession(pageB, gw.baseURL, parent.id);

    await chatInput(page).fill(`Delegate this and do not poll: ${PARENT_WAKE_TASK}`);
    await chatInput(page).press('Enter');

    await expect(page.getByText(PARENT_WAKE_PARTIAL, { exact: false })).toBeVisible({ timeout: 90_000 });
    await expect(pageB.getByText(PARENT_WAKE_PARTIAL, { exact: false })).toBeVisible({ timeout: 30_000 });
    expect(provider.failures(), 'scripted provider stayed on the delegate / helper / wake sequence').toEqual([]);

    // Browser login replaced gw's cookie session; use the browser's current cookies.
    // HTTP success/shape/progress checks live in the shared real helper; the
    // original absence assertion still covers the ENTIRE retained history.
    const otherMessages = await assertSessionHistoryExcludes(page, gw.baseURL, other.id, PARENT_WAKE_PARTIAL);
    expect(JSON.stringify(otherMessages)).not.toContain(PARENT_WAKE_PARTIAL);

    provider.releaseWake();
    await page.reload();
    await expect(page.locator(`[data-active-session-id="${parent.id}"]`)).toBeVisible();
    await waitForConnected(page);
    await expect(page.getByText(`${PARENT_WAKE_PARTIAL} and the rest`, { exact: false })).toBeVisible();
  } finally {
    await second?.close();
    await provider.close();
    await gw?.stop();
  }
});

async function login(page: Page, gw: GatewayProcess): Promise<void> {
  await page.goto(gw.baseURL);
  await expect(page.locator('#login-username')).toBeVisible();
  await page.locator('#login-username').pressSequentially(gw.adminUsername);
  await page.locator('#login-password').pressSequentially(gw.adminPassword);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('#login-username')).toBeHidden();
}

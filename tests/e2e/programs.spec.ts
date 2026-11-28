/**
 * S1 rev 8, order 36: S01/S12/S33/S53/S81 browser surfaces.
 * Each test/retry imports an owned gateway, fake gh, account and clean home.
 * S12's selected private GH_TOKEN is seeded through Dev's real request_secret card.
 * This file complements (does not replace) the complete D8/body/store Go rows.
 */
import type { Page } from '@playwright/test';
import { Agent } from '../../src/lib/api/generated/schemas';
import { test, expect } from './fixtures/programs-s1';

async function openProgramsTab(page: Page, origin: string): Promise<void> {
  await page.goto(`${origin}/#/settings`);
  const tab = page.getByRole('tab', { name: 'Programs', exact: true });
  await expect(tab).toBeVisible(); await tab.click();
  await expect(page.getByRole('tabpanel').filter({ visible: true })).toBeVisible();
}

async function openProfile(page: Page, origin: string, workspaceId: string, devId: string) {
  await page.goto(`${origin}/#/workspaces/${workspaceId}/chat?panel=team`);
  const team = page.getByTestId('workspace-team-tab');
  await expect(team).toBeVisible();
  await team.getByTestId(`team-card-actions-${devId}`).click();
  await page.getByRole('button', { name: 'Edit Dev', exact: true }).click();
  const profile = page.getByTestId('agent-profile-sheet');
  await expect(profile).toBeVisible();
  return profile;
}

async function githubConnection(gateway: import('./fixtures/gateway-process').GatewayProcess) {
  const result = await gateway.apiFetch('GET', '/api/v1/programs');
  expect(result.status, result.raw).toBe(200);
  const entries = (result.body as Record<string, unknown>).entries as Array<Record<string, unknown>>;
  const github = entries.find((entry) => entry.id === 'github');
  expect(github, 'real catalog contains GitHub').toBeDefined();
  return github!.connection as Record<string, unknown>;
}

test.describe('Programs — private real gateway', () => {
  test('S01: held check shows checking generation 1 then connected as octocat', async ({ page, programsS1: f }) => {
    f.holdCheck();
    await openProgramsTab(page, f.gateway.baseURL);
    const row = page.getByRole('row', { name: /github/i }).or(page.getByTestId('program-github'));
    await expect(row.getByText('Not connected', { exact: true })).toBeVisible();
    await row.getByRole('button', { name: /paste a token/i }).click();
    // The paste dialog's own title ("Paste a token for GitHub") also labels
    // its dialog element, so the label lookup is narrowed to the input itself.
    const field = page.locator('input').and(page.getByLabel(/token/i)).filter({ visible: true }).first();
    await field.fill('tok_valid_12345');
    const saved = page.waitForResponse((r) => r.url().endsWith('/api/v1/programs/github/credential') && r.request().method() === 'PUT');
    await field.press('Enter');
    const response = await saved;
    expect(response.status()).toBe(200);
    expect(await response.text()).not.toContain('tok_valid_12345');
    await expect.poll(f.starts).toBe(1);
    expect(await githubConnection(f.gateway)).toMatchObject({ state: 'checking', generation: 1 });
    await expect(row.getByText('Checking the account…', { exact: true })).toBeVisible();
    f.releaseCheck();
    await expect.poll(async () => (await githubConnection(f.gateway)).state).toBe('connected');
    expect(await githubConnection(f.gateway)).toMatchObject({ generation: 1, account_label: 'octocat' });
    await expect(row.getByText('Connected as octocat', { exact: true })).toBeVisible();
    expect(await page.locator('body').innerText()).not.toContain('tok_valid_12345');
  });

  test('S12: real selected private GH_TOKEN refuses switch-on in Dev profile', async ({ page, programsS1: f }) => {
    await f.seedPrivateGH_TOKEN(); // Given: Dev's own card stores and selects GH_TOKEN in W
    const profile = await openProfile(page, f.gateway.baseURL, f.workspaceId, f.devId);
    const toggle = profile.getByRole('switch', { name: /github/i });
    await expect(toggle).not.toBeChecked();
    const refusal = page.waitForResponse((r) => r.url().endsWith(`/agents/${f.devId}`) && r.request().method() === 'PUT');
    await toggle.click();
    expect((await refusal).status()).toBe(409);
    await expect(profile.getByText('GitHub cannot be switched on for Dev: Dev already has a private variable GH_TOKEN. Remove that variable, or leave GitHub off.', { exact: true })).toBeVisible();
    await expect(toggle).not.toBeChecked();
    const dev = await f.gateway.apiFetch('GET', `/api/v1/agents/${f.devId}`);
    expect(dev.status).toBe(200);
    expect((dev.body as Record<string, unknown>).programs).toEqual([]);
  });

  test('S33: clean catalog creates Acme once and reloads it not_connected', async ({ page, programsS1: f }) => {
    await openProgramsTab(page, f.gateway.baseURL);
    // Spec UI-state table, Settings → Programs row: "Add a custom program".
    await page.getByRole('button', { name: 'Add a custom program', exact: true }).click();
    await page.getByLabel(/display name/i).fill('Acme');
    await page.getByLabel(/program words?/i).fill('acme');
    await page.getByLabel(/token variables?/i).fill('ACME_TOKEN');
    await page.getByLabel(/network sites?/i).fill('api.acme.example');
    const created = page.waitForResponse((r) => r.url().endsWith('/api/v1/programs') && r.request().method() === 'POST');
    await page.getByRole('button', { name: /^create$|^save$/i }).click();
    expect((await created).status()).toBe(201);
    await page.reload();
    await page.getByRole('tab', { name: 'Programs', exact: true }).click();
    const acme = page.getByRole('row', { name: /acme/i }).or(page.getByTestId('program-acme'));
    await expect(acme.getByText('Not connected', { exact: true })).toBeVisible();
    expect(f.starts()).toBe(0);
  });

  test('S53: independently connect and enable Dev, disconnect leaves profile switch on', async ({ page, programsS1: f }) => {
    const saved = await f.gateway.apiFetch('PUT', '/api/v1/programs/github/credential', { value: 'tok_valid_12345' });
    expect(saved.status, saved.raw).toBe(200);
    await expect.poll(async () => (await githubConnection(f.gateway)).state).toBe('connected');
    const before = await githubConnection(f.gateway);
    const dev = await f.gateway.apiFetch('GET', `/api/v1/agents/${f.devId}`);
    expect(dev.status).toBe(200);
    const revision = Agent.parse(dev.body).revision;
    const enabled = await f.gateway.apiFetch('PUT', `/api/v1/agents/${f.devId}`, { revision, programs: ['github'] });
    expect(enabled.status, enabled.raw).toBe(200);
    const readback = await f.gateway.apiFetch('GET', `/api/v1/agents/${f.devId}`);
    expect((readback.body as Record<string, unknown>).programs).toEqual(['github']);
    await openProgramsTab(page, f.gateway.baseURL);
    const row = page.getByRole('row', { name: /github/i }).or(page.getByTestId('program-github'));
    await expect(row.getByText('Connected as octocat', { exact: true })).toBeVisible();
    await row.getByRole('button', { name: /^disconnect$/i }).click();
    const dialog = page.getByRole('dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(/stays valid at GitHub.*revok/i)).toBeVisible();
    await dialog.getByRole('button', { name: /^disconnect$/i }).click();
    await expect(row.getByText('Not connected', { exact: true })).toBeVisible();
    expect(await githubConnection(f.gateway)).toMatchObject({ state: 'not_connected', generation: Number(before.generation) + 1 });
    const profile = await openProfile(page, f.gateway.baseURL, f.workspaceId, f.devId);
    await expect(profile.getByRole('switch', { name: /github/i })).toBeChecked();
    // Next-command no-token is a separate named runtime BLOCKED row (R19).
  });

  test('S81: configured own bearer, no cookie, exact person refusal and unchanged state', async ({ programsS1: f }) => {
    const before = await f.gateway.apiFetch('GET', '/api/v1/programs');
    const agentBefore = await f.gateway.apiFetch('GET', `/api/v1/agents/${f.devId}`);
    const starts = f.starts();
    // Node fetch has no browser cookie jar and keeps the ephemeral bearer out
    // of Playwright traces (GatewayProcess.reloadBearer's documented contract).
    const headers = { Authorization: `Bearer ${f.gateway.reloadBearer()}` };
    const ordinary = new Request(`${f.gateway.baseURL}/api/v1/agents/${f.devId}`, { headers });
    expect(ordinary.headers.has('Cookie')).toBe(false);
    expect((await fetch(ordinary)).status, 'known existing route accepts this exact configured bearer').toBe(200);
    const attack = new Request(`${f.gateway.baseURL}/api/v1/programs/github/credential`, { method: 'PUT', headers: { ...headers, 'Content-Type': 'application/json' }, body: JSON.stringify({ value: 'tok_valid_12345' }) });
    expect(attack.headers.has('Cookie')).toBe(false);
    const denied = await fetch(attack);
    expect(denied.status).toBe(403);
    expect(await denied.text()).toContain('this action requires a signed-in person');
    expect((await f.gateway.apiFetch('GET', '/api/v1/programs')).body).toEqual(before.body);
    expect((await f.gateway.apiFetch('GET', `/api/v1/agents/${f.devId}`)).body).toEqual(agentBefore.body);
    expect(f.starts()).toBe(starts);
    // This UI-edge test does not inspect encrypted-store metadata/body reads;
    // D8 row 3 has those same-request Go oracles. H20 records that boundary.
  });
});

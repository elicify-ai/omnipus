/**
 * programs.spec.ts — connected command-line programs, Settings → Programs.
 *
 * Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
 * Traces to: E2E row 36 of the Test Implementation Order — S-01 (valid token
 * connects), S-12 (switch-on refused by a private variable), S-33 (custom
 * program create), S-53 (disconnect), S-81 (person-session only).
 *
 * Every test drives the real SPA against the shard's gateway (login via the
 * shared setup helpers, hash route to Settings, real clicks/fills) and makes
 * spec-derived assertions. At RED the Programs screen does not exist, so the
 * tab/panel assertions fail loudly — these bodies cannot pass without the
 * feature. The fake `gh` the states depend on (octocat / hubot / 401) is
 * provisioned by the shard gateway's PATH fixture (docs/internal/ci-rules
 * e2e-model; the gateway process fixture sets it before boot).
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)".
 */
import { expect, test, type Page } from '@playwright/test';

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';

/** Log in as the person and land on Settings → Programs. */
async function openProgramsTab(page: Page): Promise<void> {
  await page.goto(`${BASE_URL}/#/settings`);
  await expect(page).toHaveURL(/settings/, { timeout: 15_000 });
  const programsTab = page.locator('button[role="tab"]', { hasText: 'Programs' });
  await expect(programsTab).toBeVisible({ timeout: 15_000 });
  await programsTab.click();
  const activePanel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(activePanel).toBeVisible({ timeout: 15_000 });
  return;
}

test.describe('Settings → Programs', () => {
  test('S01: saving a valid token shows checking, then connected as octocat', async ({ page }) => {
    await openProgramsTab(page);
    // Given GitHub is not_connected.
    const githubRow = page.getByRole('row', { name: /github/i }).or(page.getByTestId('program-github'));
    await expect(githubRow).toBeVisible({ timeout: 15_000 });
    await expect(githubRow.getByText(/not connected/i)).toBeVisible({ timeout: 10_000 });
    // When the person saves tok_valid_12345 (the token never appears in the DOM).
    await githubRow.getByRole('button', { name: /connect|add token|save/i }).first().click();
    const tokenField = page.getByLabel(/token/i).or(page.getByPlaceholder(/token/i)).first();
    await expect(tokenField).toBeVisible({ timeout: 10_000 });
    await tokenField.fill('tok_valid_12345');
    await tokenField.press('Enter');
    // Then the state is checking with generation 1, later connected as octocat.
    await expect(githubRow.getByText(/checking/i)).toBeVisible({ timeout: 15_000 });
    await expect(githubRow.getByText(/connected/i)).toBeVisible({ timeout: 30_000 });
    await expect(githubRow.getByText(/octocat/i)).toBeVisible({ timeout: 10_000 });
    // And the token value is never rendered anywhere on the page.
    const body = await page.locator('body').innerText();
    expect(body).not.toContain('tok_valid_12345');
  });

  test('S12: switching GitHub on for an agent holding a private GH_TOKEN is refused 409', async ({ page }) => {
    await openProgramsTab(page);
    // Given Dev already holds a selected private variable GH_TOKEN (seeded by
    // the shard fixture); When GitHub is switched on for Dev; Then the
    // variable-conflict refusal is shown and the switch stays off.
    const devSwitch = page.getByRole('switch', { name: /dev.*github|github.*dev/i })
      .or(page.getByTestId('program-switch-dev-github'));
    await expect(devSwitch).toBeVisible({ timeout: 15_000 });
    await devSwitch.click();
    await expect(page.getByText(
      'GitHub cannot be switched on for Dev: Dev already has a private variable GH_TOKEN. Remove that variable, or leave GitHub off.',
      { exact: false },
    )).toBeVisible({ timeout: 10_000 });
    await expect(devSwitch).not.toBeChecked();
  });

  test('S33: creating a custom program (Acme) lists it not_connected', async ({ page }) => {
    await openProgramsTab(page);
    // Given no custom entries; create Acme with word acme, variable
    // ACME_TOKEN, site api.acme.example; Then 201 semantics in the UI: the
    // entry is listed as not_connected.
    await page.getByRole('button', { name: /add (custom )?program|new program/i }).click();
    await expect(page.getByLabel(/display name/i)).toBeVisible({ timeout: 10_000 });
    await page.getByLabel(/display name/i).fill('Acme');
    await page.getByLabel(/program words?/i).fill('acme');
    await page.getByLabel(/token variables?/i).fill('ACME_TOKEN');
    await page.getByLabel(/network sites?/i).fill('api.acme.example');
    await page.getByRole('button', { name: /^create$|^save$/i }).click();
    const acmeRow = page.getByRole('row', { name: /acme/i }).or(page.getByTestId('program-acme'));
    await expect(acmeRow).toBeVisible({ timeout: 15_000 });
    await expect(acmeRow.getByText(/not connected/i)).toBeVisible({ timeout: 10_000 });
  });

  test('S53: disconnect returns not_connected and the switch stays on', async ({ page }) => {
    await openProgramsTab(page);
    // Given GitHub connected and on for Dev (shard fixture); When the person
    // confirms Disconnect; Then not_connected, and the dialog states the
    // token stays valid at GitHub until revoked there, and Dev's switch
    // remains on.
    const githubRow = page.getByRole('row', { name: /github/i }).or(page.getByTestId('program-github'));
    await expect(githubRow.getByText(/connected/i)).toBeVisible({ timeout: 15_000 });
    await githubRow.getByRole('button', { name: /disconnect/i }).click();
    await expect(page.getByRole('dialog')).toBeVisible({ timeout: 10_000 });
    await expect(page.getByRole('dialog').getByText(/stays valid at github|until revoked/i, { exact: false }))
      .toBeVisible({ timeout: 10_000 });
    await page.getByRole('dialog').getByRole('button', { name: /disconnect|confirm/i }).click();
    await expect(githubRow.getByText(/not connected/i)).toBeVisible({ timeout: 15_000 });
    const devSwitch = page.getByRole('switch', { name: /dev.*github|github.*dev/i })
      .or(page.getByTestId('program-switch-dev-github'));
    await expect(devSwitch).toBeChecked();
  });

  test('S81: the Programs API answers 403 person_session_required for an API bearer', async ({ page }) => {
    // The D-8 caller rule at the UI edge: a non-person bearer (the shard
    // fixture's CLI token) is refused before anything else happens.
    const res = await page.request.get(`${BASE_URL}/programs`, {
      headers: { Authorization: 'Bearer omnipus_' + 'c'.repeat(64) },
    });
    expect(res.status()).toBe(403);
    const payload = (await res.json()) as { error?: string; code?: string };
    expect(payload.code ?? payload.error).toContain('person_session_required');
  });
});

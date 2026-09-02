import { test, expect, type Page } from '@playwright/test';

/**
 * UAT group: the audit trail and the operator controls.
 *
 * Covers browser-rework-uat-plan.md UAT-46 (every action on a signed-in site is
 * recorded) and UAT-37 (there is no browser limit setting), plus the two things
 * the group brief calls out by name:
 *
 *   1. The Audit Log screen must RENDER records whose event names contain dots
 *      (`skill.call`, `browser.live.control_taken`). The shipped defect was one
 *      badly-named record blanking the entire viewer with "Failed to load audit
 *      log" — `src/lib/api.ts::request` throws ApiSchemaError on the whole
 *      response, it does not drop a single row. Regression surface is the zod
 *      `event: z.string().regex(...)` generated from
 *      contracts/components/schemas/AuditEntry.yaml.
 *
 *   2. The viewer's event-type filter is a hardcoded list. If a dotted event
 *      renders but is not selectable, an operator cannot isolate the browser
 *      records that this rework makes visible. That is asserted, not assumed.
 *
 * These drive the REAL running gateway's SPA bundle. The audit *content* is
 * supplied by intercepting GET /api/v1/audit-log, because on a gateway booted
 * without `sandbox.audit_log:true` the product never writes an audit file at
 * all — see the "audit is off" test, which asserts that observable state rather
 * than papering over it.
 *
 * No global-setup: this file is run against a shared UAT gateway with
 * dev_mode_bypass on. It never writes to the gateway's config.
 */

const AUDIT_URL = '**/api/v1/audit-log';

/** A record shaped exactly like pkg/audit writes one for a browser tool call. */
function browserToolCall(overrides: Record<string, unknown> = {}) {
  return {
    timestamp: '2026-09-02T10:00:00Z',
    event: 'tool_call',
    decision: 'allow',
    agent_id: 'jim',
    session_id: 'sess_uat',
    tool: 'browser_click',
    parameters: { url: 'https://the-internet.herokuapp.com/secure', role: 'button', name: 'Logout' },
    ...overrides,
  };
}

async function openAuditLog(page: Page) {
  await page.goto('/#/settings?tab=security');
  const securityTab = page.locator('button[role="tab"]', { hasText: 'Security' });
  await expect(securityTab).toBeVisible({ timeout: 20_000 });
  await securityTab.click();

  // The viewer sits behind the ONE AdvancedDisclosure in SecuritySection
  // ("Process isolation, tool grid, audit log — safe to skip"), and opens from
  // its "View Log" button.
  const panel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(panel).toBeVisible({ timeout: 20_000 });
  const disclosure = panel.getByRole('button', { name: /Advanced \/ technical details/i }).first();
  await expect(disclosure).toBeVisible({ timeout: 20_000 });
  await disclosure.click();

  const opener = panel.getByRole('button', { name: /^View Log$/ }).first();
  await expect(opener).toBeVisible({ timeout: 20_000 });
  await opener.click();

  const dialog = page.getByRole('dialog').filter({ hasText: 'Audit Log' }).first();
  await expect(dialog).toBeVisible({ timeout: 20_000 });
  return dialog;
}

test.describe('audit log viewer', () => {
  test('(a) renders dotted event names instead of blanking the whole screen', async ({ page }) => {
    // Both names the brief calls out, plus the flat family, in one response.
    await page.route(AUDIT_URL, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          chain_status: 'valid',
          entries: [
            { timestamp: '2026-09-02T10:00:03Z', event: 'browser.live.control_taken', decision: 'allow', agent_id: 'ray', details: { workspace: 'alpha' } },
            { timestamp: '2026-09-02T10:00:02Z', event: 'skill.call', decision: 'allow', agent_id: 'mia', tool: 'skill' },
            browserToolCall(),
          ],
        }),
      }),
    );

    const dialog = await openAuditLog(page);

    // The failure mode under test: one bad name => this message, zero rows.
    await expect(dialog).not.toContainText('Failed to load audit log');
    await expect(dialog).toContainText('browser.live.control_taken');
    await expect(dialog).toContainText('skill.call');
    // The footer proves the viewer accepted every row, not just the ones it styles.
    await expect(dialog).toContainText('Showing 3 of 3 entries');
  });

  test('(b) a browser tool call names the agent, the action and the site', async ({ page }) => {
    await page.route(AUDIT_URL, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ chain_status: 'valid', entries: [browserToolCall()] }),
      }),
    );

    const dialog = await openAuditLog(page);
    const row = dialog.locator('tbody tr').first();

    // "which agent did that, on which site" must be answerable from the row.
    await expect(row).toContainText('jim');
    await expect(row).toContainText('browser_click');
    // The site lives in the collapsed detail; the caret is the only expander.
    await row.getByRole('button', { name: /Show details for this .* entry/i }).click();
    await expect(dialog).toContainText('the-internet.herokuapp.com');
  });

  test('(f) one unrecognised event name does not take the whole viewer down with it', async ({ page }) => {
    // The shipped defect was response-level, not row-level: src/lib/api.ts::request
    // validates the WHOLE AuditLogResponse, so a single entry the zod pattern
    // rejects throws ApiSchemaError and the operator sees "Failed to load audit
    // log" instead of the records that ARE fine. Widening the pattern to allow
    // dots fixed today's bad names; this asks whether the fragile shape is gone.
    // An audit file is append-only history nobody can rewrite, so the next name
    // that does not fit the pattern has the same blast radius.
    await page.route(AUDIT_URL, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          chain_status: 'valid',
          entries: [
            { timestamp: '2026-09-02T10:00:04Z', event: 'Browser.Live', decision: 'allow', agent_id: 'ray' },
            browserToolCall(),
          ],
        }),
      }),
    );

    const dialog = await openAuditLog(page);
    const body = (await dialog.innerText()).trim();
    expect(
      body,
      `one unrecognised event name blanked the viewer — dialog read: "${body}"`,
    ).not.toMatch(/Failed to load audit log/i);
    // The good record must survive whatever happens to the bad one.
    await expect(dialog).toContainText('browser_click');
  });

  test('(c) the event-type filter can isolate the newly-visible browser records', async ({ page }) => {
    await page.route(AUDIT_URL, (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          chain_status: 'valid',
          entries: [
            // The exact names pkg/audit emits for this rework:
            // EventBrowserAction, EventBrowserInstanceCreated,
            // EventBrowserLiveControlTaken (pkg/audit/events.go).
            { timestamp: '2026-09-02T10:00:05Z', event: 'browser_action', decision: 'allow', agent_id: 'jim', tool: 'browser_click', details: { host: 'the-internet.herokuapp.com' } },
            { timestamp: '2026-09-02T10:00:04Z', event: 'browser_instance_created', decision: 'allow', agent_id: 'jim' },
            { timestamp: '2026-09-02T10:00:03Z', event: 'browser.live.control_taken', decision: 'allow', agent_id: 'ray' },
          ],
        }),
      }),
    );

    const dialog = await openAuditLog(page);
    await dialog.getByLabel('Event type filter').click();

    const options = await page.getByRole('option').allInnerTexts();
    // The operator question: having made browser activity visible, can an
    // operator isolate it? EVENT_TYPE_OPTIONS in AuditLogViewer.tsx is a
    // hardcoded ten-name list that predates every browser event.
    expect(
      options.some((o) => o.includes('browser')),
      `event filter offers no browser option — options were: ${options.join(', ')}`,
    ).toBe(true);
  });

  test('(g) the viewer opens against the live gateway, on whatever it really holds', async ({ page }) => {
    // No interception: the plain "does the screen load" check UAT-46 step 2
    // asks for. Note that an EMPTY list here is not proof auditing works — the
    // logger is only constructed when sandbox.audit_log is true AT BOOT
    // (pkg/agent/loop.go, `if cfg.Sandbox.AuditLog`), so a gateway started
    // without it writes no system/audit.jsonl at all and this screen is
    // legitimately, permanently empty.
    const dialog = await openAuditLog(page);
    // Wait for a SETTLED state — the loading skeleton has no text at all, so an
    // immediate snapshot would report an empty dialog either way.
    await expect(dialog).toContainText(/No audit entries found|Showing \d+ of \d+ entr|Failed to load audit log/);
    const body = (await dialog.innerText()).trim();
    expect(body, `audit viewer read: "${body}"`).not.toMatch(/Failed to load audit log/i);
    console.log(`[UAT-46] live audit viewer rendered:\n${body}`);
  });
});

test.describe('operator controls', () => {
  test('(d) Settings offers no browser or tab limit control', async ({ page }) => {
    await page.goto('/#/settings');
    await expect(page.locator('button[role="tab"]').first()).toBeVisible({ timeout: 20_000 });

    const tabs = await page.locator('button[role="tab"]').allInnerTexts();
    // UAT-37 step 1: there must be no such setting, and no help text claiming one.
    expect(tabs.map((t) => t.toLowerCase()).some((t) => t.includes('browser'))).toBe(false);

    for (const tab of await page.locator('button[role="tab"]').all()) {
      await tab.click();
      const panel = page.locator('[role="tabpanel"][data-state="active"]').first();
      await expect(panel).toBeVisible({ timeout: 20_000 });
      await expect(panel).not.toContainText(/max browsers|maximum browsers|tab budget|max tabs/i);
    }
  });

  test('(e) Performance shows "automatic", never a 2000-shaped recommendation', async ({ page }) => {
    await page.goto('/#/settings?tab=performance');
    const perfTab = page.locator('button[role="tab"]', { hasText: 'Performance' });
    await expect(perfTab).toBeVisible({ timeout: 20_000 });
    await perfTab.click();

    const panel = page.locator('[role="tabpanel"][data-state="active"]').first();
    await expect(panel).toBeVisible({ timeout: 20_000 });

    // The specific silent failure UAT-37 names: an unasked-for number presented
    // as capacity on an install where nothing was configured.
    await expect(panel).not.toContainText(/Live system recommendation/i);
    await expect(panel).not.toContainText(/2000 parallel agents/);

    // ...but "no 2000" is worthless if the tab rendered nothing at all, so the
    // read has to be proved to have happened. GET /api/v1/performance is behind
    // adminWrap/RequireNotBypass, which 503s a READ while dev_mode_bypass is on.
    const text = (await panel.innerText()).trim();
    expect(
      text,
      `Performance tab did not render its settings — panel text was: "${text}"`,
    ).not.toMatch(/Could not load performance settings/i);
    await expect(panel).toContainText(/automatic/i);
  });
});

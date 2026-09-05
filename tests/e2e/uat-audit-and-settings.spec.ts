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

  /**
   * The environment fact this test is built on, established rather than assumed:
   *
   *   GET /api/v1/performance is registered with adminWrap — withAuth THEN
   *   RequireNotBypass (pkg/gateway/rest.go, `a.adminWrap(a.HandlePerformance)`)
   *   — and RequireNotBypass answers 503 on every high-blast-radius admin route
   *   while `gateway.dev_mode_bypass` is true. The E2E gateway is seeded with
   *   `"dev_mode_bypass": true` (.github/workflows/pr.yml, "Seed gateway config";
   *   the same value tests/e2e/global-setup.ts writes when no config exists),
   *   because that is what lets the suite drive onboarding before an admin
   *   account exists.
   *
   * So the endpoint CANNOT return settings here. The previous version of this
   * test asked the panel to render loaded content anyway, and once the panel was
   * fixed to say WHY a load failed instead of rendering blank (3f3068104) that
   * correct behaviour is what the assertion caught. Its own guard against a
   * blank read could not have saved it either: it looked for "Could not load
   * performance settings", a string the product has never emitted — the real
   * wording is "Failed to load performance settings".
   *
   * Turning bypass off for the suite was rejected: it is load-bearing for
   * onboarding and for auth.spec.ts (c), which asserts the bypass banner itself,
   * and RequireNotBypass is a deliberate security control.
   *
   * The UAT-37 property is therefore checked in the two places it CAN be:
   *
   *   (e1) unintercepted — the real 503 must be EXPLAINED, never blank and never
   *        a number, which is the 3f3068104 guarantee in its real environment;
   *   (e2) intercepted — the shipped bundle, handed the payload a genuinely
   *        unconfigured install produces, must render "automatic" and must not
   *        present the backstop integer as a recommendation.
   *
   * (e2) follows the interception precedent this file already sets for the audit
   * log above: the gateway cannot supply the state, so the state is supplied and
   * the REAL rendering is what gets asserted. The component-level twin lives in
   * src/components/settings/PerformanceSection.autoDefault.test.tsx.
   */
  async function openPerformanceTab(page: Page) {
    await page.goto('/#/settings?tab=performance');
    const perfTab = page.locator('button[role="tab"]', { hasText: 'Performance' });
    await expect(perfTab).toBeVisible({ timeout: 20_000 });
    await perfTab.click();
    const panel = page.locator('[role="tabpanel"][data-state="active"]').first();
    await expect(panel).toBeVisible({ timeout: 20_000 });
    return panel;
  }

  test('(e1) a Performance read the gateway refuses is explained, never blank', async ({ page }) => {
    const panel = await openPerformanceTab(page);

    // The panel must SAY something went wrong and offer the way out. A blank
    // tab is the failure mode 3f3068104 fixed, and it is indistinguishable
    // from "this install has no performance settings" to the operator reading
    // it. Asserting the Retry control (not just the sentence) also proves the
    // error branch rendered in full rather than a stray toast.
    await expect(panel).toContainText(/Failed to load performance settings/i);
    await expect(panel.locator('[data-testid="performance-retry-btn"]')).toBeVisible();

    // And even while failing it must not invent capacity.
    await expect(panel).not.toContainText(/Live system recommendation/i);
    await expect(panel).not.toContainText(/2000 parallel agents/);
  });

  test('(e2) an unconfigured install shows "automatic", never a 2000-shaped recommendation', async ({
    page,
  }) => {
    // Exactly what getPerformance (pkg/gateway/rest_performance.go) returns on
    // an install where nothing is configured: max_parallel_agents echoes the
    // resolved effective value because 0 on disk is a sentinel that is never
    // schema-valid to send, effective_max_parallel_agents carries the PHYSICAL
    // OS-thread backstop, and max_parallel_agents_configured:false is the only
    // thing distinguishing this from an operator who deliberately typed 2000.
    //
    // The backstop is put in BOTH integers on purpose. This payload hands the
    // SPA the very number the shipped defect displayed, so an assertion that
    // "2000 parallel agents" is absent can only pass if the SPA is genuinely
    // reading max_parallel_agents_configured rather than rendering the integer.
    await page.route('**/api/v1/performance', async (route) => {
      if (route.request().method() !== 'GET') {
        await route.continue();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          max_parallel_agents: 2000,
          effective_max_parallel_agents: 2000,
          max_parallel_agents_configured: false,
          tools_on_demand: true,
        }),
      });
    });

    const panel = await openPerformanceTab(page);

    // The read is proved to have happened before anything is concluded from an
    // absence: a panel still showing the load error tells us nothing about
    // whether 2000 would have been rendered.
    await expect(panel.locator('[data-testid="performance-retry-btn"]')).toHaveCount(0);
    await expect(panel).toContainText(/Max parallel agents/i);

    // The UAT-37 property itself. The backstop integer must not appear AT ALL
    // — not as a recommendation, not as the effective value, not in the input.
    // Under this payload 2000 has no legitimate source other than the field
    // the panel is required to ignore, so the bare-number assertion is the
    // strongest available here and carries no false positives.
    //
    // (Deliberately NOT a bare /Recommended/i: the Tool-loading control below
    // says "Smaller messages, lower token use. Recommended." about an entirely
    // different setting, and a guard that trips on it would be reporting the
    // wrong defect. MEASURED — that is exactly what the first draft did.)
    await expect(panel).toContainText(/automatic/i);
    await expect(panel).not.toContainText(/Live system recommendation/i);
    await expect(panel).not.toContainText('2000');
  });
});

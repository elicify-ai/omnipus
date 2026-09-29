import { test, expect, type Page } from '@playwright/test';
import { E2E_MODEL } from './fixtures/e2e-model';

/**
 * #904 — tool iteration limit, spec test-plan row 17
 * (docs/internal/specs/tool-iteration-limit-spec.md, User Stories 1, 2, 6;
 * founder decisions D11, D16, D20). No LLM is needed.
 *
 * ENVIRONMENT FACT this file is built on (established, not assumed — see
 * uat-audit-and-settings.spec.ts, "Performance" block): the E2E gateway runs
 * with `gateway.dev_mode_bypass: true`, and every /api/v1/performance route
 * (GET, PUT and the new max-tool-iterations/preview) is registered with
 * adminWrap → RequireNotBypass, which answers 503 under bypass. The real
 * gateway therefore CANNOT serve or change the global limit here. So:
 *
 *   (a) agent source line — REAL gateway end to end: an agent created through
 *       the real API with an own value of 50 is read back by the real
 *       profile screen, whose source line renders the server-computed fields.
 *   (b) Settings → D11 dialog — the shipped SPA bundle driven against
 *       intercepted /performance + preview responses (the interception
 *       precedent uat-audit-and-settings.spec.ts sets for the same 503).
 *       Cancel must send no PUT (US-6 AS-2).
 *   (c) D20 — a RAISE shows no lowering dialog, even when a preview would list
 *       a capped agent.
 *
 * CI-verified only: this spec was written without a local full-stack run.
 */

const PERF_URL = '**/api/v1/performance';
const PREVIEW_URL = '**/api/v1/performance/max-tool-iterations/preview**';

async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrf = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf');
  return csrf ? { 'X-CSRF-Token': csrf.value } : {};
}

function perfSettings(global: number) {
  return {
    max_parallel_agents: 4,
    effective_max_parallel_agents: 4,
    max_parallel_agents_configured: true,
    tools_on_demand: true,
    goal_max_rounds: 20,
    max_tool_iterations: global,
    max_tool_iterations_saved_state: 'ok',
  };
}

/** Intercepts /performance (GET → the given global; PUT → counted, refused) and the preview. */
async function stubPerformance(page: Page, global: number, previewAgents: unknown[]) {
  const seen = { puts: 0, previews: [] as string[] };
  await page.route(PREVIEW_URL, async (route) => {
    const value = Number(new URL(route.request().url()).searchParams.get('value'));
    seen.previews.push(route.request().url());
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ value, agents: previewAgents }) });
  });
  await page.route(PERF_URL, async (route) => {
    if (route.request().method() === 'PUT') {
      seen.puts++;
      await route.fulfill({ status: 500, contentType: 'application/json', body: JSON.stringify({ error: 'e2e: no PUT expected' }) });
      return;
    }
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(perfSettings(global)) });
  });
  return seen;
}

async function openPerformanceTab(page: Page) {
  await page.goto('/#/settings?tab=performance');
  const perfTab = page.locator('button[role="tab"]', { hasText: 'Performance' });
  await expect(perfTab).toBeVisible({ timeout: 20_000 });
  await perfTab.click();
  const panel = page.locator('[role="tabpanel"][data-state="active"]').first();
  await expect(panel).toBeVisible({ timeout: 20_000 });
  return panel;
}

test.describe('#904 tool iteration limit', () => {
  let agentId = '';
  let agentName = '';

  test.beforeEach(async ({ page }) => {
    await page.goto('/#/agents');
    agentName = `IterLimit-${Date.now()}`;
    const resp = await page.request.post('/api/v1/agents', {
      headers: await csrfHeaders(page),
      data: {
        name: agentName,
        soul: 'Tool iteration limit e2e soul',
        type: 'Main',
        model: E2E_MODEL,
        max_tool_iterations: 50,
      },
    });
    expect(resp.ok(), `create agent failed: ${resp.status()} ${await resp.text()}`).toBeTruthy();
    agentId = ((await resp.json()) as { id: string }).id;
  });

  test.afterEach(async ({ page }) => {
    if (!agentId) return;
    const headers = await csrfHeaders(page);
    const got = await page.request.get(`/api/v1/agents/${agentId}`, { headers });
    if (!got.ok()) return;
    const { revision } = (await got.json()) as { revision: string };
    await page.request.delete(`/api/v1/agents/${agentId}?revision=${encodeURIComponent(revision)}`, { headers });
  });

  test('(a) the agent profile shows the server-computed source line for an own value (US-2 AS-1)', async ({ page }) => {
    // Real gateway: the create response and GET carry effective 50, source "agent".
    const wire = await page.request.get(`/api/v1/agents/${agentId}`, { headers: await csrfHeaders(page) });
    expect(wire.ok()).toBeTruthy();
    const agent = (await wire.json()) as Record<string, unknown>;
    expect(agent.max_tool_iterations).toBe(50);
    expect(agent.max_tool_iterations_source).toBe('agent');
    expect(agent.max_tool_iterations_override).toBe(50);
    expect(agent.max_tool_iterations_override_ignored).toBe(false);

    await page.goto(`/#/agents/${agentId}`);
    await expect(page.getByText(agentName).first()).toBeVisible({ timeout: 20_000 });
    await page.getByTestId('tab-advanced').click();
    // "Lowered for this agent: 50" (+ " (global limit N)" when the global is readable;
    // under dev-mode bypass GET /performance is 503, so the suffix may be absent).
    await expect(page.getByTestId('tool-iteration-limit-source')).toContainText('Lowered for this agent: 50', { timeout: 15_000 });
    await expect(page.getByTestId('tool-iteration-limit-reset')).toBeVisible();
  });

  test('(b) lowering the global lists the affected agent before any write; Cancel sends nothing (US-6 AS-1/AS-2)', async ({ page }) => {
    const seen = await stubPerformance(page, 300, [
      { agent_id: agentId, agent_name: agentName, old_value: 250, new_value: 200 },
    ]);
    const panel = await openPerformanceTab(page);
    const input = panel.getByTestId('performance-max-tool-iterations-input');
    await expect(input).toHaveValue('300', { timeout: 15_000 });

    await input.fill('200');
    const dialog = page.getByRole('alertdialog');
    await expect(dialog).toBeVisible({ timeout: 10_000 });
    await expect(dialog.getByTestId('max-tool-iterations-lowering-entry')).toHaveCount(1);
    await expect(dialog.getByTestId('max-tool-iterations-lowering-entry').first()).toContainText(agentName);
    await expect(dialog.getByTestId('max-tool-iterations-lowering-entry').first()).toContainText(/250[\s\S]*200/);
    expect(seen.previews.some((u) => u.includes('value=200'))).toBe(true);

    await dialog.getByRole('button', { name: 'Cancel' }).click();
    await expect(dialog).toBeHidden();
    // Give a debounced autosave every chance to fire before concluding "no PUT".
    await page.waitForTimeout(1_500);
    expect(seen.puts, 'Cancel in the D11 dialog writes nothing').toBe(0);
  });

  test('(c) raising the global opens no lowering dialog (D20)', async ({ page }) => {
    // The preview stub lists a capped agent on purpose: D20 says a raise never
    // lowers anyone, so no dialog may appear whatever a preview would say.
    const seen = await stubPerformance(page, 200, [
      { agent_id: agentId, agent_name: agentName, old_value: 500, new_value: 300 },
    ]);
    const panel = await openPerformanceTab(page);
    const input = panel.getByTestId('performance-max-tool-iterations-input');
    await expect(input).toHaveValue('200', { timeout: 15_000 });

    await input.fill('300');
    // Positive signal instead of a fixed sleep (CHECK round 2, finding D): a
    // raise goes straight to the step-up gate (D20 + US-1 AS-4) — the password
    // dialog in local mode, a confirmation (alertdialog) in platform mode.
    // Once the gate is up, the point where a lowering dialog would have
    // appeared has passed.
    const stepUp = page.getByTestId('reauth-password-input').or(page.getByRole('alertdialog'));
    await expect(stepUp.first(), 'a raise must reach the step-up gate').toBeVisible({ timeout: 10_000 });
    await expect(page.getByTestId('max-tool-iterations-lowering-list'), 'no lowering dialog on a raise (D20)').toHaveCount(0);
    expect(seen.previews.filter((u) => u.includes('value=300')), 'a raise is never previewed (D20)').toEqual([]);
    expect(seen.puts, 'nothing is written before the step-up gate is passed').toBe(0);
  });
});

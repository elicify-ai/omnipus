import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';
import { chatInput } from './fixtures/selectors';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

test.beforeEach(async ({ page }) => {
  // HashRouter: routes live in the fragment, not the pathname.
  await page.goto('/#/command-center');
});

test('(a) all section cards load without console errors', async ({ page }) => {
  await expect(page).toHaveURL(/command-center/, { timeout: 10_000 });

  // CommandCenterScreen wraps everything in a scrollable div — wait for the route to render
  // StatusBar, RateLimitStatusCard, AttentionSection, AgentSummarySection are all rendered
  // (command-center.tsx:43-60). Assert the page has content in main.
  const main = page.locator('main');
  await expect(main).toBeVisible({ timeout: 15_000 });

  // No error alerts in the task section — tasksError banner has specific text (command-center.tsx:52-56)
  const taskErrorBanner = page.locator('text=Failed to load tasks');
  await expect(taskErrorBanner).toHaveCount(0, { timeout: 10_000 });

  await expectA11yClean(page);
});

test('(b) approval-queue: policy=ask tool call triggers approval modal and Approve routes it through', async ({ page }) => {
  // test.slow() triples the global 90s test timeout to 270s. The LLM-driven
  // approval modal occasionally takes 40-60s to appear under suite load.
  test.slow();
  // Navigate first so the SPA is running with an authenticated session.
  await page.goto('/#/command-center');
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // Helper: make an authenticated fetch via the browser's JS context (uses sessionStorage token + CSRF cookie).
  type FetchResult = { ok: boolean; status: number; body: unknown };
  const apiFetch = async (method: string, path: string, body?: object): Promise<FetchResult> => {
    return page.evaluate(async ({ method, path, body }: { method: string; path: string; body?: object }) => {
      const token = sessionStorage.getItem('omnipus_auth_token') ?? localStorage.getItem('omnipus_auth_token') ?? '';
      const csrfCookie = document.cookie.split(';').find((c) => c.trim().startsWith('csrf=') || c.trim().startsWith('__Host-csrf='));
      const csrfToken = csrfCookie ? csrfCookie.split('=').slice(1).join('=').trim() : '';
      const res = await fetch(path, {
        method,
        headers: {
          'Authorization': `Bearer ${token}`,
          'Content-Type': 'application/json',
          ...(method !== 'GET' ? { 'X-CSRF-Token': csrfToken } : {}),
        },
        ...(body ? { body: JSON.stringify(body) } : {}),
      });
      return { ok: res.ok, status: res.status, body: await res.json().catch(() => null) };
    }, { method, path, body });
  };

  // Create a temporary custom agent with exec=ask policy.
  // Custom agents are not locked, so their tools can be modified via the API.
  const createResp = await apiFetch('POST', '/api/v1/agents', { name: 'ApprovalTest-e2e', type: 'custom' });
  expect(createResp.ok).toBeTruthy();
  const agentId = (createResp.body as { id: string }).id;

  // Set exec policy to 'ask' for the new agent (exec tool is available by default_policy=allow)
  const putResp = await apiFetch('PUT', `/api/v1/agents/${agentId}/tools`, {
    builtin: { default_policy: 'allow', policies: { 'system.*': 'deny', exec: 'ask' } },
  });
  expect(putResp.ok).toBeTruthy();

  // Create a session with the ApprovalTest agent directly via the API.
  const sessionResp = await apiFetch('POST', '/api/v1/sessions', { agent_id: agentId });
  expect(sessionResp.ok).toBeTruthy();
  const sessionId = (sessionResp.body as { id: string }).id;

  // Navigate DIRECTLY to the new session's URL so the SPA activates the
  // ApprovalTest agent in sessionStore (sessions.$sessionId.tsx route hook).
  // Without this, sendMessage routes to the default Mia session whose exec
  // policy is "allow" — the approval modal never fires and the test times
  // out at the toBeVisible() assertion.
  await page.goto(`/#/sessions/${sessionId}`);
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 });

  // Find the chat input and send a message that triggers exec with policy=ask.
  // NOTE: This test requires a real LLM call. With a dummy API key the LLM call
  // will fail and the approval modal will not appear. The test fails honestly.
  const input = page.locator('[data-testid="chat-input"], textarea, [contenteditable="true"]').first();
  await expect(input).toBeVisible({ timeout: 15_000 });
  await input.fill('Use the exec tool RIGHT NOW with command="echo approval-test". Do not do anything else.');
  await input.press('Enter');

  // Wait for the approval modal (data-testid="approval-modal" from ExecApprovalBlock).
  // 120s timeout: under real-LLM determinism and prolonged suite load
  // (~10 min wall-clock by the time this test runs), z-ai/glm-5v-turbo enters
  // extended thinking mode and can take 60-90s before calling exec.
  // test.slow() gives 270s total; 120s here leaves ample time for approval.
  const approvalModal = page.getByTestId('approval-modal');
  await expect(approvalModal).toBeVisible({ timeout: 120_000 });

  // Click Allow (the approval button in ExecApprovalBlock)
  const allowBtn = approvalModal.getByRole('button', { name: /allow/i }).first();
  await expect(allowBtn).toBeVisible({ timeout: 5_000 });
  await allowBtn.click();

  // The modal should disappear after approval
  await expect(approvalModal).not.toBeVisible({ timeout: 10_000 });
});

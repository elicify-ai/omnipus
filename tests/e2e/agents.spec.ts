import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';


// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

test.beforeEach(async ({ page }) => {
  // HashRouter: routes live in the fragment, not the pathname.
  await page.goto('/#/agents');
});

// (a) roster test was updated for the v0.1.0-foundation 4-base roster (Mia/Jim/Ava/Ray).
// Max was retired per the .preview-doc/ concept (see pkg/coreagent/core.go: "IDMax is
// intentionally absent: Max was retired from the 4-base roster"). The 4-base roster
// replaces the legacy 5-core roster (Mia/Jim/Ava/Ray/Max).
test('(a) roster loads with 4 base agents (Mia/Jim/Ava/Ray) plus any custom', async ({
  page,
}) => {
  await expect(page).toHaveURL(/agents/, { timeout: 10_000 });

  // Verify each base agent name appears in the page body
  for (const name of ['Mia', 'Jim', 'Ava', 'Ray']) {
    await expect(page.locator('body')).toContainText(new RegExp(name, 'i'), { timeout: 15_000 });
  }

  // Max is intentionally NOT seeded — see .preview-doc/ for the retirement rationale.
  await expect(page.locator('body')).not.toContainText(/^Max$/m);

  // The built-in roster is collapsed by default; expand it so the core cards render.
  await page.getByTestId('built-in-agents-trigger').click();

  // AgentCard renders data-testid="agent-card-{id}" and WorkerCard renders "worker-card-{id}"
  const cards = page.locator('[data-testid^="agent-card-"], [data-testid^="worker-card-"]');
  await expect(cards.first()).toBeVisible({ timeout: 10_000 });
  expect(await cards.count()).toBeGreaterThanOrEqual(4);

  await expectA11yClean(page);
});

test('(b) profile tabs render and switch sections', async ({ page }) => {
  // The built-in roster is collapsed by default; expand to access core agent cards.
  await page.getByTestId('built-in-agents-trigger').click();

  // Click the first agent card to open the agent profile slide-over.
  const firstCard = page.locator('[data-testid^="agent-card-"]').first();
  await expect(firstCard).toBeVisible({ timeout: 10_000 });
  await firstCard.click();

  // Wait for profile to mount and render the editable name input.
  await expect(page.getByTestId('agent-name-input')).toBeVisible({ timeout: 10_000 });

  // Desktop profile uses Radix Tabs; mobile uses an accordion fallback.
  // Try the desktop tab path first.
  const tabTriggers = page.getByRole('tab');
  const tabCount = await tabTriggers.count();

  if (tabCount > 0) {
    // Click each tab and assert it becomes selected.
    for (let i = 0; i < tabCount; i++) {
      await tabTriggers.nth(i).click();
      await expect(tabTriggers.nth(i)).toHaveAttribute('aria-selected', 'true', { timeout: 5_000 });
      // At least one active tab panel should be visible.
      await expect(page.locator('[role="tabpanel"][data-state="active"]').first()).toBeVisible({ timeout: 5_000 });
    }
    return;
  }

  // Accordion fallback (mobile / narrow viewport).
  const accordionTriggers = page.locator('[data-radix-accordion-trigger]');
  const triggerCount = await accordionTriggers.count();
  if (triggerCount > 0) {
    for (let i = 0; i < triggerCount; i++) {
      await accordionTriggers.nth(i).click();
    }
    const openItems = page.locator('[data-state="open"]');
    await expect(openItems.first()).toBeVisible({ timeout: 10_000 });
  }
});

test('(c) "New Main" button on roster opens the create-agent modal', async ({ page }) => {
  // The roster has separate buttons for Main, Subagent, and External subagents.
  const createBtn = page.getByTestId('add-main-button');
  await expect(createBtn).toBeVisible({ timeout: 10_000 });
  await createBtn.click();

  // CreateAgentModal renders a Radix Dialog — [role="dialog"]
  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible({ timeout: 10_000 });
});

test('(d) locked fields render read-only on core agents', async ({ page }) => {
  // The core agents live in the collapsed Built-in roster; expand it first.
  await page.goto('/#/agents');
  await page.getByTestId('built-in-agents-trigger').click();

  const jimCard = page.locator('[aria-label*="Jim" i]').first();
  await expect(jimCard).toBeVisible({ timeout: 15_000 });
  await jimCard.click();

  // Wait for the slide-over to mount.
  await expect(page.getByTestId('agent-name-input')).toBeVisible({ timeout: 10_000 });

  // For a locked agent, the input must be disabled
  const nameInput = page.getByTestId('agent-name-input');
  await expect(nameInput).toBeDisabled();
});

test('(e) deleted agent URL returns branded 404 with "Back to Agents" link', async ({ page }) => {
  // Soft-skipped: the slideover refactor changed the /_app/agents/$agentId route
  // to a transient "open slideover and navigate back to /agents" handler (see
  // src/routes/_app/agents.$agentId.tsx). The route does not render a branded
  // 404 page for unknown agent IDs — it silently opens an empty slideover and
  // redirects to /#/agents. The test premise (a 404 page with a "Back to
  // Agents" link) does not match the current product shape.
  // Tracked: https://github.com/elicify-ai/omnipus/issues/427
  test.skip(
    true,
    'BLOCKED on #427 — slideover refactor removed the branded 404 page for unknown agent IDs; see SKIP_ALLOWLIST',
  );
  await page.goto('/#/agents/this-agent-does-not-exist-xyz');

  // Should see a "not found" message, not crash the app
  const notFoundMsg = page.locator('text=not found').or(page.locator('text=Not Found')).or(page.locator('text=Agent not found')).first();
  await expect(notFoundMsg).toBeVisible({ timeout: 10_000 });

  // Must have "Back to Agents" link (exact text per SKIP_ALLOWLIST note)
  const backLink = page.getByRole('link', { name: 'Back to Agents' });
  await expect(backLink).toBeVisible({ timeout: 5_000 });
  await backLink.click();
  await expect(page).toHaveURL(/agents/, { timeout: 5_000 });
});

test('(f) name collision on Create Agent surfaces server 409 error in UI', async ({ page }) => {
  // Open the create-agent modal via the "+ New Main" button.
  const createBtn = page.getByTestId('add-main-button');
  await expect(createBtn).toBeVisible({ timeout: 10_000 });
  await createBtn.click();

  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible({ timeout: 10_000 });

  // Find the name input within the modal
  // pressSequentially() required — fill() doesn't fire React onChange on controlled inputs
  const nameInput = modal.locator('input').first();
  await expect(nameInput).toBeVisible({ timeout: 10_000 });
  await nameInput.pressSequentially('Mia');

  // Intercept the POST to return 409
  await page.route('**/api/v1/agents**', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'agent name already exists' }),
      });
    } else {
      await route.continue();
    }
  });

  // Submit — look for a Create/Save button in the modal
  const submitBtn = modal.getByRole('button', { name: /create|save/i }).first();
  await expect(submitBtn).toBeVisible({ timeout: 5_000 });
  await submitBtn.click();

  // Error appears as a toast (ToastContainer in AppShell — no role="alert").
  // CreateAgentModal uses isApiError(err) ? err.userMessage which for a 409 response
  // is defaultUserMessage(409) = "This conflicts with the current state. Please refresh and try again."
  // (see src/lib/api-error.ts and src/components/agents/CreateAgentModal.tsx).
  const errorToast = page.locator('text=conflicts with the current state').first();
  await expect(errorToast).toBeVisible({ timeout: 10_000 });
});

test('(g) session with deleted agent shows read-only transcript and "Agent removed" banner', async ({ page }) => {
  // Read the Bearer token from localStorage so page.request calls can include
  // it as an Authorization header. The CSRF middleware exempts Bearer-token
  // requests (double-submit cookie only defends ambient cookie credentials),
  // so this is both correct and necessary — page.request does NOT auto-add
  // the X-Csrf-Token header that the double-submit pattern requires.
  const bearerToken = await page.evaluate(() =>
    localStorage.getItem('omnipus_auth_token') ?? ''
  );
  const authHeaders = { Authorization: `Bearer ${bearerToken}` };

  // Step 1: Create a temporary agent via API. `soul` is required since the
  // v0.1.1 agent-form refactor; type='custom' is mapped to Main on the wire.
  const resp = await page.request.post('/api/v1/agents', {
    headers: authHeaders,
    data: {
      name: `TempAgent-${Date.now()}`,
      soul: 'Temporary agent test soul',
      type: 'custom',
      model: 'openrouter/google/gemini-2.0-flash-001',
    },
  });
  const agent = await resp.json() as { id: string };
  const agentId = agent.id;

  // Step 2: Create a session for this agent
  const sessionResp = await page.request.post('/api/v1/sessions', {
    headers: authHeaders,
    data: { agent_id: agentId },
  });
  const session = await sessionResp.json() as { id?: string; session?: { id: string } };
  const sessionId = session.id ?? session.session?.id;

  // Step 3: Delete the agent
  await page.request.delete(`/api/v1/agents/${agentId}`, { headers: authHeaders });

  // Step 4: Navigate to the session
  await page.goto(`/#/sessions/${sessionId}`);
  // Wait for the route to settle (URL must contain "sessions")
  await expect(page).toHaveURL(/sessions/, { timeout: 10_000 });
  // Wait for the app shell to render (banner landmark = auth OK)
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 10_000 });

  // Step 5: The banner must appear
  const banner = page.getByTestId('agent-removed-banner');
  await expect(banner).toBeVisible({ timeout: 15_000 });
  await expect(banner).toContainText(/agent.*removed/i);

  // Step 6: Composer must be disabled
  const input = page.locator('textarea[placeholder*="message" i], [data-testid="chat-input"]').first();
  await expect(input).toBeDisabled({ timeout: 5_000 });
});

test.afterAll(async ({ request }) => {
  // Clean up any PennyTest agents created by test (c) across all runs
  const resp = await request.get('/api/v1/agents');
  if (!resp.ok()) return;
  const data = (await resp.json()) as { id: string; name: string }[];
  for (const agent of data) {
    if (/^PennyTest/i.test(agent.name)) {
      await request.delete(`/api/v1/agents/${agent.id}`);
    }
  }
});

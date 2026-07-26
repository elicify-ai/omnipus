import { expect } from '@playwright/test';
import type { Page } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';


// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

// The Built-in roster accordion is rendered with O2 adaptive-expand
// (AgentListScreen.tsx): when there are NO custom Main agents (the fresh-env
// case), it opens EXPANDED by default so the core cards are immediately
// visible; once a custom Main agent exists it defaults collapsed. A blind
// click on the trigger would TOGGLE it (collapsing the expanded fresh-env
// accordion and hiding the cards the test needs). This helper makes the
// roster expanded idempotently: it only clicks the trigger when the
// accordion item is currently closed.
async function ensureBuiltInExpanded(page: Page): Promise<void> {
  const trigger = page.getByTestId('built-in-agents-trigger');
  await expect(trigger).toBeVisible({ timeout: 15_000 });
  // Radix AccordionTrigger reflects open/closed via aria-expanded + data-state.
  const expanded = await trigger.getAttribute('aria-expanded');
  if (expanded !== 'true') {
    await trigger.click();
  }
  await expect(trigger).toHaveAttribute('aria-expanded', 'true', { timeout: 5_000 });
}

/**
 * csrfHeaders — build the header object `page.request` calls need for
 * state-changing requests. `page.request` shares the browser context's
 * cookie jar, so the `omnipus-session` (auth) and `csrf` cookies written by
 * global-setup.ts already ride along automatically — but the double-submit
 * CSRF pattern additionally requires the SAME csrf value echoed back as an
 * explicit `X-CSRF-Token` request header (src/lib/api.ts's withCsrfHeaders
 * does exactly this for real browser-driven fetches). Since ADR-044 removed
 * the JS-readable bearer token from localStorage entirely, there is no
 * `Authorization: Bearer <token>` path left for a test to piggyback on —
 * this cookie-header echo is the only way for `page.request` to pass the
 * gateway's CSRF middleware.
 */
async function csrfHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies();
  const csrfCookie = cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf');
  return csrfCookie ? { 'X-CSRF-Token': csrfCookie.value } : {};
}

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

  // The built-in roster opens expanded by default in a fresh env (O2 adaptive
  // expand); ensure it's expanded idempotently so the core cards render.
  await ensureBuiltInExpanded(page);

  // AgentCard renders data-testid="agent-card-{id}" and WorkerCard renders "worker-card-{id}"
  const cards = page.locator('[data-testid^="agent-card-"], [data-testid^="worker-card-"]');
  await expect(cards.first()).toBeVisible({ timeout: 10_000 });
  expect(await cards.count()).toBeGreaterThanOrEqual(4);

  await expectA11yClean(page);
});

test('(b) profile tabs render and switch sections', async ({ page }) => {
  // Ensure the built-in roster is expanded (idempotent) to access core cards.
  await ensureBuiltInExpanded(page);

  // Click the first agent card to open the agent profile slide-over.
  const firstCard = page.locator('[data-testid^="agent-card-"]').first();
  await expect(firstCard).toBeVisible({ timeout: 10_000 });
  await firstCard.click();

  // Wait for profile to mount and render the editable name input.
  // The desktop tab panel and the (hidden) mobile accordion both contain an
  // input with this test id, so scope to the first one.
  await expect(page.getByTestId('agent-name-input').first()).toBeVisible({ timeout: 10_000 });

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
  // The core agents live in the Built-in roster; ensure it's expanded first.
  await page.goto('/#/agents');
  await ensureBuiltInExpanded(page);

  const jimCard = page.locator('[aria-label*="Jim" i]').first();
  await expect(jimCard).toBeVisible({ timeout: 15_000 });
  await jimCard.click();

  // Wait for the slide-over to mount. Use .first() because the desktop tab
  // panel and the (hidden) mobile accordion both carry this test id.
  const nameInput = page.getByTestId('agent-name-input').first();
  await expect(nameInput).toBeVisible({ timeout: 10_000 });

  // For a locked agent, the input must be disabled
  await expect(nameInput).toBeDisabled();
});

test('(e) deleted agent URL surfaces a not-found affordance in the agent profile slide-over', async ({ page }) => {
  // The /_app/agents/$agentId route (src/routes/_app/agents.$agentId.tsx) is a
  // transient handler: it opens the AgentProfile slide-over for the given id
  // and immediately replaces the URL with /agents — there is no dedicated
  // 404 route/page by design (issue #427 was the "branded 404 page" premise,
  // which no longer matches the product's shape). The not-found affordance
  // instead lives INSIDE the slide-over, driven by AgentProfile's isNotFound
  // branch (src/components/agents/AgentProfile.tsx) — verified here to make
  // sure that branch actually renders real content, not an empty panel.
  await page.goto('/#/agents/this-agent-does-not-exist-xyz');

  // The transient route always replaces back to /agents (never leaves a
  // dead-end URL on the unknown id).
  await expect(page).toHaveURL(/agents/, { timeout: 10_000 });

  const sheet = page.getByTestId('agent-profile-sheet');
  await expect(sheet).toBeVisible({ timeout: 10_000 });

  // Should see a "not found" message, not a blank slide-over.
  const notFoundMsg = sheet.locator('text=Agent not found').first();
  await expect(notFoundMsg).toBeVisible({ timeout: 10_000 });
  await expect(sheet.locator('text=This agent may have been deleted.')).toBeVisible();

  // Must have a "Back to Agents" affordance. It's a button (not a
  // navigational <a>) because the slide-over never actually leaves /agents —
  // see the URL assertion above — so closing it is an in-app state change,
  // not a route change.
  const backButton = sheet.getByRole('button', { name: 'Back to Agents' });
  await expect(backButton).toBeVisible({ timeout: 5_000 });
  await backButton.click();

  // Clicking it closes the slide-over and stays on the agents list.
  await expect(sheet).not.toBeVisible({ timeout: 5_000 });
  await expect(page).toHaveURL(/agents/, { timeout: 5_000 });
});

test('(f) name collision on Create Agent surfaces server 409 error in UI', async ({ page }) => {
  // Open the create-agent modal via the "+ New Main" button.
  const createBtn = page.getByTestId('add-main-button');
  await expect(createBtn).toBeVisible({ timeout: 10_000 });
  await createBtn.click();

  const modal = page.locator('[role="dialog"]');
  await expect(modal).toBeVisible({ timeout: 10_000 });

  // ── Step 1: Identity ───────────────────────────────────────────────────────
  // pressSequentially() required — fill() doesn't fire React onChange on controlled inputs
  const nameInput = modal.getByRole('textbox', { name: /Name/i });
  await expect(nameInput).toBeVisible({ timeout: 10_000 });
  await nameInput.pressSequentially('Mia');

  // Select the seeded model from the Step 1 picker so we can advance.
  const modelSelect = modal.getByRole('combobox', { name: /Model/i });
  await expect(modelSelect).toBeVisible({ timeout: 10_000 });
  await modelSelect.click();
  const firstModelOption = modal.locator('[role="option"]').first();
  await expect(firstModelOption).toBeVisible({ timeout: 5_000 });
  await firstModelOption.click();

  await modal.getByTestId('wizard-next-1').click();

  // ── Step 2: Personality ────────────────────────────────────────────────────
  // Step 2 requires a non-empty soul before it can advance.
  const soulInput = modal.getByRole('textbox', { name: /SOUL|soul/i }).first();
  await expect(soulInput).toBeVisible({ timeout: 10_000 });
  await soulInput.pressSequentially('Test soul for collision handling.');

  // Intercept the POST to return 409 before clicking final Create.
  await page.route('**/api/v1/agents', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'agent name already exists' }),
      });
      return;
    }
    await route.continue();
  });

  await modal.getByTestId('wizard-next-2').click();

  // ── Step 3: Tools ──────────────────────────────────────────────────────────
  const createBtnStep3 = modal.getByTestId('wizard-create');
  await expect(createBtnStep3).toBeVisible({ timeout: 5_000 });
  await createBtnStep3.click();

  // Error appears inline in the wizard and as a toast from CreateAgentModal.
  // defaultUserMessage(409) = "This conflicts with the current state. Please refresh and try again."
  // (see src/lib/api-error.ts and src/components/agents/CreateAgentModal.tsx).
  const errorToast = page.locator('text=conflicts with the current state').first();
  await expect(errorToast).toBeVisible({ timeout: 10_000 });
});

test('(g) session with deleted agent shows read-only transcript and "Agent removed" banner', async ({ page }) => {
  // The agent-removed banner is wired end-to-end: the backend's getSession
  // handler (pkg/gateway/rest.go) detects a ghost session (its agent_id no
  // longer resolves in the live config) and sets agent_removed=true on
  // SessionDetail; the /sessions/$sessionId route reads it and passes
  // agentRemoved to <ChatScreen>, which renders the
  // data-testid="agent-removed-banner" bar and disables the composer
  // (src/components/chat/ChatScreen.tsx). Issue #103 tracked this gap; it is
  // now closed.

  const authHeaders = await csrfHeaders(page);

  // Step 1: Create a temporary agent via API. `soul` and `type` are required
  // since the v0.1.1 discriminated-union create contract (ADR-034); the wire
  // enum is Main / Subagent / subagent_3p — 'custom' is the PERSISTED config
  // constant and was never a legal wire value.
  const resp = await page.request.post('/api/v1/agents', {
    headers: authHeaders,
    data: {
      name: `TempAgent-${Date.now()}`,
      soul: 'Temporary agent test soul',
      type: 'Main',
      model: 'openrouter/google/gemini-2.0-flash-001',
    },
  });
  expect(resp.ok(), `create agent failed: ${resp.status()} ${await resp.text()}`).toBeTruthy();
  const agent = await resp.json() as { id: string };
  const agentId = agent.id;

  // Step 2: Create a session for this agent. A freshly created agent is
  // persisted (config.json + the in-memory config struct) synchronously
  // before this POST returns, but the runtime agent Registry — the thing
  // POST /sessions actually resolves the agent through
  // (AgentLoop.GetAgentStore -> Registry.GetAgent) — is rebuilt on a
  // slightly different path and can lag behind by a very short window
  // (confirmed directly: an immediate back-to-back call can 400 with
  // "agent ... not found", while the same call after ~100-300ms always
  // succeeds). That lag is a backend timing detail orthogonal to the
  // agent-removed banner this test exists to verify, so retry the create
  // briefly instead of letting an unrelated timing hazard flake this test.
  let sessionResp = await page.request.post('/api/v1/sessions', {
    headers: authHeaders,
    data: { agent_id: agentId },
  });
  for (let attempt = 0; !sessionResp.ok() && attempt < 10; attempt++) {
    const body = await sessionResp.text();
    if (sessionResp.status() !== 400 || !/not found/i.test(body)) {
      throw new Error(`create session failed: ${sessionResp.status()} ${body}`);
    }
    await new Promise((r) => setTimeout(r, 300));
    sessionResp = await page.request.post('/api/v1/sessions', {
      headers: authHeaders,
      data: { agent_id: agentId },
    });
  }
  expect(sessionResp.ok(), `create session failed: ${sessionResp.status()} ${await sessionResp.text()}`).toBeTruthy();
  const session = await sessionResp.json() as { id?: string; session?: { id: string } };
  const sessionId = session.id ?? session.session?.id;
  expect(sessionId, 'session response had no id').toBeTruthy();

  // Step 3: Delete the agent
  const deleteResp = await page.request.delete(`/api/v1/agents/${agentId}`, { headers: authHeaders });
  expect(deleteResp.ok(), `delete agent failed: ${deleteResp.status()} ${await deleteResp.text()}`).toBeTruthy();

  // Step 4: Navigate to the session
  await page.goto(`/#/sessions/${sessionId}`);
  // Wait for the route to settle (URL must contain "sessions")
  await expect(page).toHaveURL(/sessions/, { timeout: 10_000 });
  // Wait for the app shell to render (main landmark = auth OK, not a blank
  // page). This route renders the bare ChatScreen directly (no workspace_id
  // on the session, so no WorkspaceTabContainer/ScreenHeader wrapper) —
  // those are the only two components in the app that emit role="banner",
  // so this screen never has one; `main` is the landmark this layout
  // actually renders.
  await expect(page.getByRole('main')).toBeVisible({ timeout: 10_000 });

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

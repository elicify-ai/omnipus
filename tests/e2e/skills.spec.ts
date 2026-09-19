import { expect } from '@playwright/test';
import { test } from './fixtures/console-errors';
import { expectA11yClean } from './fixtures/a11y';

// Global storageState provides pre-authenticated session (see playwright.config.ts + global-setup.ts).

test.beforeEach(async ({ page }) => {
  // HashRouter: routes live in the fragment, not the pathname.
  await page.goto('/#/skills');
});

test('(a) Browse Skills modal opens', async ({ page }) => {
  await expect(page).toHaveURL(/skills/, { timeout: 10_000 });

  // Button text is "Browse Skills" (skills.tsx:143)
  const browseBtn = page.getByRole('button', { name: /Browse Skills/i });
  await expect(browseBtn).toBeVisible({ timeout: 10_000 });
  await browseBtn.click();

  // SkillBrowser renders inside a Radix Dialog ([role="dialog"])
  const modal = page.locator('[role="dialog"]').first();
  await expect(modal).toBeVisible({ timeout: 10_000 });

  await expectA11yClean(page);
});

test('(b) skill install from file: review dialog opens, and a rejected install is surfaced', async ({ page }) => {
  await expect(page).toHaveURL(/skills/, { timeout: 10_000 });

  // Contract: installs are file-upload + revision-checked — the SPA uploads
  // the file, then POSTs /skills/install (SkillInstallRequest.yaml). A 409
  // surfaces as the stale-review toast; a rejected install must be loudly
  // surfaced, never silently swallowed.
  await page.route('**/api/v1/skills/install**', async (route) => {
    await route.fulfill({
      status: 409,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'conflict: the installed skill changed since it was reviewed' }),
    });
  });

  // Open the Browse Skills modal
  const browseBtn = page.getByRole('button', { name: /Browse Skills/i });
  await expect(browseBtn).toBeVisible({ timeout: 10_000 });
  await browseBtn.click();

  // SkillBrowser dialog opens and renders a DialogTitle "Browse Skills".
  const modal = page.locator('[role="dialog"]').filter({ hasText: /Browse Skills/i }).first();
  await expect(modal).toBeVisible({ timeout: 10_000 });

  // Step 1: set the file input — this triggers handleFileSelected, which
  // accepts only .md / .zip skill packages. The payload carries valid
  // SKILL.md frontmatter (name + non-empty description, pkg/skills/
  // authoring.go) so the mocked-install rejection tests the rejection
  // surface against a well-formed input, not a malformed one.
  const fileInput = modal.locator('input[type="file"]');
  await fileInput.setInputFiles({
    name: 'test-skill.md',
    mimeType: 'text/markdown',
    buffer: Buffer.from(
      '---\n' +
        'name: test-skill\n' +
        'description: Runs the install review flow against a mock install endpoint.\n' +
        '---\n' +
        '# test-skill\n\nMinimal skill body for the install-dialog e2e.\n',
    ),
  });

  // Step 2: the review dialog must now be visible
  // (data-testid="skill-install-confirm-dialog" on DialogContent — SkillBrowser.tsx)
  const confirmDialog = page.locator('[data-testid="skill-install-confirm-dialog"]');
  await expect(confirmDialog).toBeVisible({ timeout: 10_000 });

  // Step 3: click "Install skill" to proceed — triggers handleConfirmInstall
  // which calls installSkillFromFile and receives the mocked 409.
  const confirmInstallBtn = confirmDialog.getByRole('button', { name: /install skill/i });
  await expect(confirmInstallBtn).toBeVisible({ timeout: 5_000 });
  await confirmInstallBtn.click();

  // Step 4: the rejection must be surfaced. handleConfirmInstall's 409 branch
  // toasts the stale-review message verbatim (SkillBrowser.tsx
  // handleConfirmInstall); assert THAT exact string — a generic
  // "something appeared" assertion would not prove the guard ran.
  const conflictToast = page.getByText(
    'This installed skill changed after you reviewed it. Close and reopen the browser before replacing it.',
  );
  await expect(conflictToast).toBeVisible({ timeout: 10_000 });
});

test('(c) MCP server add with duplicate name returns 409 and inline error', async ({ page }) => {
  // Intercept before clicking — route must be set before the request fires.
  // The API endpoint is /api/v1/mcp-servers — use exact path pattern.
  await page.route('**/api/v1/mcp-servers', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'MCP server name already exists' }),
      });
    } else {
      await route.continue();
    }
  });

  // Navigate to the MCP Servers tab (skills.tsx tab structure)
  const mcpTab = page.locator('button[role="tab"]', { hasText: /MCP Servers|Servers/i });
  await expect(mcpTab).toBeVisible({ timeout: 8_000 });
  await mcpTab.click();

  // "Add Server" button
  const addServerBtn = page.getByRole('button', { name: /Add Server/i });
  await expect(addServerBtn).toBeVisible({ timeout: 8_000 });
  await addServerBtn.click();

  // McpServerModal opens as a dialog with title "Add MCP Server"
  const modal = page.locator('[role="dialog"]').filter({ hasText: /Add MCP Server/i }).first();
  await expect(modal).toBeVisible({ timeout: 10_000 });

  // McpServerModal (#336) has a mode picker: "A network address" (default) / "A local program".
  // Default mode is 'network' — stay in network mode to avoid the stdio AlertDialog confirm.
  // In network mode the form has:
  //   - Name input (first <input> in the dialog, no testid)
  //   - Server URL input (data-testid="network-url")
  // canSubmit = name.trim().length > 0 && networkUrlValid (url must have https:// scheme)

  // Fill the Name field — pressSequentially() is required; fill() doesn't fire React onChange.
  const nameInput = modal.locator('input').first();
  await expect(nameInput).toBeVisible({ timeout: 10_000 });
  await nameInput.pressSequentially('existing-server');

  // Fill the Server URL field using its testid
  const urlInput = modal.locator('[data-testid="network-url"]');
  await expect(urlInput).toBeVisible({ timeout: 5_000 });
  await urlInput.pressSequentially('https://mcp.example.com/sse');

  // Submit — button text is "Add server" (data-testid="submit-add" — McpServerModal.tsx:405)
  const submitBtn = modal.getByRole('button', { name: /add server/i }).first();
  await expect(submitBtn).toBeEnabled({ timeout: 5_000 });
  await submitBtn.click();

  // On 409, McpServerModal calls addToast({ message: err.userMessage }) where
  // defaultUserMessage(409) = "This conflicts with the current state. Please refresh and try again."
  // (src/lib/api-error.ts:50, McpServerModal.tsx:174-182)
  const errorToast = page.locator('text=conflicts with the current state').first();
  await expect(errorToast).toBeVisible({ timeout: 10_000 });
});

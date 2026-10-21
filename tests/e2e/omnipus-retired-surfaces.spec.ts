/**
 * omnipus-retired-surfaces.spec.ts — WU-11 E2E: the retired routes are gone
 * WITHOUT forwarding, and no manual create action exists on any screen.
 *
 * Oracles (omnipus-system-agent-spec.md):
 *   - BDD-12.7 Old links (US-12 AS-5, Error Path, Outline): Given /agents,
 *     /agents/mia, /admin/chat — "Then not-found, no redirect".
 *   - FR-108: "The Agents screen and routes, the create wizard/modal and
 *     /admin/chat MUST be removed without forwarding."
 *   - BDD-12.8 No create form: "Given any screen — Then no manual create
 *     action exists; Add agent opens Omnipus."
 *   - SC-007 (team-lead steer): not-found text alone is not enough —
 *     /agents/mia and /admin/chat REDIRECT today, so each case also asserts
 *     the URL is unchanged.
 *
 * Runs in CI (stubs-1 shard) — never locally. The unit-level counterpart of
 * the same oracle is src/routes/_app/-wu11-retired-routes.test.tsx.
 *
 * CLAUDE.md — "E2E tests always target the embedded SPA (Go binary)".
 * HashRouter: routes live in the fragment, not the pathname.
 */
import { expect, test } from '@playwright/test'

const NOT_FOUND_HEADING = /drifted into the deep/

async function expectRetired(page: import('@playwright/test').Page, hash: string): Promise<void> {
  await page.goto(hash)
  // BDD-12.7: the branded 404 (src/routes/__root.tsx::NotFoundPage).
  await expect(page.getByText(NOT_FOUND_HEADING)).toBeVisible({ timeout: 15_000 })
  // SC-007: no redirect — the URL stays exactly where the old link points.
  expect(page.url()).toContain(hash)
  // FR-108/BDD-12.8: none of the removed surfaces may render.
  await expect(page.getByRole('button', { name: 'New agent' })).toHaveCount(0)
  await expect(page.getByText('Restoring your conversation…')).toHaveCount(0)
}

test.describe('WU-11 — retired surfaces are removed without forwarding', () => {
  test('BDD-12.7 /#/agents lands on not-found and keeps the URL', async ({ page }) => {
    await expectRetired(page, '/#/agents')
  })

  test('BDD-12.7 /#/agents/mia lands on not-found and keeps the URL (deep link does not open the editor)', async ({ page }) => {
    await expectRetired(page, '/#/agents/mia')
  })

  test('BDD-12.7 /#/admin/chat lands on not-found and keeps the URL (no forwarding to a workspace chat)', async ({ page }) => {
    await expectRetired(page, '/#/admin/chat')
  })

  test('BDD-12.8 the signed-in shell offers no Agents link and no manual create action', async ({ page }) => {
    await page.goto('/#/')
    // A fresh visit is not pinned: the sidebar starts closed and the header
    // shows the hamburger (Show sidebar) — the same precondition every other
    // shell spec handles (tests/e2e/sidebar.spec.ts). Open it so the nav is
    // actually on screen; without this the CI viewport has no nav at all and
    // the no-Agents-link assertion would pass vacuously.
    const showSidebar = page.getByRole('button', { name: 'Show sidebar' })
    await expect(showSidebar).toBeVisible({ timeout: 15_000 })
    await showSidebar.click()
    // The shell settles on the default workspace chat; the opened nav must
    // not carry the retired Agents entry (Sidebar.tsx::ASSET_ITEMS) and no
    // create-agent control may exist anywhere on it.
    const nav = page.locator('nav[aria-label="Main navigation"]')
    await expect(nav).toBeVisible({ timeout: 15_000 })
    await expect(page.getByRole('link', { name: 'Agents' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'New agent' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'New Main' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: 'New Subagent' })).toHaveCount(0)
    await expect(page.getByRole('button', { name: /Add Subagent/ })).toHaveCount(0)
  })
})

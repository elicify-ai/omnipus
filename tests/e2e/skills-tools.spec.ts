// skills-tools.spec.ts — order 31 (E2E, written and NOT run per the
// dispatch brief; targeted Playwright spec file). Traces to: S-03, S-15,
// S-20, S-57, S-58, S-60 to S-65, S-68, S-69.
//
// Oracles: skills-tools-redesign-spec.md — the page opens on the Skills tab
// with counts (S-03); a service's switch acts at once (S-60); a failing
// service shows its reason with Retry and Edit (S-61); the MCP panel reads
// tools and descriptions (S-62 to S-65); skill install and grant flows
// (S-15, S-20); agent Tools tab rows (S-57, S-58, S-68, S-69).
//
// This file is a RED skeleton: the page and its surfaces are GREEN work,
// so the assertions below fail loudly (page/route not implemented) until
// the component lands. Not executed locally — E2E belongs to CI at the cut.
import { test, expect } from '@playwright/test'

test.describe('Skills & Tools page (order 31)', () => {
  test('BLOCKED until the page route exists', async ({ page }) => {
    await page.goto('/skills')
    // The rewritten page owns /skills; today the old screen renders there.
    await expect(
      page.getByRole('heading', { name: /Skills & Tools/i }),
      'BLOCKED: the rewritten Skills & Tools page is not implemented — required by skills-tools-redesign-spec S-03, S-15, S-20, S-57, S-58, S-60 to S-65, S-68, S-69',
    ).toBeVisible()
  })
})

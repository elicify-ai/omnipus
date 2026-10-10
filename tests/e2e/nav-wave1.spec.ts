import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import type { Session as WireSession } from '../../src/lib/api/generated/openapi-types'

// FR-026 / FR-020. The browser surfaces that do not need a model turn.
// The inline indicator's path matrix is the vitest file
// src/components/chat/ChatScreen.wave1-indicator.test.tsx — a live turn is
// not started here.

test('Sessions view is titled Sessions', async ({ page }) => {
  await page.goto('/#/')
  // The sidebar starts closed unless pinned; its search button is inside it.
  await page.getByRole('button', { name: 'Show sidebar', exact: true }).click()
  await page.getByRole('button', { name: 'Search sessions' }).click()
  await expect(page.getByRole('heading', { name: 'Sessions', exact: true })).toBeVisible({ timeout: 15_000 })
})

// W1-9 / phone live-check: same-prefix titles must remain distinguishable.
// These are complete generated wire fixtures, not hand-written API types.
function phoneSession(id: string, title: string): WireSession {
  return {
    id, title, agent_id: 'mia', type: 'chat', status: 'active', lifecycle_state: 'done',
    created_at: '2026-10-08T09:00:00Z', updated_at: '2026-10-08T10:00:00Z',
    channel: 'webchat', partitions: [],
    stats: { tokens_in: 10, tokens_out: 20, tokens_total: 30, cost: 0, tool_calls: 0, message_count: 2 },
  }
}

test('phone Sessions titles wrap above metadata and keep same-prefix chats distinguishable', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const sessions = [
    phoneSession('phone-identity', 'Navigation review — selected figure and role identity'),
    phoneSession('phone-channel', 'Navigation review — channel routing and human names'),
  ]
  await page.route((url) => url.pathname === '/api/v1/sessions', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ sessions }),
  }))
  await page.goto('/#/')
  await page.getByRole('button', { name: 'Show sidebar', exact: true }).click()
  await page.getByRole('button', { name: 'Search sessions' }).click()
  await expect(page.getByRole('heading', { name: 'Sessions', exact: true })).toBeVisible()
  for (const session of sessions) {
    const row = page.locator(`[data-session-id="${session.id}"]`)
    const open = row.getByRole('button', { name: `Open ${session.title}`, exact: true })
    const title = row.getByText(session.title, { exact: true })
    await expect(open).toBeVisible()
    await expect(title).toBeVisible()
    const layout = await title.evaluate((node) => {
      const titleBox = node.getBoundingClientRect()
      const action = node.closest('button')
      const metadata = action?.querySelector('[data-testid^="session-status-"]')
      if (!action || !metadata) throw new Error('Session Open action or metadata is missing')
      const actionBox = action.getBoundingClientRect()
      return {
        titleLineWidth: node.parentElement?.getBoundingClientRect().width, actionWidth: actionBox.width,
        titleBottom: titleBox.bottom, metadataTop: metadata.getBoundingClientRect().top,
        whiteSpace: getComputedStyle(node).whiteSpace,
        clippedHorizontally: node.scrollWidth > node.clientWidth,
        clippedVertically: node.scrollHeight > node.clientHeight,
        viewportOverflow: document.documentElement.scrollWidth > window.innerWidth,
      }
    })
    // Relationship oracles: title owns the Open action's width, and status is
    // BELOW it. A horizontally squeezed/truncated title fails these checks.
    expect(layout.titleLineWidth).toBe(layout.actionWidth)
    expect(layout.metadataTop).toBeGreaterThanOrEqual(layout.titleBottom)
    expect(layout.whiteSpace).toBe('normal')
    expect(layout.clippedHorizontally).toBe(false)
    expect(layout.clippedVertically).toBe(false)
    expect(layout.viewportOverflow).toBe(false)
  }
  await page.screenshot({ path: testInfo.outputPath('sessions-phone-reflow.png'), fullPage: true })
})

test('a built-in agent shows locked figure, role and colour and no upload', async ({ page }) => {
  await page.goto('/#/agents')
  await page.getByTestId('agent-card-mia').click()
  const figure = page.getByRole('button', { name: 'Omnipus' })
  await expect(figure).toBeVisible({ timeout: 15_000 })
  await expect(figure).toBeDisabled()
  await expect(page.getByRole('button', { name: 'General assistant' })).toBeDisabled()
  await expect(page.getByRole('button', { name: 'Azure' })).toBeDisabled()
  await expect(page.locator('input[type="file"]')).toHaveCount(0)
})

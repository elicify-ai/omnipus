/**
 * side-panel-expand-multitab.spec.ts — side-panel-shell-spec.md §9 W4, W5, W7.
 *
 * WRITTEN, NOT EXECUTED in the RED pass. Parse is proved with
 * `npx playwright test --list`. Multi-tab rows (OBS-201).
 *
 * W4 Expand opens the full-page Library and closes the docked panel.
 * W5 Clicking the toggle again focuses the app-opened tab; no second tab.
 * W7 A manually opened full-page tab is not duplicated; the affordance shows.
 */
import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'

async function createWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext()
  try {
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name: `E2E expand ${Date.now()}` },
    })
    if (!res.ok()) throw new Error(`createWorkspace ${res.status()}: ${await res.text()}`)
    return ((await res.json()) as { id: string }).id
  } finally {
    await ctx.dispose()
  }
}

async function deleteWorkspace(id: string): Promise<void> {
  const ctx = await newAdminApiContext()
  try {
    await ctx.delete(`/api/v1/workspaces/${encodeURIComponent(id)}`)
  } catch {
    // best-effort
  } finally {
    await ctx.dispose()
  }
}

let workspaceId: string

test.beforeAll(async () => {
  workspaceId = await createWorkspace()
})

test.afterAll(async () => {
  if (workspaceId) await deleteWorkspace(workspaceId)
})

test('W4 — Expand opens full-page Library and closes the docked panel', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  const pop = await opened
  await expect(pop).toHaveURL(/\/library/)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
})

test('W5 — a second Library toggle focuses the existing tab and does not open another', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  await opened
  const before = context.pages().length
  await page.getByRole('button', { name: /^library$/i }).click()
  await expect.poll(() => context.pages().length).toBe(before)
})

test('W7 — a manually opened Library tab is not duplicated; the switch affordance shows', async ({ page, context }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  const manual = await context.newPage()
  await manual.goto(`/#/library?workspace=${workspaceId}`)
  await page.bringToFront()
  await page.getByRole('button', { name: /^library$/i }).click()
  await expect(page.getByText(/already open/i)).toBeVisible()
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  expect(context.pages().filter((p) => p.url().includes('/library')).length).toBe(1)
})

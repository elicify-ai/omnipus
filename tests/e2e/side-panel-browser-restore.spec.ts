/**
 * side-panel-browser-restore.spec.ts — side-panel-shell-spec.md §9 W9–W10, §12 #16.
 *
 * WRITTEN, NOT EXECUTED in the RED pass. Parse is proved with
 * `npx playwright test --list`.
 *
 * W9  a live Browser panel does not come back on reload (SP-28).
 * W10 Back then Forward follows the store, not a stale panel value (MAJ-206).
 */
import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'

async function createWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext()
  try {
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name: `E2E browser restore ${Date.now()}` },
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

test('W9 — reload does not restore the Browser panel', async ({ page }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  await page.getByRole('button', { name: /open browser|watch live/i }).click()
  await expect(page.getByTestId('side-panel-header')).toContainText('Browser')
  await page.reload()
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  await expect(page).not.toHaveURL(/panel=browser/)
})

test('W10 — Back then Forward follows the store, not a stale panel value', async ({ page }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  await page.getByRole('button', { name: /open browser|watch live/i }).click()
  await expect(page.getByTestId('side-panel-header')).toContainText('Browser')
  const before = page.url()
  await page.goBack()
  await page.goForward()
  await expect(page).toHaveURL(before)
  const panels = await page.getByTestId('side-panel').count()
  expect(panels).toBeLessThanOrEqual(1)
})

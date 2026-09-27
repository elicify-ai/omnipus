/**
 * side-panel-signin-return.spec.ts — side-panel-shell-spec.md §9 W11, §12 #16 / #18.
 *
 * WRITTEN, NOT EXECUTED in the RED pass. Parse is proved with
 * `npx playwright test --list`.
 *
 * W11: a signed-out visit to a panel link, then sign-in, lands on that link
 * (workspace + panel), not on `/` (SP-27).
 *
 * This file logs in through the shared UI helper and rewrites storageState in
 * afterAll, the same recovery auth.spec.ts uses, so the single-slot session
 * cookie is valid for every spec that runs after it.
 */
import path from 'path'
import { fileURLToPath } from 'url'
import { chromium, expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { loginAs } from './fixtures/login'
import { newAdminApiContext } from './fixtures/admin-api'

const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE
  ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
  : path.join(path.dirname(fileURLToPath(import.meta.url)), 'fixtures/.auth/admin.json')

test.use({ storageState: { cookies: [], origins: [] } })

async function createWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext()
  try {
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name: `E2E signin return ${Date.now()}` },
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
  const browser = await chromium.launch()
  const context = await browser.newContext({
    baseURL: process.env.OMNIPUS_URL || 'http://localhost:6060',
  })
  const page = await context.newPage()
  await page.goto('/')
  await loginAs(page, 'admin', 'admin123')
  await context.storageState({ path: AUTH_FILE })
  await browser.close()
})

// squad-lead ruling (batch 2): calendar is unregistered until wave 3
// (§8.2/§10, §12 dataset row 7) — wave 1 exercises W11's sign-in return with
// the registered `library` panel; re-run with ?panel=calendar in wave 3.
test('W11 — sign-in returns to the opened panel link, not /', async ({ page }) => {
  const target = `/#/workspaces/${workspaceId}/chat?panel=library`
  await page.goto(target)
  await loginAs(page, 'admin', 'admin123')
  await expect(page).toHaveURL(new RegExp(`/workspaces/${workspaceId}/chat`))
  await expect(page).toHaveURL(/panel=library/)
  await expect(page).not.toHaveURL(/#\/$/)
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
})

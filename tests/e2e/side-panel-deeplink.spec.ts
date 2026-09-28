/**
 * side-panel-deeplink.spec.ts — side-panel-shell-spec.md §9 rows W1–W3, §12 #16.
 *
 * WRITTEN, NOT EXECUTED in the RED pass. Parse is proved with
 * `npx playwright test --list`. The rows need the wave-1 shell in the running app.
 *
 * W1  ?panel=library restores the Library panel.
 *     (squad-lead ruling, batch 2: calendar is NOT a registered panel until
 *     wave 3 — spec §8.2/§10 and §12 dataset row 7 drop it in wave 1 — so
 *     wave 1 runs this row against `library`; the Calendar variant re-runs in
 *     wave 3.)
 * W2  ?panel=bogus and ?panel=tasks (unregistered in wave 1) are dropped.
 * W3  ?panel=browser, bare or with session/agent, is always dropped (SP-28).
 */
import { expect } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'

async function createWorkspace(): Promise<string> {
  const ctx = await newAdminApiContext()
  try {
    const res = await ctx.post('/api/v1/workspaces', {
      data: { name: `E2E side panel ${Date.now()}` },
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

// squad-lead ruling (batch 2): calendar is unregistered until wave 3
// (§8.2/§10, §12 dataset row 7) — wave 1 exercises W1 with the registered
// `library` panel; re-run with ?panel=calendar in wave 3.
test('W1 — ?panel=library restores the Library panel', async ({ page }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  await expect(page).toHaveURL(/panel=library/)
})

test('W2 — unknown and unregistered panel ids are dropped', async ({ page }) => {
  for (const panel of ['bogus', 'tasks']) {
    await page.goto(`/#/workspaces/${workspaceId}/chat?panel=${panel}`)
    await expect(page.getByTestId('side-panel')).toHaveCount(0)
    await expect(page).not.toHaveURL(new RegExp(`panel=${panel}`))
  }
})

test('W3 — panel=browser is always dropped, and no session id stays in the link', async ({ page }) => {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=browser`)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  await expect(page).not.toHaveURL(/panel=browser/)

  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=browser&session=s1&agent=a1`)
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  await expect(page).not.toHaveURL(/panel=browser/)
  await expect(page).not.toHaveURL(/session=/)
})

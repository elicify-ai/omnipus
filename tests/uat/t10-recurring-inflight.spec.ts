/** Post-landing Test Lab / prepared exact-cut browser lane, not a CI shard.
 * Requires an isolated held-worker fixture and its own tester account.
 * No product PASS is claimed by collection; team-lead supplies fixture receipt.
 */
import { expect } from '@playwright/test'
import { test } from '../e2e/fixtures/console-errors'
import { newAdminApiContext } from '../e2e/fixtures/admin-api'

// Traces to: schedules-t10-spec-r6.md lines 1066–1076, 1277; TDD 24, D-9.1.
// RED binding: the acceptance lane must supply an isolated task whose REAL
// worker is held in flight. No route interception, mocked 200, seeded status,
// or skipped test can stand in for an actual run. This file does not mint a
// shared login or operate on an arbitrary founder task.
test('T10 #1243: After N save during a real in-flight run never blames a stale view', async ({ page }, info) => {
  const id = process.env.T10_INFLIGHT_TASK_ID
  const ws = process.env.T10_INFLIGHT_WORKSPACE_ID
  if (!id || !ws) throw new Error('BLOCKED: supply T10_INFLIGHT_TASK_ID and T10_INFLIGHT_WORKSPACE_ID from the isolated held-worker fixture; an actual open run is required by S-J.2/D-9.1')
  const api = await newAdminApiContext()
  try {
    const read = await api.get(`/api/v1/tasks/${encodeURIComponent(id)}`)
    expect(read.ok()).toBe(true)
    const task = await read.json()
    expect(task.workspace_id).toBe(ws)
    expect(task.trigger.type).toBe('recurring')
    const before = await api.get(`/api/v1/tasks/${encodeURIComponent(id)}/runs`)
    expect(before.ok()).toBe(true)
    const run = (await before.json()).find((r: { status: string; ended_at?: string | null }) => r.status === 'in_progress' && !r.ended_at)
    expect(run, 'fixture must have a real open run, not just task.status=in_progress').toBeTruthy()
    await page.goto(`/#/workspaces/${encodeURIComponent(ws)}/calendar`)
    await page.waitForLoadState('networkidle')
    await page.locator('.fc-event', { hasText: task.title }).first().click()
    const repeat = page.getByRole('combobox', { name: 'Repeat', exact: true })
    await expect(repeat).toBeVisible()
    // Choosing Custom is a real user action, not direct state manipulation.
    await repeat.click()
    await page.getByRole('option', { name: 'Custom…', exact: true }).click()
    await page.getByRole('button', { name: 'After', exact: true }).click()
    await page.getByLabel('Number of occurrences', { exact: true }).fill('7')
    const responsePromise = page.waitForResponse(r => r.url().endsWith(`/api/v1/tasks/${id}`) && r.request().method() === 'PATCH')
    await page.getByRole('button', { name: 'Save', exact: true }).click()
    const response = await responsePromise
    const payload = response.request().postDataJSON()
    expect(payload.trigger.config.rrule).toContain('COUNT=7')
    const body = await response.text()
    expect(body).not.toMatch(/stale.view|view.*(stale|out.of.date)|reload.*view/i)
    if (response.ok()) {
      const saved = await api.get(`/api/v1/tasks/${encodeURIComponent(id)}`)
      expect(saved.ok()).toBe(true)
      expect((await saved.json()).trigger.config.rrule).toContain('COUNT=7')
    } else {
      expect([400, 409]).toContain(response.status())
      expect(body).toMatch(/(active|in.flight|running).*(run|definition|criteria)|(run|definition|criteria).*(active|in.flight|running)/i)
      await expect(page.getByText(/(active|in.flight|running).*(run|definition|criteria)/i).first()).toBeVisible()
    }
    const after = await api.get(`/api/v1/tasks/${encodeURIComponent(id)}/runs`)
    expect(after.ok()).toBe(true)
    expect((await after.json()).find((r: { run_id: string }) => r.run_id === run.run_id)?.ended_at).toBeFalsy()
    await page.screenshot({ path: info.outputPath('t10-after-n-save.png'), fullPage: true })
  } finally {
    await api.dispose()
  }
})

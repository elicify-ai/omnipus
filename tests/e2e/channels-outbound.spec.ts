/**
 * T-31 / S-80 / S-81 / S-82 / FR-026, channels-spec-r8.
 * Real embedded SPA, panel, clients, generated validation and native keyboard.
 * Only HTTP edges are intercepted. No live Telegram/Discord/model call.
 * Persistence below means UI read-after-write against this fake HTTP server;
 * actual durable backend persistence belongs to T-14, not these browser rows.
 */
import { expect, type Locator, type Page, type Route } from '@playwright/test'
import { test } from './fixtures/console-errors'
import {
  ChannelEntry as EntrySchema,
  ChannelOutboundRead as ReadSchema,
  ChannelOutboundRejection as RejectionSchema,
  ChannelOutboundUpdateRequest as UpdateSchema,
  ChannelOutboundWriteResponse as WriteSchema,
} from '../../src/lib/api/generated/schemas'

// No live-model variance here: a failed deterministic UI row is not retried.
test.describe.configure({ retries: 0 })

const FIELD = 'People and groups the agent may message'
const CANONICAL = ['telegram:123456', 'telegram:-1001234567890/45']
const BARE = ['123456', '-1001234567890/45']
const STALE = 'These settings changed since you opened them. The current values are shown; check them and save again.'

async function stubOutbound(page: Page, mode: 'success' | 'invalid' | 'stale') {
  let read = ReadSchema.parse({
    outbound: { policy: mode === 'stale' ? 'agent_may_start' : 'reply_only', targets: mode === 'stale' ? ['telegram:111'] : [] },
    revision: 't31-v1', availability: { state: 'not_running', reason: 'not started' }, start_supported: true,
  })
  const writes: ReturnType<typeof UpdateSchema.parse>[] = []
  const unexpected: string[] = []
  let reads = 0
  const json = (route: Route, status: number, value: unknown) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
  await page.route('**/api/v1/channels', (route) => json(route, 200, [EntrySchema.parse({
    id: 'telegram', instance_id: 'telegram', identity: { kind: 'agent', id: 'mia' },
    name: 'Telegram', transport: 'webhook', enabled: false, description: 'Synthetic local Channels test connection',
  })]))
  await page.route('**/api/v1/channels/telegram', (route) => json(route, 200, { token: '[configured]' }))
  await page.route('**/api/v1/channels/telegram/routing', (route) => json(route, 200, { workspace_id: '', default_agent_id: '', bound: false }))
  await page.route('**/api/v1/agents', (route) => json(route, 200, []))
  await page.route('**/api/v1/workspaces?**', (route) => json(route, 200, []))
  for (const action of ['configure', 'enable', 'disable']) {
    await page.route(`**/api/v1/channels/telegram/${action}`, (route) => {
      unexpected.push(`${route.request().method()} ${action}`)
      return json(route, 500, { error: 'Starting messages must use only its dedicated outbound route' })
    })
  }
  await page.route('**/api/v1/channels/telegram/outbound', async (route) => {
    if (route.request().method() === 'GET') {
      reads++
      await json(route, 200, read)
      return
    }
    expect(route.request().method()).toBe('PUT')
    const requested = UpdateSchema.parse(route.request().postDataJSON())
    writes.push(requested)
    if (mode === 'invalid') {
      await json(route, 400, RejectionSchema.parse({
        code: 'invalid_targets', error: 'invalid targets',
        target_errors: [{ index: 1, target: '@alice', code: 'username_not_allowed', reason: 'a message cannot be sent to a name; use the numeric id' }],
      }))
      return
    }
    if (mode === 'stale') {
      read = ReadSchema.parse({ ...read, outbound: { policy: 'agent_may_start', targets: ['telegram:999'] }, revision: 't31-concurrent' })
      await json(route, 409, RejectionSchema.parse({ error: 'stale revision', code: 'stale_revision' }))
      return
    }
    const reply = WriteSchema.parse({ outbound: { policy: requested.policy, targets: CANONICAL }, revision: writes.length === 1 ? 't31-v2' : 't31-v3' })
    read = ReadSchema.parse({ ...read, outbound: reply.outbound, revision: reply.revision })
    await json(route, 200, reply)
  })
  return { writes, unexpected, readCount: () => reads }
}

async function openPanel(page: Page) {
  await page.goto('/#/connectors')
  const configure = page.getByRole('button', { name: 'Configure telegram', exact: true })
  await expect(configure).toBeVisible()
  await configure.click()
  const panel = page.getByRole('dialog')
  await expect(panel).toBeVisible()
  await expect(panel.getByText('Starting messages', { exact: true })).toBeVisible()
  return panel
}

function ownSave(panel: Locator) {
  // The nearest ancestor with Save is the section's own form, not the generic
  // Configure Save. Require the catalogued button; no invented test-id/tag.
  return panel.getByRole('radio', { name: 'Only answer', exact: true })
    .locator('xpath=ancestor::*[.//button[normalize-space(.)="Save"]][1]')
    .getByRole('button', { name: 'Save', exact: true })
}

async function tabTo(page: Page, target: Locator) {
  // A bounded traversal is a hang guard, not a synthetic focus operation.
  // Every transition uses a real browser Tab. Radix roving radio focus is
  // exercised separately with arrows; fireEvent(Tab) is not a substitute.
  for (let i = 0; i < 80; i++) {
    if (await target.evaluate((element) => element === document.activeElement)) return
    await page.keyboard.press('Tab')
  }
  await expect(target, 'S-82: keyboard Tab traversal must reach this control without programmatic focus').toBeFocused()
}

test('T-31 saves Telegram ids, reopens with canonical readback and switches back without losing the list', async ({ page, consoleErrors }) => {
  const server = await stubOutbound(page, 'success')
  let panel = await openPanel(page)
  await expect(panel.getByRole('radio', { name: 'Only answer', exact: true })).toBeChecked()
  await expect(panel.getByRole('textbox', { name: FIELD })).toHaveCount(0)
  await panel.getByRole('radio', { name: 'Answer and also start messages', exact: true }).click()
  await panel.getByRole('textbox', { name: FIELD }).fill(BARE.join('\n'))
  await ownSave(panel).click()
  await expect.poll(() => server.writes).toEqual([{ policy: 'agent_may_start', targets: BARE, expected_revision: 't31-v1' }])
  // Joining the mutation before closing prevents a local dirty form from
  // masquerading as persisted server state on reopen.
  await expect(ownSave(panel)).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(panel).not.toBeVisible()
  const readsBeforeReopen = server.readCount()
  await page.getByRole('button', { name: 'Configure telegram', exact: true }).click()
  panel = page.getByRole('dialog')
  await expect.poll(server.readCount).toBeGreaterThan(readsBeforeReopen)
  await expect(panel.getByRole('textbox', { name: FIELD })).toHaveValue(BARE.join('\n'))
  await expect(panel.getByRole('radio', { name: 'Answer and also start messages', exact: true })).toBeChecked()
  await panel.getByRole('radio', { name: 'Only answer', exact: true }).click()
  await expect(panel.getByRole('textbox', { name: FIELD })).toHaveCount(0)
  await ownSave(panel).click()
  await expect.poll(() => server.writes).toEqual([
    { policy: 'agent_may_start', targets: BARE, expected_revision: 't31-v1' },
    { policy: 'reply_only', targets: BARE, expected_revision: 't31-v2' },
  ])
  expect(server.unexpected).toEqual([])
  expect(consoleErrors).toEqual([])
})

test('T-31 / S-82 keyboard-only policy, textarea and Save announce an indexed FormError', async ({ page }) => {
  // This row deliberately provokes HTTP 400. It makes no zero-console-error
  // claim; the happy-path row above explicitly activates that fixture.
  const server = await stubOutbound(page, 'invalid')
  const panel = await openPanel(page) // Given: the real section is open.
  const onlyAnswer = panel.getByRole('radio', { name: 'Only answer', exact: true })
  const mayStart = panel.getByRole('radio', { name: 'Answer and also start messages', exact: true })
  await tabTo(page, onlyAnswer)
  await expect(onlyAnswer).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(mayStart).toBeFocused()
  await expect(mayStart).toBeChecked()
  await page.keyboard.press('ArrowUp')
  await expect(onlyAnswer).toBeFocused()
  await expect(onlyAnswer).toBeChecked()
  await page.keyboard.press('ArrowDown')
  await expect(mayStart).toBeChecked()
  const field = panel.getByRole('textbox', { name: FIELD })
  await tabTo(page, field)
  await page.keyboard.insertText('111\n@alice')
  await expect(field).toHaveValue('111\n@alice')
  await tabTo(page, ownSave(panel))
  await page.keyboard.press('Enter')
  await expect.poll(() => server.writes).toEqual([{ policy: 'agent_may_start', targets: ['111', '@alice'], expected_revision: 't31-v1' }])
  // role=alert has implicit assertive live semantics, as FormError does today.
  const announcement = panel.getByRole('alert').filter({ hasText: 'a message cannot be sent to a name; use the numeric id' })
  await expect(announcement).toBeVisible()
  await expect(announcement).toContainText(/(?:^|\D)2(?:\D|$)/)
  await expect(field).toHaveValue('111\n@alice')
  expect(server.unexpected).toEqual([])
})

test('T-31 stale revision rereads current values instead of overwriting the concurrent list', async ({ page }) => {
  const server = await stubOutbound(page, 'stale')
  const panel = await openPanel(page)
  const field = panel.getByRole('textbox', { name: FIELD })
  await expect(field).toHaveValue('111')
  await field.fill('333')
  await ownSave(panel).click()
  await expect.poll(() => server.writes).toEqual([{ policy: 'agent_may_start', targets: ['333'], expected_revision: 't31-v1' }])
  await expect(field).toHaveValue('999')
  await expect(page.getByText(STALE, { exact: true })).toBeVisible()
  expect(server.readCount()).toBeGreaterThanOrEqual(2)
  expect(server.unexpected).toEqual([])
})

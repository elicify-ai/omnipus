/**
 * side-panel-expand-multitab.spec.ts — side-panel-shell-spec.md §9 W4, W5, W7, W8.
 *
 * Multi-tab rows (OBS-201). W7 was first executed by CI run 36915376719 and
 * reported flaky (attempt 1 clicked the toggle before the manual tab's
 * presence announcement had been processed; retry passed). Triage
 * (REPORT-w7-library-affordance-race.md) ruled it a test-setup
 * synchronization gap, not a production defect: §8.3 resolves an entry-point
 * click synchronously — "no wait window".
 *
 * The W7/W8 synchronization seam: before clicking, the row PROVES the chat
 * page's own presence monitor has processed the manual tab's real
 * announcement. A passive tee — installed by addInitScript BEFORE any app
 * script — wraps window.BroadcastChannel and, for every message event any
 * channel dispatches, defers a tee entry with setTimeout(0). That macrotask
 * cannot run until the whole dispatch task has finished, i.e. until every
 * other listener on that channel — the app presence monitor's, among them —
 * has fully run. A tee entry of `presence` carrying the §8.3 identity key
 * (sha256(`library:<workspaceId>`)) therefore proves the app monitor stored
 * the announcement. Nothing is fabricated, production delivery is not
 * intercepted, and no production test hook is involved. If the announcement
 * is delayed or removed, the gate fails by timeout and the row stays red.
 *
 * W4 Expand opens the full-page Library and closes the docked panel.
 * W5 Clicking the toggle again focuses the app-opened tab; no second tab.
 * W7 A manually opened full-page tab is not duplicated; the affordance shows.
 * W8 Closing the manual tab; the toggle opens the docked panel normally.
 */
import { createHash } from 'node:crypto'
import { expect, type Page } from '@playwright/test'
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

/** `PRESENCE_CHANNEL_NAME` in src/lib/panelTabPresence.ts. */
const PRESENCE_CHANNEL_NAME = 'omnipus-panel-tab-presence'

type PresenceTeeEntry = { channel: string; data: unknown }

declare global {
  interface Window {
    __w7PresenceTee?: PresenceTeeEntry[]
  }
}

/**
 * Passive BroadcastChannel tee. It only ADDS a listener per constructed
 * channel — messages reach the app's listeners exactly as before, so
 * production delivery is untouched. The tee listener registers at
 * construction, before the app registers its own, and its entry lands in a
 * later macrotask: entry present ⇒ the app's listener for that same event
 * already ran to completion.
 */
const PRESENCE_TEE_INIT_SCRIPT = `(() => {
  const Native = window.BroadcastChannel
  if (!Native) return
  const tee = (window.__w7PresenceTee = [])
  class TeeBroadcastChannel extends Native {
    constructor(...args) {
      super(...args)
      const channelName = String(args[0] ?? '')
      this.addEventListener('message', (event) => {
        setTimeout(() => {
          tee.push({ channel: channelName, data: event.data })
        }, 0)
      })
    }
  }
  window.BroadcastChannel = TeeBroadcastChannel
})()`

function installPresenceTee(page: Page) {
  return page.addInitScript(PRESENCE_TEE_INIT_SCRIPT)
}

function readPresenceTee(page: Page): Promise<PresenceTeeEntry[]> {
  return page.evaluate(() => window.__w7PresenceTee ?? [])
}

/** §8.3 identity key — `panelId × workspaceId`, opaque as sha256, exactly the
 * key `panelPresenceKey` publishes and `resolveExistingPanelTab` matches. */
function libraryPresenceKey(workspaceId: string): string {
  return createHash('sha256').update(`library:${workspaceId}`).digest('hex')
}

type PresenceWireData = { type: unknown; tabId: unknown; identityKey: unknown }

function isPresenceForLibrary(
  entry: PresenceTeeEntry,
  identityKey: string,
): entry is PresenceTeeEntry & { data: PresenceWireData & { tabId: string } } {
  if (entry.channel !== PRESENCE_CHANNEL_NAME) return false
  const data = entry.data as Partial<PresenceWireData> | null
  return (
    data !== null &&
    typeof data === 'object' &&
    data.type === 'presence' &&
    data.identityKey === identityKey &&
    typeof data.tabId === 'string'
  )
}

function isLeaveForTab(entry: PresenceTeeEntry, tabId: string): boolean {
  if (entry.channel !== PRESENCE_CHANNEL_NAME) return false
  const data = entry.data as Partial<PresenceWireData> | null
  return (
    data !== null &&
    typeof data === 'object' &&
    data.type === 'leave' &&
    data.tabId === tabId
  )
}

/**
 * W7/W8 gate: resolves with the announcing tab's id only once the chat page's
 * OWN app presence monitor has processed the manual tab's real announcement
 * (the manual tab announces through the unmodified production path). No
 * announcement ⇒ timeout ⇒ red — never a widened wait.
 */
async function awaitLibraryPresenceAtChatPage(page: Page, identityKey: string): Promise<string> {
  let announcedTabId = ''
  await expect
    .poll(
      async () => {
        announcedTabId =
          (await readPresenceTee(page))
            .find((entry) => isPresenceForLibrary(entry, identityKey))
            ?.data.tabId ?? ''
        return announcedTabId !== ''
      },
      {
        timeout: 15_000,
        message:
          'Manual Library tab presence never reached the chat page presence monitor; the toggle click would race the announcement (§8.3 has no wait window).',
      },
    )
    .toBe(true)
  return announcedTabId
}

/** W8 counterpart: the closed manual tab's `leave` was processed too, so the
 * click below is genuinely past the presence window. */
async function awaitLibraryLeaveAtChatPage(page: Page, tabId: string): Promise<void> {
  await expect
    .poll(
      async () => (await readPresenceTee(page)).some((entry) => isLeaveForTab(entry, tabId)),
      {
        timeout: 15_000,
        message: `Closed manual tab ${tabId} never announced leave to the chat page presence monitor; the toggle click would race the leave.`,
      },
    )
    .toBe(true)
}

/**
 * The workspace-tab-bar Library toggle — the entry point the W7 CI trace
 * resolved (`WORKSPACE_TABS` segment 'media' renders data-testid
 * `workspace-tab-media` with data-panel-trigger `library`). Pinned by BOTH
 * attributes so a sidebar or other "Library" control can never satisfy the
 * row with a different identity (`library:app`).
 */
function libraryWorkspaceToggle(page: Page) {
  return page.getByTestId('workspace-tab-media').and(page.locator('[data-panel-trigger="library"]'))
}

test('W7 — a manually opened Library tab is not duplicated; the switch affordance shows', async ({ page, context }) => {
  await installPresenceTee(page)
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  const manual = await context.newPage()
  await manual.goto(`/#/library?workspace=${workspaceId}`)
  await page.bringToFront()

  await awaitLibraryPresenceAtChatPage(page, libraryPresenceKey(workspaceId))

  await libraryWorkspaceToggle(page).click()
  await expect(page.getByText(/already open/i)).toBeVisible()
  await expect(page.getByTestId('side-panel')).toHaveCount(0)
  expect(context.pages().filter((p) => p.url().includes('/library')).length).toBe(1)
})

test('W8 — after the manual Library tab closes, the toggle opens the docked panel normally', async ({ page, context }) => {
  await installPresenceTee(page)
  await page.goto(`/#/workspaces/${workspaceId}/chat`)
  const manual = await context.newPage()
  await manual.goto(`/#/library?workspace=${workspaceId}`)
  await page.bringToFront()

  const announcedTabId = await awaitLibraryPresenceAtChatPage(page, libraryPresenceKey(workspaceId))

  await manual.close()
  await awaitLibraryLeaveAtChatPage(page, announcedTabId)

  await libraryWorkspaceToggle(page).click()
  await expect(page.getByTestId('side-panel')).toBeVisible()
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  await expect(page.getByText(/already open/i)).toHaveCount(0)
  expect(context.pages().filter((p) => p.url().includes('/library')).length).toBe(0)
})

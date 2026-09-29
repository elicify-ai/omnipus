/**
 * Full-screen Library exit — UAT row 5, side-panel-shell-spec.md FR-012/FR-013.
 * An Escape directed at the discard dialog must never discard a dirty draft
 * or close the tab. Browser key propagation and paint timing are part of this
 * contract, so a DOM-only event simulation is not an adequate substitute.
 */
import { expect, type BrowserContext, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { newAdminApiContext } from './fixtures/admin-api'

const NOTE = 'escape-guard-e2e.md'
const INITIAL_TEXT = '# Saved before editing\n'
let workspaceId: string

test.describe.configure({ retries: 0 })

test.beforeAll(async () => {
  const api = await newAdminApiContext()
  try {
    const created = await api.post('/api/v1/workspaces', {
      data: { name: `E2E escape guard ${Date.now()}` },
    })
    expect(created.ok(), `workspace setup failed: ${created.status()}`).toBe(true)
    workspaceId = ((await created.json()) as { id: string }).id
    const uploaded = await api.post(`/api/v1/library/${workspaceId}/upload?path=`, {
      multipart: {
        note: { name: NOTE, mimeType: 'text/markdown', buffer: Buffer.from(INITIAL_TEXT) },
      },
    })
    expect(uploaded.ok(), `note setup failed: ${uploaded.status()} ${await uploaded.text()}`).toBe(true)
  } finally {
    await api.dispose()
  }
})

test.afterAll(async () => {
  if (!workspaceId) return
  const api = await newAdminApiContext()
  try {
    const current = await api.get(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}`)
    expect(current.ok(), `workspace teardown read failed: ${current.status()}`).toBe(true)
    const { revision } = (await current.json()) as { revision?: string }
    if (!revision) throw new Error('workspace teardown needs the current revision')
    const deleted = await api.delete(`/api/v1/workspaces/${encodeURIComponent(workspaceId)}?revision=${encodeURIComponent(revision)}`)
    expect(deleted.ok(), `workspace teardown failed: ${deleted.status()} ${await deleted.text()}`).toBe(true)
  } finally {
    await api.dispose()
  }
})

async function openDirtyLibrary(page: Page, context: BrowserContext, draft: string): Promise<{
  fullScreen: Page
  editor: ReturnType<Page['locator']>
}> {
  await page.goto(`/#/workspaces/${workspaceId}/chat?panel=library`)
  await expect(page.getByTestId('side-panel-header')).toContainText('Library')
  const opened = context.waitForEvent('page')
  await page.getByTestId('panel-expand').click()
  const fullScreen = await opened
  await expect(fullScreen).toHaveURL(/\/#\/panel\/library\?[^#]*workspace=/)
  await expect(fullScreen.getByTestId('library-panel-fullscreen')).toBeVisible()
  await fullScreen.getByTestId(`library-row-${NOTE}`).click()
  await fullScreen.getByTestId('library-preview-mode-edit').click()
  const editor = fullScreen.locator('[data-testid="library-code-editor"] .cm-content[contenteditable="true"]')
  await expect(editor).toBeVisible()
  await editor.fill(draft)
  await expect(editor).toHaveText(draft)
  await expect(fullScreen.getByTestId('library-preview-save'), 'the editor must be unsaved before Escape').toBeEnabled()
  return { fullScreen, editor }
}

for (const [pace, delay] of [['fast', 8], ['natural', 150]] as const) {
  test(`dirty full-screen edit survives Escape on the open discard dialog (${pace} keypress)`, async ({ page, context }) => {
    const draft = `# Unsaved ${pace} Escape draft\n`
    const { fullScreen, editor } = await openDirtyLibrary(page, context, draft)
    const dialog = fullScreen.getByRole('alertdialog', { name: 'Discard unsaved changes?' })

    // The editable-field exception: the first Escape is deliberately a no-op.
    await fullScreen.keyboard.press('Escape')
    await expect(fullScreen).toHaveURL(/\/#\/panel\/library\?/)
    await expect(dialog).toHaveCount(0)
    await expect(editor).toHaveText(draft)

    // Focus leaves the editor without triggering the exit action.
    await fullScreen.getByRole('button', { name: 'Back to chat' }).focus()
    await fullScreen.keyboard.press('Escape')
    // No waitFor/auto-retrying visibility assertion here: only a short REAL
    // keypress interval, then a one-shot check that the modal was open.
    await fullScreen.waitForTimeout(delay)
    expect(await dialog.isVisible(), 'discard dialog must be open before third Escape').toBe(true)
    await fullScreen.keyboard.press('Escape')

    // The Escape is allowed to cancel/dismiss the dialog or leave it open; it
    // is NOT permission to discard the draft or to close/navigate the tab.
    await fullScreen.waitForTimeout(250)
    expect(fullScreen.isClosed(), 'third Escape closed the full-screen tab without Discard').toBe(false)
    await expect(fullScreen).toHaveURL(/\/#\/panel\/library\?/)
    await expect(editor, 'third Escape discarded the unsaved Library draft').toHaveText(draft)
    await expect(fullScreen.getByTestId('library-preview-save')).toBeEnabled()
  })
}

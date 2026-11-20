/**
 * saveBar.leaveGuard.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 20 (FR-016/FR-019, leave prompt): with an
 * unsaved draft present, EVERY way out inside the app asks first — switching
 * sections, opening another screen, closing the browser tab. Inside the app
 * the guard shows the app's own confirm; leaving the page uses the
 * browser's own prompt, registered as a `beforeunload` listener. Without a
 * draft nothing asks.
 *
 * Same BLOCKED-import pattern as saveBar.outcomes.test.tsx: the SaveBar
 * does not exist yet, so each test fails loudly with a BLOCKED message
 * naming its FR; after GREEN the real component loads and the assertions
 * run.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-016/FR-019): the Settings SaveBar component does not exist yet — ' +
        'required so every way out with an unsaved draft asks first',
    )
  }
}

async function renderWithDraft() {
  const SaveBar = await loadSaveBar()
  render(
    <SaveBar
      section="gateway"
      fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'debug'] }]}
      onSave={vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))}
      onDiscard={vi.fn()}
    />,
  )
  await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('FR-016/FR-019 — every way out with an unsaved draft asks first', () => {
  it('an in-app navigation away asks before leaving (no silent drop)', async () => {
    await renderWithDraft()
    // The app shell's own navigation (the sidebar link the person clicks).
    const sidebarLink = screen.getByRole('link', { name: /workspaces/i })
    await userEvent.click(sidebarLink)
    expect(
      await screen.findByText(/unsaved changes/i, {}, { timeout: 3000 }),
    ).toBeInTheDocument()
  })

  it('closing or reloading the tab registers beforeunload (the browser asks)', async () => {
    const listener = vi.fn()
    window.addEventListener('beforeunload', listener)
    await renderWithDraft()
    // The Save bar must have registered its own beforeunload guard while a
    // draft exists.
    expect(listener).not.toHaveBeenCalled()
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    // The registered guard prevents the default (the browser then asks).
    expect(event.defaultPrevented).toBe(true)
    window.removeEventListener('beforeunload', listener)
  })

  it('with no draft nothing asks — navigating and unloading are free', async () => {
    const SaveBar = await loadSaveBar()
    render(
      <SaveBar
        section="gateway"
        fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'debug'] }]}
        onSave={vi.fn()}
        onDiscard={vi.fn()}
      />,
    )
    const event = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(false)
    expect(screen.queryByText(/unsaved changes/i)).not.toBeInTheDocument()
  })
})

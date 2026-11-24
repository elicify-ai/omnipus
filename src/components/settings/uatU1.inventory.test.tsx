// U1-10. Independent oracle: settings-redesign-spec.md::D-9/FR-059,
// referenced design-notes.md::section 2 ids3,6,7,11,25 (and R35/S-Q12 A).
// "Kept" permits a changed layout, but does not erase the explicit New place
// or Renamed instructions. Report the docs' older "Check CLI" wording rather
// than silently adopting it as the new label. Real sections, sheets and menus.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function providers() {
  const t = transportG1()
  sectionG1('providers')
  const row = await screen.findByTestId('provider-row-openrouter')
  return { t, row }
}

describe('U1-10 — exact D-9 control-map destinations and names', () => {
  it('id3 — Connect a provider opens the real picker with Recent and All providers initially collapsed', async () => {
    const { t } = await providers()
    await userEvent.click(screen.getByRole('button', { name: 'Connect a provider' }))
    const picker = await screen.findByRole('dialog', { name: 'Connect a provider' })
    expect(within(picker).getByPlaceholderText('Search providers')).toBeVisible()
    // The fixture has a configured provider, so absence of Recent is not a
    // legitimate empty-state shortcut for the collapsed-group requirement.
    const recent = within(picker).queryByRole('button', { name: /^Recent(?:\s|$)/i })
    expect(recent, 'U1-10 id3/R35: Recent is a collapsed disclosure, not an expanded list').not.toBeNull()
    expect(recent).toHaveAttribute('aria-expanded', 'false')
    expect(within(picker).getByRole('button', { name: /All providers/ })).toHaveAttribute('aria-expanded', 'false')
    expect(within(picker).queryByRole('listbox', { name: 'Recent' })).not.toBeInTheDocument()
    expect(t.writes()).toEqual([])
  })

  it.each([
    [6, 'Set as default model…'],
    [7, 'Check with my account'],
  ] as const)('id%i — %s belongs to the provider More actions menu, not inline', async (id, action) => {
    const { t, row } = await providers()
    expect(within(row).queryByRole('button', { name: action }), `U1-10 id${id}: secondary action is not an inline row button`).not.toBeInTheDocument()
    const more = within(row).queryByRole('button', { name: /More actions/i })
    expect(more, `U1-10 id${id}: the configured provider exposes More actions`).not.toBeNull()
    await userEvent.click(more!)
    const menu = await screen.findByRole('menu')
    expect(within(menu).getByRole('menuitem', { name: action })).toBeVisible()
    expect(t.writes(), 'opening a menu never runs an account check or changes the default').toEqual([])
  })

  it('id11 — the connected-provider Configure sheet names its diagnostic Test connection', async () => {
    const { t, row } = await providers()
    await userEvent.click(within(row).getByRole('button', { name: /Edit|Configure/ }))
    const sheet = await screen.findByTestId('provider-config-sheet')
    expect(within(sheet).queryByRole('button', { name: 'Test connection' }),
      'U1-10 id11: Configure names the test action Test connection, not Test').not.toBeNull()
    expect(within(sheet).queryByRole('button', { name: 'Test' })).not.toBeInTheDocument()
    expect(t.writes(), 'inspecting the Configure action makes no connection/model call').toEqual([])
  })

  it('id25 — all three third-party program rows use Check and inspecting them saves nothing', async () => {
    const t = transportG1()
    sectionG1('third-party-agents')
    await screen.findByRole('textbox', { name: 'Codex program path' })
    for (const cli of ['claude-code', 'codex', 'opencode']) {
      const row = screen.getByTestId(`third-party-agent-row-${cli}`)
      expect(within(row).queryByRole('button', { name: 'Check' }),
        `U1-10 id25: ${cli} uses the explicitly renamed Check action`).not.toBeNull()
      expect(within(row).queryByRole('button', { name: 'Check CLI' })).not.toBeInTheDocument()
    }
    expect(t.writes()).toEqual([])
  })
})

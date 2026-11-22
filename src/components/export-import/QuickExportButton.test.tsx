// Quick Export button — spec order 23 (issue #1967 B1).
// Oracles: spec scenarios "Quick Export opens the wizard with the agent
// chosen" (sheet closes; the address is
// /export-import?wizard=export&step=choose&scope=agents&preselect=agent:W) and
// "No Quick Export for built-in agents" (the mode × type × locked table).
// The button is rendered directly (its parent sheet is not the unit here).
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'

const { navigateSpy } = vi.hoisted(() => ({ navigateSpy: vi.fn() }))

vi.mock('@tanstack/react-router', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@tanstack/react-router')>()),
  useNavigate: () => navigateSpy,
}))

import { QuickExportButton } from './QuickExportButton'
import { rememberIdentityMode } from '@/lib/api/identityMode'

type Agent = { id: string; name: string; type: 'core' | 'system' | 'Main' | 'Subagent' | 'subagent_3p'; locked: boolean }

const writer = (over: Partial<Agent> = {}): Agent => ({
  id: 'a-writer',
  name: 'Writer',
  type: 'Main',
  locked: false,
  ...over,
})

beforeEach(() => {
  navigateSpy.mockReset()
  rememberIdentityMode('local')
})

function renderButton(agent: Agent, onClose?: () => void) {
  return render(<QuickExportButton agent={agent} onClose={onClose} />)
}

describe('Quick Export visibility (US-3.2)', () => {
  const rows: Array<{ mode: 'local' | 'platform' | undefined; label: string; agent: Agent; shown: boolean }> = [
    { mode: 'local', label: 'local Main', agent: writer(), shown: true },
    { mode: 'local', label: 'local Subagent unlocked', agent: writer({ type: 'Subagent' }), shown: true },
    { mode: 'local', label: 'local subagent_3p', agent: writer({ type: 'subagent_3p' }), shown: true },
    { mode: 'local', label: 'local core (locked)', agent: writer({ type: 'core', locked: true }), shown: false },
    { mode: 'local', label: 'local locked Subagent', agent: writer({ type: 'Subagent', locked: true }), shown: false },
    { mode: 'local', label: 'local system', agent: writer({ type: 'system' }), shown: false },
    { mode: 'platform', label: 'platform Main', agent: writer(), shown: false },
    { mode: undefined, label: 'unknown edition Main', agent: writer(), shown: false },
  ]

  for (const row of rows) {
    it(`${row.label}: the Export button is ${row.shown ? 'shown' : 'absent'}`, () => {
      act(() => rememberIdentityMode(row.mode))
      renderButton(row.agent)
      if (row.shown) {
        expect(screen.getByRole('button', { name: 'Export' })).toBeInTheDocument()
      } else {
        expect(screen.queryByRole('button', { name: 'Export' })).toBeNull()
      }
    })
  }
})

describe('Quick Export click (US-3.1)', () => {
  it('closes the sheet and navigates to the EXACT address with the agent preselected', () => {
    const onClose = vi.fn()
    renderButton(writer({ id: 'a-writer' }), onClose)
    fireEvent.click(screen.getByRole('button', { name: 'Export' }))
    expect(onClose, 'the sheet closes').toHaveBeenCalledTimes(1)
    expect(navigateSpy, 'exactly one navigation').toHaveBeenCalledTimes(1)
    // The exact address as structured args (CHECK W02): substring checks
    // cannot catch a wrong step or a renamed search key — this can.
    expect(navigateSpy.mock.calls[0]).toHaveLength(1)
    const arg = navigateSpy.mock.calls[0][0] as { to?: string; search?: Record<string, string> }
    expect(arg.to).toBe('/export-import')
    expect(arg.search).toEqual({ wizard: 'export', step: 'choose', scope: 'agents', preselect: 'agent:a-writer' })
  })
})

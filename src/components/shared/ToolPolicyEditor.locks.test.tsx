// ToolPolicyEditor.locks.test.tsx — Skills & Tools redesign (#1963, epic
// #1519, page F), order 22 (S-19, S-22, S-24; FR-023; Dataset D1 screen rows).
//
// Oracle: skills-tools-redesign-spec.md FR-023 ("In an agent's Tools tab,
// badges looser than the global value MUST be disabled, with the tooltip
// 'The global setting is Ask. Set it to Allow on Skills & Tools first.'
// (or 'The global setting is Deny. Change it on Skills & Tools first.')")
// and the D1 screen rows (agent looser than global → disabled with the
// reason; the stored looser value is kept, never widened silently).
// The Omnipus per-agent Ask row is disabled with its own reason, and a
// disabled control is never clickable (S-22's screen half; the 422 API
// half is order 14).
//
// RED today: the editor takes globalPolicies but does not disable or
// explain looser agent values, so every guard below fails.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { RegistryTool } from '@/lib/api'
import { ToolPolicyEditor } from './ToolPolicyEditor'

const tools: RegistryTool[] = [
  {
    name: 'write_file',
    description: 'Write a file.',
    scope: 'core',
    category: 'file',
    source: 'builtin',
    visibility: 'full',
    visibility_locked: false,
    permission_fixed: false,
  },
]

function mountEditor(
  value: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
  globalPolicies: { policies: Record<string, 'allow' | 'ask' | 'deny'> },
) {
  return render(
    <ToolPolicyEditor tools={tools} value={value} globalPolicies={globalPolicies} onChange={() => {}} />,
  )
}

describe('ToolPolicyEditor — global locks (order 22)', () => {
  it('S-19/D1#3: an agent Allow under a global Ask is disabled with the Ask tooltip', () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } })
    const allow = screen.getByRole('button', { name: /allow/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(/the global setting is ask/i)
  })

  it('S-19/D1#4: an agent Allow under a global Deny is disabled with the Deny tooltip', () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'deny' } })
    const allow = screen.getByRole('button', { name: /allow/i })
    expect(allow).toBeDisabled()
    expect(allow).toHaveAccessibleDescription(/the global setting is deny/i)
  })

  it('S-22: a disabled looser control never opens a click path', async () => {
    const user = userEvent.setup()
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } })
    const allow = screen.getByRole('button', { name: /allow/i })
    await user.click(allow)
    // The stored value is unchanged: no silent widening through the lock.
    expect(allow).toBeDisabled()
  })

  it('FR-023: the tooltip names where to change it', () => {
    mountEditor({ policies: { write_file: 'allow' } }, { policies: { write_file: 'ask' } })
    const allow = screen.getByRole('button', { name: /allow/i })
    expect(allow).toHaveAccessibleDescription(/skills & tools/i)
  })
})

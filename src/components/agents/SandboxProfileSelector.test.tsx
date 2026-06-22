/**
 * Tests for SandboxProfileSelector (O13)
 *
 * Coverage:
 * 1. Renders 4 radio buttons (none, workspace, workspace+net, host) — 'off' REMOVED
 * 2. There is NO 'off' radio (no-sandbox is the global god-mode switch only)
 * 3. Selecting a profile calls onChange immediately (no confirm dialog)
 * 4. A widening profile (workspace+net) shows the standing warning badge
 * 5. The control is editable regardless of agent identity (no god-mode gating)
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { SandboxProfileSelector } from './SandboxProfileSelector'

const AGENT_NAME = 'Test Agent'

function renderSelector(overrides: Partial<Parameters<typeof SandboxProfileSelector>[0]> = {}) {
  const onChange = vi.fn()
  render(
    <SandboxProfileSelector
      value={undefined}
      agentName={AGENT_NAME}
      onChange={onChange}
      {...overrides}
    />,
  )
  return { onChange }
}

describe('SandboxProfileSelector', () => {
  it('renders 4 radio buttons and no off option', () => {
    renderSelector()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(4)
    expect(screen.getByTestId('sandbox-profile-radio-none')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-workspace')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-workspace+net')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-host')).toBeInTheDocument()
    // O13: 'off' is removed from the per-agent picker.
    expect(screen.queryByTestId('sandbox-profile-radio-off')).not.toBeInTheDocument()
  })

  it('selecting a profile calls onChange immediately (no confirm dialog)', () => {
    const { onChange } = renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-workspace'))
    expect(onChange).toHaveBeenCalledWith('workspace')
  })

  it('selecting host calls onChange with host', () => {
    const { onChange } = renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-host'))
    expect(onChange).toHaveBeenCalledWith('host')
  })

  it('shows the widening badge when workspace+net is the active profile', () => {
    renderSelector({ value: 'workspace+net' })
    expect(screen.getByTestId('sandbox-widening-badge')).toBeInTheDocument()
  })

  it('does not show the widening badge for the inherit default', () => {
    renderSelector({ value: undefined })
    expect(screen.queryByTestId('sandbox-widening-badge')).not.toBeInTheDocument()
  })

  it('treats a legacy off value as the inherit default and stays editable', () => {
    // A legacy agent persisted with sandbox_profile='off' must not crash and
    // must surface as the inherit default — the user can then pick an enforced
    // profile. (off is never re-selectable.)
    const { onChange } = renderSelector({ value: 'off' as never })
    expect(screen.getByTestId('sandbox-profile-radio-none')).toBeChecked()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-workspace'))
    expect(onChange).toHaveBeenCalledWith('workspace')
  })

  it('every profile radio is enabled (no god-mode gating)', () => {
    renderSelector()
    for (const p of ['none', 'workspace', 'workspace+net', 'host']) {
      expect(screen.getByTestId(`sandbox-profile-radio-${p}`)).toBeEnabled()
    }
  })
})

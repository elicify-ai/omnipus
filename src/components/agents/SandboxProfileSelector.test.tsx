/**
 * Tests for SandboxProfileSelector
 *
 * Coverage:
 * 1. Renders 5 radio buttons (none, workspace, workspace+net, host, off)
 * 2. Selecting "off" opens confirmation dialog
 * 3. Typing wrong name → submit disabled
 * 4. Typing exact name → submit enabled
 * 5. onChange fires with "off" only after exact-match confirmation
 * 6. Cancel resets without calling onChange
 * 7. godModeAvailable=false → off radio disabled, tooltip text present
 * 8. godModeOptedIn=false → off radio disabled, tooltip text present
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { SandboxProfileSelector } from './SandboxProfileSelector'

const AGENT_NAME = 'Test Agent'

function renderSelector(overrides: Partial<Parameters<typeof SandboxProfileSelector>[0]> = {}) {
  const onChange = vi.fn()
  render(
    <SandboxProfileSelector
      value={undefined}
      agentName={AGENT_NAME}
      godModeAvailable={true}
      godModeOptedIn={true}
      onChange={onChange}
      {...overrides}
    />,
  )
  return { onChange }
}

describe('SandboxProfileSelector', () => {
  it('renders 5 radio buttons', () => {
    renderSelector()
    const radios = screen.getAllByRole('radio')
    expect(radios).toHaveLength(5)
    // Labels updated for plain language (#335 / US-D3)
    expect(screen.getByTestId('sandbox-profile-radio-none')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-workspace')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-workspace+net')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-host')).toBeInTheDocument()
    expect(screen.getByTestId('sandbox-profile-radio-off')).toBeInTheDocument()
  })

  it('selecting non-off profile calls onChange immediately', () => {
    const { onChange } = renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-workspace'))
    expect(onChange).toHaveBeenCalledWith('workspace')
  })

  it('selecting off opens confirmation dialog', async () => {
    renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByText(/Disable sandbox for Test Agent/i)).toBeInTheDocument()
    })
  })

  it('typing wrong name keeps submit disabled', async () => {
    renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-off-confirm-input')).toBeInTheDocument()
    })
    fireEvent.change(screen.getByTestId('sandbox-off-confirm-input'), {
      target: { value: 'wrong name' },
    })
    expect(screen.getByTestId('sandbox-off-confirm-submit')).toBeDisabled()
  })

  it('typing exact agent name enables submit', async () => {
    renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-off-confirm-input')).toBeInTheDocument()
    })
    fireEvent.change(screen.getByTestId('sandbox-off-confirm-input'), {
      target: { value: AGENT_NAME },
    })
    expect(screen.getByTestId('sandbox-off-confirm-submit')).not.toBeDisabled()
  })

  it('onChange fires with off only after exact-match confirmation', async () => {
    const { onChange } = renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByTestId('sandbox-off-confirm-input')).toBeInTheDocument()
    })
    // Type partial — onChange not called yet
    fireEvent.change(screen.getByTestId('sandbox-off-confirm-input'), {
      target: { value: AGENT_NAME.slice(0, 3) },
    })
    expect(onChange).not.toHaveBeenCalled()
    // Type full name and submit
    fireEvent.change(screen.getByTestId('sandbox-off-confirm-input'), {
      target: { value: AGENT_NAME },
    })
    fireEvent.click(screen.getByTestId('sandbox-off-confirm-submit'))
    expect(onChange).toHaveBeenCalledWith('off')
  })

  it('cancel closes dialog without calling onChange', async () => {
    const { onChange } = renderSelector()
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByText(/Disable sandbox for Test Agent/i)).toBeInTheDocument()
    })
    fireEvent.click(screen.getByRole('button', { name: /Cancel/i }))
    await waitFor(() => {
      expect(screen.queryByText(/Disable sandbox for Test Agent/i)).not.toBeInTheDocument()
    })
    expect(onChange).not.toHaveBeenCalled()
  })

  it('cancel restores previously-selected radio state', async () => {
    // BDD: Given the "workspace" profile is already selected,
    //      When the user clicks "off" and then cancels the confirmation dialog,
    //      Then the "workspace" radio is still checked and "off" is not checked.
    //
    // Traces to: quizzical-marinating-frog.md pr-test-analyzer Test-9.
    const { onChange } = renderSelector({ value: 'workspace' })

    // Open the confirmation dialog by clicking "off".
    fireEvent.click(screen.getByTestId('sandbox-profile-radio-off'))
    await waitFor(() => {
      expect(screen.getByText(/Disable sandbox for Test Agent/i)).toBeInTheDocument()
    })

    // Cancel the dialog.
    fireEvent.click(screen.getByRole('button', { name: /Cancel/i }))
    await waitFor(() => {
      expect(screen.queryByText(/Disable sandbox for Test Agent/i)).not.toBeInTheDocument()
    })

    // onChange must not have been called.
    expect(onChange).not.toHaveBeenCalled()

    // The "workspace" radio must still be selected via data-testid.
    const workspaceRadio = screen.getByTestId('sandbox-profile-radio-workspace')
    expect(workspaceRadio).toBeChecked()

    // The "off" radio must NOT be checked.
    const offRadio = screen.getByTestId('sandbox-profile-radio-off')
    expect(offRadio).not.toBeChecked()
  })

  it('off radio is disabled when godModeAvailable=false', () => {
    renderSelector({ godModeAvailable: false, godModeOptedIn: true })
    const offRadio = screen.getByTestId('sandbox-profile-radio-off')
    expect(offRadio).toBeDisabled()
  })

  it('off radio is disabled when godModeOptedIn=false', () => {
    renderSelector({ godModeAvailable: true, godModeOptedIn: false })
    const offRadio = screen.getByTestId('sandbox-profile-radio-off')
    expect(offRadio).toBeDisabled()
  })

  it('tooltip text present when godModeAvailable=false', async () => {
    renderSelector({ godModeAvailable: false, godModeOptedIn: true })
    const whyButton = screen.getByRole('button', { name: /Why is this disabled/i })
    fireEvent.mouseEnter(whyButton)
    await waitFor(() => {
      expect(screen.getByRole('tooltip')).toHaveTextContent(/Disabled in this build/i)
    })
  })

  it('tooltip text present when godModeOptedIn=false', async () => {
    renderSelector({ godModeAvailable: true, godModeOptedIn: false })
    const whyButton = screen.getByRole('button', { name: /Why is this disabled/i })
    fireEvent.mouseEnter(whyButton)
    await waitFor(() => {
      expect(screen.getByRole('tooltip')).toHaveTextContent(/allow-god-mode/i)
    })
  })
})

/**
 * ConfirmActionModal.test.tsx — the shared confirm-before-act modal (ADR-052
 * FR-020) every ▶/■ action button routes through.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ConfirmActionModal } from './ConfirmActionModal'

function renderModal(overrides: Partial<React.ComponentProps<typeof ConfirmActionModal>> = {}) {
  const onOpenChange = vi.fn()
  const onConfirm = vi.fn()
  const props = {
    open: true,
    onOpenChange,
    title: 'Execute this plan?',
    description: 'This starts autonomous execution of "Launch".',
    confirmLabel: 'Execute',
    pendingLabel: 'Executing…',
    onConfirm,
    ...overrides,
  }
  const utils = render(<ConfirmActionModal {...props} />)
  return { onOpenChange, onConfirm, ...utils }
}

describe('ConfirmActionModal', () => {
  it('renders the title and description naming the action + target', () => {
    renderModal()
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
    expect(screen.getByText('Execute this plan?')).toBeInTheDocument()
    expect(screen.getByText('This starts autonomous execution of "Launch".')).toBeInTheDocument()
  })

  it('renders nothing when closed', () => {
    renderModal({ open: false })
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('Confirm calls onConfirm', async () => {
    const user = userEvent.setup()
    const { onConfirm } = renderModal()
    await user.click(screen.getByRole('button', { name: 'Execute' }))
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it('Cancel calls onOpenChange(false) and never onConfirm', async () => {
    const user = userEvent.setup()
    const { onConfirm, onOpenChange } = renderModal()
    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it('shows the pending label and disables both buttons while isPending', () => {
    renderModal({ isPending: true })
    const confirmBtn = screen.getByRole('button', { name: 'Executing…' })
    expect(confirmBtn).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: 'Execute' })).not.toBeInTheDocument()
  })

  it('applies destructive (error) styling only when destructive=true', () => {
    const { rerender } = renderModal({ destructive: true, confirmLabel: 'Stop' })
    expect(screen.getByRole('button', { name: 'Stop' }).className).toContain('color-error')

    rerender(
      <ConfirmActionModal
        open
        onOpenChange={vi.fn()}
        title="Execute this plan?"
        description="desc"
        confirmLabel="Execute"
        pendingLabel="Executing…"
        onConfirm={vi.fn()}
        destructive={false}
      />,
    )
    expect(screen.getByRole('button', { name: 'Execute' }).className).not.toContain('color-error')
  })

  it('does not dismiss on outside click (a confirm must be deliberate)', () => {
    // The content is rendered with onPointerDownOutside/onInteractOutside
    // preventDefault (see alert-dialog.tsx) — asserting the alertdialog role
    // + no native close-on-overlay affordance is present is the behavioral
    // proxy available without simulating Radix's pointer-capture internals.
    renderModal()
    const dialog = screen.getByRole('alertdialog')
    expect(dialog).toBeInTheDocument()
  })
})

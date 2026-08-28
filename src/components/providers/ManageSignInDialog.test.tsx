/**
 * ManageSignInDialog.test.tsx — ADR-068 FR-034 T068-26 Manage surface for a
 * `signed_in` row: shows the account and offers Sign out, rather than firing
 * a destructive sign-out directly off the ambiguously-labelled Manage
 * button. No network call happens on open — the account label already came
 * down with the row from GET /providers.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { ManageSignInDialog } from './ManageSignInDialog'

function renderDialog(props?: Partial<React.ComponentProps<typeof ManageSignInDialog>>) {
  const onOpenChange = vi.fn()
  const onSignOut = vi.fn()
  const utils = render(
    <ManageSignInDialog
      open
      onOpenChange={onOpenChange}
      providerLabel="ChatGPT"
      accountLabel="user@example.com"
      onSignOut={onSignOut}
      {...props}
    />,
  )
  return { ...utils, onOpenChange, onSignOut }
}

describe('ManageSignInDialog', () => {
  it('shows "Signed in as <label>" when an account label is present', () => {
    renderDialog()
    expect(screen.getByTestId('manage-sign-in-status')).toHaveTextContent('Signed in as user@example.com')
  })

  it('shows "Signed in" (no dangling "as") when the account label is absent', () => {
    renderDialog({ accountLabel: undefined })
    const status = screen.getByTestId('manage-sign-in-status')
    expect(status).toHaveTextContent('Signed in')
    expect(status.textContent).not.toMatch(/as\s*$/)
  })

  it('Sign out fires the passed handler — a real, wired action, not a dead button', () => {
    const { onSignOut } = renderDialog()
    fireEvent.click(screen.getByTestId('manage-sign-out-btn'))
    expect(onSignOut).toHaveBeenCalledTimes(1)
  })

  it('disables Sign out while the mutation is in flight', () => {
    renderDialog({ signingOut: true })
    expect(screen.getByTestId('manage-sign-out-btn')).toBeDisabled()
  })

  it('Done closes the dialog', () => {
    const { onOpenChange } = renderDialog()
    fireEvent.click(screen.getByTestId('manage-sign-in-done-btn'))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('renders nothing when closed', () => {
    renderDialog({ open: false })
    expect(screen.queryByTestId('manage-sign-in-dialog')).not.toBeInTheDocument()
  })
})

/**
 * ReSignInDialog.test.tsx — ADR-068 FR-034/FR-009 (amended 2026-08-23) T068-26
 * re-sign-in dialog for an `expired` `cli_login` row.
 *
 * Covers (BDD "Expired session routes to re-sign-in"):
 *  - Renders the static, provider-specific instruction copy on open — never
 *    fetched from the server (no startSignIn/POST call of any kind).
 *  - *Check* calls ONLY `GET /providers/{id}/sign-in/status`
 *    (fetchSignInStatus) — never a POST, never a "refresh" request (MAJ-006).
 *  - state signed_in -> success view + onSignedIn fired with the status.
 *  - state expired -> "still expired" retry hint, dialog stays open.
 *  - state not_signed_in -> "not signed in yet" retry hint.
 *  - A network failure surfaces the error text, not a silent no-op.
 *  - aria-live="polite" status line.
 *  - Cancel closes without ever having called fetchSignInStatus.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    startSignIn: vi.fn(),
    fetchSignInStatus: vi.fn(),
  }
})

import * as api from '@/lib/api'
import { ReSignInDialog } from './ReSignInDialog'

function renderDialog(props?: Partial<React.ComponentProps<typeof ReSignInDialog>>) {
  const onOpenChange = vi.fn()
  const onSignedIn = vi.fn()
  const utils = render(
    <ReSignInDialog
      open
      onOpenChange={onOpenChange}
      providerId="codex-cli"
      providerLabel="Codex CLI"
      cliKind="codex"
      onSignedIn={onSignedIn}
      {...props}
    />,
  )
  return { ...utils, onOpenChange, onSignedIn }
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('ReSignInDialog — codex-cli', () => {
  it('shows the static "Run `codex login` again, then check" copy on open, with no network call', () => {
    renderDialog()

    expect(screen.getByTestId('re-sign-in-command')).toHaveTextContent('codex login')
    expect(screen.getByTestId('re-sign-in-instruction')).toHaveTextContent(
      'Run `codex login` again, then check',
    )
    expect(api.fetchSignInStatus).not.toHaveBeenCalled()
    expect(api.startSignIn).not.toHaveBeenCalled()
  })

  it('Check calls ONLY GET /providers/{id}/sign-in/status — never startSignIn', async () => {
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'acct_7f3a' })
    renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-check-btn'))

    await waitFor(() => {
      expect(api.fetchSignInStatus).toHaveBeenCalledWith('codex-cli')
    })
    expect(api.startSignIn).not.toHaveBeenCalled()
  })

  it('signed_in reaches the success view and fires onSignedIn with the status', async () => {
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'acct_7f3a' })
    const { onSignedIn } = renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-check-btn'))

    await waitFor(() => {
      expect(screen.getByTestId('re-sign-in-success')).toHaveTextContent('Signed in as acct_7f3a')
    })
    expect(onSignedIn).toHaveBeenCalledWith({ state: 'signed_in', account_label: 'acct_7f3a' })
    // Success replaces Check with Done — the terminal state has no further action.
    expect(screen.queryByTestId('re-sign-in-check-btn')).not.toBeInTheDocument()
    expect(screen.getByTestId('re-sign-in-done-btn')).toBeInTheDocument()
  })

  it('expired stays expired: shows a retry hint, dialog stays open, onSignedIn never fires', async () => {
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'expired' })
    const { onOpenChange, onSignedIn } = renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-check-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/Still expired/)
    })
    expect(onSignedIn).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByTestId('re-sign-in-check-btn')).toBeInTheDocument()
  })

  it('not_signed_in shows a "run the command above" retry hint', async () => {
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'not_signed_in' })
    renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-check-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent(/Not signed in yet/)
    })
  })

  it('a network failure surfaces the error text rather than failing silently', async () => {
    vi.mocked(api.fetchSignInStatus).mockRejectedValue(new Error('network unreachable'))
    renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-check-btn'))

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('network unreachable')
    })
  })

  it('announces status changes via aria-live="polite"', () => {
    renderDialog()
    const status = screen.getByTestId('re-sign-in-status')
    expect(status).toHaveAttribute('aria-live', 'polite')
  })

  it('Cancel closes the dialog without ever calling fetchSignInStatus', () => {
    const { onOpenChange } = renderDialog()

    fireEvent.click(screen.getByTestId('re-sign-in-cancel-btn'))

    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(api.fetchSignInStatus).not.toHaveBeenCalled()
  })
})

describe('ReSignInDialog — github-copilot', () => {
  it('shows the "Run `copilot login` again, then check" copy', () => {
    renderDialog({ providerId: 'github-copilot', providerLabel: 'GitHub Copilot', cliKind: 'copilot' })

    expect(screen.getByTestId('re-sign-in-command')).toHaveTextContent('copilot login')
    expect(screen.getByTestId('re-sign-in-instruction')).toHaveTextContent(
      'Run `copilot login` again, then check',
    )
  })
})

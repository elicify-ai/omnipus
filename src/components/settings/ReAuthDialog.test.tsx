/**
 * ReAuthDialog.test.tsx — Spec-6 U5 (FR-12.2 consent primitive, UI layer),
 * reconciled to the Settings redesign #1519 password-prompt oracle.
 *
 * Verifies the dialog calls reAuth, hands the minted token to onConfirmed on
 * success, surfaces an inline error (without confirming) on a wrong password,
 * and — D-11 row 3 / FR-029 — refuses an EMPTY submit inline, without a
 * request.
 *
 * Part 11: the old "requires a non-empty password" case (Confirm disabled
 * until text is typed) is REPLACED by the redesign spec: Dataset D-11 row 3
 * ("empty password, press Confirm" → "Enter your password to continue."; no
 * request; wireframe `reauth-ok`) and FR-029 ("an empty submit MUST say
 * 'Enter your password to continue.' without a request"). The spec has ONE
 * password prompt — "The password prompt is the existing ReAuthDialog" — and
 * the founder-approved wireframe implements the inline refusal in the ONE
 * openReauth dialog it opens for every prompt: the Save chain (app.js:508),
 * God Mode (app.js:811), Add a stored key (app.js:812), Rotate the master key
 * (app.js:813) — no disabled-Confirm variant exists in the approved design.
 * The production `items`-conditional (Confirm pressable-with-empty only for
 * the Settings Save prompt, disabled elsewhere) is a spec conflict, listed
 * for team-lead: D-11 row 3 / FR-029 / wireframe app.js::reauth-ok versus
 * ReAuthDialog.tsx's `disabled={submitting || (!items?.length &&
 * !password.trim())}`. This file keeps rendering the bare dialog (no items),
 * so the case is RED until the dialog refuses an empty press inline wherever
 * it opens.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, reAuth: vi.fn(), isApiError: actual.isApiError }
})

import * as api from '@/lib/api'
import { ApiError } from '@/lib/api'
import { ReAuthDialog } from './ReAuthDialog'

beforeEach(() => vi.clearAllMocks())

describe('ReAuthDialog', () => {
  it('calls reAuth and hands the token to onConfirmed on success', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_xyz', expires_in: 300 } as never)
    const onConfirmed = vi.fn()
    const onOpenChange = vi.fn()
    render(<ReAuthDialog open onOpenChange={onOpenChange} onConfirmed={onConfirmed} />)

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'pw' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('pw')
      expect(onConfirmed).toHaveBeenCalledWith('reauth_xyz')
    })
  })

  it('shows an inline error and does NOT confirm on a wrong password (401)', async () => {
    vi.mocked(api.reAuth).mockRejectedValue(new ApiError(401, 'password is incorrect'))
    const onConfirmed = vi.fn()
    render(<ReAuthDialog open onOpenChange={vi.fn()} onConfirmed={onConfirmed} />)

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'wrong' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(screen.getByTestId('reauth-error')).toHaveTextContent(/incorrect/i)
    })
    expect(onConfirmed).not.toHaveBeenCalled()
  })

  it('an empty submit is refused inline — "Enter your password to continue.", no request (D-11 row 3, FR-029)', async () => {
    const onConfirmed = vi.fn()
    render(<ReAuthDialog open onOpenChange={vi.fn()} onConfirmed={onConfirmed} />)

    // D-11 row 3's input is "empty password, press Confirm": the button is
    // pressable with an empty field and the refusal is the dialog's own
    // inline message (wireframe app.js::reauth-ok). The retired assertion —
    // Confirm disabled until something is typed — is replaced by D-11 row 3
    // and FR-029; see the file header for the spec conflict this RED pins.
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    expect(await screen.findByTestId('reauth-error')).toHaveTextContent('Enter your password to continue.')
    expect(api.reAuth).not.toHaveBeenCalled()
    expect(onConfirmed).not.toHaveBeenCalled()
    // The prompt stays open for the person to type into (reauth-ok returns
    // without closing the dialog).
    expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
  })
})

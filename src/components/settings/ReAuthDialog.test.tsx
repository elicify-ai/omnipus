/**
 * ReAuthDialog.test.tsx — Spec-6 U5 (FR-12.2 consent primitive, UI layer).
 *
 * Verifies the dialog calls reAuth, hands the minted token to onConfirmed on
 * success, and surfaces an inline error (without confirming) on a wrong password.
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

  it('requires a non-empty password', async () => {
    render(<ReAuthDialog open onOpenChange={vi.fn()} onConfirmed={vi.fn()} />)
    // Confirm is disabled with no input.
    expect(screen.getByTestId('reauth-confirm')).toBeDisabled()
  })
})

// CliPathValidationHint.personRequired.test.tsx — T11 slice A, SF-4.
//
// Spec §3.2: Check CLI accepts only a signed-in person. A permanent 403
// (development bypass, or no signed-in person) must not be shown as the
// transient "couldn't verify, you can still save, will retry" hint.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { Button } from '@/components/ui/button'
import { CliPathValidationHint } from './CliPathValidationHint'
import { useCliPathValidation } from '@/hooks/useCliPathValidation'
import { ApiError } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchCliValidate: vi.fn() }
})

import { fetchCliValidate } from '@/lib/api'

function Harness() {
  const { status, validate } = useCliPathValidation()
  return (
    <>
      <Button type="button" onClick={() => validate('claude-code', '/usr/local/bin/claude')}>
        check
      </Button>
      <CliPathValidationHint validation={status} detectHint={{ kind: 'unknown' }} />
    </>
  )
}

describe('CliPathValidationHint 403', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  async function settle() {
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: 'check' }))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400)
    })
  }

  it('explains how to sign in only for the person-gate code', async () => {
    vi.mocked(fetchCliValidate).mockRejectedValue(
      new ApiError(403, 'unused', {
        code: 'person_session_required',
        body: JSON.stringify({ error: 'a signed-in person is required', code: 'person_session_required' }),
      }),
    )
    await settle()
    const hint = screen.getByText(/sign in to your account/i)
    expect(hint).toHaveAttribute('data-cli-status', 'forbidden')
    expect(hint.textContent ?? '').not.toMatch(/refused by the server/i)
    expect(hint.textContent ?? '').not.toMatch(/you can still save/i)
    expect(hint.textContent ?? '').not.toMatch(/will retry/i)
  })

  it('shows the server message for any other 403', async () => {
    vi.mocked(fetchCliValidate).mockRejectedValue(
      new ApiError(403, 'unused', {
        code: 'something_else',
        body: JSON.stringify({ error: 'the check is turned off' }),
      }),
    )
    await settle()
    const hint = screen.getByText(/CLI check refused by the server: the check is turned off/i)
    expect(hint).toHaveAttribute('data-cli-status', 'forbidden')
    expect(hint.textContent ?? '').not.toMatch(/sign in to your account/i)
    expect(hint.textContent ?? '').not.toMatch(/you can still save/i)
    expect(hint.textContent ?? '').not.toMatch(/will retry/i)
  })
})

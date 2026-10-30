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

describe('CliPathValidationHint person-session 403', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.mocked(fetchCliValidate).mockRejectedValue(
      new ApiError(403, 'this action requires a signed-in person'),
    )
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('tells the person to sign in and does not call the refusal a retry', async () => {
    render(<Harness />)
    fireEvent.click(screen.getByRole('button', { name: 'check' }))
    // Debounce is 400ms (useCliPathValidation). act flushes the timer and the
    // rejected request so the assertion sees the settled hint, not "Checking…".
    await act(async () => {
      await vi.advanceTimersByTimeAsync(400)
    })

    const hint = screen.getByText(/signed-in person/i)
    expect(hint).toHaveAttribute('data-cli-status', 'forbidden')
    expect(hint.textContent ?? '').not.toMatch(/you can still save/i)
    expect(hint.textContent ?? '').not.toMatch(/will retry/i)
  })
})

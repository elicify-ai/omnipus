// Unit tests for the shared QueryErrorState component (D9).
//
// Covers: message rendering, optional Retry wiring, both layout variants,
// and the deterministic-race suppression (renders null while a forced
// logout is in flight — see authLogout.ts's isForceLoggingOut()).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

const mockIsForceLoggingOut = vi.fn().mockReturnValue(false)
vi.mock('@/lib/authLogout', () => ({
  isForceLoggingOut: () => mockIsForceLoggingOut(),
}))

import { QueryErrorState } from './QueryErrorState'

beforeEach(() => {
  mockIsForceLoggingOut.mockReturnValue(false)
})

describe('QueryErrorState', () => {
  it('renders the message', () => {
    render(<QueryErrorState message="Something broke" />)
    expect(screen.getByText('Something broke')).toBeInTheDocument()
  })

  it('renders no Retry button when onRetry is omitted', () => {
    render(<QueryErrorState message="Something broke" />)
    expect(screen.queryByRole('button')).toBeNull()
  })

  it('renders a Retry button that calls onRetry when clicked', () => {
    const onRetry = vi.fn()
    render(<QueryErrorState message="Something broke" onRetry={onRetry} />)
    fireEvent.click(screen.getByRole('button', { name: /retry/i }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('defaults to the "absolute" layout (absolute inset-0)', () => {
    render(<QueryErrorState message="Something broke" testId="err" />)
    expect(screen.getByTestId('err').className).toContain('absolute')
    expect(screen.getByTestId('err').className).toContain('inset-0')
  })

  it('"fill" layout does not use absolute positioning', () => {
    render(<QueryErrorState message="Something broke" layout="fill" testId="err" />)
    expect(screen.getByTestId('err').className).not.toContain('absolute')
  })

  it('D9: renders NOTHING while a forced logout is in flight — no stale error flash before the redirect bounce', () => {
    mockIsForceLoggingOut.mockReturnValue(true)
    const { container } = render(<QueryErrorState message="Session gone" onRetry={vi.fn()} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders normally once isForceLoggingOut() returns false again', () => {
    mockIsForceLoggingOut.mockReturnValue(false)
    render(<QueryErrorState message="Something broke" />)
    expect(screen.getByText('Something broke')).toBeInTheDocument()
  })
})

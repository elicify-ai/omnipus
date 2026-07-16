import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AutoSaveIndicator } from '@/components/ui/AutoSaveIndicator'

describe('AutoSaveIndicator', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-06-19T12:00:00Z'))
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps a mounted, empty live region when status is idle (so later status changes announce reliably)', () => {
    const { container } = render(<AutoSaveIndicator status="idle" />)
    expect(container.firstChild).not.toBeNull()
    expect(container.firstChild).toHaveAttribute('aria-live', 'polite')
    expect(container.textContent).toBe('')
  })

  it('marks the saving/saved states as a polite live region', () => {
    const { container: savingContainer } = render(<AutoSaveIndicator status="saving" />)
    expect(savingContainer.firstChild).toHaveAttribute('aria-live', 'polite')

    const { container: savedContainer } = render(<AutoSaveIndicator status="saved" />)
    expect(savedContainer.firstChild).toHaveAttribute('aria-live', 'polite')
  })

  it('marks the error state as an alert region instead of aria-live=polite', () => {
    const { container } = render(<AutoSaveIndicator status="error" error="Network failed" />)
    expect(container.firstChild).toHaveAttribute('role', 'alert')
  })

  it('renders "Saved just now" when lastSavedAt is within 5s', () => {
    const now = new Date('2026-06-19T12:00:00Z')
    render(<AutoSaveIndicator status="saved" lastSavedAt={now} />)
    expect(screen.getByText('Saved just now')).toBeInTheDocument()
  })

  it('renders "Saved Ns ago" when lastSavedAt is 10s ago', () => {
    const tenSecondsAgo = new Date('2026-06-19T11:59:50Z')
    render(<AutoSaveIndicator status="saved" lastSavedAt={tenSecondsAgo} />)
    expect(screen.getByText('Saved 10s ago')).toBeInTheDocument()
  })

  it('renders "Saved Ns ago" when lastSavedAt is 30s ago', () => {
    const thirtySecondsAgo = new Date('2026-06-19T11:59:30Z')
    render(<AutoSaveIndicator status="saved" lastSavedAt={thirtySecondsAgo} />)
    expect(screen.getByText('Saved 30s ago')).toBeInTheDocument()
  })

  it('renders "Saved Nm ago" when lastSavedAt is 5m ago', () => {
    const fiveMinutesAgo = new Date('2026-06-19T11:55:00Z')
    render(<AutoSaveIndicator status="saved" lastSavedAt={fiveMinutesAgo} />)
    expect(screen.getByText('Saved 5m ago')).toBeInTheDocument()
  })

  it('renders "Saved at HH:MM:SS" when lastSavedAt is over 1h ago', () => {
    const twoHoursAgo = new Date('2026-06-19T10:00:00Z')
    render(<AutoSaveIndicator status="saved" lastSavedAt={twoHoursAgo} />)
    expect(screen.getByText(/Saved at /)).toBeInTheDocument()
  })

  it('renders plain "Saved" when lastSavedAt is undefined', () => {
    render(<AutoSaveIndicator status="saved" />)
    expect(screen.getByText('Saved')).toBeInTheDocument()
  })

  it('renders Saving... when status is saving', () => {
    render(<AutoSaveIndicator status="saving" />)
    expect(screen.getByText('Saving...')).toBeInTheDocument()
  })

  it('renders error message when status is error', () => {
    render(<AutoSaveIndicator status="error" error="Network failed" />)
    expect(screen.getByText('Network failed')).toBeInTheDocument()
  })

  it('renders default error text when status is error and no error prop', () => {
    render(<AutoSaveIndicator status="error" />)
    expect(screen.getByText('Save failed')).toBeInTheDocument()
  })
})

import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { JobStatus } from './job-status'

describe('JobStatus — truthful long-running job state', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-17T00:00:00Z')) })
  afterEach(() => vi.useRealTimers())

  it('keeps stable polite and assertive regions while moving between states', () => {
    const { rerender } = render(<JobStatus status="queued" label="Index documents" />)
    const statusRegion = document.querySelector('[data-job-announcement="polite"]') as HTMLElement
    const alertRegion = document.querySelector('[data-job-announcement="assertive"]') as HTMLElement
    expect(statusRegion).toHaveTextContent('Queued')
    expect(alertRegion).toBeEmptyDOMElement()

    rerender(<JobStatus status="complete" label="Index documents" />)
    expect(document.querySelector('[data-job-announcement="polite"]')).toBe(statusRegion)
    expect(statusRegion).toHaveTextContent('Complete')
    expect(document.querySelector('[data-job-announcement="assertive"]')).toBe(alertRegion)

    rerender(<JobStatus status="failed" label="Index documents" message="Network error" />)
    expect(document.querySelector('[data-job-announcement="polite"]')).toBe(statusRegion)
    expect(statusRegion).toBeEmptyDOMElement()
    expect(document.querySelector('[data-job-announcement="assertive"]')).toBe(alertRegion)
    expect(alertRegion).toHaveTextContent('Failed. Network error')
  })

  it('announces stall escalation through the existing status region without duplicate live roles', () => {
    render(<JobStatus status="progress" label="Upload" progress={25} lastProgressAt={Date.now()} />)
    const statusRegion = document.querySelector('[data-job-announcement="polite"]') as HTMLElement
    expect(statusRegion).toHaveTextContent('In progress')
    act(() => vi.advanceTimersByTime(10_000))
    expect(document.querySelectorAll('[data-job-announcement="polite"]')).toHaveLength(1)
    expect(statusRegion).toHaveTextContent('In progress. No progress reported recently.')
    expect(screen.getByText('No progress reported recently.')).not.toHaveAttribute('role')
  })

  it.each([
    ['queued', 'Queued'], ['running', 'Running'], ['paused', 'Paused'],
    ['failed', 'Failed'], ['complete', 'Complete'], ['cancelled', 'Cancelled'],
  ] as const)('announces the caller-controlled %s state', (status, label) => {
    render(<JobStatus status={status} label="Index documents" />)
    expect(screen.getByText(label, { selector: 'p' })).toBeInTheDocument()
  })

  it('renders real zero progress as determinate', () => {
    render(<JobStatus status="progress" label="Upload" progress={0} max={100} />)
    expect(screen.getByRole('progressbar', { name: 'Upload progress' })).toHaveAttribute('aria-valuenow', '0')
    expect(screen.getByText('0%')).toBeInTheDocument()
  })

  it('does not fabricate a percentage for unknown or invalid progress', () => {
    const { rerender } = render(<JobStatus status="progress" label="Upload" progress={undefined} />)
    expect(screen.queryByText(/%/)).toBeNull()
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
    rerender(<JobStatus status="progress" label="Upload" progress={101} max={100} />)
    expect(screen.queryByText(/%/)).toBeNull()
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
  })

  it('retains a previously reported real value when a later report becomes unknown', () => {
    const { rerender } = render(<JobStatus status="progress" label="Upload" progress={40} />)
    rerender(<JobStatus status="progress" label="Upload" progress={undefined} />)
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '40')
    expect(screen.getByText('40%')).toBeInTheDocument()
  })

  it('does not reuse a retained value across a changed scale', () => {
    const { rerender } = render(<JobStatus status="progress" label="Upload" progress={50} max={100} />)
    rerender(<JobStatus status="progress" label="Upload" progress={undefined} max={200} />)
    expect(screen.queryByText('25%')).toBeNull()
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
  })

  it('resets retained progress and the stall clock for a new job identity', () => {
    const { rerender } = render(<JobStatus jobKey="first" status="progress" label="Upload" progress={100} />)
    act(() => vi.advanceTimersByTime(10_000))
    expect(screen.getByText('No progress reported recently.')).toBeInTheDocument()
    rerender(<JobStatus jobKey="second" status="running" label="Upload" progress={undefined} />)
    expect(screen.queryByText('100%')).toBeNull()
    expect(screen.queryByText('No progress reported recently.')).toBeNull()
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('Still working; progress is unavailable.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('restarts the no-progress clock when the reported value changes', () => {
    const { rerender } = render(<JobStatus status="progress" label="Upload" progress={10} />)
    act(() => vi.advanceTimersByTime(9_000))
    rerender(<JobStatus status="progress" label="Upload" progress={20} />)
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('No progress reported recently.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('No progress reported recently.')).toBeInTheDocument()
  })

  it('escalates after exactly 10000ms without progress and retains the last known value', () => {
    render(<JobStatus status="progress" label="Upload" progress={25} lastProgressAt={Date.now()} />)
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('No progress reported recently.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('No progress reported recently.')).toBeInTheDocument()
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '25')
    expect(screen.getByText('25%')).toBeInTheDocument()
  })

  it.each([Number.NaN, Number.POSITIVE_INFINITY, 'not-a-date'])('starts the stall clock at observation time for invalid timestamp %s', (lastProgressAt) => {
    render(<JobStatus status="running" label="Sync" lastProgressAt={lastProgressAt} />)
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('Still working; progress is unavailable.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('clamps a future timestamp to observation time instead of postponing escalation', () => {
    render(<JobStatus status="running" label="Sync" lastProgressAt={Date.now() + 60_000} />)
    act(() => vi.advanceTimersByTime(10_000))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('does not restart the stall clock when NaN progress rerenders with identical props', () => {
    const { rerender } = render(<JobStatus status="running" label="Sync" progress={Number.NaN} />)
    act(() => vi.advanceTimersByTime(5_000))
    rerender(<JobStatus status="running" label="Sync" progress={Number.NaN} />)
    act(() => vi.advanceTimersByTime(4_999))
    expect(screen.queryByText('Still working; progress is unavailable.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('keeps an existing stall warning when NaN progress rerenders with identical props', () => {
    const { rerender } = render(<JobStatus status="running" label="Sync" progress={Number.NaN} />)
    act(() => vi.advanceTimersByTime(10_000))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
    rerender(<JobStatus status="running" label="Sync" progress={Number.NaN} />)
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('escalates a paused job with unknown progress using paused copy, not still-working copy', () => {
    render(<JobStatus status="paused" label="Sync" />)
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('Paused; progress is unavailable.')).toBeNull()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Paused; progress is unavailable.')).toBeInTheDocument()
    expect(screen.queryByText('Still working; progress is unavailable.')).toBeNull()
  })

  it('escalates a paused job holding a known value without claiming work continues', () => {
    render(<JobStatus status="paused" label="Sync" progress={40} />)
    act(() => vi.advanceTimersByTime(10_000))
    expect(screen.getByText('Paused; no progress reported recently.')).toBeInTheDocument()
    expect(screen.queryByText('Still working; progress is unavailable.')).toBeNull()
  })

  it('exposes applicable cancel, retry, and background callbacks', () => {
    const onCancel = vi.fn(); const onRetry = vi.fn(); const onBackground = vi.fn()
    const { rerender } = render(<JobStatus status="running" label="Build" onCancel={onCancel} onBackground={onBackground} />)
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    fireEvent.click(screen.getByRole('button', { name: 'Run in background' }))
    expect(onCancel).toHaveBeenCalledOnce(); expect(onBackground).toHaveBeenCalledOnce()
    rerender(<JobStatus status="failed" label="Build" onRetry={onRetry} onCancel={onCancel} onBackground={onBackground} />)
    expect(screen.queryByRole('button', { name: 'Cancel' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Run in background' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledOnce()
  })

  it('announces terminal state changes and removes a stale stall warning immediately', () => {
    const { rerender } = render(<JobStatus status="progress" label="Upload" progress={25} lastProgressAt={Date.now() - 10_000} />)
    expect(screen.getByText('No progress reported recently.')).toBeInTheDocument()
    rerender(<JobStatus status="complete" label="Upload" progress={25} />)
    expect(screen.queryByText('No progress reported recently.')).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('Complete')
    rerender(<JobStatus status="failed" label="Upload" message="Network error" />)
    expect(screen.getByRole('alert')).toHaveTextContent('Failed')
  })

  it('uses readable secondary text for the job label', () => {
    render(<JobStatus status="queued" label="Index documents" />)
    expect(screen.getByText('Index documents')).toHaveClass('text-[var(--color-secondary)]')
  })
})


describe('JobStatus — restart and owned announcements', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-17T00:00:00Z'))
  })
  afterEach(() => vi.useRealTimers())

  it.each(['complete', 'failed', 'cancelled'] as const)('starts a fresh progress history and stall interval after %s on the same job key', (terminalStatus) => {
    const { rerender } = render(<JobStatus jobKey="upload" status="progress" label="Upload" progress={40} />)
    act(() => vi.advanceTimersByTime(10_000))
    expect(screen.getByText('No progress reported recently.')).toBeInTheDocument()
    rerender(<JobStatus jobKey="upload" status={terminalStatus} label="Upload" progress={undefined} />)
    expect(screen.queryByText('No progress reported recently.')).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(5_000))
    // Progress stays unknown across this transition, isolating the restart
    // contract from the separate clock reset caused by changing progress.
    rerender(<JobStatus jobKey="upload" status="running" label="Upload" progress={undefined} />)
    expect(screen.queryByText('40%')).not.toBeInTheDocument()
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
    expect(screen.getByRole('status').textContent).toBe('Running')
    expect(screen.getByRole('alert')).toBeEmptyDOMElement()
    act(() => vi.advanceTimersByTime(9_999))
    expect(screen.queryByText('Still working; progress is unavailable.')).not.toBeInTheDocument()
    // Shared contract: escalation occurs at 10,000ms of the new attempt.
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
    expect(screen.getByRole('status').textContent).toBe('Running. Still working; progress is unavailable.')
    expect(screen.getByRole('progressbar')).not.toHaveAttribute('aria-valuenow')
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Still working; progress is unavailable.')).toBeInTheDocument()
  })

  it('invokes only Retry for a cancelled job and leaves lifecycle state with the caller', () => {
    const onRetry = vi.fn(); const onCancel = vi.fn(); const onBackground = vi.fn()
    render(<JobStatus status="cancelled" label="Upload" onRetry={onRetry} onCancel={onCancel} onBackground={onBackground} />)
    expect(onRetry).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Run in background' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(onRetry).toHaveBeenCalledOnce()
    expect(onCancel).not.toHaveBeenCalled()
    expect(onBackground).not.toHaveBeenCalled()
    expect(screen.getByText('Cancelled', { selector: 'p' })).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('keeps job announcements separate from empty idle action statuses', () => {
    const { rerender } = render(<JobStatus status="running" label="Upload" onCancel={vi.fn()} onBackground={vi.fn()} />)
    const jobRegion = document.querySelector('[data-job-announcement="polite"]') as HTMLElement
    const failureRegion = screen.getByRole('alert')
    expect(jobRegion).toHaveAttribute('role', 'status')
    expect(jobRegion).toHaveAttribute('aria-live', 'polite')
    expect(jobRegion).toHaveAttribute('aria-atomic', 'true')
    expect(failureRegion).toHaveAttribute('aria-atomic', 'true')
    expect(failureRegion).toBeEmptyDOMElement()
    expect(screen.getAllByRole('status').map((region) => region.textContent)).toEqual(['Running', '', ''])
    rerender(<JobStatus status="failed" label="Upload" message="Network error" onRetry={vi.fn()} />)
    expect(document.querySelector('[data-job-announcement="polite"]')).toBe(jobRegion)
    expect(screen.getByRole('alert')).toBe(failureRegion)
    expect(screen.getAllByRole('status').map((region) => region.textContent)).toEqual(['', ''])
    expect(failureRegion.textContent).toBe('Failed. Network error')
    expect(screen.getByText('Failed', { selector: 'p' })).not.toHaveAttribute('role')
  })
})

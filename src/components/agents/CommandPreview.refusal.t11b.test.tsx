// Architect F7 / T11 FR-12: the real hook's run-refusal 400 is shown as
// that same actionable refusal, never blamed on an invalid typed value.
// Only the HTTP/API edge is mocked; CommandPreview and its hook are real.
import { act, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, fetchExecutorPreview } from '@/lib/api'
import { CommandPreview } from './CommandPreview'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchExecutorPreview: vi.fn() }
})

async function settleDebounce() {
  await act(async () => {
    vi.advanceTimersByTime(400)
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

beforeEach(() => {
  vi.mocked(fetchExecutorPreview).mockReset()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('T11-B actual preview refusal presentation (F7)', () => {
  it.each([
    'This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex.',
    'This agent cannot run: the program set for Codex in Settings → Third-party agents does not pass its checks (not_found at /tmp/t11b-deleted-program). A person can fix it there.',
  ])('shows the run refusal without a value-validation wrapper: %s', async (refusal) => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new ApiError(400, refusal))
    render(<CommandPreview req={{ cli: 'codex' }} testId="t11b-preview" />)
    await settleDebounce()

    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
    expect(fetchExecutorPreview).toHaveBeenCalledWith(
      { cli: 'codex' }, { signal: expect.any(AbortSignal) },
    )
    const block = screen.getByTestId('t11b-preview')
    expect(block).toHaveAttribute('data-preview-status', 'error')
    expect(block).toHaveTextContent(refusal)
    expect(block).not.toHaveTextContent("This value couldn't be validated")
    expect(block).not.toHaveTextContent('check it before saving')
    expect(block).not.toHaveTextContent('the settings below still apply')
    expect(screen.queryByTestId('t11b-preview-command-line')).not.toBeInTheDocument()
  })

  it('still distinguishes a genuinely schema-invalid request from a run refusal', async () => {
    const validation = 'max_tool_iterations must be between 1 and 1000'
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new ApiError(400, validation))
    render(<CommandPreview req={{ cli: 'codex' }} testId="t11b-preview" />)
    await settleDebounce()
    const block = screen.getByTestId('t11b-preview')
    expect(block).toHaveTextContent(`This value couldn't be validated (${validation}) — check it before saving.`)
    expect(block).not.toHaveTextContent('the settings below still apply')
  })

  it.each([429, 500])('keeps a transport/server failure separate (HTTP %s)', async (status) => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new ApiError(status, 'temporarily unavailable'))
    render(<CommandPreview req={{ cli: 'codex' }} testId="t11b-preview" />)
    await settleDebounce()
    const block = screen.getByTestId('t11b-preview')
    expect(block).toHaveTextContent("Couldn't compute a live command preview")
    expect(block).not.toHaveTextContent("This value couldn't be validated")
  })
})

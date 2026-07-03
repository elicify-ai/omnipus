// useCommandPreview.test.ts — executor-command-preview UI. Covers: debounce
// (>=400ms, mirrors useCliPathValidation's F-17), re-fetch on field-VALUE
// change (not on every re-render/new-object-literal), cancel-in-flight on a
// rapid subsequent change, idle when no `cli` is selected, success/error
// states, and the retry() affordance.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useCommandPreview, buildExecutorPreviewRequest } from './useCommandPreview'
import { fetchExecutorPreview, ApiError } from '@/lib/api'
import type { ExecutorCommandPreviewRequest, ExecutorCommandPreviewResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchExecutorPreview: vi.fn() }
})

function okResponse(overrides: Partial<ExecutorCommandPreviewResponse> = {}): ExecutorCommandPreviewResponse {
  return {
    binary: 'claude',
    argv: ['-p', '--output-format', 'stream-json'],
    command_line: 'claude -p --output-format stream-json',
    prompt_delivery: 'stdin',
    dropped_args: [],
    ...overrides,
  }
}

function req(overrides: Partial<ExecutorCommandPreviewRequest> = {}): ExecutorCommandPreviewRequest {
  return { cli: 'claude-code', ...overrides }
}

// Advances fake timers past the debounce window, then drains microtasks so
// the resulting fetchExecutorPreview().then()/.catch() has a chance to
// settle — mirrors useCliPathValidation.test.ts's `advanceAndFlush`.
async function advanceAndFlush(ms: number) {
  await act(async () => {
    vi.advanceTimersByTime(ms)
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

describe('useCommandPreview — idle when no CLI selected', () => {
  it('resolves to idle and never calls fetchExecutorPreview when req is undefined', async () => {
    const { result } = renderHook(() => useCommandPreview(undefined))
    expect(result.current.status).toEqual({ kind: 'idle' })
    await advanceAndFlush(1000)
    expect(fetchExecutorPreview).not.toHaveBeenCalled()
  })

  it('resolves to idle when req.cli is empty/undefined', async () => {
    const { result } = renderHook(() => useCommandPreview({ cli: undefined } as unknown as ExecutorCommandPreviewRequest))
    expect(result.current.status).toEqual({ kind: 'idle' })
    await advanceAndFlush(1000)
    expect(fetchExecutorPreview).not.toHaveBeenCalled()
  })
})

describe('useCommandPreview — debounce (>=400ms)', () => {
  it('does not call fetchExecutorPreview before the debounce window elapses', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { result } = renderHook(() => useCommandPreview(req()))
    expect(result.current.status).toEqual({ kind: 'pending' })

    await advanceAndFlush(399)
    expect(fetchExecutorPreview).not.toHaveBeenCalled()

    await advanceAndFlush(1)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
    expect(fetchExecutorPreview).toHaveBeenCalledWith(req(), expect.objectContaining({ signal: expect.any(AbortSignal) }))
  })

  it('resolves to a "result" state carrying the preview response', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({ kind: 'result', result: okResponse() })
  })
})

describe('useCommandPreview — fires on field-VALUE change, not on every render', () => {
  it('a new req OBJECT with the same values does not re-fetch or reset to pending', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { result, rerender } = renderHook(({ r }) => useCommandPreview(r), { initialProps: { r: req() } })
    await advanceAndFlush(400)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
    expect(result.current.status.kind).toBe('result')

    // Fresh object literal, same field values — must NOT trigger a new fetch
    // or flicker the already-resolved state back to pending.
    rerender({ r: { cli: 'claude-code' } })
    expect(result.current.status.kind).toBe('result')
    await advanceAndFlush(1000)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
  })

  it('changing model re-fetches with the new value after the debounce', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { rerender } = renderHook(({ r }) => useCommandPreview(r), { initialProps: { r: req() } })
    await advanceAndFlush(400)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)

    rerender({ r: req({ model: 'claude-sonnet-4-6' }) })
    await advanceAndFlush(400)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(2)
    expect(fetchExecutorPreview).toHaveBeenLastCalledWith(
      req({ model: 'claude-sonnet-4-6' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  it('changing cli_args re-fetches after the debounce', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { rerender } = renderHook(({ r }) => useCommandPreview(r), { initialProps: { r: req() } })
    await advanceAndFlush(400)

    rerender({ r: req({ cli_args: '--add-dir /extra' }) })
    await advanceAndFlush(400)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(2)
  })
})

describe('useCommandPreview — cancel in-flight on rapid field change', () => {
  it('a field change before the debounce fires cancels the previous timer entirely', async () => {
    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    const { rerender } = renderHook(({ r }) => useCommandPreview(r), { initialProps: { r: req({ model: 'one' }) } })
    await advanceAndFlush(200)
    rerender({ r: req({ model: 'two' }) })
    await advanceAndFlush(400)

    // Only the second (latest) burst reached the network.
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
    expect(fetchExecutorPreview).toHaveBeenCalledWith(req({ model: 'two' }), expect.anything())
  })

  it('aborts an in-flight request when a new field change starts a fresh burst', async () => {
    let capturedSignal: AbortSignal | undefined
    vi.mocked(fetchExecutorPreview).mockImplementation((_r, opts) => {
      capturedSignal = opts?.signal
      return new Promise<ExecutorCommandPreviewResponse>(() => {
        // never resolves — simulates a slow in-flight request
      })
    })
    const { rerender } = renderHook(({ r }) => useCommandPreview(r), { initialProps: { r: req({ model: 'one' }) } })
    await advanceAndFlush(400)
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(1)
    expect(capturedSignal?.aborted).toBe(false)

    rerender({ r: req({ model: 'two' }) })
    expect(capturedSignal?.aborted).toBe(true)
  })
})

describe('useCommandPreview — error state', () => {
  it('a rejected fetchExecutorPreview (transport failure) lands in "error" with the real message and isValidationError: false', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new Error('network down'))
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({ kind: 'error', message: 'network down', isValidationError: false })
  })

  it('a 400 (schema-validation) ApiError lands in "error" with the real server message and isValidationError: true', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(
      new ApiError(400, 'request body does not match schema ExecutorCommandPreviewRequest: model is too long'),
    )
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({
      kind: 'error',
      message: 'request body does not match schema ExecutorCommandPreviewRequest: model is too long',
      isValidationError: true,
    })
  })

  it('a 5xx ApiError lands in "error" with isValidationError: false', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(new ApiError(500, 'The server is unavailable. Please try again in a moment.'))
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({
      kind: 'error',
      message: 'The server is unavailable. Please try again in a moment.',
      isValidationError: false,
    })
  })

  it('a 429 (rate-limited) ApiError lands in "error" with isValidationError: false — not the operator\'s fault', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValue(
      new ApiError(429, 'Too many requests. Please slow down and try again shortly.'),
    )
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({
      kind: 'error',
      message: 'Too many requests. Please slow down and try again shortly.',
      isValidationError: false,
    })
  })

  it('logs the swallowed error via console.error so it is never invisible to engineers debugging a report', async () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const err = new ApiError(400, 'model is too long')
    vi.mocked(fetchExecutorPreview).mockRejectedValue(err)
    renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)

    expect(consoleErrorSpy).toHaveBeenCalledWith(expect.stringContaining('preview fetch failed'), err)
    consoleErrorSpy.mockRestore()
  })

  it('retry() re-issues the request for the current req', async () => {
    vi.mocked(fetchExecutorPreview).mockRejectedValueOnce(new Error('network down'))
    const { result } = renderHook(() => useCommandPreview(req()))
    await advanceAndFlush(400)
    expect(result.current.status).toEqual({ kind: 'error', message: 'network down', isValidationError: false })

    vi.mocked(fetchExecutorPreview).mockResolvedValue(okResponse())
    act(() => {
      result.current.retry()
    })
    expect(result.current.status).toEqual({ kind: 'pending' })
    await advanceAndFlush(400)
    expect(result.current.status).toEqual({ kind: 'result', result: okResponse() })
    expect(fetchExecutorPreview).toHaveBeenCalledTimes(2)
  })
})

describe('buildExecutorPreviewRequest — shared request-building helper', () => {
  it('trims a non-empty model and passes cli_path/cli_args through as-is', () => {
    expect(buildExecutorPreviewRequest('claude-code', '  claude-sonnet-4-6  ', '/usr/local/bin/claude', '--verbose')).toEqual({
      cli: 'claude-code',
      model: 'claude-sonnet-4-6',
      cli_path: '/usr/local/bin/claude',
      cli_args: '--verbose',
    })
  })

  it('collapses a blank/whitespace-only model to undefined', () => {
    expect(buildExecutorPreviewRequest('codex', '   ', undefined, undefined)).toEqual({
      cli: 'codex',
      model: undefined,
      cli_path: undefined,
      cli_args: undefined,
    })
  })
})

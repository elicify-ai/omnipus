// useCliPathValidation — external-executor-cli-path-detection spec
// (ADR-030). Covers: debounce (>=400ms per F-17), cancel-in-flight,
// blank-path short-circuit, error/non-result states never blocking
// (FR-019), and the reason-gated `cliValidationBlocked()` helper
// (FR-008/FR-018 — never gate on raw `ok`).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { cliValidationBlocked, useCliPathValidation } from './useCliPathValidation'
import { fetchCliValidate } from '@/lib/api'
import type { CliValidateResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchCliValidate: vi.fn() }
})

function okResponse(overrides: Partial<CliValidateResponse> = {}): CliValidateResponse {
  return { ok: true, reason: 'ok', resolved_path: '/usr/local/bin/claude', version: '1.2.3', detail: 'OK', ...overrides }
}

// Advances fake timers past the debounce window, then drains microtasks so
// the resulting fetchCliValidate().then()/.catch() has a chance to settle —
// mirrors the pattern in MediaActionToolbar.test.tsx (fake timers + waitFor
// don't mix well; draining microtasks directly is the reliable approach).
async function advanceAndFlush(ms: number) {
  await act(async () => {
    vi.advanceTimersByTime(ms)
    await Promise.resolve()
    await Promise.resolve()
    await Promise.resolve()
  })
}

beforeEach(() => {
  vi.mocked(fetchCliValidate).mockReset()
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('useCliPathValidation — debounce (F-17: >=400ms)', () => {
  it('does not call fetchCliValidate before the debounce window elapses', async () => {
    vi.mocked(fetchCliValidate).mockResolvedValue(okResponse())
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/usr/local/bin/claude')
    })
    expect(result.current.status).toEqual({ kind: 'pending' })

    await advanceAndFlush(399)
    expect(fetchCliValidate).not.toHaveBeenCalled()

    await advanceAndFlush(1)
    expect(fetchCliValidate).toHaveBeenCalledTimes(1)
    expect(fetchCliValidate).toHaveBeenCalledWith(
      'claude-code',
      '/usr/local/bin/claude',
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  it('resolves to a "result" state carrying the classified response', async () => {
    vi.mocked(fetchCliValidate).mockResolvedValue(okResponse({ reason: 'ok' }))
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/usr/local/bin/claude')
    })
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({ kind: 'result', result: okResponse({ reason: 'ok' }) })
  })

  it('a blank path short-circuits to idle without a network call', async () => {
    const { result } = renderHook(() => useCliPathValidation())
    act(() => {
      result.current.validate('claude-code', '   ')
    })
    expect(result.current.status).toEqual({ kind: 'idle' })
    await advanceAndFlush(1000)
    expect(fetchCliValidate).not.toHaveBeenCalled()
  })
})

describe('useCliPathValidation — cancel in-flight (F-17)', () => {
  it('a second validate() call before the debounce fires cancels the first timer entirely', async () => {
    vi.mocked(fetchCliValidate).mockResolvedValue(okResponse())
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/one/claude')
    })
    await advanceAndFlush(200)
    act(() => {
      result.current.validate('claude-code', '/two/claude')
    })
    await advanceAndFlush(400)

    // Only the second call's debounce fired — the first was cancelled
    // before it ever reached the network.
    expect(fetchCliValidate).toHaveBeenCalledTimes(1)
    expect(fetchCliValidate).toHaveBeenCalledWith('claude-code', '/two/claude', expect.anything())
  })

  it('aborts an in-flight request when a new validate() call starts', async () => {
    let capturedSignal: AbortSignal | undefined
    vi.mocked(fetchCliValidate).mockImplementation((_cli, _path, opts) => {
      capturedSignal = opts?.signal
      return new Promise(() => {
        // never resolves — simulates a slow in-flight request
      })
    })
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/one/claude')
    })
    await advanceAndFlush(400)
    expect(fetchCliValidate).toHaveBeenCalledTimes(1)
    expect(capturedSignal?.aborted).toBe(false)

    act(() => {
      result.current.validate('claude-code', '/two/claude')
    })
    expect(capturedSignal?.aborted).toBe(true)
  })

  it('reset() cancels a pending debounce and returns to idle', async () => {
    vi.mocked(fetchCliValidate).mockResolvedValue(okResponse())
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/usr/local/bin/claude')
    })
    expect(result.current.status.kind).toBe('pending')

    act(() => {
      result.current.reset()
    })
    expect(result.current.status).toEqual({ kind: 'idle' })

    await advanceAndFlush(1000)
    expect(fetchCliValidate).not.toHaveBeenCalled()
  })
})

describe('useCliPathValidation — FR-019 (no trap on error)', () => {
  it('a rejected fetchCliValidate (network error / 429) lands in "error", not a block', async () => {
    vi.mocked(fetchCliValidate).mockRejectedValue(new Error('rate limited'))
    const { result } = renderHook(() => useCliPathValidation())

    act(() => {
      result.current.validate('claude-code', '/usr/local/bin/claude')
    })
    await advanceAndFlush(400)

    expect(result.current.status).toEqual({ kind: 'error' })
    expect(cliValidationBlocked(result.current.status)).toBe(false)
  })
})

describe('cliValidationBlocked — gates on reason, never raw ok (FR-008/FR-018)', () => {
  it('blocks on missing-binary and handshake-failed', () => {
    expect(cliValidationBlocked({ kind: 'result', result: okResponse({ ok: false, reason: 'missing-binary' }) })).toBe(true)
    expect(cliValidationBlocked({ kind: 'result', result: okResponse({ ok: false, reason: 'handshake-failed' }) })).toBe(true)
  })

  it('does not block on ok, unauthenticated, unknown-cli, idle, pending, or error', () => {
    expect(cliValidationBlocked({ kind: 'result', result: okResponse({ reason: 'ok' }) })).toBe(false)
    expect(cliValidationBlocked({ kind: 'result', result: okResponse({ reason: 'unauthenticated' }) })).toBe(false)
    expect(cliValidationBlocked({ kind: 'result', result: okResponse({ ok: false, reason: 'unknown-cli' }) })).toBe(false)
    expect(cliValidationBlocked({ kind: 'idle' })).toBe(false)
    expect(cliValidationBlocked({ kind: 'pending' })).toBe(false)
    expect(cliValidationBlocked({ kind: 'error' })).toBe(false)
  })
})

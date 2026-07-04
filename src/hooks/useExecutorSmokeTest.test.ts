// useExecutorSmokeTest.test.ts — imperative "send a real test message"
// action backing CommandPreview's smoke-test button. Unlike
// useCommandPreview (reactive, debounced, auto-fires on field-value
// change), this hook must NEVER fire on its own — only run() does, and only
// one call is ever in flight at a time. Covers: idle by default (no call on
// mount), idle -> pending -> result (both an ok:true and a domain-level
// ok:false response, which is still a "result" not an "error"), idle ->
// pending -> error on a genuine transport/auth failure, cancel() resetting
// to idle and aborting the in-flight request, a rapid re-run() aborting the
// previous in-flight request, and cancel-on-unmount.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useExecutorSmokeTest } from './useExecutorSmokeTest'
import { fetchExecutorSmokeTest, ApiError } from '@/lib/api'
import type { ExecutorSmokeTestRequest, ExecutorSmokeTestResponse } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchExecutorSmokeTest: vi.fn() }
})

function req(overrides: Partial<ExecutorSmokeTestRequest> = {}): ExecutorSmokeTestRequest {
  return { cli: 'claude-code', ...overrides }
}

function okResult(overrides: Partial<ExecutorSmokeTestResponse> = {}): ExecutorSmokeTestResponse {
  return { ok: true, response_text: '2', duration_ms: 1234, used_agent_workspace: false, ...overrides }
}

beforeEach(() => {
  vi.mocked(fetchExecutorSmokeTest).mockReset()
})

describe('useExecutorSmokeTest — idle by default', () => {
  it('never calls fetchExecutorSmokeTest on mount', () => {
    const { result } = renderHook(() => useExecutorSmokeTest())
    expect(result.current.status).toEqual({ kind: 'idle' })
    expect(fetchExecutorSmokeTest).not.toHaveBeenCalled()
  })
})

describe('useExecutorSmokeTest — run() success', () => {
  it('goes idle -> pending -> result, calling fetchExecutorSmokeTest with an AbortSignal', async () => {
    let resolveFetch: (value: ExecutorSmokeTestResponse) => void = () => {}
    vi.mocked(fetchExecutorSmokeTest).mockReturnValue(
      new Promise((resolve) => {
        resolveFetch = resolve
      }),
    )
    const { result } = renderHook(() => useExecutorSmokeTest())
    expect(result.current.status).toEqual({ kind: 'idle' })

    act(() => {
      result.current.run(req())
    })
    expect(result.current.status).toEqual({ kind: 'pending' })
    expect(fetchExecutorSmokeTest).toHaveBeenCalledTimes(1)
    expect(fetchExecutorSmokeTest).toHaveBeenCalledWith(req(), expect.objectContaining({ signal: expect.any(AbortSignal) }))

    await act(async () => {
      resolveFetch(okResult())
      await Promise.resolve()
    })
    expect(result.current.status).toEqual({ kind: 'result', result: okResult() })
  })

  it('a domain-level ok:false response lands in "result", not "error"', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue({
      ok: false,
      error: 'Authentication failed.',
      duration_ms: 500,
      used_agent_workspace: false,
    })
    const { result } = renderHook(() => useExecutorSmokeTest())

    await act(async () => {
      result.current.run(req())
      await Promise.resolve()
    })

    expect(result.current.status).toEqual({
      kind: 'result',
      result: { ok: false, error: 'Authentication failed.', duration_ms: 500, used_agent_workspace: false },
    })
  })
})

describe('useExecutorSmokeTest — agent_id pass-through', () => {
  it('forwards agent_id in the request when the caller provides one', () => {
    vi.mocked(fetchExecutorSmokeTest).mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useExecutorSmokeTest())

    act(() => {
      result.current.run(req({ agent_id: 'agent-123' }))
    })

    expect(fetchExecutorSmokeTest).toHaveBeenCalledWith(
      req({ agent_id: 'agent-123' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  it('omits agent_id from the request when the caller does not provide one (create-wizard case)', () => {
    vi.mocked(fetchExecutorSmokeTest).mockReturnValue(new Promise(() => {}))
    const { result } = renderHook(() => useExecutorSmokeTest())

    act(() => {
      result.current.run(req())
    })

    const call = vi.mocked(fetchExecutorSmokeTest).mock.calls[0][0]
    expect(call.agent_id).toBeUndefined()
  })

  it('carries used_agent_workspace: true through to the result state when the run used the agent\'s own workspace', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okResult({ used_agent_workspace: true }))
    const { result } = renderHook(() => useExecutorSmokeTest())

    await act(async () => {
      result.current.run(req({ agent_id: 'agent-123' }))
      await Promise.resolve()
    })

    expect(result.current.status).toEqual({ kind: 'result', result: okResult({ used_agent_workspace: true }) })
  })

  it('carries used_agent_workspace: false through to the result state for the ephemeral scratch-dir fallback', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockResolvedValue(okResult({ used_agent_workspace: false }))
    const { result } = renderHook(() => useExecutorSmokeTest())

    await act(async () => {
      result.current.run(req())
      await Promise.resolve()
    })

    expect(result.current.status).toEqual({ kind: 'result', result: okResult({ used_agent_workspace: false }) })
  })
})

describe('useExecutorSmokeTest — run() failure', () => {
  it('goes idle -> pending -> error on a rejected fetch, carrying the ApiError userMessage', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockRejectedValue(new ApiError(500, 'The CLI failed to start.'))
    const { result } = renderHook(() => useExecutorSmokeTest())

    await act(async () => {
      result.current.run(req())
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(result.current.status).toEqual({ kind: 'error', message: 'The CLI failed to start.' })
  })

  it('falls back to the Error message for a non-ApiError rejection', async () => {
    vi.mocked(fetchExecutorSmokeTest).mockRejectedValue(new Error('network down'))
    const { result } = renderHook(() => useExecutorSmokeTest())

    await act(async () => {
      result.current.run(req())
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(result.current.status).toEqual({ kind: 'error', message: 'network down' })
  })
})

describe('useExecutorSmokeTest — cancel()', () => {
  it('resets to idle and aborts the in-flight request', () => {
    let capturedSignal: AbortSignal | undefined
    vi.mocked(fetchExecutorSmokeTest).mockImplementation((_r, opts) => {
      capturedSignal = opts?.signal
      return new Promise<ExecutorSmokeTestResponse>(() => {
        // never resolves — simulates a slow in-flight request
      })
    })
    const { result } = renderHook(() => useExecutorSmokeTest())

    act(() => {
      result.current.run(req())
    })
    expect(result.current.status).toEqual({ kind: 'pending' })
    expect(capturedSignal?.aborted).toBe(false)

    act(() => {
      result.current.cancel()
    })
    expect(result.current.status).toEqual({ kind: 'idle' })
    expect(capturedSignal?.aborted).toBe(true)
  })
})

describe('useExecutorSmokeTest — rapid re-run cancels the previous in-flight request', () => {
  it('aborts the first request signal when run() is called again before it resolves', () => {
    const signals: AbortSignal[] = []
    vi.mocked(fetchExecutorSmokeTest).mockImplementation((_r, opts) => {
      if (opts?.signal) signals.push(opts.signal)
      return new Promise<ExecutorSmokeTestResponse>(() => {})
    })
    const { result } = renderHook(() => useExecutorSmokeTest())

    act(() => {
      result.current.run(req({ model: 'one' }))
    })
    act(() => {
      result.current.run(req({ model: 'two' }))
    })

    expect(signals).toHaveLength(2)
    expect(signals[0].aborted).toBe(true)
    expect(signals[1].aborted).toBe(false)
    expect(fetchExecutorSmokeTest).toHaveBeenLastCalledWith(
      req({ model: 'two' }),
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })
})

describe('useExecutorSmokeTest — unmount cancels in-flight request', () => {
  it('aborts the in-flight request when the component unmounts', () => {
    let capturedSignal: AbortSignal | undefined
    vi.mocked(fetchExecutorSmokeTest).mockImplementation((_r, opts) => {
      capturedSignal = opts?.signal
      return new Promise<ExecutorSmokeTestResponse>(() => {})
    })
    const { result, unmount } = renderHook(() => useExecutorSmokeTest())

    act(() => {
      result.current.run(req())
    })
    expect(capturedSignal?.aborted).toBe(false)

    unmount()
    expect(capturedSignal?.aborted).toBe(true)
  })
})

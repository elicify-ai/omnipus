import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

// useAutoSave imports isReAuthCancelled from useReAuthGate. Mock the module so
// the hook picks up the mock in tests; use the real predicate logic (pure fn).
vi.mock('@/components/settings/useReAuthGate', () => ({
  isReAuthCancelled: (err: unknown) =>
    err instanceof Error && err.message === 'Re-authentication cancelled',
}))

import { useAutoSave } from '@/hooks/useAutoSave'
import { ApiError } from '@/lib/api-error'

describe('useAutoSave revision conflicts', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(window, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('does not blindly retry a queued draft after a revision conflict', async () => {
    let rejectFirst!: (err: unknown) => void
    const first = new Promise<void>((_resolve, reject) => { rejectFirst = reject })
    const saveFn = vi.fn().mockReturnValue(first)
    let data = { v: 1 }
    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 50 }),
      { initialProps: { d: data } },
    )

    data = { v: 2 }
    rerender({ d: data })
    await act(async () => vi.advanceTimersByTime(100))
    data = { v: 3 }
    rerender({ d: data })
    await act(async () => vi.advanceTimersByTime(100))

    await act(async () => {
      rejectFirst(new ApiError(409, 'Revision conflict'))
      await Promise.resolve()
    })

    expect(saveFn).toHaveBeenCalledTimes(1)
    expect(result.current.status).toBe('conflict')
    expect(result.current.hasPendingChanges()).toBe(true)
  })

})

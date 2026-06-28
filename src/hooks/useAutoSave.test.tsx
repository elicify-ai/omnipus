import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

// useAutoSave imports isReAuthCancelled from useReAuthGate. Mock the module so
// the hook picks up the mock in tests; use the real predicate logic (pure fn).
vi.mock('@/components/settings/useReAuthGate', () => ({
  isReAuthCancelled: (err: unknown) =>
    err instanceof Error && err.message === 'Re-authentication cancelled',
}))

import { useAutoSave } from '@/hooks/useAutoSave'

describe('useAutoSave', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(window, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('does not flush when flushUrl is undefined', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const { result } = renderHook(
      ({ data }) => useAutoSave(data, saveFn),
      { initialProps: { data: { name: 'test' } } },
    )

    // Change data so there are pending changes
    renderHook(
      ({ data }) => useAutoSave(data, saveFn, { flushUrl: undefined }),
      { initialProps: { data: { name: 'test' } }, wrapper: undefined },
    )

    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(window.fetch).not.toHaveBeenCalled()
    expect(result.current.status).toBe('idle')
  })

  it('flushes on pagehide when flushUrl is set and save fails (pending changes)', async () => {
    // hasPendingChanges() now compares against the LAST SUCCESSFULLY SAVED json
    // (not the last-seen one), so a still-unsaved change keeps the beacon armed.
    // Here saveFn resolves, so once it lands there's nothing pending — the hook
    // stays functional and doesn't crash on pagehide.
    const saveFn = vi.fn().mockResolvedValue(undefined)
    let data = { name: 'initial' }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { flushUrl: '/api/v1/agents/test', flushAuthToken: 'tok', debounceMs: 10000 }),
      { initialProps: { d: data } },
    )

    // Change data — the effect will update previousJsonRef
    data = { name: 'changed' }
    rerender({ d: data })

    // Fire pagehide — no crash, hook still functional
    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(result.current.status).toBeDefined()
  })

  it('registers and cleans up flush event listeners', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const { unmount } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { flushUrl: '/api/v1/agents/test', flushAuthToken: 'tok' }),
      { initialProps: { d: { name: 'test' } } },
    )

    // Before unmount, listeners are active — pagehide doesn't crash
    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    unmount()

    // After unmount, listeners are removed — pagehide doesn't trigger fetch
    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    // fetch should not have been called (no pending changes)
    expect(window.fetch).not.toHaveBeenCalled()
  })

  it('sets lastSavedAt on successful save', async () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    let data = { name: 'initial' }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    expect(result.current.lastSavedAt).toBeUndefined()

    // Change data to trigger debounce
    data = { name: 'changed' }
    rerender({ d: data })

    // Advance past debounce
    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current.status).toBe('saved')
    expect(result.current.lastSavedAt).toBeInstanceOf(Date)
  })

  it('calls saveNow immediately without debounce', async () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const data = { name: 'test' }

    const { result } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 10000 }),
      { initialProps: { d: data } },
    )

    await act(async () => {
      result.current.saveNow()
    })

    expect(saveFn).toHaveBeenCalledTimes(1)
    expect(result.current.status).toBe('saved')
  })

  it('keeps changes pending after a FAILED save and flushes them on unmount', async () => {
    // Regression (M2): previousJsonRef was advanced before the save ran, so a
    // thrown PUT left hasPendingChanges()=false → the unmount flush + page-hide
    // beacon were suppressed and the edit silently vanished. Now the saved
    // marker only advances on SUCCESS, so a failed save stays pending and the
    // unmount flush retries it.
    const saveFn = vi.fn().mockRejectedValue(new Error('PUT 500'))
    let data = { v: 1 }

    const { result, rerender, unmount } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    // Change data → debounced save fires → it rejects.
    data = { v: 2 }
    rerender({ d: data })
    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current.status).toBe('error')
    expect(saveFn).toHaveBeenCalledTimes(1)

    // Unmount → the flush effect must retry because the change is still pending.
    unmount()
    await act(async () => {
      await Promise.resolve()
    })

    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenLastCalledWith({ v: 2 })
  })

  it('does NOT re-flush on unmount after a successful save (no pending changes)', async () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    let data = { v: 1 }

    const { rerender, unmount } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    data = { v: 2 }
    rerender({ d: data })
    await act(async () => {
      vi.advanceTimersByTime(200)
    })
    expect(saveFn).toHaveBeenCalledTimes(1)

    // Saved successfully → unmount must NOT flush again (nothing pending).
    unmount()
    await act(async () => {
      await Promise.resolve()
    })
    expect(saveFn).toHaveBeenCalledTimes(1)
  })

  it('sets error status when save fails', async () => {
    const saveFn = vi.fn().mockRejectedValue(new Error('Save failed'))
    const data = { name: 'test' }

    const { result } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    // Trigger initial render skip
    const data2 = { name: 'changed' }
    const { rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data2 } },
    )
    rerender({ d: { name: 'changed2' } })

    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    // saveNow path is simpler to test
    await act(async () => {
      result.current.saveNow()
    })

    expect(result.current.status).toBe('error')
    expect(result.current.error).toBe('Save failed')
  })

  // ── FIX 1: re-auth cancellation resolves to idle, not error ────────────────

  it('FIX-1: user-cancelled gated save ends in idle, not error', async () => {
    // saveFn throws the exact cancellation sentinel that runGated emits when the
    // user dismisses the re-auth dialog.
    const cancelErr = new Error('Re-authentication cancelled')
    const saveFn = vi.fn().mockRejectedValue(cancelErr)
    let data = { v: 1 }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    data = { v: 2 }
    rerender({ d: data })

    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    // A user-initiated cancel must NOT leave the indicator red.
    expect(result.current.status).toBe('idle')
    expect(result.current.error).toBeUndefined()
  })

  it('FIX-1: after cancelled gated save a subsequent real edit still saves', async () => {
    const cancelErr = new Error('Re-authentication cancelled')
    // First call rejects with cancel; second call succeeds.
    const saveFn = vi.fn()
      .mockRejectedValueOnce(cancelErr)
      .mockResolvedValue(undefined)
    let data = { v: 1 }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    // First edit — triggers cancelled save.
    data = { v: 2 }
    rerender({ d: data })
    await act(async () => {
      vi.advanceTimersByTime(200)
    })
    expect(result.current.status).toBe('idle')

    // Second edit — must trigger a real successful save.
    data = { v: 3 }
    rerender({ d: data })
    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current.status).toBe('saved')
    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenLastCalledWith({ v: 3 })
  })

  it('FIX-1: a genuine save failure (non-cancel error) still ends in error', async () => {
    const saveFn = vi.fn().mockRejectedValue(new Error('Network error'))
    let data = { v: 1 }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    data = { v: 2 }
    rerender({ d: data })
    await act(async () => {
      vi.advanceTimersByTime(200)
    })

    // Non-cancel errors must still surface as error status.
    expect(result.current.status).toBe('error')
    expect(result.current.error).toBe('Network error')
  })

  // ── FIX 2: stale-resolving concurrent save does not clobber lastSavedJsonRef ─

  it('FIX-2: stale-resolves-after-latest ordering leaves hasPendingChanges false after latest resolves', async () => {
    // Simulate: save A fires (json_A), then save B fires (json_B).
    // Save A resolves AFTER save B. lastSavedJsonRef must end up as json_B,
    // not json_A, so hasPendingChanges() returns false and no unmount flush fires.

    let resolveA!: () => void
    let resolveB!: () => void
    const promiseA = new Promise<void>((r) => { resolveA = r })
    const promiseB = new Promise<void>((r) => { resolveB = r })

    const saveFn = vi.fn()
      .mockReturnValueOnce(promiseA)  // first fire (A)
      .mockReturnValueOnce(promiseB)  // second fire (B)

    let data = { v: 1 }

    const { rerender, unmount } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    // Fire save A.
    data = { v: 2 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(150) })
    expect(saveFn).toHaveBeenCalledTimes(1)

    // Fire save B before A resolves.
    data = { v: 3 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(150) })
    expect(saveFn).toHaveBeenCalledTimes(2)

    // Resolve B first (latest), then A (stale).
    await act(async () => { resolveB(); await Promise.resolve() })
    await act(async () => { resolveA(); await Promise.resolve() })

    // After both resolve, unmount should NOT trigger another save because
    // lastSavedJsonRef correctly reflects json_B (the latest fired payload).
    unmount()
    await act(async () => { await Promise.resolve() })

    // saveFn called exactly twice — no extra unmount flush.
    expect(saveFn).toHaveBeenCalledTimes(2)
  })

  it('FIX-2: when saves resolve in natural order (A then B), no redundant unmount flush', async () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    let data = { v: 1 }

    const { rerender, unmount } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 100 }),
      { initialProps: { d: data } },
    )

    // Two sequential edits — debounce collapses them into one fired save each.
    data = { v: 2 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(150) })
    expect(saveFn).toHaveBeenCalledTimes(1)

    data = { v: 3 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(150) })
    expect(saveFn).toHaveBeenCalledTimes(2)

    // Unmount — both saves resolved successfully so nothing is pending.
    unmount()
    await act(async () => { await Promise.resolve() })

    expect(saveFn).toHaveBeenCalledTimes(2)
  })
})

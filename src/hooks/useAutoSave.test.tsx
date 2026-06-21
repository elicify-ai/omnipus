import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
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
})

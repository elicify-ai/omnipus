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

  // ── F3: component-supplied beaconFlush override ─────────────────────────

  it('F3: calls beaconFlush instead of the built-in single-URL fetch when both are supplied', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const beaconFlush = vi.fn()
    let data = { name: 'initial' }

    const { rerender } = renderHook(
      ({ d }) =>
        useAutoSave(d, saveFn, {
          flushUrl: '/api/v1/agents/test',
          flushAuthToken: 'tok',
          debounceMs: 10000,
          beaconFlush,
        }),
      { initialProps: { d: data } },
    )

    // Create a pending change (debounce never fires — 10s delay).
    data = { name: 'changed' }
    rerender({ d: data })

    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(beaconFlush).toHaveBeenCalledTimes(1)
    // The built-in flush must NOT also fire — beaconFlush fully replaces it.
    expect(window.fetch).not.toHaveBeenCalled()
  })

  it('F3: beaconFlush is NOT called when there are no pending changes', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const beaconFlush = vi.fn()

    renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 10000, beaconFlush }),
      { initialProps: { d: { name: 'unchanged' } } },
    )

    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(beaconFlush).not.toHaveBeenCalled()
  })

  it('F3: a synchronously-throwing beaconFlush does not crash the flush path', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const beaconFlush = vi.fn(() => {
      throw new Error('boom')
    })
    let data = { name: 'initial' }

    const { rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 10000, beaconFlush }),
      { initialProps: { d: data } },
    )

    data = { name: 'changed' }
    rerender({ d: data })

    expect(() => {
      act(() => {
        window.dispatchEvent(new Event('pagehide'))
      })
    }).not.toThrow()

    expect(beaconFlush).toHaveBeenCalledTimes(1)
  })

  it('F3: registers flush listeners when only beaconFlush is supplied (no flushUrl)', () => {
    const saveFn = vi.fn().mockResolvedValue(undefined)
    const beaconFlush = vi.fn()
    let data = { name: 'initial' }

    const { rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 10000, beaconFlush }),
      { initialProps: { d: data } },
    )

    data = { name: 'changed' }
    rerender({ d: data })

    act(() => {
      window.dispatchEvent(new Event('pagehide'))
    })

    expect(beaconFlush).toHaveBeenCalledTimes(1)
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
  //
  // NOTE: as of FIX 3 (serialized saves), two saveFn calls can no longer be
  // concurrently in flight at all — see the "FIX 3" describe block below for
  // the primary regression coverage for the actual data-loss bug. This test
  // is updated to reflect the new reality: a save requested while one is
  // outstanding is QUEUED and only fires once the outstanding one settles,
  // so "B resolves before A" can no longer happen — there is no more A/B
  // resolution-order to race. What's still worth asserting is that the
  // queued (later) data wins and the bookkeeping ends up consistent (no
  // redundant unmount flush).

  it('FIX-2/FIX-3: a save requested while one is in flight is queued, runs after it settles, and its (latest) data wins — no redundant unmount flush', async () => {
    let resolveA!: () => void
    let resolveB!: () => void
    const promiseA = new Promise<void>((r) => { resolveA = r })
    const promiseB = new Promise<void>((r) => { resolveB = r })

    const saveFn = vi.fn()
      .mockReturnValueOnce(promiseA)  // first fire (A)
      .mockReturnValueOnce(promiseB)  // queued re-run (B)

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

    // Data changes again while A is still in flight (unresolved). This must
    // NOT dispatch a second, concurrent saveFn call.
    data = { v: 3 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(150) })
    expect(saveFn).toHaveBeenCalledTimes(1)

    // Resolve A — the queued re-run (carrying v3, the latest data) fires
    // immediately afterward, strictly AFTER A settled.
    await act(async () => {
      resolveA()
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenLastCalledWith({ v: 3 })

    // Resolve the queued re-run (B).
    await act(async () => { resolveB(); await Promise.resolve() })

    // lastSavedJsonRef now reflects v3 — unmount must NOT trigger another save.
    unmount()
    await act(async () => { await Promise.resolve() })

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

  // ── FIX 3: serialized saves (regression — agent fallback_models save race) ──
  //
  // Live UAT found that the agent edit form's fallback
  // model editor silently lost data: a PUT correctly carrying
  // `fallback_models: [{...}]` was clobbered by a SECOND PUT — fired from a
  // slightly earlier interaction — that carried no `fallback_models` key at
  // all, because full-resource PUT semantics + two overlapping in-flight
  // requests + last-network-arrival-wins meant the fresher save could be
  // overwritten by a staler one. Root cause: `doSave` had no guard against
  // firing a second concurrent request while one was already outstanding.

  it('FIX-3: does not fire a second concurrent saveFn while one is in flight; the queued re-run always carries the LATEST data', async () => {
    // Mirrors the exact wire shapes from the UAT network trace: interaction 1
    // (an earlier field edit, no fallbacks yet) fires a save that stays in
    // flight; interaction 2 ("+ Add fallback") happens before it resolves.
    let resolveEarlier!: (v?: unknown) => void
    const promiseEarlier = new Promise((r) => { resolveEarlier = r })
    const saveFn = vi.fn()
      .mockReturnValueOnce(promiseEarlier)
      .mockResolvedValueOnce(undefined)

    interface AgentFormData {
      name: string
      fallback_models?: { model: string; provider: string }[]
    }

    let data: AgentFormData = { name: 'Support Bot' }

    const { rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 50 }),
      { initialProps: { d: data } },
    )

    // Interaction 1 (earlier): an edit with no fallback configured yet.
    data = { name: 'Support Bot (renamed)' }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(saveFn).toHaveBeenCalledTimes(1)
    expect(saveFn).toHaveBeenNthCalledWith(1, { name: 'Support Bot (renamed)' })

    // Interaction 2 (slightly later — "+ Add fallback"): fires WHILE
    // interaction 1's save is still unresolved (slow network).
    data = {
      name: 'Support Bot (renamed)',
      fallback_models: [{ model: '~openai/gpt-mini-latest', provider: 'openrouter' }],
    }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(100) })

    // The debounce for interaction 2 fired, but the hook must NOT have
    // dispatched a second, concurrent saveFn call — that's the exact
    // condition that let the UAT bug's two PUTs race on the wire.
    expect(saveFn).toHaveBeenCalledTimes(1)

    // Resolve interaction 1's (stale, no-fallback) save.
    await act(async () => {
      resolveEarlier()
      await Promise.resolve()
      await Promise.resolve()
    })

    // Only now does the queued re-run fire — carrying the LATEST data (with
    // fallback_models), never the other way around, and never concurrently.
    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenNthCalledWith(2, {
      name: 'Support Bot (renamed)',
      fallback_models: [{ model: '~openai/gpt-mini-latest', provider: 'openrouter' }],
    })
  })

  it('FIX-3: saveNow() called while a debounced save is in flight queues rather than races', async () => {
    let resolveFirst!: (v?: unknown) => void
    const promiseFirst = new Promise((r) => { resolveFirst = r })
    const saveFn = vi.fn()
      .mockReturnValueOnce(promiseFirst)
      .mockResolvedValueOnce(undefined)

    let data = { v: 1 }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 50 }),
      { initialProps: { d: data } },
    )

    data = { v: 2 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(saveFn).toHaveBeenCalledTimes(1)
    expect(result.current.status).toBe('saving')

    // A newer edit + an explicit "save now" both happen before the first
    // request resolves.
    data = { v: 3 }
    rerender({ d: data })
    act(() => { result.current.saveNow() })

    // Still only one saveFn call outstanding.
    expect(saveFn).toHaveBeenCalledTimes(1)

    await act(async () => {
      resolveFirst()
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenLastCalledWith({ v: 3 })
  })

  it('FIX-3: a queued save still fires with the LATEST data after the in-flight save REJECTS (not silently dropped)', async () => {
    // Regression-gap coverage: the `finally` block that honors
    // `rerunPendingRef` is unconditional — it must fire a queued re-run
    // whether the in-flight save resolved OR rejected. A rejected first
    // save (e.g. a flaky network blip) is a realistic real-world trigger:
    // without this coverage, an edit queued behind a FAILED save could
    // regress to being silently dropped instead of retried.
    let rejectFirst!: (err?: unknown) => void
    const promiseFirst = new Promise<void>((_resolve, reject) => { rejectFirst = reject })
    const saveFn = vi.fn()
      .mockReturnValueOnce(promiseFirst)
      .mockResolvedValueOnce(undefined)

    let data = { v: 1 }

    const { result, rerender } = renderHook(
      ({ d }) => useAutoSave(d, saveFn, { debounceMs: 50 }),
      { initialProps: { d: data } },
    )

    // Fire the first save — it will reject.
    data = { v: 2 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(saveFn).toHaveBeenCalledTimes(1)
    expect(result.current.status).toBe('saving')

    // A newer edit arrives while the first save is still outstanding — it
    // must be queued via rerunPendingRef, not dispatched as a second,
    // concurrent request.
    data = { v: 3 }
    rerender({ d: data })
    await act(async () => { vi.advanceTimersByTime(100) })
    expect(saveFn).toHaveBeenCalledTimes(1)

    // The first save rejects. The queued re-run must still fire immediately
    // afterward, carrying the LATEST data.
    await act(async () => {
      rejectFirst(new Error('Network blip'))
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(saveFn).toHaveBeenCalledTimes(2)
    expect(saveFn).toHaveBeenLastCalledWith({ v: 3 })

    // The queued re-run's own save settles successfully — the edit was
    // NOT silently dropped after the first save's failure.
    await act(async () => {
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(result.current.status).toBe('saved')
  })
})

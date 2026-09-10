// viewEvaluationPool.test.ts — unit coverage of the pool's own admission
// logic, isolated from BasePreview's React/TanStack Query wiring (see
// BasePreview.pool.test.tsx for the integrated, in-flight-count-based proof
// that the component actually goes THROUGH this pool). This file proves the
// pool's own contract in isolation: the ceiling, the queue order, idempotent
// release, and cancellation of a still-queued request — the same shape
// pdfWorkerPool.test.ts already proves for EMB-032, applied to this pool's
// ceiling of 4 (ADR-083 spec ~line 901) instead of 2.

import { describe, it, expect, beforeEach, vi } from 'vitest'

describe('viewEvaluationPool', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('grants up to the ceiling immediately, without ever calling onQueued', async () => {
    const { viewEvaluationPool, VIEW_EVALUATION_POOL_CEILING } = await import('./viewEvaluationPool')
    expect(VIEW_EVALUATION_POOL_CEILING).toBe(4)

    const onQueuedFns = [vi.fn(), vi.fn(), vi.fn(), vi.fn()]
    const leases = []
    for (const onQueued of onQueuedFns) {
      leases.push(await viewEvaluationPool.acquire(new AbortController().signal, onQueued))
    }

    for (const onQueued of onQueuedFns) expect(onQueued).not.toHaveBeenCalled()
    expect(viewEvaluationPool.activeLeaseCountForTests).toBe(4)
    expect(viewEvaluationPool.queuedCountForTests).toBe(0)

    for (const lease of leases) lease.release()
  })

  it('queues a request beyond the ceiling and calls onQueued SYNCHRONOUSLY', async () => {
    const { viewEvaluationPool } = await import('./viewEvaluationPool')
    const lease1 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease2 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease3 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease4 = await viewEvaluationPool.acquire(new AbortController().signal)

    const onQueued5 = vi.fn()
    const acquire5 = viewEvaluationPool.acquire(new AbortController().signal, onQueued5)
    // MUTATION THIS DIES ON: firing onQueued asynchronously (e.g. via
    // `Promise.resolve().then(onQueued)`) — a caller setting UI state from it
    // would flash "waiting" even on the granted path if this ever moved off
    // the synchronous call.
    expect(onQueued5).toHaveBeenCalledTimes(1)
    expect(viewEvaluationPool.queuedCountForTests).toBe(1)

    lease1.release()
    const lease5 = await acquire5
    expect(viewEvaluationPool.queuedCountForTests).toBe(0)
    expect(viewEvaluationPool.activeLeaseCountForTests).toBe(4)

    lease2.release()
    lease3.release()
    lease4.release()
    lease5.release()
  })

  it('release is idempotent — calling it twice grants the next waiter only once', async () => {
    const { viewEvaluationPool } = await import('./viewEvaluationPool')
    const lease1 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease2 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease3 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease4 = await viewEvaluationPool.acquire(new AbortController().signal)

    // Deliberately does NOT release the granted lease inside `.then` — doing
    // so would itself free a slot and grant the OTHER queued waiter too,
    // masking the exact double-grant this test exists to catch.
    const grants: number[] = []
    const acquire5 = viewEvaluationPool.acquire(new AbortController().signal).then((lease) => {
      grants.push(5)
      return lease
    })
    const acquire6 = viewEvaluationPool.acquire(new AbortController().signal).then((lease) => {
      grants.push(6)
      return lease
    })

    lease1.release()
    lease1.release() // MUTATION THIS DIES ON: a non-idempotent release granting a slot twice.
    await new Promise((resolve) => setTimeout(resolve, 0))

    // Exactly ONE of the two queued waiters was granted by the one real
    // release — the double-call must not manufacture a second slot.
    expect(grants).toHaveLength(1)
    expect(viewEvaluationPool.activeLeaseCountForTests).toBe(4)

    const lease5 = await acquire5
    lease2.release()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(grants).toHaveLength(2)
    const lease6 = await acquire6

    lease3.release()
    lease4.release()
    lease5.release()
    lease6.release()
  })

  it('leaves the queue and rejects with AbortError when the signal aborts before a slot frees', async () => {
    const { viewEvaluationPool } = await import('./viewEvaluationPool')
    const lease1 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease2 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease3 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease4 = await viewEvaluationPool.acquire(new AbortController().signal)

    const controller5 = new AbortController()
    const acquire5 = viewEvaluationPool.acquire(controller5.signal)
    expect(viewEvaluationPool.queuedCountForTests).toBe(1)

    controller5.abort()
    await expect(acquire5).rejects.toThrow()
    // MUTATION THIS DIES ON: aborting without removing the entry from the
    // queue — the next release would then grant a lease nobody is waiting
    // for, silently starving a REAL subsequent request behind a phantom one.
    expect(viewEvaluationPool.queuedCountForTests).toBe(0)

    lease1.release()
    lease2.release()
    lease3.release()
    lease4.release()
  })

  it('rejects immediately when given an already-aborted signal', async () => {
    const { viewEvaluationPool } = await import('./viewEvaluationPool')
    const controller = new AbortController()
    controller.abort()
    await expect(viewEvaluationPool.acquire(controller.signal)).rejects.toThrow()
    expect(viewEvaluationPool.activeLeaseCountForTests).toBe(0)
  })

  it('a request that gave up its queued place does not consume the next freed slot', async () => {
    const { viewEvaluationPool } = await import('./viewEvaluationPool')
    const lease1 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease2 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease3 = await viewEvaluationPool.acquire(new AbortController().signal)
    const lease4 = await viewEvaluationPool.acquire(new AbortController().signal)

    const controller5 = new AbortController()
    const acquire5 = viewEvaluationPool.acquire(controller5.signal)
    controller5.abort()
    await expect(acquire5).rejects.toThrow()

    // A genuine sixth request, queued AFTER the abandoned fifth, must be the
    // one granted when lease1 frees — not silently skipped.
    const acquire6 = viewEvaluationPool.acquire(new AbortController().signal)
    lease1.release()
    const lease6 = await acquire6
    expect(viewEvaluationPool.activeLeaseCountForTests).toBe(4)

    lease2.release()
    lease3.release()
    lease4.release()
    lease6.release()
  })
})

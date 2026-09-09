// pdfWorkerPool.test.ts — unit coverage of the pool's own admission logic,
// isolated from LibraryPdfPreview's React/pdfjs wiring (see
// LibraryPdfPreview.pool.test.tsx for the integrated, worker-count-based
// proof that the component actually goes THROUGH this pool). This file
// proves the pool's own contract in isolation: the ceiling, the queue order,
// idempotent release, and cancellation of a still-queued request.

import { describe, it, expect, beforeEach, vi } from 'vitest'

describe('pdfWorkerPool', () => {
  beforeEach(() => {
    vi.resetModules()
  })

  it('grants up to the ceiling immediately, without ever calling onQueued', async () => {
    const { pdfWorkerPool, PDF_WORKER_POOL_CEILING } = await import('./pdfWorkerPool')
    expect(PDF_WORKER_POOL_CEILING).toBe(2)

    const onQueued1 = vi.fn()
    const onQueued2 = vi.fn()
    const lease1 = await pdfWorkerPool.acquire(new AbortController().signal, onQueued1)
    const lease2 = await pdfWorkerPool.acquire(new AbortController().signal, onQueued2)

    expect(onQueued1).not.toHaveBeenCalled()
    expect(onQueued2).not.toHaveBeenCalled()
    expect(pdfWorkerPool.activeLeaseCountForTests).toBe(2)
    expect(pdfWorkerPool.queuedCountForTests).toBe(0)

    lease1.release()
    lease2.release()
  })

  it('queues a request beyond the ceiling and calls onQueued SYNCHRONOUSLY', async () => {
    const { pdfWorkerPool } = await import('./pdfWorkerPool')
    const lease1 = await pdfWorkerPool.acquire(new AbortController().signal)
    const lease2 = await pdfWorkerPool.acquire(new AbortController().signal)

    const onQueued3 = vi.fn()
    const acquire3 = pdfWorkerPool.acquire(new AbortController().signal, onQueued3)
    // MUTATION THIS DIES ON: firing onQueued asynchronously (e.g. via
    // `Promise.resolve().then(onQueued)`) — a caller setting UI state from it
    // would flash "waiting" even on the granted path if this ever moved off
    // the synchronous call.
    expect(onQueued3).toHaveBeenCalledTimes(1)
    expect(pdfWorkerPool.queuedCountForTests).toBe(1)

    lease1.release()
    const lease3 = await acquire3
    expect(pdfWorkerPool.queuedCountForTests).toBe(0)
    expect(pdfWorkerPool.activeLeaseCountForTests).toBe(2)

    lease2.release()
    lease3.release()
  })

  it('release is idempotent — calling it twice grants the next waiter only once', async () => {
    const { pdfWorkerPool } = await import('./pdfWorkerPool')
    const lease1 = await pdfWorkerPool.acquire(new AbortController().signal)
    const lease2 = await pdfWorkerPool.acquire(new AbortController().signal)

    // Deliberately does NOT release the granted lease inside `.then` — doing
    // so would itself free a slot and grant the OTHER queued waiter too,
    // masking the exact double-grant this test exists to catch.
    const grants: number[] = []
    const acquire3 = pdfWorkerPool.acquire(new AbortController().signal).then((lease) => {
      grants.push(3)
      return lease
    })
    const acquire4 = pdfWorkerPool.acquire(new AbortController().signal).then((lease) => {
      grants.push(4)
      return lease
    })

    lease1.release()
    lease1.release() // MUTATION THIS DIES ON: a non-idempotent release granting a slot twice.
    await new Promise((resolve) => setTimeout(resolve, 0))

    // Exactly ONE of the two queued waiters was granted by the one real
    // release — the double-call must not manufacture a second slot.
    expect(grants).toHaveLength(1)
    expect(pdfWorkerPool.activeLeaseCountForTests).toBe(2)

    const lease3 = await acquire3
    lease2.release()
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(grants).toHaveLength(2)
    const lease4 = await acquire4

    lease3.release()
    lease4.release()
  })

  it('leaves the queue and rejects with AbortError when the signal aborts before a slot frees', async () => {
    const { pdfWorkerPool } = await import('./pdfWorkerPool')
    const lease1 = await pdfWorkerPool.acquire(new AbortController().signal)
    const lease2 = await pdfWorkerPool.acquire(new AbortController().signal)

    const controller3 = new AbortController()
    const acquire3 = pdfWorkerPool.acquire(controller3.signal)
    expect(pdfWorkerPool.queuedCountForTests).toBe(1)

    controller3.abort()
    await expect(acquire3).rejects.toThrow()
    // MUTATION THIS DIES ON: aborting without removing the entry from the
    // queue — the next release would then grant a lease nobody is waiting
    // for, silently starving a REAL subsequent request behind a phantom one.
    expect(pdfWorkerPool.queuedCountForTests).toBe(0)

    lease1.release()
    lease2.release()
  })

  it('rejects immediately when given an already-aborted signal', async () => {
    const { pdfWorkerPool } = await import('./pdfWorkerPool')
    const controller = new AbortController()
    controller.abort()
    await expect(pdfWorkerPool.acquire(controller.signal)).rejects.toThrow()
    expect(pdfWorkerPool.activeLeaseCountForTests).toBe(0)
  })

  it('a request that gave up its queued place does not consume the next freed slot', async () => {
    const { pdfWorkerPool } = await import('./pdfWorkerPool')
    const lease1 = await pdfWorkerPool.acquire(new AbortController().signal)
    const lease2 = await pdfWorkerPool.acquire(new AbortController().signal)

    const controller3 = new AbortController()
    const acquire3 = pdfWorkerPool.acquire(controller3.signal)
    controller3.abort()
    await expect(acquire3).rejects.toThrow()

    // A genuine fourth request, queued AFTER the abandoned third, must be
    // the one granted when lease1 frees — not silently skipped.
    const acquire4 = pdfWorkerPool.acquire(new AbortController().signal)
    lease1.release()
    const lease4 = await acquire4
    expect(pdfWorkerPool.activeLeaseCountForTests).toBe(2)

    lease2.release()
    lease4.release()
  })
})

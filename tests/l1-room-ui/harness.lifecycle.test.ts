/**
 * harness.lifecycle.test.ts — deterministic proof of the #1572 lifecycle
 * generation. Issue #1572: a suite whose first test times out while the
 * harness's dynamic RoomScreen import is still in flight leaves an aborted
 * openRoom running; without invalidation its late continuation created a
 * socket and a rendered root AFTER stopHarness, leaking into later tests
 * (the K45 cascade on RoomScreen.history.test.tsx, reproduced on df045e0b4
 * at pod load ~5, /workspace/logs/wave2-kit/roomscreen-history-df045.log).
 *
 * Expected values come from issue #1572, never from current behaviour:
 *   - "a stale continuation rejects explicitly (never silently)";
 *   - "asserts zero late roots and zero live sockets afterwards".
 *
 * The import is HELD, not slowed: vi.mock resolves the RoomScreen module only
 * when the test releases the gate, so the proof is deterministic — no
 * wall-clock load is reproduced. The real stores, WsConnection, router and
 * query client stay real; only fetch (Network), the WebSocket (TestSocket)
 * and this one module import are doubled. The harness's deliberate
 * ResizeObserver absence is untouched.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useConnectionStore } from '@/store/connection'
import { Network, openRoom, resetRealStores, snapshot, stopHarness, TestSocket } from './harness'

const gate = vi.hoisted(() => {
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  return { held, release: () => { release() } }
})

// Hold exactly the import #1572 names — the harness's dynamic import of
// RoomScreen (its import.meta.glob loader resolves to this same module).
vi.mock('../../src/components/rooms/RoomScreen.tsx', () => gate.held.then(() => ({
  RoomScreen: () => null,
})))

/** One macrotask: every pending microtask continuation (import resolution, openRoom awaits) drains before it fires. */
function drain(): Promise<void> {
  return new Promise((resolve) => { setTimeout(resolve, 0) })
}

describe('harness lifecycle generation (#1572) — stopHarness invalidates pending openRoom/mountAt work', () => {
  afterEach(() => stopHarness())

  it('an openRoom whose RoomScreen import resolves after stopHarness rejects explicitly and leaves zero late roots and zero live sockets', async () => {
    resetRealStores()
    const net = new Network()
    const pending = openRoom(net, snapshot())
    // Attach the rejection observer BEFORE any await: the abandoned openRoom
    // rejects while this test is draining macrotasks, and a handler attached
    // only later would let Vitest count a momentarily-unhandled rejection as a
    // file error. If openRoom resolves instead, the observer rejects loudly.
    const settled = pending.then(
      () => { throw new Error('expected the abandoned openRoom to reject after stopHarness, but it resolved') },
      (error: unknown) => error,
    )

    await drain()
    // Instrument self-check: the gate must hold — no socket, nothing rendered.
    // If vi.mock did not intercept the harness's glob import, openRoom would
    // have proceeded already and this assertion names the broken instrument,
    // not the fix.
    expect(TestSocket.instances, 'instrument: the RoomScreen import must still be held by the gate (vi.mock must intercept the harness glob import)').toHaveLength(0)
    const bodyBeforeCleanup = [...document.body.children]

    stopHarness()   // cleanup runs while the import is still in flight → generation raised
    gate.release()  // the import now resolves into an already-torn-down harness
    await drain()
    await drain()

    // Zero late roots (#1572): nothing may appear in the document after cleanup.
    const lateRoots = [...document.body.children].filter((element) => !bodyBeforeCleanup.includes(element))
    expect(lateRoots, 'no late root may leak into later tests (#1572)').toHaveLength(0)
    // Zero live sockets (#1572).
    expect(TestSocket.instances, 'the abandoned openRoom must not create a socket (#1572)').toHaveLength(0)
    expect(useConnectionStore.getState().connection, 'the hub connection must stay unset for later tests (#1572)').toBeNull()
    // Explicit rejection, naming both generations — never a silent continuation (#1572).
    const rejection = await settled
    expect(rejection, 'the abandoned openRoom must reject explicitly (#1572)').toBeInstanceOf(Error)
    expect((rejection as Error).message, 'the rejection must name both generations (#1572)')
      .toMatch(/was abandoned — stopHarness ran while it was in flight \(ticket generation \d+, current generation \d+\)/)
  })
})

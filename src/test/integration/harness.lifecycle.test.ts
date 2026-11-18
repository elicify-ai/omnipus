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
 *   - "a lifecycle generation ticket so stopHarness invalidates pending
 *     openRoom/mountAt work";
 *   - "register each connection/root for cleanup as soon as it exists";
 *   - "a stale continuation rejects explicitly (never silently)";
 *   - "zero late roots and zero live sockets afterwards".
 *
 * Placement: src/test/integration — the `lib-store` CI vitest group covers
 * src/test/ (CHECK F1: no group matched tests/l1-room-ui/, so the coverage
 * guard failed for the original location).
 *
 * The imports are HELD, not slowed: module mocks resolve only when the case
 * releases its gate, so every proof is deterministic — no wall-clock load is
 * reproduced. The real stores, WsConnection, router and query client stay
 * real; only fetch (Network), the WebSocket (TestSocket) and the two held
 * module imports are doubled. The harness's deliberate ResizeObserver absence
 * is untouched.
 *
 * CASE ORDER IS PART OF THIS FILE'S CONTRACT: vi.mock factories run once per
 * worker, so a held-import case consumes its gate for the whole file. The
 * cases are declared in the order they consume gates — the import-abort case
 * first (screen gate), the store-abort case second (store gate via
 * vi.doMock) — and vitest executes in-file cases strictly in declaration
 * order, so the sequence is deterministic.
 */
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createElement, useEffect } from 'react'
import { screen, waitFor } from '@testing-library/react'
import { useConnectionStore } from '@/store/connection'
import {
  Network, connectSocket, mountAt, openRoom, resetRealStores, snapshot, stopHarness, TestSocket,
} from '../../../tests/l1-room-ui/harness'

// Resolve from this file's location to the exact module the harness's
// import.meta.glob loads, so vi.doMock intercepts that dynamic import.
const STORE_PATH = '../../../src/store/rooms.ts'

// Gates are vi.hoisted consts: the hoisted vi.mock factory below may only
// reference values that are themselves hoisted (the documented pattern this
// file's round-1 predecessor used).
const screenGate = vi.hoisted(() => {
  let release!: () => void
  let released = false
  let enter!: () => void
  const held = new Promise<void>((resolve) => { release = () => { released = true; resolve() } })
  const enteredPromise = new Promise<void>((resolve) => { enter = resolve })
  return {
    held,
    enter: () => enter(),
    entered: () => enteredPromise,
    release: () => release(),
    isReleased: () => released,
  }
})

const storeGate = vi.hoisted(() => {
  let release!: () => void
  let released = false
  let enter!: () => void
  const held = new Promise<void>((resolve) => { release = () => { released = true; resolve() } })
  const enteredPromise = new Promise<void>((resolve) => { enter = resolve })
  return {
    held,
    enter: () => enter(),
    entered: () => enteredPromise,
    release: () => release(),
    isReleased: () => released,
  }
})

// Hold exactly the import #1572 names — the harness's dynamic import of
// RoomScreen (its import.meta.glob loader resolves to this same module).
// The path is a literal because vi.mock (and its factory) are hoisted above
// every other statement. The factory announces itself before parking on the
// gate: that announcement is the positive interception handshake the
// import-abort case awaits.
vi.mock('../../../src/components/rooms/RoomScreen.tsx', () => {
  screenGate.enter()
  return screenGate.held.then(() => ({ RoomScreen: () => null }))
})

/** Attach the rejection observer BEFORE any await: the abandoned operation rejects while the case is doing other work, and a handler attached only later would let Vitest count a momentarily-unhandled rejection as a file error. If the operation resolves instead, the observer rejects loudly. */
function observeRejection(pending: Promise<unknown>): Promise<unknown> {
  return pending.then(
    () => { throw new Error('expected the abandoned operation to reject after stopHarness, but it resolved') },
    (error: unknown) => error,
  )
}

afterEach(() => stopHarness())

describe('harness lifecycle generation (#1572) — stopHarness invalidates pending openRoom/mountAt work', () => {
  it('holds the RoomScreen import across stopHarness: the abandoned openRoom rejects explicitly and leaves zero late roots and zero live sockets', async () => {
    resetRealStores()
    const net = new Network()
    const pending = openRoom(net, snapshot())
    const settled = observeRejection(pending)

    // Positive interception handshake (CHECK F2): the mock factory must have
    // run for the harness's glob import before anything else is claimed. If
    // the mock is deleted or stops matching, this await never resolves and
    // the case fails at its budget — the committed failure mode for a broken
    // instrument.
    await screenGate.entered()
    // Broken-instrument control: the factory was reached, but the gate must
    // still hold, and no resource may exist while the import is pending.
    expect(screenGate.isReleased(), 'instrument: the import gate must still hold the RoomScreen module').toBe(false)
    expect(TestSocket.instances, 'instrument: no socket may exist while the import is held').toHaveLength(0)
    expect(document.body.children, 'instrument: nothing may be rendered while the import is held').toHaveLength(0)

    stopHarness()   // cleanup runs while the import is still in flight → generation raised
    screenGate.release()
    // Settle the abandoned operation completely, then assert — no fixed
    // number of timer turns.
    const rejection = await settled

    // Zero late roots (#1572): nothing may appear in the document after cleanup.
    expect(document.body.children, 'no late root may leak into later tests (#1572)').toHaveLength(0)
    // Zero live sockets (#1572).
    expect(TestSocket.instances, 'the abandoned openRoom must not create a socket (#1572)').toHaveLength(0)
    expect(useConnectionStore.getState().connection, 'the hub connection must stay unset for later tests (#1572)').toBeNull()
    // Explicit rejection, naming both generations — never a silent continuation (#1572).
    expect(rejection, 'the abandoned openRoom must reject explicitly (#1572)').toBeInstanceOf(Error)
    expect((rejection as Error).message, 'the rejection must name both generations (#1572)')
      .toMatch(/was abandoned — stopHarness ran while it was in flight \(ticket generation \d+, current generation \d+\)/)
  })

  it('cancelling while the room store import is in flight rejects at the post-store-import check and leaves zero late roots and zero live sockets', async () => {
    resetRealStores()
    screenGate.release() // this phase is about the store import; the screen module may resolve instantly
    vi.doMock(STORE_PATH, () => {
      storeGate.enter()
      return storeGate.held.then(async () => await vi.importActual<Record<string, unknown>>(STORE_PATH))
    })
    try {
      const net = new Network()
      const pending = openRoom(net, snapshot())
      const settled = observeRejection(pending)

      // Phase marker: openRoom has passed the screen import and its check and
      // is now waiting on the store import — exactly the window before the
      // post-store-import generation check runs.
      await storeGate.entered()
      expect(storeGate.isReleased(), 'instrument: the store gate must still hold the store module').toBe(false)
      expect(TestSocket.instances, 'instrument: no socket may exist before the connection step').toHaveLength(0)
      expect(document.body.children, 'instrument: no root may exist before the mount step').toHaveLength(0)

      stopHarness()
      storeGate.release()
      const rejection = await settled

      // Leak evidence first: the post-store-import check must have fired
      // before connectSocket or mountAt could run (kills CHECK mutant M3,
      // which deletes that check).
      expect(TestSocket.instances, '#1572: no socket may be created after cleanup').toHaveLength(0)
      expect(document.body.children, '#1572: no late root may be created after cleanup').toHaveLength(0)
      expect(useConnectionStore.getState().connection, 'the hub connection must stay unset for later tests (#1572)').toBeNull()
      expect(rejection, 'the abandoned openRoom must reject explicitly (#1572)').toBeInstanceOf(Error)
      // The message pins the phase: it must be the post-store-import check
      // that fired, not a later one.
      expect((rejection as Error).message, 'the post-store-import generation check must fire (#1572)')
        .toMatch(/openRoom \(the room store import resolved after cleanup\) was abandoned — stopHarness ran while it was in flight \(ticket generation \d+, current generation \d+\)/)
    } finally {
      vi.doUnmock(STORE_PATH)
    }
  })

  it('cancelling after mount and connection exist retires the old transport, keeps a newer generation alive, and preserves the original failure in the rejection', async () => {
    resetRealStores()
    screenGate.release()
    const net = new Network()
    const pending = openRoom(net, snapshot())
    const settled = observeRejection(pending)

    // Phase marker: the mount has happened (a rendered root exists in the
    // document) and the connection exists — openRoom is waiting for
    // attach_room, which the dummy RoomScreen never sends.
    await waitFor(() => expect(document.body.children.length).toBeGreaterThan(0))
    expect(TestSocket.instances.length, 'instrument: the connection phase was reached').toBeGreaterThan(0)
    const oldSockets = [...TestSocket.instances]
    for (const socket of oldSockets) expect(socket.readyState, 'instrument: the old transport must be live').toBe(TestSocket.OPEN)

    stopHarness()
    // A NEWER generation creates resources that must survive the stale
    // ticket's disposal.
    const fresh = connectSocket()
    expect(fresh.socket.readyState, 'instrument: the newer generation socket must be live').toBe(TestSocket.OPEN)

    const rejection = await settled
    expect(oldSockets.every((socket) => socket.readyState === TestSocket.CLOSED),
      '#1572: the abandoned generation must not strand a registered socket (kills CHECK mutant M2)').toBe(true)
    expect(fresh.socket.readyState, '#1572: a newer generation socket must survive the stale ticket disposal').toBe(TestSocket.OPEN)
    expect(useConnectionStore.getState().connection, '#1572: the newer generation connection must stay the live one').toBe(fresh.connection)
    expect(document.body.children, '#1572: no late root may remain or appear').toHaveLength(0)
    expect(rejection, 'the abandoned openRoom must reject explicitly (#1572)').toBeInstanceOf(Error)
    expect((rejection as Error).message, 'the rejection must name both generations (#1572)')
      .toMatch(/was abandoned — stopHarness ran while it was in flight \(ticket generation \d+, current generation \d+\)/)
    expect((rejection as Error).message, '#1572: the original non-lifecycle failure must be preserved (kills CHECK mutant M4)').toContain('K45')
  })

  it('a mountAt whose generation was raised while its router was loading rejects explicitly and renders no late root', async () => {
    resetRealStores()
    const bodyBefore = [...document.body.children]
    const pending = mountAt(createElement('div', { 'data-testid': 'late-root' }, 'mounted'))
    // Synchronous ordering — no timing: the generation is raised while
    // mountAt is still awaiting its router, before the pre-render check.
    stopHarness()
    let rejection: unknown
    try {
      await pending
    } catch (error) {
      rejection = error
    }
    expect(rejection, 'mountAt must reject explicitly when abandoned (#1572)').toBeInstanceOf(Error)
    expect((rejection as Error).message, 'the pre-render mountAt check must fire (#1572)')
      .toMatch(/mountAt \(the router loaded after cleanup\) was abandoned — stopHarness ran while it was in flight \(ticket generation \d+, current generation \d+\)/)
    expect([...document.body.children].filter((element) => !bodyBefore.includes(element)),
      '#1572: no late root may be rendered after cleanup').toHaveLength(0)
  })

  it('an ordinary stop retires both registered connections and removes the root', async () => {
    resetRealStores()
    const first = connectSocket()
    const second = connectSocket()
    await mountAt(createElement('div', { 'data-testid': 'ordinary-root' }, 'mounted'))
    expect(screen.getByTestId('ordinary-root'), 'instrument: the root is mounted').toBeInTheDocument()
    expect(first.socket.readyState, 'instrument: the first transport is live').toBe(TestSocket.OPEN)
    expect(second.socket.readyState, 'instrument: the second transport is live').toBe(TestSocket.OPEN)
    stopHarness()
    expect(first.socket.readyState, '#1572: the first registration is not lost when another connection opens').toBe(TestSocket.CLOSED)
    expect(second.socket.readyState, '#1572: every registered transport is closed').toBe(TestSocket.CLOSED)
    expect(screen.queryByTestId('ordinary-root')).toBeNull()
  })

  it('a component cleanup error stays visible, propagates unchanged, and must not strand a registered OPEN socket', async () => {
    resetRealStores()
    const linked = connectSocket()
    const failure = new Error('harness lifecycle: deliberate component teardown failure')
    let effectInstalled = false
    function ThrowOnCleanup() {
      useEffect(() => {
        effectInstalled = true
        return () => { throw failure }
      }, [])
      return createElement('div', { 'data-testid': 'throwing-root' }, 'mounted')
    }
    await mountAt(createElement(ThrowOnCleanup))
    expect(effectInstalled, 'instrument: the throwing cleanup must actually be installed').toBe(true)
    expect(linked.socket.readyState, 'instrument: the transport must be live before cleanup').toBe(TestSocket.OPEN)
    let observedError: unknown
    try {
      stopHarness()
    } catch (error) {
      observedError = error
    }
    expect(observedError, '#1572: the original teardown failure must not be swallowed or replaced').toBe(failure)
    expect(linked.socket.readyState, '#1572: cleanup failure must not skip the registered socket teardown').toBe(TestSocket.CLOSED)
    expect(screen.queryByTestId('throwing-root')).toBeNull()
  })

  it('repeated stopHarness calls are safe: duplicate disposal neither closes a transport twice nor throws', () => {
    resetRealStores()
    const linked = connectSocket()
    const close = vi.spyOn(linked.socket, 'close')
    stopHarness(linked.connection)
    stopHarness(linked.connection)
    expect(linked.socket.readyState, '#1572: the transport is closed after the first stop').toBe(TestSocket.CLOSED)
    expect(close, 'idempotence: duplicate disposal must not close the transport again').toHaveBeenCalledExactlyOnceWith(1000, 'User disconnected')
  })
})

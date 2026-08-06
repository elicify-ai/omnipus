// browserLiveHandoff.test.ts — BUG 2 fix (live UAT re-run 2026-07-28): the
// same-origin BroadcastChannel signal that lets the pop-out route
// (routes/_app/browser-live.tsx) tell the docked panel
// (BrowserLivePanel.tsx) "I just closed, re-dock yourself for THIS exact
// (sessionId, agentId)". See browserLiveHandoff.ts's own module doc for why
// this exists (the two are separate `noopener` windows with no direct JS
// reference to each other).

import { describe, it, expect, vi } from 'vitest'
import { announcePopoutClosed, onPopoutClosed } from './browserLiveHandoff'

// BroadcastChannel delivery is asynchronous even within a single JS realm, so
// every assertion here has to wait for it. HOW it waits matters:
//
//   - POSITIVE assertions ("the message arrived") must wait ON THE CONDITION,
//     not on a fixed delay. An earlier revision used a single
//     `setTimeout(resolve, 0)` macrotask tick, which passes on an idle dev box
//     and FAILED on the shared CI worker (one tick is not a guarantee that
//     jsdom has drained the channel under contention). That is the fixed-sleep-
//     before-async-assert anti-pattern CLAUDE.md calls out; `vi.waitFor` polls
//     until the expectation holds and only fails after a real budget, so it is
//     both faster in the common case and robust under load.
//   - NEGATIVE assertions ("the message did NOT arrive") cannot poll — there is
//     no condition to converge on, and waitFor would just succeed instantly on
//     an assertion that is trivially true at t=0. They need a genuine settle
//     delay, and it must be generous enough that a merely-slow delivery is not
//     mistaken for a correctly-suppressed one. SETTLE_MS below is that budget.
const SETTLE_MS = 250

const settle = () => new Promise((resolve) => setTimeout(resolve, SETTLE_MS))

describe('browserLiveHandoff', () => {
  it('delivers an announced popout-closed event to a subscriber', async () => {
    const received: Array<[string, string]> = []
    const unsubscribe = onPopoutClosed((sessionId, agentId) => {
      received.push([sessionId, agentId])
    })

    announcePopoutClosed('sess-1', 'agent-1')

    await vi.waitFor(() => {
      expect(received).toEqual([['sess-1', 'agent-1']])
    })

    unsubscribe()
  })

  it('stops delivering after unsubscribe', async () => {
    const callback = vi.fn()
    const unsubscribe = onPopoutClosed(callback)
    unsubscribe()

    announcePopoutClosed('sess-2', 'agent-2')
    await settle()

    expect(callback).not.toHaveBeenCalled()
  })

  it('does not throw when BroadcastChannel is unavailable in this environment', () => {
    const original = globalThis.BroadcastChannel
    // @ts-expect-error — simulating an environment without BroadcastChannel.
    delete globalThis.BroadcastChannel

    expect(() => announcePopoutClosed('sess-3', 'agent-3')).not.toThrow()
    const unsubscribe = onPopoutClosed(() => {})
    expect(() => unsubscribe()).not.toThrow()

    globalThis.BroadcastChannel = original
  })

  it('ignores messages of an unrecognized shape rather than invoking the callback with garbage', async () => {
    const callback = vi.fn()
    const unsubscribe = onPopoutClosed(callback)

    // Post directly on the same channel name, bypassing announcePopoutClosed,
    // with a payload that isn't a valid BrowserLiveHandoffMessage.
    const rogue = new BroadcastChannel('omnipus-browser-live-handoff')
    rogue.postMessage({ type: 'something-else' })
    rogue.close()

    // Negative assertion — settle, don't poll (see SETTLE_MS's rationale).
    await settle()

    expect(callback).not.toHaveBeenCalled()
    unsubscribe()
  })
})

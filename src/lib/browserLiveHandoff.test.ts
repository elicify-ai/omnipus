// browserLiveHandoff.test.ts — BUG 2 fix (live UAT re-run 2026-07-28): the
// same-origin BroadcastChannel signal that lets the pop-out route
// (routes/_app/browser-live.tsx) tell the docked panel
// (BrowserLivePanel.tsx) "I just closed, re-dock yourself for THIS exact
// (sessionId, agentId)". See browserLiveHandoff.ts's own module doc for why
// this exists (the two are separate `noopener` windows with no direct JS
// reference to each other).

import { describe, it, expect, vi } from 'vitest'
import { announcePopoutClosed, onPopoutClosed } from './browserLiveHandoff'

describe('browserLiveHandoff', () => {
  it('delivers an announced popout-closed event to a subscriber', async () => {
    const received: Array<[string, string]> = []
    const unsubscribe = onPopoutClosed((sessionId, agentId) => {
      received.push([sessionId, agentId])
    })

    announcePopoutClosed('sess-1', 'agent-1')

    // BroadcastChannel delivery to other listeners on the same channel is
    // asynchronous even within one JS realm — wait a tick.
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(received).toEqual([['sess-1', 'agent-1']])
    unsubscribe()
  })

  it('stops delivering after unsubscribe', async () => {
    const callback = vi.fn()
    const unsubscribe = onPopoutClosed(callback)
    unsubscribe()

    announcePopoutClosed('sess-2', 'agent-2')
    await new Promise((resolve) => setTimeout(resolve, 0))

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

    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(callback).not.toHaveBeenCalled()
    unsubscribe()
  })
})

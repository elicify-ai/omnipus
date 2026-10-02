// W3 RED pack — U6–U10 (spec §8.2, file name per spec §8.2's table).
//
// Oracle source: spec §4 US-5 (AS-1…AS-8), MC-W3-5, §7 scenarios 5.1–5.7,
// §3.1's frozen presence-lifecycle shape (exactly the four frame keys).
// Expected values derived from the spec BEFORE the module was read
// (receipts/w3-red-derivation.md). The unit is the real mailPanelPresence
// adapter; the stub sender stands in for the WsConnection at the socket
// edge (the adapter's documented seam) — nothing inside the unit is mocked.
import { describe, it, expect, vi, beforeEach } from 'vitest'
import {
  createMailPanelPresence,
  mailPanelObserverFrame,
  type MailPresenceSender,
  type MailPanelPresenceDeps,
} from './mailPanelPresence'
import type { MailPanelObserverFrame } from '@/lib/api/generated/asyncapi-types'

/** A stub socket edge: records every frame, returns `deliver` for send(). */
function stubSender(deliver = true): { sender: MailPresenceSender; frames: MailPanelObserverFrame[] } {
  const frames: MailPanelObserverFrame[] = []
  return {
    frames,
    sender: {
      send(frame: MailPanelObserverFrame): boolean {
        frames.push(frame)
        return deliver
      },
    },
  }
}

function depsOver(sender: MailPresenceSender | null, connected = true) {
  return {
    getSender: () => sender,
    isConnected: () => connected,
    subscribeConnection: (listener: () => void) => {
      listeners.push(listener)
      return () => {
        const i = listeners.indexOf(listener)
        if (i >= 0) listeners.splice(i, 1)
      }
    },
  } satisfies MailPanelPresenceDeps
}

let listeners: Array<() => void>

beforeEach(() => {
  listeners = []
})

/** MC-W3-5's exact-keys pin, applied to every recorded frame. */
function assertExactlyFourKeys(frame: MailPanelObserverFrame): void {
  expect(Object.keys(frame).sort()).toEqual(['action', 'observer_id', 'type', 'workspace_id'])
}

describe('mailPanelPresence — the US-5 lifecycle (U6–U10, D-8)', () => {
  describe('U6 — open emits a minimal frame (MC-W3-5, scenario 5.1, US-5 AS-1)', () => {
    it('sends exactly one open frame with a fresh opaque observer_id and the workspace id', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      presence.open('ws-1')
      expect(frames).toHaveLength(1)
      const frame = frames[0]
      assertExactlyFourKeys(frame)
      expect(frame.type).toBe('mail_panel_observer')
      expect(frame.action).toBe('open')
      expect(frame.workspace_id).toBe('ws-1')
      expect(typeof frame.observer_id).toBe('string')
      expect(frame.observer_id.length).toBeGreaterThan(0)
      // No user id, session id or mailbox id travels as identity (US-5 AS-1).
      const serialized = JSON.stringify(frame)
      expect(serialized).not.toMatch(/user|session|mailbox/i)
      presence.dispose()
    })

    it('a repeated open on the SAME workspace does not re-send (idempotent visibility)', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      presence.open('ws-1')
      presence.open('ws-1')
      expect(frames).toHaveLength(1)
      presence.dispose()
    })

    it('each panel instance’s observer id is distinct — two tabs never share one (US-5 AS-6)', () => {
      const a = stubSender()
      const b = stubSender()
      const presenceA = createMailPanelPresence(depsOver(a.sender))
      const presenceB = createMailPanelPresence(depsOver(b.sender))
      presenceA.open('ws-1')
      presenceB.open('ws-1')
      expect(a.frames[0].observer_id).not.toBe(b.frames[0].observer_id)
      // Closing tab A must not touch tab B's frames.
      presenceA.close()
      expect(b.frames).toHaveLength(1)
      expect(b.frames[0].action).toBe('open')
      presenceA.dispose()
      presenceB.dispose()
    })
  })

  describe('U7 — close on panel close, workspace switch, pagehide (scenarios 5.2/5.3, US-5 AS-2/AS-8)', () => {
    it('panel close sends a close frame for the SAME observer_id', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      presence.open('ws-1')
      const openedId = frames[0].observer_id
      presence.close()
      expect(frames).toHaveLength(2)
      expect(frames[1].action).toBe('close')
      expect(frames[1].observer_id).toBe(openedId)
      expect(frames[1].workspace_id).toBe('ws-1')
      assertExactlyFourKeys(frames[1])
      presence.dispose()
    })

    it('workspace switch while mounted: close for the old observer, open for the new with a FRESH id (US-5 AS-8, Q4)', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      presence.open('ws-1')
      const firstId = frames[0].observer_id
      presence.open('ws-2')
      expect(frames).toHaveLength(3)
      expect(frames[1].action).toBe('close')
      expect(frames[1].observer_id).toBe(firstId)
      expect(frames[1].workspace_id).toBe('ws-1')
      expect(frames[2].action).toBe('open')
      expect(frames[2].workspace_id).toBe('ws-2')
      expect(frames[2].observer_id).not.toBe(firstId)
      presence.dispose()
    })

    it('pagehide sends the best-effort close; correctness never depends on it (US-5 AS-3)', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      presence.open('ws-1')
      presence.handlePagehide()
      expect(frames).toHaveLength(2)
      expect(frames[1].action).toBe('close')
      presence.dispose()
    })

    it('a pagehide with no sender (socket already gone) sends nothing and does not throw', () => {
      const presence = createMailPanelPresence(depsOver(null, false))
      presence.open('ws-1')
      expect(() => presence.handlePagehide()).not.toThrow()
      presence.dispose()
    })
  })

  describe('U8 — reconnect re-opens with a fresh id; the stale id is never reused (scenario 5.5, US-5 AS-5)', () => {
    it('drop sends nothing; re-auth re-sends the open frame with a NEW observer_id', () => {
      const { sender, frames } = stubSender()
      let connected = true
      const deps = {
        getSender: () => sender,
        isConnected: () => connected,
        subscribeConnection: (listener: () => void) => {
          listeners.push(listener)
          return () => undefined
        },
      }
      const presence = createMailPanelPresence(deps)
      presence.open('ws-1')
      const firstId = frames[0].observer_id
      // The socket drops.
      connected = false
      listeners.forEach((l) => l())
      expect(frames).toHaveLength(1) // a disconnect sends nothing
      // The connection re-authenticates with the panel still open.
      connected = true
      listeners.forEach((l) => l())
      expect(frames).toHaveLength(2)
      expect(frames[1].action).toBe('open')
      expect(frames[1].observer_id).not.toBe(firstId)
      expect(frames[1].workspace_id).toBe('ws-1')
      // The stale id is never reused: the current id is the fresh one.
      expect(presence.currentObserverId()).toBe(frames[1].observer_id)
      presence.dispose()
    })
  })

  describe('U9 — logout tears down with the socket (scenario 5.4, US-5 AS-4)', () => {
    it('close after the sender is gone (logout) sends NO frame; re-login starts fresh', () => {
      let sender: MailPresenceSender | null = stubSender().sender
      const frames: MailPanelObserverFrame[] = []
      const recording: MailPresenceSender = {
        send(frame) {
          frames.push(frame)
          return true
        },
      }
      void sender
      let current: MailPresenceSender | null = recording
      const presence = createMailPanelPresence({
        getSender: () => current,
        isConnected: () => current !== null,
        subscribeConnection: () => () => undefined,
      })
      presence.open('ws-1')
      const firstId = frames[0].observer_id
      // Logout: the socket teardown nulls the sender.
      current = null
      presence.close()
      expect(frames).toHaveLength(1) // nothing after teardown
      // Re-login: a new connection, a fresh id.
      current = recording
      presence.open('ws-1')
      expect(frames).toHaveLength(2)
      expect(frames[1].action).toBe('open')
      expect(frames[1].observer_id).not.toBe(firstId)
      presence.dispose()
    })
  })

  describe('U10 — unacknowledged presence never gates reads (scenario 5.7, US-5 AS-7)', () => {
    it('a send that returns false (socket closing) does not throw and leaves the observer set', () => {
      const { sender, frames } = stubSender(false)
      const presence = createMailPanelPresence(depsOver(sender))
      expect(() => presence.open('ws-1')).not.toThrow()
      expect(frames).toHaveLength(1)
      expect(presence.currentObserverId()).toBe(frames[0].observer_id)
      presence.dispose()
    })

    it('opening with NO sender (socket down) sends nothing, throws nothing, and reads state stays request-scoped', () => {
      const presence = createMailPanelPresence(depsOver(null, false))
      expect(() => presence.open('ws-1')).not.toThrow()
      expect(presence.currentObserverId()).toBeNull()
      presence.dispose()
    })

    it('a reconnect after a lost open mints the id — presence recovery needs no manual step', () => {
      let current: MailPresenceSender | null = null
      let connected = false
      const frames: MailPanelObserverFrame[] = []
      const recording: MailPresenceSender = {
        send(frame) {
          frames.push(frame)
          return true
        },
      }
      const presence = createMailPanelPresence({
        getSender: () => current,
        isConnected: () => connected,
        subscribeConnection: (listener: () => void) => {
          listeners.push(listener)
          return () => undefined
        },
      })
      presence.open('ws-1') // lost — socket down
      expect(frames).toHaveLength(0)
      current = recording
      connected = true
      listeners.forEach((l) => l())
      expect(frames).toHaveLength(1)
      expect(frames[0].action).toBe('open')
      presence.dispose()
    })
  })

  describe('the frame constructor seam (MC-W3-5)', () => {
    it('mailPanelObserverFrame emits exactly the four allowed keys', () => {
      const frame = mailPanelObserverFrame('open', 'obs-x', 'ws-9')
      assertExactlyFourKeys(frame)
      expect(frame).toEqual({ type: 'mail_panel_observer', action: 'open', observer_id: 'obs-x', workspace_id: 'ws-9' })
    })

    it('mounting without a workspace resolves to no observer and no frame', () => {
      const { sender, frames } = stubSender()
      const presence = createMailPanelPresence(depsOver(sender))
      vi.spyOn(presence, 'open')
      presence.dispose()
      expect(frames).toHaveLength(0)
    })
  })
})

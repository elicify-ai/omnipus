// Round-2 regression: a FAILED session reattach (conn.send returns false on the
// WS onConnected path) must PRESERVE the existing transcript bucket. Since the
// reattach failed, no gateway replay will rebuild the bucket — wiping it would
// leave the user a blank chat behind the "please reload" error.
//
// reattachActiveSession is the helper the onConnected path delegates to. These
// tests drive it with a fake sender to lock both branches:
//   - send() === false  → bucket preserved, isReplaying stays false (the flag
//     is armed only on a SUCCESSFUL send), error surfaced
//   - send() === true   → bucket ALSO preserved (#823 catch-up redesign,
//     BE-DESIGN.md §6.1 — the numbered cursor carries the bucket across the
//     reattach; only an explicit session_snapshot frame wipes now)

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { reattachActiveSession } from './OmnipusRuntimeProvider'
import type { ReattachSender } from './OmnipusRuntimeProvider'
import { useChatStore, getMessages } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'

const SID = 'sess-reattach'

function existingMessage(): ChatMessage {
  return {
    id: 'm1',
    role: 'user',
    content: 'previous turn the user must not lose',
    timestamp: '2026-06-10T10:00:00Z',
  }
}

// Seeds the chat store with one bucket containing a single message, with the
// session marked active in both stores so getActiveSid() resolves it.
function seedPopulatedSession(): void {
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: null, activeAgentType: null })
  // Reset to a clean store, set the active session, then append a message.
  useChatStore.setState({ sessionsById: {}, activeSessionId: SID } as never)
  useChatStore.getState().appendMessage(existingMessage())
}

function bucketMessages(sessionId: string): ChatMessage[] {
  const bucket = useChatStore.getState().sessionsById[sessionId]
  return bucket ? getMessages(bucket) : []
}

describe('reattachActiveSession — failed reattach preserves transcript', () => {
  beforeEach(() => {
    seedPopulatedSession()
  })

  it('does NOT wipe the bucket when send() fails (no replay will rebuild it)', () => {
    // Sanity: the message we are protecting is present before reattach.
    expect(bucketMessages(SID).map((m) => m.content)).toContain(
      'previous turn the user must not lose',
    )

    const sender: ReattachSender = { send: vi.fn().mockReturnValue(false) }
    const setConnectionError = vi.fn()

    const ok = reattachActiveSession(sender, setConnectionError)

    expect(ok).toBe(false)
    // The existing transcript is preserved — the user is not left a blank chat.
    expect(bucketMessages(SID).map((m) => m.content)).toContain(
      'previous turn the user must not lose',
    )
    // The error is surfaced so the chat header can show the "please reload" CTA.
    expect(setConnectionError).toHaveBeenCalledWith(
      'Failed to reattach session — please reload',
    )
  })

  // S3-12: this test used to read
  //   expect(sessionsById[SID]?.isReplaying ?? false).toBe(false)
  // which passes when the bucket is MISSING (undefined ?? false) and could
  // never fail for the reason its comment gave: there is no optimistic
  // isReplaying=true before send() to roll back — the flag is set only AFTER
  // a successful send (OmnipusRuntimeProvider.tsx, the setReplaying(true)
  // call on the success path). The assertion now pins the bucket's existence
  // explicitly, and the companion test below proves the flag is observable on
  // this exact fixture, so this one cannot pass vacuously.
  it('a failed reattach leaves isReplaying false — nothing was armed, so nothing to roll back', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(false) }
    reattachActiveSession(sender, vi.fn())

    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket, 'the bucket must survive a failed reattach').toBeTruthy()
    expect(bucket?.isReplaying).toBe(false)
  })

  it('a SUCCESSFUL reattach does arm isReplaying — the failed case above is not vacuous', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    expect(reattachActiveSession(sender, vi.fn())).toBe(true)
    const bucket = useChatStore.getState().sessionsById[SID]
    expect(bucket, 'instrument: the same fixture keeps its bucket on success too').toBeTruthy()
    expect(bucket?.isReplaying, 'the success path is what arms the flag').toBe(true)
  })

  // PROVENANCE (#823 catch-up redesign, BE-DESIGN.md §6.1, founder decision
  // Q3 — REPLACE, guarantee kept, test rewritten never weakened): this test
  // used to assert the OPPOSITE — a successful reattach wiped the bucket so
  // a full gateway replay could rebuild it from scratch. §6.1 replaces that
  // mechanism with a numbered cursor carried across the reattach
  // (`attach_session{since_seq, boot_id}`, see reattachActiveSession's own
  // updated doc comment) so the gateway can answer with an INCREMENTAL
  // catch-up instead — wiping the bucket on every reattach would defeat
  // that. History is now wiped ONLY by an explicit `session_snapshot` frame
  // (src/store/chat/slices/catchup-frames.ts). The user-visible guarantee —
  // a reattach always ends with a correct, complete transcript on screen —
  // is unchanged; only the mechanism moved.
  it('does NOT reset the bucket on a SUCCESSFUL reattach — the cursor carries it across instead', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    const setConnectionError = vi.fn()

    const ok = reattachActiveSession(sender, setConnectionError)

    expect(ok).toBe(true)
    // The existing transcript survives — the incremental catch-up (or, if
    // the cursor turns out not to be servable, an explicit session_snapshot)
    // is what reconciles it, not a client-side wipe on every attach.
    expect(bucketMessages(SID).map((m) => m.content)).toContain(
      'previous turn the user must not lose',
    )
    expect(setConnectionError).not.toHaveBeenCalled()
  })

  it('is a no-op when there is no active session', () => {
    useSessionStore.setState({ activeSessionId: null, activeAgentId: null, activeAgentType: null })
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    const setConnectionError = vi.fn()

    const ok = reattachActiveSession(sender, setConnectionError)

    expect(ok).toBe(false)
    expect(sender.send).not.toHaveBeenCalled()
    expect(setConnectionError).not.toHaveBeenCalled()
  })
})

describe('reattachActiveSession — unowned stuck "__pending" never attaches (FR-047/K9 retarget)', () => {
  beforeEach(() => {
    // No ordinary first-send owns this stale sentinel. The client kickoff
    // producer is retired, but generic never-attach/reset behavior remains.
    useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: 'ava', activeAgentType: 'core' })
    useChatStore.setState({ sessionsById: {}, pendingFirstSend: null })
    useChatStore.getState().appendMessage({
      id: 'unowned-placeholder',
      session_id: '__pending',
      role: 'assistant',
      content: '',
      timestamp: '2026-01-01T00:00:00Z',
      status: 'streaming',
      isStreaming: true,
    })
  })

  it('does NOT send attach_session for the "__pending" sentinel — sending it would be a protocol violation', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    const setConnectionError = vi.fn()

    const ok = reattachActiveSession(sender, setConnectionError)

    expect(ok).toBe(false)
    expect(sender.send).not.toHaveBeenCalled()
    expect(setConnectionError).not.toHaveBeenCalled()
  })

  it('resets an unowned stuck pending sentinel to no active session', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }

    reattachActiveSession(sender, vi.fn())

    // Retired kickoff slot/bucket/attempt cleanup is not a current oracle.
    // The composer returns to a fresh, unstuck state — no reload needed,
    // no stuck '__pending' sentinel blocking every future sendMessage call.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is idempotent after an earlier generic pending-sentinel reset', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    reattachActiveSession(sender, vi.fn())

    expect(() => reattachActiveSession(sender, vi.fn())).not.toThrow()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(sender.send).not.toHaveBeenCalled()
  })
})

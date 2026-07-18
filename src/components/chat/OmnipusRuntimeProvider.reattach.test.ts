// Round-2 regression: a FAILED session reattach (conn.send returns false on the
// WS onConnected path) must PRESERVE the existing transcript bucket. Since the
// reattach failed, no gateway replay will rebuild the bucket — wiping it would
// leave the user a blank chat behind the "please reload" error.
//
// reattachActiveSession is the helper the onConnected path delegates to. These
// tests drive it with a fake sender to lock both branches:
//   - send() === false  → bucket preserved, isReplaying cleared, error surfaced
//   - send() === true   → bucket reset (replay will repopulate it)

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

  it('clears isReplaying on a failed reattach so the composer re-enables', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(false) }
    reattachActiveSession(sender, vi.fn())
    // The optimistic isReplaying=true set before send() must be rolled back —
    // no replay is coming to flip it false.
    expect(useChatStore.getState().sessionsById[SID]?.isReplaying ?? false).toBe(false)
  })

  it('resets the bucket on a SUCCESSFUL reattach (replay repopulates it)', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }
    const setConnectionError = vi.fn()

    const ok = reattachActiveSession(sender, setConnectionError)

    expect(ok).toBe(true)
    // Success path wipes the bucket so the in-flight gateway replay rebuilds it
    // from scratch (preventing duplicate bubbles). No error surfaced.
    expect(bucketMessages(SID)).toHaveLength(0)
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

describe('reattachActiveSession — "__pending" (mid-kickoff reconnect) never attaches', () => {
  const WORKSPACE_ID = 'ws-reattach-pending'

  beforeEach(() => {
    // A workspace-setup kickoff was outstanding when the connection dropped.
    useSessionStore.setState({ activeSessionId: '__pending', activeAgentId: 'ava', activeAgentType: 'core' })
    useChatStore.setState({ sessionsById: {}, pendingKickoff: { workspaceId: WORKSPACE_ID } } as never)
    useChatStore.getState().appendMessage({
      id: 'kickoff-placeholder',
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

  it('tears down the dead kickoff (pendingKickoff cleared, bucket dropped, kickoffAttemptStatus failed) and resets activeSessionId to null', () => {
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }

    reattachActiveSession(sender, vi.fn())

    expect(useChatStore.getState().pendingKickoff).toBeNull()
    expect(useChatStore.getState().sessionsById['__pending']).toBeUndefined()
    expect(useChatStore.getState().kickoffAttemptStatus[WORKSPACE_ID]).toBe('failed')
    // The composer returns to a fresh, unstuck state — no reload needed,
    // no stuck '__pending' sentinel blocking every future sendMessage call.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
  })

  it('is idempotent when pendingKickoff was already cleared (e.g. by an earlier disconnect-time clearStreamingState call)', () => {
    useChatStore.setState({ pendingKickoff: null })
    const sender: ReattachSender = { send: vi.fn().mockReturnValue(true) }

    expect(() => reattachActiveSession(sender, vi.fn())).not.toThrow()
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    expect(sender.send).not.toHaveBeenCalled()
  })
})

// clear-transcript-refetch.test.ts — FR-030/031 (U10b), R3: after the /clear
// turn completes, the SPA re-reads the session's transcript/window state from
// the server and applies the fresh projection to the view. It must never fake
// a cleared view locally.
//
// Oracles: docs/internal/specs/session-core-spec.md FR-030 ("clears the chat
// UI and model context with a marker by moving/advancing the ... display
// window start", "preserves the transcript") + core's pkg/agent/
// clear_session.go (git 67345b1d7): a successful /clear appends ONE chat-view
// marker entry (type system, role system, content "Conversation context
// cleared") to the archive — an entry the live WS stream does not push, so
// the only way the view can reflect the server is to re-read the transcript.
//
// What is real: the chat store's real sendMessage → real outgoing MessageFrame
// → real frame reducer ('done'). What is mocked: the network/cache edge only
// — the WS connection (a send spy) and the React Query client's transcript
// query (invalidate + a canned fresh projection standing in for the refetch).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { queryClient } from '@/lib/queryClient'
import type { ServerFrame } from '@/lib/ws'
import type { Message } from '@/lib/api'

const SID = 'sess-clear-refetch'
const MARKER_TEXT = 'Conversation context cleared'

// The fresh server projection a post-clear re-read returns: the /clear
// command reply and the ONE chat-view marker entry.
const FRESH_PROJECTION: Message[] = [
  { id: 'u-clear', role: 'user', content: '/clear', timestamp: '2026-10-10T00:00:00Z', agentId: 'jim', status: 'done' },
  { id: 'a-reply', role: 'assistant', content: 'Context cleared. The conversation and its history are kept; the assistant continues from here with a fresh context.', timestamp: '2026-10-10T00:00:01Z', agentId: 'jim', status: 'done' },
  { id: 'clear-marker-1', role: 'system', content: MARKER_TEXT, timestamp: '2026-10-10T00:00:02Z', agentId: 'jim', status: 'done' },
]

let sentFrames: unknown[] = []

function doneFrame(seq: number): ServerFrame {
  return {
    type: 'done', session_id: SID, message_id: 'a-reply', turn_id: 'turn-clear', seq, stats: { tokens: 1, cost: 0 },
  } as ServerFrame
}

beforeEach(() => {
  sentFrames = []
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null })
  useChatStore.setState({ sessionsById: {}, messages: [], messagesById: {} } as never)
  useConnectionStore.setState({
    connection: { send: (p: unknown) => { sentFrames.push(p); return true }, close: () => {} } as never,
    isConnected: true,
  } as never)
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('the /clear transcript re-read (FR-030/031, R3)', () => {
  it('a sent /clear arms the re-read; the turn done re-reads the transcript query and applies the fresh server projection', async () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined as never)
    const getQueryDataSpy = vi.spyOn(queryClient, 'getQueryData').mockReturnValue(FRESH_PROJECTION as never)

    // The real send path — the outgoing frame must be the ordinary message
    // frame carrying the command text (DeliveryAgent: the server executes).
    useChatStore.getState().sendMessage('/clear')
    expect(sentFrames).toHaveLength(1)
    expect(sentFrames[0]).toMatchObject({ type: 'message', content: '/clear', session_id: SID })

    // The reply turn completes → the armed re-read fires.
    useChatStore.getState().handleFrame(doneFrame(1))

    await vi.waitFor(() => {
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ['messages', SID] })
    })

    // The view now reflects the server: the bucket carries the fresh
    // projection, marker row included — not a locally faked cleared view.
    await vi.waitFor(() => {
      const b = useChatStore.getState().sessionsById[SID]!
      expect(b.messageOrder).toEqual(['u-clear', 'a-reply', 'clear-marker-1'])
      expect(b.messagesById['clear-marker-1']?.content).toBe(MARKER_TEXT)
      expect(b.messagesById['clear-marker-1']?.role).toBe('system')
    })
    expect(getQueryDataSpy).toHaveBeenCalledWith(['messages', SID])
  })

  it('an ordinary message never arms the re-read', async () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined as never)

    useChatStore.getState().sendMessage('hello there')
    expect(sentFrames).toHaveLength(1)
    useChatStore.getState().handleFrame(doneFrame(1))
    // Let any misplaced async work surface.
    await new Promise((r) => setTimeout(r, 10))

    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['messages', SID] })
  })

  it('consumes the arm exactly once — a second done for the same session does not re-read again', async () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined as never)
    vi.spyOn(queryClient, 'getQueryData').mockReturnValue(FRESH_PROJECTION as never)
    // Count only THIS query's invalidations — other frames legitimately
    // invalidate unrelated queries (['sessions'], ['agents'], …).
    const transcriptInvalidations = () =>
      invalidateSpy.mock.calls.filter(
        (args) => (args[0] as { queryKey?: unknown[] } | undefined)?.queryKey?.[0] === 'messages' &&
          (args[0] as { queryKey?: unknown[] }).queryKey?.[1] === SID,
      ).length

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame(1))
    await vi.waitFor(() => {
      expect(transcriptInvalidations()).toBe(1)
    })

    useChatStore.getState().handleFrame(doneFrame(2))
    await new Promise((r) => setTimeout(r, 10))

    expect(transcriptInvalidations()).toBe(1)
  })

  it('a failed /clear send (connection dropped) does not arm the re-read', async () => {
    const invalidateSpy = vi.spyOn(queryClient, 'invalidateQueries').mockResolvedValue(undefined as never)
    useConnectionStore.setState({
      connection: { send: () => false, close: () => {} } as never,
      isConnected: true,
    } as never)

    useChatStore.getState().sendMessage('/clear')
    useChatStore.getState().handleFrame(doneFrame(1))
    await new Promise((r) => setTimeout(r, 10))

    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ['messages', SID] })
  })
})

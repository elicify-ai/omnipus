/**
 * Founder-blocking defect (2026-10-11): a persisted transcript holding two rows
 * with the same message id (a re-queued subagent_state row) made assistant-ui's
 * MessageRepository throw "A message with the same id already exists", and the
 * router showed "Something went wrong loading this page".
 *
 * Drives the real path: chat store setMessages -> foreground `messages`
 * projection -> the real useOmnipusRuntime / useExternalStoreRuntime.
 * Contract: no throw, one row per id, the LAST occurrence wins at its position.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { renderHook } from '@testing-library/react'

vi.mock('@/lib/api', () => ({ uploadFiles: vi.fn(), createSession: vi.fn() }))

import { useSessionStore } from '@/store/session'
import { useChatStore } from '@/store/chat'
import type { ChatMessage } from '@/store/chat'
import { useOmnipusRuntime } from './omnipus-runtime'

const DUP = 'chatcmpl-tool-85124f61de9e3200:1:state:queued'

function msg(id: string, content: string): ChatMessage {
  return { id, role: 'system', content, timestamp: '2026-10-11T08:30:46Z', status: 'done' } as ChatMessage
}

describe('duplicate message ids reach the runtime', () => {
  beforeEach(() => {
    useChatStore.setState({ sessionsById: {}, messages: [] })
    useSessionStore.setState({ activeSessionId: 'sess-dup', activeAgentId: 'jim', activeAgentType: null })
  })

  it('does not throw and keeps one row per id, the last occurrence at its position', () => {
    useChatStore.getState().setMessages([
      msg('a', 'first'),
      msg(DUP, 'queued (first run)'),
      msg('b', 'between'),
      msg(DUP, 'queued (re-queue)'),
      msg('c', 'last'),
    ])

    const { result } = renderHook(() => useOmnipusRuntime())
    const rows = result.current.thread.getState().messages

    expect(rows.map((r) => r.id)).toEqual(['a', 'b', DUP, 'c'])
    const kept = rows.find((r) => r.id === DUP)!
    expect(JSON.stringify(kept.content)).toContain('re-queue')
    expect(JSON.stringify(kept.content)).not.toContain('first run')
  })
})

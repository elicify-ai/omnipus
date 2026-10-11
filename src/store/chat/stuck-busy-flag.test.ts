// Founder bug (2026-10-11, ui-feedback point 2): the transcript shows no
// activity, yet the composer shows Stop next to Send. Stop is driven by the
// store's `isStreaming` flag (ChatScreen: isStreaming || goalRunning || ...);
// the activity cue is driven by a message that is actually streaming or a tool
// that is actually running. `tool_call_start` sets the flag but carries no
// turn_id on the wire, so a straggler that lands after a turn ended (after Stop,
// after done, or after a reconnect) re-arms the flag with no turn left to end it.
//
// Invariant pinned here: the busy flag must be BACKED - by a streaming message,
// a running tool, an announced active turn, or a replay in progress.

import { beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import { useChatStore } from './store'
import { useSessionStore } from '@/store/session'
import type { ServerFrame } from '@/lib/ws'

const SID = 'stuck-busy-sid'
const frame = (o: Record<string, unknown>) => ({ session_id: SID, ...o }) as unknown as ServerFrame

function feed(...frames: Array<Record<string, unknown>>): void {
  act(() => { for (const fr of frames) useChatStore.getState().handleFrame(frame(fr)) })
}
const bucket = () => useChatStore.getState()
const anyStreamingMessage = () => bucket().messages.some((m) => m.isStreaming || m.status === 'streaming')
const anyRunningTool = () => Object.values(bucket().toolCalls).some((t) => t.status === 'running')

const TOKEN = { type: 'token', content: 'working', turn_id: 't1', message_id: 'm1' }
const DONE = { type: 'done', turn_id: 't1', message_id: 'm1', stats: {} }
const CANCELED = { type: 'error', code: 'turn_canceled', message: 'stopped' }
const TOOL_START = { type: 'tool_call_start', call_id: 'late-1', tool: 'bash', params: {} }
const TOOL_RESULT = { type: 'tool_call_result', call_id: 'late-1', tool: 'bash', result: 'ok', status: 'success' }

beforeEach(() => {
  act(() => {
    useChatStore.getState().clearStreamingState()
    useChatStore.setState({ sessionsById: {}, messages: [], isStreaming: false } as never)
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'ava', activeAgentType: null })
  })
})

describe('busy flag with nothing behind it', () => {
  it('a straggler tool call that lands after Stop does not leave the composer busy', () => {
    feed(TOKEN, CANCELED, TOOL_START, TOOL_RESULT)
    expect(anyStreamingMessage()).toBe(false)
    expect(anyRunningTool()).toBe(false)
    expect(bucket().isStreaming, 'Stop must not show with no activity').toBe(false)
  })

  it('a straggler tool call that lands after done does not leave the composer busy', () => {
    feed(TOKEN, DONE, TOOL_START, TOOL_RESULT)
    expect(bucket().isStreaming).toBe(false)
  })

  it('a bare snapshot saying no turn is in flight releases an unbacked busy flag', () => {
    feed(TOKEN, DONE, TOOL_START, TOOL_RESULT)
    act(() => { useChatStore.setState({ isStreaming: true, sessionsById: { ...bucket().sessionsById, [SID]: { ...bucket().sessionsById[SID], isStreaming: true } } } as never) })
    feed({ type: 'session_state', user_id: 'u', pending_approvals: [], emitted_at: '2026-10-11T00:00:00Z' })
    expect(bucket().isStreaming).toBe(false)
  })
})

describe('busy flag with something behind it stays up (negative controls)', () => {
  it('stays busy while the straggler tool is still running', () => {
    feed(TOKEN, DONE, TOOL_START)
    expect(anyRunningTool()).toBe(true)
    expect(bucket().isStreaming).toBe(true)
  })

  it('stays busy while a message is streaming', () => {
    feed(TOKEN, TOOL_START, TOOL_RESULT)
    expect(anyStreamingMessage()).toBe(true)
    expect(bucket().isStreaming).toBe(true)
  })

  it('a snapshot with no active turn does not clear the flag while a reply bubble is streaming', () => {
    feed(TOKEN)
    feed({ type: 'session_state', user_id: 'u', pending_approvals: [], emitted_at: '2026-10-11T00:00:00Z' })
    expect(bucket().isStreaming).toBe(true)
  })

  it('a snapshot with no active turn does not clear the flag while a tool is still running', () => {
    feed(TOKEN, DONE, TOOL_START)
    feed({ type: 'session_state', user_id: 'u', pending_approvals: [], emitted_at: '2026-10-11T00:00:00Z' })
    expect(bucket().isStreaming).toBe(true)
  })

  it('a snapshot that announces an active turn keeps it busy', () => {
    feed(TOKEN, DONE, TOOL_START, TOOL_RESULT)
    feed({ type: 'session_state', user_id: 'u', pending_approvals: [], emitted_at: '2026-10-11T00:00:00Z', active_turn: { turn_id: 't9', agent_id: 'ava' } })
    expect(bucket().isStreaming).toBe(true)
  })
})

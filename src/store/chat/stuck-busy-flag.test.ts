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
import { useConnectionStore } from '@/store/connection'
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

// ── A send that never starts a turn ──────────────────────────────────────────
// sendMessage puts the user row and an empty "thinking" placeholder in the view
// and raises the busy flag. If the server answers message_status discarded or
// failed, no turn will follow: that placeholder must go and the flag must drop -
// unless another turn is genuinely running (a discarded mid-turn steer must
// never stop or hide it).

describe('a send that is discarded or fails', () => {
  let sent: Array<Record<string, unknown>> = []
  const cid = (n = 0) => String(sent[n]?.client_message_id)
  const placeholders = () => bucket().messages.filter((m) => m.role === 'assistant' && !m.content && (m.isStreaming || m.status === 'streaming'))
  beforeEach(() => {
    sent = []
    useConnectionStore.setState({
      connection: { send: (p: unknown) => { sent.push(p as Record<string, unknown>); return true }, close: () => {} } as never,
      isConnected: true,
    } as never)
  })

  it.each(['discarded', 'failed'] as const)('while idle: %s removes its own placeholder and releases the busy flag', (state) => {
    act(() => { useChatStore.getState().sendMessage('hello') })
    expect(bucket().isStreaming).toBe(true)
    expect(placeholders()).toHaveLength(1)

    feed({ type: 'message_status', client_message_id: cid(), state })
    expect(placeholders(), 'no thinking placeholder for a turn that will never start').toHaveLength(0)
    expect(bucket().isStreaming, 'Stop must not show').toBe(false)
    const user = bucket().messages.find((m) => m.role === 'user')!
    expect(user.deliveryStatus).toBe(state)
  })

  it('while a turn runs: a discarded mid-turn steer neither stops nor hides the running turn', () => {
    feed(TOKEN)
    act(() => { useChatStore.getState().sendMessage('also do this') })
    expect(sent).toHaveLength(1)
    const before = bucket().messages.map((m) => m.id)
    feed({ type: 'message_status', client_message_id: cid(), state: 'discarded' })

    expect(bucket().isStreaming, 'the turn is still running').toBe(true)
    expect(bucket().messages.map((m) => m.id), 'nothing the running turn owns is removed').toEqual(before)
    expect(bucket().messages.find((m) => m.content === 'working')).toBeTruthy()
    // and the turn can still continue and finish normally
    feed({ type: 'token', content: ' more', turn_id: 't1', message_id: 'm1b' }, DONE)
    expect(bucket().isStreaming).toBe(false)
  })

  it('a steer that closed the running bubble keeps the flag up when a tool result lands', () => {
    feed(TOKEN, TOOL_START)
    act(() => { useChatStore.getState().sendMessage('change course') })
    feed(TOOL_RESULT)
    expect(bucket().isStreaming, 'the steered turn is still in flight').toBe(true)
  })

  it('does not remove a reply that has already started streaming', () => {
    act(() => { useChatStore.getState().sendMessage('hello') })
    feed({ type: 'token', content: 'real reply' })
    feed({ type: 'message_status', client_message_id: cid(), state: 'failed' })
    expect(bucket().messages.find((m) => m.content === 'real reply')).toBeTruthy()
    expect(bucket().isStreaming).toBe(true)
  })
})

// ── Delta review R3 ──────────────────────────────────────────────────────────

describe('C3: a straggler tool call after a discarded send', () => {
  beforeEach(() => {
    useConnectionStore.setState({
      connection: { send: () => true, close: () => {} } as never,
      isConnected: true,
    } as never)
  })

  it.each(['discarded', 'failed'] as const)('send -> %s -> straggler tool start/result leaves no stuck Stop', (state) => {
    act(() => { useChatStore.getState().sendMessage('hello') })
    const user = bucket().messages.find((m) => m.role === 'user')!
    feed({ type: 'message_status', client_message_id: user.id, state })
    expect(bucket().isStreaming).toBe(false)

    feed(TOOL_START)
    expect(anyRunningTool()).toBe(true)
    feed(TOOL_RESULT)
    expect(anyStreamingMessage(), 'the straggler must not mint a streaming bubble that backs the flag').toBe(false)
    expect(bucket().isStreaming, 'Stop must not stay up with no activity').toBe(false)
  })

  it('negative control: a NEW send after the discard still gets a normal streaming turn', () => {
    act(() => { useChatStore.getState().sendMessage('first') })
    const first = bucket().messages.find((m) => m.role === 'user')!
    feed({ type: 'message_status', client_message_id: first.id, state: 'discarded' })
    act(() => { useChatStore.getState().sendMessage('second') })
    feed(TOOL_START, TOOL_RESULT)
    expect(bucket().isStreaming, 'the second send is a live turn; its flag stays up').toBe(true)
  })
})

describe('C11: no Stop blink between a mid-turn tool result and the next token', () => {
  it('a live turn whose last bubble is already finalized keeps the flag up after a tool result', () => {
    // Turn is running (flag armed by the user's send), the last assistant bubble
    // was finalized at an agent boundary, no turn id announced.
    feed(TOKEN)
    act(() => {
      const sid = SID
      const b = useChatStore.getState().sessionsById[sid]
      const id = b.messageOrder[b.messageOrder.length - 1]
      useChatStore.setState({
        sessionsById: { ...useChatStore.getState().sessionsById, [sid]: { ...b, isStreaming: true, messagesById: { ...b.messagesById, [id]: { ...b.messagesById[id], isStreaming: false, status: 'done' } } } },
      } as never)
    })
    feed(TOOL_START, TOOL_RESULT)
    expect(bucket().isStreaming, 'the turn is still in flight; Stop must not blink off').toBe(true)
  })
})

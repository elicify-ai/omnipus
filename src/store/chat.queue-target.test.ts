// #1428 step 1 — a queued message is only ever sent to the chat it was written in.
//
// Oracle: the issue's "Expected" line ("A typed or queued message is only ever
// sent to the conversation it was written in") and the chief's decision for
// Q-a: a queued message for a chat that is not open waits until that chat is
// opened. No expected value was read from the implementation. Real: chat,
// session and connection stores, sendMessage, drain. Replaced edge: the socket
// sender only. Boundary: the server-side effect is not observable here.
import { act } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import type { QueuedOutboundMessage } from '@/store/chat'

function resetStores() {
  act(() => {
    useSessionStore.setState({ ...useSessionStore.getInitialState(), activeSessionId: null, activeAgentId: 'mia', sessionByWorkspace: {} }, true)
    useChatStore.setState({ ...useChatStore.getInitialState(), sessionsById: {}, abandonedFirstSendIds: [], pendingDrainQueue: [], outboundQueue: [] }, true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
  })
}
beforeEach(resetStores)
afterEach(resetStores)

const flush = () => act(async () => { await Promise.resolve() })

function openChat(id: string) {
  act(() => { useSessionStore.getState().setActiveSession(id, 'mia') })
}
function connect() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
  })
  return sender
}
const sentFrames = (sender: { send: ReturnType<typeof vi.fn> }) =>
  sender.send.mock.calls.map(([frame]) => frame as { type: string; content?: string; session_id?: string })
    .filter((frame) => frame.type === 'message')
const waiting = () => [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue]
  .map((item) => typeof item === 'string' ? item : item.content)

/** Typed in chat A while the connection is down, so it is queued. */
function queueInChatA() {
  openChat('chat-a')
  act(() => { useChatStore.getState().sendMessage('written in chat A') })
  expect(waiting(), 'fixture: the message is queued, not sent').toStrictEqual(['written in chat A'])
}

describe('#1428 step 1: queued messages keep their chat', () => {
  it('records the chat a message was typed in when it is queued', () => {
    queueInChatA()
    const item = useChatStore.getState().outboundQueue[0] as QueuedOutboundMessage
    expect(item.targetSessionId).toBe('chat-a')
  })

  it('does not drain a message into another chat after navigation, and keeps it queued', async () => {
    queueInChatA()
    openChat('chat-b')
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender), 'nothing written in A goes to B').toStrictEqual([])
    expect(waiting(), 'the message stays queued for A').toStrictEqual(['written in chat A'])
  })

  it('sends it to chat A once chat A is opened again', async () => {
    queueInChatA()
    openChat('chat-b')
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    openChat('chat-a')
    await flush()
    const frames = sentFrames(sender)
    expect(frames.map((frame) => ({ content: frame.content, sid: frame.session_id })))
      .toStrictEqual([{ content: 'written in chat A', sid: 'chat-a' }])
    expect(waiting()).toStrictEqual([])
  })

  it('a message waiting for chat A does not block a message written in chat B', async () => {
    queueInChatA()
    openChat('chat-b')
    act(() => { useChatStore.getState().sendMessage('written in chat B') })
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender).map((frame) => ({ content: frame.content, sid: frame.session_id })))
      .toStrictEqual([{ content: 'written in chat B', sid: 'chat-b' }])
    expect(waiting(), 'A\'s message is still waiting').toStrictEqual(['written in chat A'])
  })

  it('a first message of a new chat (no chat id yet) is not held back', async () => {
    act(() => { useSessionStore.setState({ activeSessionId: null }) })
    act(() => { useChatStore.getState().sendMessage('very first message') })
    const item = useChatStore.getState().outboundQueue[0] as QueuedOutboundMessage
    expect(item.targetSessionId, 'no chat id exists yet, so none is recorded').toBeUndefined()
  })
})

describe('#1428 step 1: which queued items a chat shows and may send', () => {
  const item = (targetSessionId?: string): QueuedOutboundMessage => ({ id: 'q', content: 'c', timestamp: 't', ...(targetSessionId ? { targetSessionId } : {}) })
  it('shows and sends only items of the open chat, plus items with no chat yet and plain legacy strings', async () => {
    const { queuedItemBelongsHere, firstDrainableIndex } = await import('./chat/queue-target')
    expect(queuedItemBelongsHere(item('chat-a'), 'chat-a')).toBe(true)
    expect(queuedItemBelongsHere(item('chat-a'), 'chat-b')).toBe(false)
    expect(queuedItemBelongsHere(item('chat-a'), null)).toBe(false)
    expect(queuedItemBelongsHere(item(), 'chat-b')).toBe(true)
    expect(queuedItemBelongsHere('legacy text', 'chat-b')).toBe(true)
    expect(firstDrainableIndex([item('chat-a'), item('chat-b'), item('chat-b')], 'chat-b')).toBe(1)
    expect(firstDrainableIndex([item('chat-a')], 'chat-b')).toBe(-1)
  })
})

describe('#1428 step 1 (CHECK round 1): follow-ups, full queue, order, shown rows', () => {
  const at = (n: number) => `2026-11-14T10:00:0${n}.000Z`
  const qi = (id: string, n: number, targetSessionId?: string): QueuedOutboundMessage =>
    ({ id, content: id, timestamp: at(n), ...(targetSessionId ? { targetSessionId } : {}) })
  const idOf = (entry: unknown) => typeof entry === 'string' ? entry : (entry as QueuedOutboundMessage).id

  it('F1: a follow-up typed under a pending first message stays with that chat after the ack', async () => {
    const sender = connect()
    act(() => { useChatStore.getState().sendMessage('first message') })
    const first = sentFrames(sender)[0] as { client_message_id?: string }
    act(() => { useChatStore.getState().sendMessage('follow-up while pending') })
    expect(sentFrames(sender), 'fixture: follow-up is held, only the first went out').toHaveLength(1)
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'chat-a', agent_id: 'mia', client_message_id: first.client_message_id,
      } as never)
    })
    const all = [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue]
    const item = all.find((entry) => typeof entry !== 'string' && entry.content === 'follow-up while pending') as QueuedOutboundMessage
    expect(item, 'fixture: follow-up still waiting').toBeDefined()
    expect(item.targetSessionId, 'bound to the new chat').toBe('chat-a')
    openChat('chat-b')
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender).filter((frame) => frame.content === 'follow-up while pending')).toStrictEqual([])
  })

  it('F2: an accepted message bouncing back is kept at the cap; a brand-new one is still refused', () => {
    act(() => {
      for (let i = 0; i < 5; i += 1) useChatStore.getState().enqueueOutboundMessage(`n${i}`, qi(`n${i}`, i, 'chat-a'))
    })
    expect(useChatStore.getState().outboundQueue).toHaveLength(5)
    let kept = false
    let refused = true
    act(() => {
      kept = useChatStore.getState().enqueueOutboundMessage('held', qi('held', 0, 'chat-a'), true)
      refused = useChatStore.getState().enqueueOutboundMessage('brand new', qi('b', 9, 'chat-a'))
    })
    expect(kept).toBe(true)
    expect(useChatStore.getState().outboundQueue.map(idOf)).toContain('held')
    expect(refused).toBe(false)
  })

  it('F2 (offline drain): the held message survives a failed drain into a full queue', async () => {
    queueInChatA()
    act(() => {
      for (let i = 0; i < 4; i += 1) useChatStore.getState().sendMessage(`fill ${i}`)
    })
    expect(useChatStore.getState().outboundQueue).toHaveLength(5)
    // Move the batch to the held list, fill the queue again, then try to drain while offline.
    act(() => {
      useChatStore.setState({ pendingDrainQueue: useChatStore.getState().outboundQueue, outboundQueue: [] })
      for (let i = 0; i < 5; i += 1) useChatStore.getState().enqueueOutboundMessage(`new ${i}`, qi(`new${i}`, 5, 'chat-a'))
    })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(waiting()).toContain('written in chat A')
  })

  it('R1: a foreign acknowledgement does not take over A\'s follow-up', async () => {
    const sender = connect()
    act(() => { useChatStore.getState().sendMessage('first message') })
    const first = sentFrames(sender)[0] as { client_message_id?: string }
    act(() => { useChatStore.getState().sendMessage('A follow-up') })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'foreign', agent_id: 'mia', client_message_id: 'not-ours',
      } as never)
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'chat-a', agent_id: 'mia', client_message_id: first.client_message_id,
      } as never)
    })
    const all = [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue]
    const item = all.find((entry) => typeof entry !== 'string' && entry.content === 'A follow-up') as QueuedOutboundMessage | undefined
    // A foreign ack orphans the pending first send, so the follow-up stays owned and unbound:
    // it is held (stuck, #1833), never bound to or sent into the foreign chat.
    expect(item, 'still held').toBeDefined()
    expect(item?.targetSessionId, 'not taken over by the foreign chat').toBeUndefined()
    openChat('foreign')
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender).filter((frame) => frame.content === 'A follow-up' && frame.session_id === 'foreign'),
      'A\'s follow-up is never sent to the foreign chat').toStrictEqual([])
  })

  it('R3-F1: after a foreign acknowledgement, opening another chat does not send A\'s follow-up there; they stay held (stuck, #1833)', async () => {
    const sender = connect()
    act(() => { useChatStore.getState().sendMessage('first message') })
    act(() => {
      useChatStore.getState().sendMessage('A follow 1')
      useChatStore.getState().sendMessage('A follow 2')
    })
    act(() => {
      useChatStore.getState().handleFrame({
        type: 'session_started', session_id: 'foreign', agent_id: 'mia', client_message_id: 'not-ours',
      } as never)
    })
    openChat('chat-b')
    await flush()
    expect(sentFrames(sender).filter((frame) => frame.session_id === 'chat-b'), 'nothing of A goes to B').toStrictEqual([])
    const held = [...useChatStore.getState().pendingDrainQueue, ...useChatStore.getState().outboundQueue] as QueuedOutboundMessage[]
    expect(held.map((entry) => entry.content).sort(), 'both follow-ups are still held').toStrictEqual(['A follow 1', 'A follow 2'])
    expect(held.map((entry) => entry.targetSessionId), 'bound to no chat').toStrictEqual([undefined, undefined])
    openChat('chat-a')
    await flush()
    expect(sentFrames(sender).filter((frame) => frame.content?.startsWith('A follow')), 'sent nowhere').toStrictEqual([])
  })

  it('F2 (legacy object): an accepted object without a number keeps its place when it bounces', async () => {
    openChat('chat-a')
    const legacy = { id: 'a1', content: 'legacy A1', timestamp: '2026-11-14T10:00:00.000Z', targetSessionId: 'chat-a' } as QueuedOutboundMessage
    const modern = { id: 'a2', content: 'modern A2', timestamp: '2026-11-14T10:00:00.001Z', targetSessionId: 'chat-a', seq: 5 } as QueuedOutboundMessage
    useChatStore.setState({ outboundQueue: [legacy], pendingDrainQueue: [modern] })
    // Offline: the drain re-sends the legacy object, which bounces back into the queue.
    act(() => { useChatStore.getState().drainOutboundQueue() })
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender)[0]?.content, 'the older legacy object is still sent first').toBe('legacy A1')
  })

  it('T1: a bounce keeps the acceptance order of two same-time messages', async () => {
    vi.useFakeTimers()
    vi.setSystemTime('2026-11-14T10:00:00.000Z')
    try {
      queueInChatA()
      act(() => { useChatStore.getState().sendMessage('A2 later') })
      // Offline drain moves both to the held list and bounces A1 straight back.
      act(() => { useChatStore.getState().drainOutboundQueue() })
      const sender = connect()
      act(() => { useChatStore.getState().drainOutboundQueue() })
      await act(async () => { await Promise.resolve() })
      expect(sentFrames(sender)[0]?.content, 'A1 is sent before A2').toBe('written in chat A')
    } finally {
      vi.useRealTimers()
    }
  })

  it('M4b: items nobody owns are bound to the chat of a legacy acknowledgement', async () => {
    act(() => { useSessionStore.setState({ activeSessionId: null }) })
    act(() => {
      useChatStore.getState().sendMessage('one')
      useChatStore.getState().sendMessage('two')
    })
    const sender = connect()
    act(() => {
      useChatStore.getState().handleFrame({ type: 'session_started', session_id: 'chat-a', agent_id: 'mia' } as never)
    })
    openChat('chat-b')
    act(() => { useChatStore.getState().drainOutboundQueue() })
    await flush()
    expect(sentFrames(sender).filter((frame) => frame.session_id === 'chat-b'), 'nothing is sent to B').toStrictEqual([])
  })

  it('M5b: a real offline cleanup into a full queue keeps the accepted held message', async () => {
    queueInChatA()
    const heldItem = useChatStore.getState().outboundQueue[0] as QueuedOutboundMessage
    act(() => {
      useChatStore.setState({
        pendingDrainQueue: [heldItem],
        outboundQueue: [1, 2, 3, 4, 5].map((n) => qi(`full${n}`, n, 'chat-a')),
      })
    })
    act(() => { useChatStore.getState().clearStreamingState() })
    await flush()
    expect(waiting(), 'the accepted message survives').toContain('written in chat A')
  })

  it('F3 (equal and backward times): acceptance order decides, not the clock', async () => {
    const { mergeDrainBatch } = await import('./chat/queue-target')
    const s = (id: string, seq: number, timestamp: string): QueuedOutboundMessage => ({ id, content: id, timestamp, targetSessionId: 'chat-a', seq })
    expect(mergeDrainBatch([s('a2', 2, at(1))], [s('a1', 1, at(1))]).map(idOf), 'equal times').toStrictEqual(['a1', 'a2'])
    expect(mergeDrainBatch([s('a2', 2, at(0))], [s('a1', 1, at(5))]).map(idOf), 'clock went backwards').toStrictEqual(['a1', 'a2'])
    expect(mergeDrainBatch([s('a1', 1, at(1))], [s('a2', 2, at(1))]).map(idOf), 'earlier bounce stays first').toStrictEqual(['a1', 'a2'])
  })

  it('F3 (store): a fresh A2 typed after a held A1 is sent after it, with equal times', async () => {
    vi.useFakeTimers()
    vi.setSystemTime('2026-11-14T10:00:00.000Z')
    try {
      queueInChatA()
      openChat('chat-b')
      const sender = connect()
      act(() => { useChatStore.getState().drainOutboundQueue() })
      act(() => { useConnectionStore.getState().setConnected(false) })
      openChat('chat-a')
      act(() => { useChatStore.getState().sendMessage('A2 fresh') })
      act(() => { useConnectionStore.getState().setConnected(true) })
      act(() => { useChatStore.getState().drainOutboundQueue() })
      await act(async () => { await Promise.resolve() })
      expect(sentFrames(sender)[0]?.content).toBe('written in chat A')
    } finally {
      vi.useRealTimers()
    }
  })

  it('F3: a newer message never overtakes an older held one of the same chat', async () => {
    const { mergeDrainBatch } = await import('./chat/queue-target')
    expect(mergeDrainBatch([qi('new', 3, 'chat-a')], [qi('old', 1, 'chat-a'), qi('mid', 2, 'chat-a')]).map(idOf))
      .toStrictEqual(['old', 'mid', 'new'])
    expect(mergeDrainBatch([qi('b', 2, 'chat-a')], [qi('c', 2, 'chat-a')]).map(idOf), 'equal times: bounced first')
      .toStrictEqual(['b', 'c'])
    expect(mergeDrainBatch([qi('x', 5)], ['legacy text', qi('y', 1)]).map(idOf))
      .toStrictEqual(['x', 'legacy text', 'y'])
  })

  it('F3 (store): the older held message is sent before a newer one', async () => {
    act(() => {
      useSessionStore.getState().setActiveSession('chat-b', 'mia')
      useChatStore.setState({ pendingDrainQueue: [qi('old', 1, 'chat-a')], outboundQueue: [qi('new', 2, 'chat-a')] })
    })
    const sender = connect()
    act(() => { useChatStore.getState().drainOutboundQueue() })
    openChat('chat-a')
    await flush()
    expect(sentFrames(sender)[0]?.content).toBe('old')
  })

  it('T1: queued rows shown are only the open chat, unseen, oldest first', async () => {
    const { selectQueuedRows } = await import('./chat/queue-target')
    const rows = selectQueuedRows(
      [qi('a1', 2, 'chat-a')],
      [qi('b1', 1, 'chat-b'), qi('a2', 3, 'chat-a'), qi('seen', 4, 'chat-a'), qi('free', 0)],
      new Set(['seen']),
      'chat-a',
    )
    expect(rows.map((row) => row.id)).toStrictEqual(['free', 'a1', 'a2'])
  })

  it('bindUnboundQueueItems binds only unbound objects', async () => {
    const { bindUnboundQueueItems } = await import('./chat/queue-target')
    expect(bindUnboundQueueItems({ outboundQueue: [qi('t', 1, 'chat-x')], pendingDrainQueue: ['s'] }, 'chat-a')).toStrictEqual({})
    const out = bindUnboundQueueItems({ outboundQueue: [qi('u', 1), qi('t', 2, 'chat-x')], pendingDrainQueue: [qi('v', 3)] }, 'chat-a')
    expect((out.outboundQueue as QueuedOutboundMessage[]).map((entry) => entry.targetSessionId)).toStrictEqual(['chat-a', 'chat-x'])
    expect((out.pendingDrainQueue as QueuedOutboundMessage[])[0].targetSessionId).toBe('chat-a')
  })
})

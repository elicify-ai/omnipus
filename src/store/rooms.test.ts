// RED — K45 attach/cursor on existing hub, FR-199/201/217; test 173 SPA half.
// No assumed room-state layout; observe generated outbound frames only.
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { act } from 'react'
import type { AttachRoomFrame } from '@/lib/api/generated/asyncapi-types'
import type { WsConnection } from '@/lib/ws'
import { useChatStore } from './chat'
import { useSessionStore } from './session'
import {
  connectSocket, entry, loadRoomStore, MAIN_ID, Network, resetRealStores,
  ROOM_ID, snapshot, stopHarness,
} from '../../tests/l1-room-ui/harness'

let connection: WsConnection | undefined
beforeEach(() => { resetRealStores(); new Network(); connection = undefined })
afterEach(() => stopHarness(connection))

async function setup() {
  const store = await loadRoomStore()
  act(() => store.setState(store.getInitialState(), true))
  const linked = connectSocket(); connection = linked.connection
  return { store, ...linked }
}

describe('room store public attach — existing connection, independent room cursor', () => {
  it('[FR-199; K45] first attach emits room id and after_seq=0, never an agent-session attach', async () => {
    const { store, socket } = await setup()
    act(() => store.getState().attachRoom(ROOM_ID))
    const expected: AttachRoomFrame = { type: 'attach_room', room_id: ROOM_ID, after_seq: 0 }
    expect(socket.sent).toEqual([expected])
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
    expect(useSessionStore.getState().activeAgentId).toBe('mia')
  })

  it('[FR-201/217; K45; SC-019] a snapshot and live entry advance only that room’s reconnect cursor', async () => {
    const { store, socket, emit } = await setup()
    act(() => store.getState().attachRoom(ROOM_ID))
    await emit(snapshot({ seq: 7, entries: [entry({ seq: 7 })] }))
    // The actual socket-parser batch settles before the observable cursor read.
    act(() => store.getState().attachRoom('other-room'))
    act(() => store.getState().attachRoom(ROOM_ID))
    expect(socket.sent.at(-1)).toEqual({ type: 'attach_room', room_id: ROOM_ID, after_seq: 7 })
    await emit({ type: 'room_entry', room_id: ROOM_ID, seq: 8, entry: entry({ id: 'room-entry-8', seq: 8 }) })
    act(() => store.getState().attachRoom('other-room'))
    act(() => store.getState().attachRoom(ROOM_ID))
    expect(socket.sent.at(-1)).toEqual({ type: 'attach_room', room_id: ROOM_ID, after_seq: 8 })
    const otherAttaches = socket.sent.filter((frame) => frame.type === 'attach_room' && frame.room_id === 'other-room')
    expect(otherAttaches.length).toBeGreaterThan(0)
    expect(otherAttaches.every((frame) => frame.type === 'attach_room' && frame.after_seq === 0)).toBe(true)
  })

  it('[FR-201; K45] stale duplicate room frames cannot rewind the cursor on a subsequent attach', async () => {
    const { store, socket, emit } = await setup()
    act(() => store.getState().attachRoom(ROOM_ID))
    await emit(snapshot({ seq: 9, entries: [entry({ seq: 9 })] }))
    await emit({ type: 'room_entry', room_id: ROOM_ID, seq: 3, entry: entry({ id: 'stale-entry', seq: 3 }) })
    act(() => store.getState().attachRoom('other-room'))
    act(() => store.getState().attachRoom(ROOM_ID))
    expect(socket.sent.at(-1)).toEqual({ type: 'attach_room', room_id: ROOM_ID, after_seq: 9 })
  })

  it('[FR-199/201; K45] room frames do not replace or create the active agent’s main transcript bucket', async () => {
    const { store, socket, emit } = await setup()
    useChatStore.getState().appendMessage({ id: 'main-text', session_id: MAIN_ID, role: 'user', content: 'main private text', timestamp: '2026-10-14T09:00:00Z', status: 'done' })
    const before = useChatStore.getState().sessionsById[MAIN_ID]
    act(() => store.getState().attachRoom(ROOM_ID))
    await emit(snapshot({ entries: [entry()] }))
    act(() => store.getState().attachRoom('other-room'))
    act(() => store.getState().attachRoom(ROOM_ID))
    expect(socket.sent.at(-1)).toEqual({ type: 'attach_room', room_id: ROOM_ID, after_seq: 1 })
    expect(useChatStore.getState().sessionsById[MAIN_ID]).toEqual(before)
    expect(useChatStore.getState().sessionsById[ROOM_ID]).toBeUndefined()
    expect(useSessionStore.getState().activeSessionId).toBe(MAIN_ID)
  })
})

// #1428 step 2. Spec 4.13 BR-22. Rooms are unchanged.
// If this passes on today's code it is a pass-by-design guard, not a RED proof.
// CHECK's MU-24 proves it can fail.
import { afterEach, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import { fireEvent, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { addDraftFiles, addLibraryPick, setDraftText } from '@/store/chat/drafts'
import {
  Network, ROOM_ID, WS, loadRoomScreen, openRoom, resetRealStores, snapshot, stopHarness,
} from '../../../tests/l1-room-ui/harness'
import type { WsConnection } from '@/lib/ws'

let net: Network
let connection: WsConnection | undefined

beforeAll(async () => { await loadRoomScreen() })
beforeEach(() => {
  resetRealStores()
  net = new Network()
  connection = undefined
})
afterEach(() => stopHarness(connection))

function sessionDraftSnapshot(): string {
  // Spec 4.13 / BR-22: the session draft store, not the message buckets.
  // File objects do not JSON-serialize their name; keep the name in the snapshot.
  return JSON.stringify(useChatStore.getState().draftState, (_key, value: unknown) => (
    value instanceof File ? `file:${value.name}:${value.size}` : value
  ))
}

describe('BR-22 rooms stay on room REST (spec 4.13)', () => {
  it('a room send with text does not touch the session pipeline', async () => {
    const harness = await openRoom(net, snapshot())
    connection = harness.connection
    setDraftText('chat-a', 'session draft')
    addDraftFiles('chat-a', [new File(['native'], 'native.txt', { type: 'text/plain' })])
    addLibraryPick('chat-a', {
      mediaId: 'lib-1',
      ref: 'media://workspace/sales/lib.txt',
      filename: 'lib.txt',
      contentType: 'text/plain',
    })
    const draftsBefore = sessionDraftSnapshot()
    net.respond('POST', '/api/v1/upload', () => new Response(JSON.stringify({
      files: [{
        name: 'room.txt', path: 'room.txt', size: 4,
        content_type: 'text/plain', ref: 'media://workspace/sales/room.txt',
      }],
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    const user = userEvent.setup()
    await user.type(screen.getByRole('combobox', { name: 'Message the group' }), 'hello group')
    expect(screen.getByRole('button', { name: 'Attach files' }), 'BR-22: the room has an attach control').toBeTruthy()
    // AddAttachment does not put an input[type=file] in the document. Drop is
    // the same upload path (useFileUpload.onDrop → the room adapter).
    const zone = screen.getByTestId('composer-card').parentElement
    expect(zone, 'BR-22: the composer accepts a drop').not.toBeNull()
    fireEvent.drop(zone!, { dataTransfer: { files: [new File(['room'], 'room.txt', { type: 'text/plain' })] } })
    expect(await screen.findByText('room.txt'), 'BR-22: the file is staged').toBeTruthy()
    await user.click(screen.getByRole('button', { name: /^send/i }))
    const writes = net.writes()
    const uploads = writes.filter((write) => write.path.includes('/upload'))
    expect(uploads, 'BR-22: one workspace upload').toHaveLength(1)
    expect(uploads[0]?.path, 'BR-22: the upload is the workspace upload route').toBe('/api/v1/upload')
    expect(uploads[0]?.form?.workspace_id, 'BR-22: the upload names the room workspace').toBe(WS)
    expect(uploads[0]?.form?.session_id, 'BR-22: no session_id in the upload body').toBeUndefined()
    expect(uploads[0]?.path.includes('session_id'), 'BR-22: no session_id in the upload path').toBe(false)
    expect(uploads[0]?.query.includes('session_id'), 'BR-22: no session_id in the upload query').toBe(false)
    expect(uploads[0]?.form?.files, 'BR-22: the staged file is the uploaded part').toBe('file:room.txt')
    const roomPosts = writes.filter((write) => write.method === 'POST' && write.path === `/api/v1/rooms/${ROOM_ID}/messages`)
    expect(roomPosts, 'BR-22: one room REST send').toHaveLength(1)
    expect(roomPosts[0]?.body, 'BR-22: the room message carries the text and the workspace media ref').toEqual(
      expect.objectContaining({
        text: 'hello group',
        media: ['media://workspace/sales/room.txt'],
      }),
    )
    expect(
      writes.some((write) => write.path.includes('/sessions') && write.method === 'POST'),
      'BR-22: no session is created',
    ).toBe(false)
    const controls = harness.socket.sent.filter((frame) =>
      frame.type === 'message' || frame.type === 'redirect' || frame.type === 'cancel')
    expect(controls, 'BR-22: zero session frames').toEqual([])
    expect(sessionDraftSnapshot(), 'BR-22: the session draft store is unchanged').toBe(draftsBefore)
  })
})

// #1428 step 2, Defect 3 (Team B, df26ab6418): a "May not have been
// delivered" item made in Nova's chat (W2) is listed above Mia's box (W1)
// after Mia's chat is opened through Search sessions.
//
// Spec C r9 4.9: uncertain items "live only in the list (not in a box)", and
// the Unsent list shows "from any other chat"; so the item being LISTED in
// Mia's chat is the specified behaviour. What the spec also requires is that
// every list item names its chat: 4.7 close row ("May not have been
// delivered" item: origin chat name, the text, Copy and Discard) and 4.9
// ("Each list item: chat name ..."). The uncertain row named no chat, so in
// Mia's chat it read as Mia's own stale item. Oracle: the fixture's titles.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { act } from 'react'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { WsConnection } from '@/lib/ws'
import { useConnectionStore } from '@/store/connection'
import {
  FakeSocket, apiMocks, deliverFrame, flushInbound, installFakeWebSocket, makeAgent, makeSession, makeWorkspace,
  mountComposer, resetBindingWorld, typeIntoComposer,
} from '@/test/sendBinding1428'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

let live: WsConnection | null = null

afterEach(() => {
  live?.disconnect()
  live = null
  vi.unstubAllGlobals()
})

function openLive(): FakeSocket {
  installFakeWebSocket()
  live = new WsConnection({
    onFrames: (frames) => { for (const frame of frames) deliverFrame(frame as never) },
    onConnected: () => {},
    onDisconnected: () => {},
    onError: () => {},
  })
  live.connect()
  const socket = FakeSocket.instances.at(-1)
  if (!socket) throw new Error('socket was not created')
  act(() => { socket.open() })
  act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
  useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
  return socket
}

describe('Defect 3: an uncertain item names its origin chat wherever it is listed', () => {
  it('Nova\'s uncertain redirect, listed in Mia\'s chat opened through Search sessions, names "Nova chat"', async () => {
    resetBindingWorld()
    apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace({ id: 'w1', name: 'My Workspace' }), makeWorkspace({ id: 'w2', name: 'W2' })])
    apiMocks.fetchAgents.mockResolvedValue([makeAgent({ id: 'mia', name: 'Mia' }), makeAgent({ id: 'nova', name: 'Nova' })])
    const sessions = [
      makeSession({ id: 'chat-a', agent_id: 'mia', workspace_id: 'w1', title: 'Mia chat' }),
      makeSession({ id: 'chat-n', agent_id: 'nova', workspace_id: 'w2', title: 'Nova chat' }),
    ]
    apiMocks.fetchSessions.mockResolvedValue(sessions)
    apiMocks.fetchSessionPage.mockResolvedValue({ sessions })
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w2')
      useSessionStore.getState().setActiveSession('chat-n', 'nova', 'core')
    })
    const socket = openLive()
    mountComposer({ sidebar: true })
    await typeIntoComposer('/stop-redirect nova only')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'the redirect was handed over').toBe(true)
    // The socket closes before the receipt (spec 4.7 close row): an uncertain item for chat-n.
    // A 4008 close reconnects at once; the page is online again before the person switches chats.
    act(() => { socket.close(4008, 'catch-up required') })
    const next = FakeSocket.instances.at(-1)
    if (!next || next === socket) throw new Error('the close opens the next connection')
    act(() => { next.open() })
    act(() => { next.receive({ type: 'server_capabilities', send_origin: true }) })
    await flushInbound()
    expect(screen.getByText('May not have been delivered'), 'the close made the uncertain item').toBeTruthy()

    await user.click(screen.getByRole('button', { name: 'Search sessions' }))
    const dialog = await screen.findByRole('dialog')
    await user.click(await within(dialog).findByText('Mia chat'))
    expect(useSessionStore.getState().activeSessionId, 'Search sessions opened Mia\'s chat').toBe('chat-a')

    const notice = screen.getByTestId('unsent-notice')
    expect(within(notice).getByText('May not have been delivered'), 'the item is listed (spec 4.9)').toBeTruthy()
    expect(within(notice).getByText('Nova chat'), 'spec 4.7/4.9: the item names its origin chat').toBeTruthy()
  })
})

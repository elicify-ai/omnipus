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

  // Team B, a86b8f92e6: the validator's uncertain redirect item read only
  // "Chat". A chat whose list entry carries no title of its own (a main chat:
  // its name is derived from its agent, sessionDisplayTitle.ts) fell through
  // to the generic word. Oracle: the name the sidebar gives the same chat.
  it('an uncertain redirect from Mia\'s main chat names "Mia - Main Session", not "Chat"', async () => {
    resetBindingWorld()
    apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace({ id: 'w1', name: 'My Workspace' })])
    apiMocks.fetchAgents.mockResolvedValue([makeAgent({ id: 'mia', name: 'Mia' })])
    const sessions = [makeSession({ id: 'main-mia', agent_id: 'mia', workspace_id: 'w1', title: '', type: 'main' })]
    apiMocks.fetchSessions.mockResolvedValue(sessions)
    apiMocks.fetchSessionPage.mockResolvedValue({ sessions })
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w1')
      useSessionStore.getState().setActiveSession('main-mia', 'mia', 'core')
    })
    const socket = openLive()
    mountComposer()
    await typeIntoComposer('/stop-redirect x-main')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'the redirect was handed over').toBe(true)
    act(() => { socket.close(4008, 'catch-up required') })
    const next = FakeSocket.instances.at(-1)
    if (!next || next === socket) throw new Error('the close opens the next connection')
    act(() => { next.open() })
    act(() => { next.receive({ type: 'server_capabilities', send_origin: true }) })
    await flushInbound()
    const notice = screen.getByTestId('unsent-notice')
    expect(within(notice).getByText('May not have been delivered'), 'the close made the uncertain item').toBeTruthy()
    expect(await within(notice).findByText('Mia - Main Session'), 'spec 4.7/4.9: the item names its origin chat').toBeTruthy()
    expect(within(notice).queryByText('Chat'), 'not the generic word').toBeNull()
  })
})

// Chief ruling 2026-11-19 19:46Z (Team B, a86b8f92e6, first-s2k4 and V): the
// uncertain redirect item must lead back to its origin chat with an Open
// action, and an untitled or unlisted origin must still be reachable; its name
// falls back to the sidebar's "Untitled session". Oracle: the fixture's ids
// and the sidebar wording (SessionRow.tsx).
describe('Defect 3: Open on an uncertain redirect item returns to its origin chat', () => {
  async function uncertainRedirectFromNovaThenMia(listed: boolean): Promise<HTMLElement> {
    resetBindingWorld()
    apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace({ id: 'w1', name: 'My Workspace' }), makeWorkspace({ id: 'w2', name: 'W2' })])
    apiMocks.fetchAgents.mockResolvedValue([makeAgent({ id: 'mia', name: 'Mia' }), makeAgent({ id: 'nova', name: 'Nova' })])
    const sessions = [
      makeSession({ id: 'chat-a', agent_id: 'mia', workspace_id: 'w1', title: 'Mia chat' }),
      ...(listed ? [makeSession({ id: 'chat-n', agent_id: 'nova', workspace_id: 'w2', title: '' })] : []),
    ]
    apiMocks.fetchSessions.mockResolvedValue(sessions)
    apiMocks.fetchSessionPage.mockResolvedValue({ sessions })
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w2')
      useSessionStore.getState().setActiveSession('chat-n', 'nova', 'core')
    })
    const socket = openLive()
    mountComposer()
    await typeIntoComposer('/stop-redirect nova only')
    const user = userEvent.setup()
    await user.keyboard('{Enter}')
    expect(socket.sent.some((frame) => frame.type === 'redirect'), 'the redirect was handed over').toBe(true)
    act(() => { socket.close(4008, 'catch-up required') })
    const next = FakeSocket.instances.at(-1)
    if (!next || next === socket) throw new Error('the close opens the next connection')
    act(() => { next.open() })
    act(() => { next.receive({ type: 'server_capabilities', send_origin: true }) })
    await flushInbound()
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w1')
      useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
    })
    await flushInbound()
    const notice = screen.getByTestId('unsent-notice')
    expect(within(notice).getByText('May not have been delivered'), 'the close made the uncertain item').toBeTruthy()
    return notice
  }

  for (const listed of [true, false]) {
    it(`an ${listed ? 'untitled' : 'unlisted'} Nova chat in W2: named "Untitled session", Open goes back to it`, async () => {
      const notice = await uncertainRedirectFromNovaThenMia(listed)
      expect(await within(notice).findByText('Untitled session'), 'the sidebar\'s name for an untitled chat').toBeTruthy()
      const user = userEvent.setup()
      await user.click(within(notice).getByRole('button', { name: 'Open' }))
      expect(useSessionStore.getState().activeSessionId, 'Open returns to the origin chat').toBe('chat-n')
      expect(useWorkspacesStore.getState().activeWorkspaceId, 'in the origin chat\'s own workspace').toBe('w2')
      expect(useSessionStore.getState().activeAgentId, 'with the origin chat\'s agent').toBe('nova')
    })
  }
})

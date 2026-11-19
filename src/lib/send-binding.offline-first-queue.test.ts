// #1428 step 2, UAT-16 repro (spec C r9 4.11, B-17, BR-13b, Q2 = A).
// The socket is DOWN (not a connected socket whose write fails): the first
// message of new chat K is queued, then another chat is opened.
import { describe, expect, it } from 'vitest'
import {
  apiMocks,
  connection,
  assertNewChatKey,
  deleteOpenChatThroughSidebar,
  framesOf,
  makeSession,
  makeWorkspace,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  sent,
  typeIntoComposer,
} from '@/test/sendBinding1428'
import { act } from 'react'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useChatStore } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { runWorkspaceEntry } from '@/store/session/workspaceEntryFlow'

async function queueFirstWhileOffline(text: string): Promise<string> {
  resetBindingWorld()
  mountComposer({ sidebar: true })
  const saved = makeSession({ id: 'saved-q', title: 'Saved Q', workspace_id: 'w1', agent_id: 'mia' })
  seedSavedChat(saved, undefined, undefined)
  apiMocks.fetchSessions.mockResolvedValue([saved])
  apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
  await deleteOpenChatThroughSidebar('Saved Q')
  const key = assertNewChatKey(useSessionStore.getState().newChatKey)
  act(() => { useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }) })
  const user = userEvent.setup()
  await typeIntoComposer(text)
  await user.click(screen.getByRole('button', { name: /Queue message|Send message/ }))
  return key
}

describe('UAT-16 offline first message for K, socket down (spec 4.11)', () => {
  it('another chat open: K is listed as not saved yet and nothing is sent at reconnect', async () => {
    await queueFirstWhileOffline('first-uat16')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(await screen.findByText('New chat with Mia, not saved yet'), '4.11: the Unsent list names K').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Open and send' })).toBeTruthy()
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(apiMocks.createSession, '4.11: no create while B is open').not.toHaveBeenCalled()
    expect(framesOf('message', sent()), '4.11: no first message while B is open').toEqual([])
  })

  it('Open and send re-enters K and sends the queued message once with origin K', async () => {
    const key = await queueFirstWhileOffline('first-open')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Open and send' }))
    expect(useSessionStore.getState().newChatKey, '4.11: back in K').toBe(key)
    const frames = framesOf('message', sent())
    expect(frames, '4.11: sent once').toHaveLength(1)
    expect(frames[0], '4.11: origin K, agent and text kept').toEqual(expect.objectContaining({
      content: 'first-open',
      agent_id: 'mia',
      origin: { kind: 'new_chat', new_chat_key: key },
    }))
  })

  it('Discard removes the queued message; nothing is sent when K is opened later', async () => {
    const key = await queueFirstWhileOffline('first-discard')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Discard' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Discard' }))
    expect(screen.queryByText('New chat with Mia, not saved yet'), 'Discard removes the row').toBeNull()
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useSessionStore.setState({ newChatKey: key }); useSessionStore.getState().setActiveSession(null, 'mia', 'core') })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'a discarded message is never sent').toEqual([])
  })
})

/** UAT-16 run B setup: K (Mia, W1) is queued offline, then workspace W2 is entered and the entry fails offline. */
async function failEntryIntoOtherWorkspace(text: string): Promise<string> {
  const key = await queueFirstWhileOffline(text)
  apiMocks.fetchSessions.mockRejectedValue(new Error('offline'))
  apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace(), makeWorkspace({ id: 'w2', name: 'Ops' })])
  act(() => { useWorkspacesStore.getState().setActiveWorkspaceId('w2') })
  await act(async () => { await runWorkspaceEntry('w2') })
  return key
}

describe('UAT-16 run B: failed entry into another workspace with K queued (Option A, chief ruling)', () => {
  it('K is no longer the open chat; its message is listed as not saved yet and is not sent at reconnect', async () => {
    await failEntryIntoOtherWorkspace('first-runb')
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: the W2 entry failed').toBe('failed-attempt')
    expect(await screen.findByText('New chat with Mia, not saved yet'), 'Option A: K is listed in Unsent').toBeTruthy()
    expect(screen.getByRole('button', { name: 'Open and send' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Copy' })).toBeTruthy()
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(apiMocks.createSession, 'no chat is created while W2 is shown').not.toHaveBeenCalled()
    expect(framesOf('message', sent()), 'no first message while W2 is shown').toEqual([])
  })

  it('Discard from the item removes K\'s message; nothing is sent at reconnect', async () => {
    await failEntryIntoOtherWorkspace('first-runb-discard')
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Discard' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Discard' }))
    expect(screen.queryByText('New chat with Mia, not saved yet'), 'Discard removes the row').toBeNull()
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(framesOf('message', sent()), 'a discarded message is never sent').toEqual([])
  })

  it('FR-004: a saved chat of W1 stays open after a failed W2 entry', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-q', title: 'Saved Q', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    act(() => { useConnectionStore.setState({ isConnected: false, reconnectPhase: 'reconnecting' }) })
    apiMocks.fetchSessions.mockRejectedValue(new Error('offline'))
    act(() => { useWorkspacesStore.getState().setActiveWorkspaceId('w2') })
    await act(async () => { await runWorkspaceEntry('w2') })
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: the W2 entry failed').toBe('failed-attempt')
    expect(useSessionStore.getState().activeSessionId, 'FR-004: the committed saved chat stays open').toBe('saved-q')
  })

  it('scope: a failed re-entry of K\'s own workspace keeps K open', async () => {
    const key = await queueFirstWhileOffline('first-own-ws')
    apiMocks.fetchSessions.mockRejectedValue(new Error('offline'))
    await act(async () => { await runWorkspaceEntry('w1') })
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: the W1 entry failed').toBe('failed-attempt')
    expect(useSessionStore.getState().newChatKey, 'K belongs to W1: it stays the open chat').toBe(key)
    expect(screen.queryByText('New chat with Mia, not saved yet'), 'the open chat is not listed as Unsent').toBeNull()
  })

  it('a successful W2 entry is unaffected: W2\'s main opens, K is listed and not sent', async () => {
    await queueFirstWhileOffline('first-w2-ok')
    const w2main = makeSession({ id: 'w2-main', title: 'Nova main', workspace_id: 'w2', agent_id: 'nova', type: 'main' as never })
    apiMocks.fetchSessions.mockResolvedValue([w2main])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'nova', name: 'Nova', type: 'core' } as never, { id: 'mia', name: 'Mia', type: 'core' } as never])
    apiMocks.fetchWorkspace.mockResolvedValue(makeWorkspace({ id: 'w2', name: 'Ops', core_team: ['nova'], member_configs: { nova: { main_session_id: 'w2-main' } } } as never))
    act(() => { useWorkspacesStore.getState().setActiveWorkspaceId('w2') })
    await act(async () => { await runWorkspaceEntry('w2') })
    expect(useSessionStore.getState().workspaceEntry?.status, 'W2 entry succeeded').toBe('welcome')
    expect(useSessionStore.getState().activeSessionId, 'W2 main is open').toBe('w2-main')
    expect(await screen.findByText('New chat with Mia, not saved yet')).toBeTruthy()
    act(() => { useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    act(() => { useChatStore.getState().drainOutboundQueue() })
    expect(apiMocks.createSession).not.toHaveBeenCalled()
    expect(framesOf('message', sent()), 'K\'s message is not sent into W2').toEqual([])
  })
})

/** K (Mia, W1) is queued offline, then W2 is entered with the given network answers. */
async function enterW2WithKQueued(text: string, setup: () => void): Promise<void> {
  await queueFirstWhileOffline(text)
  apiMocks.fetchWorkspaces.mockResolvedValue([makeWorkspace(), makeWorkspace({ id: 'w2', name: 'Ops' })])
  apiMocks.fetchAgents.mockResolvedValue([{ id: 'nova', name: 'Nova', type: 'core' } as never, { id: 'mia', name: 'Mia', type: 'core' } as never])
  setup()
  act(() => { useWorkspacesStore.getState().setActiveWorkspaceId('w2') })
  await act(async () => { await runWorkspaceEntry('w2') })
}

/** Option A: K is no longer open, its message is listed in Unsent, and nothing goes out at reconnect. */
async function expectKListedAndNotSent(): Promise<void> {
  expect(useSessionStore.getState().newChatKey, 'Option A: K is not the open chat under W2').toBeNull()
  expect(await screen.findByText('New chat with Mia, not saved yet'), 'Option A: K is listed in Unsent').toBeTruthy()
  act(() => { connection().fail = false; useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
  act(() => { useChatStore.getState().drainOutboundQueue() })
  expect(apiMocks.createSession, 'no chat is created while W2 is shown').not.toHaveBeenCalled()
  expect(framesOf('message', sent()), 'no first message while W2 is shown').toEqual([])
}

describe('UAT-16 run B: every blocked W2 entry closes K (review of 4822aba7bb)', () => {
  it('failed attach (connected, send refused): failed-attempt closes K', async () => {
    await enterW2WithKQueued('first-attach-fail', () => {
      const w2main = makeSession({ id: 'w2-main', title: 'Nova main', workspace_id: 'w2', agent_id: 'nova', type: 'main' as never })
      apiMocks.fetchSessions.mockResolvedValue([w2main])
      apiMocks.fetchWorkspace.mockResolvedValue(makeWorkspace({ id: 'w2', name: 'Ops', core_team: ['nova'], member_configs: { nova: { main_session_id: 'w2-main' } } } as never))
      act(() => { connection().fail = true; useConnectionStore.setState({ isConnected: true, reconnectPhase: null }) })
    })
    const entry = useSessionStore.getState().workspaceEntry
    expect(entry?.status, 'precondition: the attach was refused').toBe('failed-attempt')
    expect(entry?.status === 'failed-attempt' && entry.attempted.sessionId, 'precondition: W2 main was attempted').toBe('w2-main')
    await expectKListedAndNotSent()
  })

  it('an entry that throws: the unavailable state closes K', async () => {
    await enterW2WithKQueued('first-entry-throws', () => {
      apiMocks.fetchSessions.mockResolvedValue([])
      apiMocks.fetchWorkspace.mockResolvedValue({ get member_configs(): never { throw new Error('unexpected shape') } } as never)
    })
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: the entry threw').toBe('unavailable')
    await expectKListedAndNotSent()
  })

  it('unavailable (first agent has no main): closes K', async () => {
    await enterW2WithKQueued('first-unavailable', () => {
      apiMocks.fetchSessions.mockResolvedValue([])
      apiMocks.fetchWorkspace.mockResolvedValue(makeWorkspace({ id: 'w2', name: 'Ops', core_team: ['nova'], member_configs: {} } as never))
    })
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: no main to open').toBe('unavailable')
    await expectKListedAndNotSent()
  })

  it('empty team: closes K', async () => {
    await enterW2WithKQueued('first-empty-team', () => {
      apiMocks.fetchSessions.mockResolvedValue([])
      apiMocks.fetchWorkspace.mockResolvedValue(makeWorkspace({ id: 'w2', name: 'Ops', core_team: [], member_configs: {} } as never))
    })
    expect(useSessionStore.getState().workspaceEntry?.status, 'precondition: no conversation agent').toBe('empty-team')
    await expectKListedAndNotSent()
  })
})

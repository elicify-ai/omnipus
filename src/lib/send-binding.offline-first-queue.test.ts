// #1428 step 2, UAT-16 repro (spec C r9 4.11, B-17, BR-13b, Q2 = A).
// The socket is DOWN (not a connected socket whose write fails): the first
// message of new chat K is queued, then another chat is opened.
import { describe, expect, it } from 'vitest'
import {
  apiMocks,
  assertNewChatKey,
  deleteOpenChatThroughSidebar,
  framesOf,
  makeSession,
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

// #1428 step 2. Production gaps pinned to spec C r9. These stay red until the
// code matches the clause named on each assertion. Do not loosen them.
import { afterEach, describe, expect, it } from 'vitest'
import {
  NOT_STOPPED,
  SWITCHED,
  apiMocks,
  assertNewChatKey,
  connection,
  deleteOpenChatThroughSidebar,
  deliverFrame,
  flushInbound,
  framesOf,
  installFakeWebSocket,
  makeSession,
  makeWorkspace,
  mountComposer,
  resetBindingWorld,
  seedSavedChat,
  seedStreamingAnswer,
  sent,
  typeIntoComposer,
  FakeSocket,
} from '@/test/sendBinding1428'
import { act } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SEND_TEXT, submissionForMessage } from '@/lib/bound-send'
import { submitAnnotation } from '@/lib/browserAnnotate'
import { ChatControls } from '@/components/chat/ChatControls'
import { OMNIPUS_AGENT_ID } from '@/lib/omnipus'
import { queryClient } from '@/lib/queryClient'
import { WsConnection } from '@/lib/ws'
import { useChatStore } from '@/store/chat'
import { emptyHistoryState } from '@/store/chat/history-state'
import { makeBucketMessages } from '@/store/chat/messages'
import { emptySessionState } from '@/store/chat/session'
import { addDraftFiles, addLibraryPick, getDraft, useDraftStore } from '@/store/chat/drafts'
import { openOmnipusChat } from '@/store/omnipusChat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useWorkspacesStore } from '@/store/workspacesStore'

let live: WsConnection | null = null

afterEach(() => {
  live?.disconnect()
  live = null
})

function withinDialogDiscard(dialog: HTMLElement): HTMLElement {
  const button = Array.from(dialog.querySelectorAll('button')).find((candidate) => candidate.textContent === 'Discard')
  if (!button) throw new Error('Discard confirm missing')
  return button
}

function controlSent(socket: FakeSocket) {
  return socket.sent.filter((frame) => frame.type === 'message' || frame.type === 'redirect' || frame.type === 'cancel')
}

/** An OPEN socket whose first frame is not server_capabilities (spec 4.10). */
function openUnsupported(): FakeSocket {
  installFakeWebSocket()
  live = new WsConnection({
    onFrames: () => {},
    onConnected: () => {},
    onDisconnected: () => {},
    onError: () => {},
  })
  live.connect()
  const socket = FakeSocket.instances.at(-1)
  if (!socket) throw new Error('socket missing')
  act(() => { socket.open() })
  // Spec 4.10: the first frame is classified before the parser can drop it.
  act(() => {
    socket.receive({
      type: 'session_state',
      user_id: 'ada',
      pending_approvals: [],
      session_id: 'chat-a',
      emitted_at: '2026-11-18T00:00:00Z',
      auto_approve_modifier: true,
    })
  })
  useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
  expect(live.sendCapability, '4.10: a non-capability first frame is unsupported').toBe('unsupported')
  return socket
}

describe('production gaps the spec already decides (4.4.3 / 4.6 / 4.7 / 4.8 / 4.9 / 4.10)', () => {
  registerCapabilityGaps()
  registerDiscardGaps()
  registerNoticeGap()
  registerSlashGaps()
  registerAcceptedGap()
  registerAnnotationGaps()
  registerDraftWarningGaps()
  registerRefusalReasons()
})

function registerCapabilityGaps(): void {
  it('F2 Retry sends nothing while the open socket is unsupported', () => {
    // Spec 4.10 / B-16: no message frame while unsupported. 4.6.1 Resend row
    // still goes through that gate.
    resetBindingWorld()
    const socket = openUnsupported()
    queryClient.setQueryData(['sessions'], [{ id: 'chat-a', agent_id: 'mia', workspace_id: 'w1', title: 'A' }])
    act(() => {
      useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core')
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          'chat-a': {
            ...emptySessionState(),
            ...makeBucketMessages([{
              id: 'retry-1',
              role: 'user' as const,
              content: 'retry me',
              timestamp: '2026-01-01T00:00:00Z',
              status: 'done' as const,
              deliveryStatus: 'failed' as const,
            }]),
            history: emptyHistoryState(),
            isStreaming: false,
            isReplaying: false,
            toolCalls: {},
            toolCallOrder: [],
          },
        },
      }))
    })
    act(() => { useChatStore.getState().resendMessage('retry-1') })
    expect(controlSent(socket), 'F2: Retry sends no frame while unsupported').toEqual([])
    expect(apiMocks.createSession, 'F2: Retry does not create a chat').not.toHaveBeenCalled()
    expect(apiMocks.uploadFiles, 'F2: Retry does not upload').not.toHaveBeenCalled()
  })

  it('F2 Open and send sends nothing while the open socket is unsupported', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-cap', title: 'Saved Cap', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved Cap')
    assertNewChatKey(useSessionStore.getState().newChatKey)
    connection().fail = true
    const user = userEvent.setup()
    await typeIntoComposer('first for K')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    const socket = openUnsupported()
    await user.click(screen.getByRole('button', { name: 'Open and send' }))
    expect(controlSent(socket), 'F2: Open and send sends no frame while unsupported').toEqual([])
    expect(apiMocks.createSession, 'F2: Open and send does not create a chat').not.toHaveBeenCalled()
    expect(apiMocks.uploadFiles, 'F2: Open and send does not upload').not.toHaveBeenCalled()
  })

  it('F2 a workflow button sends nothing while the open socket is unsupported', () => {
    resetBindingWorld()
    const socket = openUnsupported()
    const home = makeWorkspace({
      id: 'home',
      name: 'Home',
      is_default: true,
      member_configs: { [OMNIPUS_AGENT_ID]: { main_session_id: 'omnipus-main' } },
    })
    act(() => { useSessionStore.getState().setActiveSession('omnipus-main', OMNIPUS_AGENT_ID, 'core') })
    openOmnipusChat({
      workspaces: [home],
      sessions: [],
      workflow: { workflow: 'W1', workspaceId: 'home', workspaceName: 'Home' },
    })
    expect(controlSent(socket), 'F2: a workflow start sends no frame while unsupported').toEqual([])
    expect(apiMocks.createSession, 'F2: a workflow start does not create a chat').not.toHaveBeenCalled()
    expect(apiMocks.uploadFiles, 'F2: a workflow start does not upload').not.toHaveBeenCalled()
  })

  it('F2 Open browser does not create a chat while the open socket is unsupported', async () => {
    resetBindingWorld()
    mountComposer({ sidebar: true })
    const saved = makeSession({ id: 'saved-browser', title: 'Saved Browser', workspace_id: 'w1', agent_id: 'mia' })
    seedSavedChat(saved, undefined, undefined)
    apiMocks.fetchSessions.mockResolvedValue([saved])
    apiMocks.fetchAgents.mockResolvedValue([{ id: 'mia', name: 'Mia', type: 'core' } as never])
    await deleteOpenChatThroughSidebar('Saved Browser')
    assertNewChatKey(useSessionStore.getState().newChatKey)
    openUnsupported()
    render(<ChatControls />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Open browser' }))
    expect(apiMocks.createSession, 'F2: Open browser does not POST /sessions while unsupported').not.toHaveBeenCalled()
    expect(apiMocks.uploadFiles, 'F2: Open browser does not upload').not.toHaveBeenCalled()
  })

  it('F2 an annotation uploads nothing while the open socket is unsupported', async () => {
    resetBindingWorld()
    const socket = openUnsupported()
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    apiMocks.uploadFiles.mockResolvedValue({
      files: [{ ref: 'media://workspace/w1/shot', name: 'shot.png', content_type: 'image/png' }],
    })
    await submitAnnotation({
      comment: 'look',
      file: new File(['png'], 'shot.png', { type: 'image/png' }),
      point: null,
      isPointCurrent: () => false,
      sessionId: 'chat-a',
      agentId: 'mia',
    })
    expect(apiMocks.uploadFiles, 'F2: an annotation does not upload while unsupported').not.toHaveBeenCalled()
    expect(controlSent(socket), 'F2: an annotation sends no frame while unsupported').toEqual([])
    expect(apiMocks.createSession, 'F2: an annotation does not create a chat').not.toHaveBeenCalled()
  })
}

function registerDiscardGaps(): void {
  async function refuseInA(): Promise<ReturnType<typeof userEvent.setup>> {
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('for A')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const id = (framesOf('message', sent())[0] as { client_message_id?: string }).client_message_id
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'message',
      session_id: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_message_id: id,
    })
    return user
  }

  it('F7 Discard from the list clears the origin draft and the list item', async () => {
    // Spec 4.4.3 / 4.9: one recovered input, shown in two places. Discard drops it.
    const user = await refuseInA()
    expect(getDraft('chat-a').text, 'F7: the refusal restored the origin draft').toBe('for A')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(withinDialogDiscard(dialog))
    expect(getDraft('chat-a').text, 'F7: list Discard clears the origin draft').toBe('')
    expect(useDraftStore.getState().unsent, 'F7: list Discard removes the list item').toEqual([])
  })

  it('F7 Discard in the box clears the list item as well as the draft', async () => {
    const user = await refuseInA()
    await user.click(screen.getByRole('button', { name: 'Discard' }))
    expect(getDraft('chat-a').text, 'F7: in-box Discard clears the draft').toBe('')
    expect(useDraftStore.getState().unsent, 'F7: in-box Discard removes the list item').toEqual([])
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    expect(screen.queryByText('1 not sent'), 'F7: the other chat does not still list it').toBeNull()
  })
}

function registerNoticeGap(): void {
  it('F8 a message-scoped error keeps the Stop notice; a turn-ending done removes it', async () => {
    // Spec 4.7 / B-24 and ErrorFrame client_message_id: a message-scoped error
    // ends no turn. Only that chat's own turn-ending done/error removes the notice.
    resetBindingWorld()
    installFakeWebSocket()
    const parsed: { type?: string; client_message_id?: string }[] = []
    live = new WsConnection({
      onFrames: (frames) => {
        for (const frame of frames) {
          parsed.push(frame as { type?: string; client_message_id?: string })
          deliverFrame(frame as never)
        }
      },
      onConnected: () => {},
      onDisconnected: () => {},
      onError: () => {},
    })
    live.connect()
    const socket = FakeSocket.instances.at(-1)
    if (!socket) throw new Error('socket missing')
    act(() => { socket.open() })
    act(() => { socket.receive({ type: 'server_capabilities', send_origin: true }) })
    useConnectionStore.setState({ connection: live, isConnected: true, reconnectPhase: null })
    seedStreamingAnswer('chat-a')
    mountComposer()
    fireEvent.click(screen.getByTestId('stop-btn'))
    const cancel = socket.sent.find((frame) => frame.type === 'cancel') as { client_frame_id?: string } | undefined
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'cancel',
      session_id: 'chat-a',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_frame_id: cancel?.client_frame_id,
    })
    expect(screen.getByText(NOT_STOPPED), 'F8: the refusal established the notice').toBeTruthy()
    act(() => {
      socket.receive({
        type: 'error',
        message: 'That message was refused.',
        session_id: 'chat-a',
        client_message_id: 'msg-scoped-1',
      })
    })
    await flushInbound()
    expect(
      parsed.some((frame) => frame.type === 'error' && frame.client_message_id === 'msg-scoped-1'),
      'F8: the message-scoped error passed the parser',
    ).toBe(true)
    expect(screen.getByText(NOT_STOPPED), 'F8: a message-scoped error ends no turn').toBeTruthy()
    expect(screen.getByTestId('stop-btn'), 'F8: Stop stays while the turn runs').toBeEnabled()
    act(() => { socket.receive({ type: 'done', session_id: 'chat-a' }) })
    await flushInbound()
    expect(screen.queryByText(NOT_STOPPED), 'F8: the chat\'s own done removes the notice').toBeNull()
  })
}

function registerSlashGaps(): void {
  async function holdSlash(text: string): Promise<(commands: unknown) => Promise<void>> {
    let release: (value: unknown) => void = () => {}
    apiMocks.fetchCommands.mockImplementation(() => new Promise((resolve) => { release = resolve }))
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer(text)
    await user.keyboard('{Enter}')
    return async (commands: unknown) => {
      await act(async () => {
        release(commands)
        await new Promise((r) => setTimeout(r, 50))
      })
    }
  }

  const commands = [
    { name: 'stop-redirect', label: '/stop-redirect', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
  ]

  it('F9 a held slash command sends the original instruction after an edit', async () => {
    // Spec 4.6.1: /stop-redirect deferred freezes the instruction at the
    // original submit. A later edit is a newer draft, not the held command.
    resetBindingWorld()
    const flush = await holdSlash('/stop-redirect x')
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: '/stop-redirect y' } })
    await flush(commands)
    expect(framesOf('redirect', sent()), 'F9: the held instruction is x').toEqual([
      expect.objectContaining({
        type: 'redirect',
        session_id: 'chat-a',
        instruction: 'x',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    expect(screen.getByTestId('chat-input'), 'F9: the newer text stays in the draft').toHaveValue('/stop-redirect y')
  })

  it('F9 a held slash command checked on return to A still sends the original instruction', async () => {
    // Spec 4.6.2: A→B→A is eligible at flush, and the check does not rewrite
    // the instruction captured at the original submit.
    resetBindingWorld()
    const flush = await holdSlash('/stop-redirect x')
    act(() => { useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core') })
    act(() => { useSessionStore.getState().setActiveSession('chat-a', 'mia', 'core') })
    fireEvent.change(screen.getByTestId('chat-input'), { target: { value: '/stop-redirect y' } })
    await flush(commands)
    expect(framesOf('redirect', sent()), 'F9: back in A, the held instruction is still x').toEqual([
      expect.objectContaining({
        instruction: 'x',
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
    expect(screen.getByTestId('chat-input'), 'F9: y remains after the held command').toHaveValue('/stop-redirect y')
    expect(screen.queryByText(SWITCHED), 'F9: returning to A is not a switched-chat refusal').toBeNull()
  })
}

function registerAcceptedGap(): void {
  it('F10 an accepted message leaves the pending map and a later refusal restores nothing', async () => {
    // Spec 4.4.3: accepted send → submission dropped. 4.8: a refusal with no
    // matching submission changes no draft.
    resetBindingWorld()
    mountComposer()
    const user = userEvent.setup()
    await typeIntoComposer('accepted text')
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const id = (framesOf('message', sent())[0] as { client_message_id?: string }).client_message_id ?? 'missing'
    expect(submissionForMessage(id), 'F10: the hand-over keeps the record until acceptance').toBeDefined()
    deliverFrame({
      type: 'message_status',
      state: 'received',
      session_id: 'chat-a',
      client_message_id: id,
    } as never)
    expect(submissionForMessage(id), 'F10: acceptance drops the pending submission').toBeUndefined()
    deliverFrame({
      type: 'send_refused',
      reason: 'wrong_chat',
      refused_frame: 'message',
      session_id: 'chat-b',
      origin: { kind: 'chat', session_id: 'chat-a' },
      client_message_id: id,
    })
    expect(screen.getByTestId('chat-input'), 'F10: a later refusal does not restore accepted text').toHaveValue('')
    expect(useDraftStore.getState().unsent, 'F10: a later refusal creates no recovery item').toEqual([])
  })
}

function registerAnnotationGaps(): void {
  it('F11 an annotation uploads to the panel workspace, not the foreground workspace', async () => {
    // Spec 4.6.1 annotation row: panel session, agent and the panel session's
    // workspace are frozen at submit. The foreground chat is W2.
    resetBindingWorld()
    queryClient.setQueryData(['sessions'], [
      { id: 'chat-a', agent_id: 'mia', workspace_id: 'w1', title: 'Panel' },
      { id: 'chat-b', agent_id: 'mia', workspace_id: 'w2', title: 'Foreground' },
    ])
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w2')
      useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core')
    })
    apiMocks.uploadFiles.mockResolvedValue({
      files: [{ ref: 'media://workspace/w1/shot', name: 'shot.png', content_type: 'image/png' }],
    })
    await submitAnnotation({
      comment: 'look here',
      file: new File(['png'], 'shot.png', { type: 'image/png' }),
      point: null,
      isPointCurrent: () => false,
      sessionId: 'chat-a',
      agentId: 'mia',
    })
    expect(apiMocks.uploadFiles, 'F11: one upload').toHaveBeenCalledTimes(1)
    expect(apiMocks.uploadFiles.mock.calls[0]?.[0], 'F11: the upload session is the panel chat').toBe('chat-a')
    expect(apiMocks.uploadFiles.mock.calls[0]?.[2], 'F11: the upload workspace is the panel workspace W1').toBe('w1')
    expect(framesOf('message', connection().sent), 'F11: origin stays the panel chat').toEqual([
      expect.objectContaining({
        session_id: 'chat-a',
        origin: { kind: 'chat', session_id: 'chat-a' },
      }),
    ])
  })

  it('F11 a streaming foreground chat does not block an idle panel annotation', async () => {
    resetBindingWorld()
    act(() => {
      useWorkspacesStore.getState().setActiveWorkspaceId('w1')
      useSessionStore.getState().setActiveSession('chat-b', 'mia', 'core')
      useChatStore.setState((s) => ({
        ...s,
        isStreaming: true,
        sessionsById: {
          'chat-a': { ...emptySessionState(), isStreaming: false, history: emptyHistoryState(), toolCalls: {}, toolCallOrder: [] },
          'chat-b': { ...emptySessionState(), isStreaming: true, history: emptyHistoryState(), toolCalls: {}, toolCallOrder: [] },
        },
      }))
    })
    apiMocks.uploadFiles.mockResolvedValue({
      files: [{ ref: 'media://workspace/w1/shot', name: 'shot.png', content_type: 'image/png' }],
    })
    await expect(submitAnnotation({
      comment: 'look here',
      file: new File(['png'], 'shot.png', { type: 'image/png' }),
      point: null,
      isPointCurrent: () => false,
      sessionId: 'chat-a',
      agentId: 'mia',
    }), 'F11: the foreground bucket streaming does not block the panel').resolves.toBeUndefined()
    expect(apiMocks.uploadFiles, 'F11: the panel upload still runs').toHaveBeenCalledTimes(1)
  })
}

function registerDraftWarningGaps(): void {
  it('F14 a library-only draft warns before the page is left', () => {
    // Spec 4.4.3 / B-23: every non-empty draft warns. A library pick with no
    // text is non-empty (4.4.3 entry includes libraryPicks).
    resetBindingWorld()
    mountComposer()
    addLibraryPick('chat-a', {
      mediaId: 'lib-1',
      ref: 'media://workspace/w1/lib.txt',
      filename: 'lib.txt',
      contentType: 'text/plain',
    })
    expect(getDraft('chat-a').text, 'F14: the draft has no text').toBe('')
    expect(getDraft('chat-a').libraryPicks, 'F14: the library pick is staged').toHaveLength(1)
    const leaving = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(leaving)
    expect(leaving.defaultPrevented, 'F14: a library-only draft warns').toBe(true)
  })

  it('F14 a native-file-only draft is listed while the connection is unsupported', async () => {
    // Spec 4.9 / 4.10: unsupported mode lists every non-empty draft. A native
    // file with no text is non-empty. The list row is the Copy action inside
    // the notice (and the "1 not sent" count). The composer file chip also
    // shows the filename, so the filename is not the list.
    resetBindingWorld()
    openUnsupported()
    mountComposer()
    act(() => {
      addDraftFiles('chat-a', [new File(['x'], 'notes.txt', { type: 'text/plain' })])
    })
    expect(getDraft('chat-a').text, 'F14: the draft has no text').toBe('')
    const notice = await screen.findByTestId('unsent-notice')
    expect(within(notice).getByRole('button', { name: 'Copy' }), 'F14: the unsupported list row can be copied').toHaveAccessibleName('Copy')
    expect(within(notice).getByText('1 not sent'), 'F14: the file-only draft is one list row').toHaveTextContent('1 not sent')
  })
}

function registerRefusalReasons(): void {
  const rows = [
    { reason: 'wrong_chat' as const, page: false, unverified: false },
    { reason: 'origin_malformed' as const, page: true, unverified: false },
    { reason: 'frame_malformed' as const, page: true, unverified: false },
    { reason: 'origin_unverified' as const, page: false, unverified: true },
  ]

  it.each(rows)('F16 send_refused $reason shows only the explanation spec 4.8 names', async ({ reason, page, unverified }) => {
    resetBindingWorld()
    mountComposer()
    act(() => {
      useChatStore.setState((s) => ({
        ...s,
        sessionsById: {
          ...(s.sessionsById ?? {}),
          'chat-c': { ...emptySessionState(), isStreaming: true, messagesById: {}, messageOrder: [], toolCalls: {}, toolCallOrder: [] },
        },
      }))
    })
    const user = userEvent.setup()
    await typeIntoComposer(`for ${reason}`)
    await user.click(screen.getByRole('button', { name: 'Send message' }))
    const id = (framesOf('message', sent())[0] as { client_message_id?: string }).client_message_id
    const refusal = {
      type: 'send_refused' as const,
      reason,
      refused_frame: 'message' as const,
      session_id: 'chat-b',
      origin: { kind: 'chat' as const, session_id: 'chat-a' },
      client_message_id: id,
    }
    deliverFrame(refusal)
    expect(screen.getByTestId('chat-input'), 'F16: the text is restored once').toHaveValue(`for ${reason}`)
    expect(screen.queryByText(SEND_TEXT.pageError) !== null, 'F16: page-error text').toBe(page)
    expect(screen.queryByText(SEND_TEXT.unverified) !== null, 'F16: unverified text').toBe(unverified)
    expect(useDraftStore.getState().unsent, 'F16: one recovery item').toHaveLength(1)
    expect(useChatStore.getState().sessionsById['chat-c']?.isStreaming, 'F16: another chat keeps running').toBe(true)
    deliverFrame(refusal)
    expect(useDraftStore.getState().unsent, 'F16: a duplicate refusal does not add an item').toHaveLength(1)
    expect(screen.getByTestId('chat-input'), 'F16: a duplicate refusal does not restore twice').toHaveValue(`for ${reason}`)
  })
}

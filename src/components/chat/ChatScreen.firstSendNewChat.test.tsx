import { act } from 'react'
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { AssistantRuntimeProvider } from '@assistant-ui/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { SlashCommand } from '@/lib/api'
import { useOmnipusRuntime } from '@/lib/omnipus-runtime'
import type { WsConnection } from '@/lib/ws'
import { getMessages, useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { OmnipusComposer, VirtualUserMessageRow } from './ChatScreen'

// Oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1090/design-1090.md,
// D7/D8, retained by FOUNDER DECISION "middle way".
// T4 invokes startNewSession directly: a deleted/inverted composer-confirmation
// branch cannot fail it. These tests drive /new through the real composer,
// real useSlashMenu, real runtime bridge and real catalogued ConfirmDialog.
// Cases: exact warning + both choices; a stale choice cannot clear a newer
// first-send attempt sharing __pending (D8's "the old" bucket/correlation rule).
// No store action, chat logic, UI component or library hook is mocked/spied.
// Only socket/fetch/browser-layout edges and clock are replaced. The typed command
// cache fixture uses useSlashMenu's existing SlashCommand shape, not a guessed
// wire response. Other picker API requests fail at fetch, not inside the UI.
// CHECK probes (not applied): bypass requestNewSession; make cancel destructive;
// omit pending-request cleanup; remove the old-client-ID confirmation guard.
// No sized/numeric boundary is introduced. Known gaps: actual workspace route
// mounting and browser screenshots are not proven by this component harness.
// RED-before-GREEN, GREEN certification and mutation proof are deferred to CHECK.

const OLD_ID = '1090-red5-dialog-old'
const OLD_CONTENT = 'Do not discard this unconfirmed first message.  '
const NEW_ID = '1090-red5-dialog-new'
const NEW_CONTENT = 'A newer first message must survive an old dialog choice.'
const WORKSPACE = '1090-red5-dialog-workspace'
const WARNING = 'Delivery not confirmed. Copy your message before starting a new chat.'
const NEW_COMMAND: SlashCommand = {
  name: 'new',
  label: '/new',
  description: 'Start a new chat',
  delivery: 'client',
  available_while_streaming: true,
}

let client: QueryClient
let originalScrollIntoView: PropertyDescriptor | undefined

function resetDialogStores() {
  act(() => {
    useWorkspacesStore.setState(useWorkspacesStore.getInitialState(), true)
    useSessionStore.setState({
      ...useSessionStore.getInitialState(),
      activeSessionId: null,
      activeAgentId: 'mia',
      attachedSessionType: null,
      attachedTaskTitle: null,
      sessionByWorkspace: {},
    }, true)
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState(useConnectionStore.getInitialState(), true)
    useUiStore.setState(useUiStore.getInitialState(), true)
    localStorage.clear()
    sessionStorage.clear()
  })
}

beforeEach(() => {
  vi.useFakeTimers()
  resetDialogStores()
  // jsdom has no scrolling/layout implementation. Supply only that browser
  // edge so the real slash-menu effect can run; never replace chat logic.
  originalScrollIntoView = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'scrollIntoView')
  Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
    configurable: true, writable: true, value: vi.fn(),
  })
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } },
  })
  client.setQueryData<SlashCommand[]>(['commands', 'web'], [NEW_COMMAND])
  client.setQueryData(['skills'], [])
  client.setQueryData(['agents'], [])
  // Optional picker queries are outside this campaign row. A real rejected
  // fetch leaves their ordinary error handling intact and performs no I/O.
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockRejectedValue(new Error('Composer fixture: optional API is offline.')))
})

afterEach(() => {
  cleanup()
  client.clear()
  resetDialogStores()
  vi.clearAllTimers()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  if (originalScrollIntoView) Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', originalScrollIntoView)
  else Reflect.deleteProperty(HTMLElement.prototype, 'scrollIntoView')
})

function users() {
  return useChatStore.getState().messages.filter((message) => message.role === 'user')
    .map(({ id, content }) => ({ id, content }))
}

function connectAndSendUnconfirmedFirst() {
  const sender = { send: vi.fn<WsConnection['send']>().mockReturnValue(true) }
  act(() => {
    useConnectionStore.getState().setConnection(sender as unknown as WsConnection)
    useConnectionStore.getState().setConnected(true)
    useWorkspacesStore.setState({ activeWorkspaceId: WORKSPACE })
    useChatStore.getState().sendMessage(OLD_CONTENT, { clientMessageId: OLD_ID })
    const chat = useChatStore.getState()
    useConnectionStore.getState().recordDisconnect(chat.isStreaming ? chat.lastAssistantMessageId : null)
    useChatStore.getState().clearStreamingState()
    useConnectionStore.getState().setConnected(true)
  })
  expect(useSessionStore.getState().activeSessionId, 'fixture: foreground is an ordinary pending chat').toBe('__pending')
  expect(useChatStore.getState().pendingKickoff, 'fixture: not a workspace-setup kickoff').toBeNull()
  expect(useChatStore.getState().pendingFirstSend?.status, 'fixture: message delivery is unconfirmed').toBe('unconfirmed')
  expect(users(), 'fixture: original message remains visible after the drop').toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
  expect(sender.send.mock.calls.map(([frame]) => frame), 'fixture: one first send, no automatic retransmission').toStrictEqual([{
    type: 'message', client_message_id: OLD_ID, content: OLD_CONTENT, agent_id: 'mia',
    metadata: { workspace_id: WORKSPACE },
  }])
  return sender
}

function RealComposerHarness() {
  const runtime = useOmnipusRuntime()
  const messages = useChatStore((state) => state.messages)
  return (
    <AssistantRuntimeProvider runtime={runtime}>
      {messages.filter((message) => message.role === 'user').map((message) => (
        <VirtualUserMessageRow
          key={message.id} message={message} skills={[]} commandLabels={['/new']} agentName="Mia" latest
        />
      ))}
      <OmnipusComposer />
    </AssistantRuntimeProvider>
  )
}

async function renderComposer() {
  await act(async () => {
    render(<QueryClientProvider client={client}><RealComposerHarness /></QueryClientProvider>)
  })
  await act(async () => { await vi.advanceTimersByTimeAsync(0) })
  expect(screen.getByRole('combobox', { name: 'Message input' }), 'fixture: actual composer accepts /new').toBeEnabled()
  expect(screen.getByTestId('user-message').querySelector('p')?.textContent,
    'fixture: production message row renders the exact original text').toBe(OLD_CONTENT)
}

function requestNewViaComposer() {
  const input = screen.getByRole('combobox', { name: 'Message input' })
  fireEvent.change(input, { target: { value: '/new' } })
  fireEvent.keyDown(input, { key: 'Enter', code: 'Enter' })
  const dialog = screen.getByRole('alertdialog', { name: 'Start a new chat?' })
  expect(within(dialog).getByText(WARNING, { exact: true }).textContent, 'D8: exact copy advice, not a claim of failed delivery')
    .toBe(WARNING)
  expect(within(dialog).getByRole('button', { name: /^Keep this chat$/ }), 'D8: non-destructive choice is reachable')
    .toBeEnabled()
  expect(within(dialog).getByRole('button', { name: /^Start a new chat$/ }), 'D8: explicit discard choice is reachable')
    .toBeEnabled()
  return dialog
}

describe('#1090 red5 — real /new confirmation for an unconfirmed first message', () => {
  it('D8: /new warns before discarding; Keep this chat retains it, and only Start a new chat clears the bubble and recovery request', async () => {
    const sender = connectAndSendUnconfirmedFirst()
    const originalRequest = structuredClone(useChatStore.getState().pendingFirstSend)
    await renderComposer()

    const keepDialog = requestNewViaComposer()

    expect(users(), 'D8: asking for /new must not silently drop the message').toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
    expect(useChatStore.getState().pendingFirstSend, 'D8: opening confirmation keeps the complete recovery request')
      .toStrictEqual(originalRequest)
    fireEvent.click(within(keepDialog).getByRole('button', { name: /^Keep this chat$/ }))
    expect(screen.queryByRole('alertdialog'), 'D8: Keep this chat dismisses the confirmation').not.toBeInTheDocument()
    expect(useSessionStore.getState().activeSessionId, 'D8: cancelling /new retains the active pending chat').toBe('__pending')
    expect(users(), 'D8: Keep this chat preserves exactly the original message').toStrictEqual([{ id: OLD_ID, content: OLD_CONTENT }])
    expect(useChatStore.getState().pendingFirstSend, 'D8: Keep this chat preserves the original Retry identity and payload')
      .toStrictEqual(originalRequest)
    expect(screen.getByRole('button', { name: /^Retry$/ }), 'D7/D8: keeping the chat retains connected delivery Retry').toBeEnabled()
    expect(sender.send.mock.calls, 'D8: opening/cancelling /new must not send chat text or a recovery request').toHaveLength(1)

    // Drain only already-queued, zero-delay runtime notifications between user
    // actions. The real command clears its composer text before opening the
    // dialog; a fake clock must let that controlled-input update settle before
    // typing the same command again. Never wait after the discard decision.
    const commandInput = screen.getByRole('combobox', { name: 'Message input' }) as HTMLTextAreaElement
    const inputBeforeFlush = commandInput.value
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    console.info('#1090 composer inter-action notification evidence', {
      inputBeforeFlush, inputAfterFlush: commandInput.value,
    })
    expect(commandInput, 'instrument: consumed command is cleared before another user types /new').toHaveValue('')

    const discardDialog = requestNewViaComposer()
    fireEvent.click(within(discardDialog).getByRole('button', { name: /^Start a new chat$/ }))

    // No wait/next tick: D8 requires synchronous cleanup before slot reuse.
    expect(useSessionStore.getState().activeSessionId, 'D8: explicit confirmation selects an empty new chat').toBeNull()
    expect(useChatStore.getState().sessionsById.__pending, 'D8: old pending bucket is gone synchronously').toBeUndefined()
    expect(useChatStore.getState().pendingFirstSend, 'D8: old recovery request is gone synchronously').toBeNull()
    expect(useChatStore.getState().messages, 'D8: explicitly discarded bubble is not promised to remain visible').toStrictEqual([])
    expect(screen.queryByTestId('user-message'), 'D8: production UI no longer renders the discarded first message').not.toBeInTheDocument()
    expect(screen.queryByRole('alertdialog'), 'D8: confirmed /new dismisses the dialog').not.toBeInTheDocument()
    act(() => useChatStore.getState().retryFirstSend())
    expect(sender.send.mock.calls, 'D8: discarded first message cannot be retried from this tab').toHaveLength(1)
    expect(useChatStore.getState().outboundQueue, 'D8: /new is a local decision, not an outbound command/message').toStrictEqual([])
    expect(useChatStore.getState().pendingDrainQueue, 'D8: no old recovery request hides in the drain queue').toStrictEqual([])
  })

  it('D8: an old /new confirmation cannot discard a newer first message that has reused the pending slot', async () => {
    const sender = connectAndSendUnconfirmedFirst()
    await renderComposer()
    const oldDialog = requestNewViaComposer()

    // Another explicit new-chat decision replaced the old attempt before the
    // old modal was answered. Use real actions; the shared key alone is not
    // enough to identify which message the stale choice is allowed to clear.
    act(() => {
      useSessionStore.getState().startNewSession()
      useChatStore.getState().sendMessage(NEW_CONTENT, { clientMessageId: NEW_ID })
    })
    expect(users(), 'fixture: new attempt occupies __pending instead of the old one')
      .toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    const newRequest = structuredClone(useChatStore.getState().pendingFirstSend)
    const newBucket = structuredClone(useChatStore.getState().sessionsById.__pending)
    const sentBefore = structuredClone(sender.send.mock.calls.map(([frame]) => frame))
    expect(sentBefore, 'fixture: exactly the old and new first sends').toHaveLength(2)

    fireEvent.click(within(oldDialog).getByRole('button', { name: /^Start a new chat$/ }))

    expect(screen.queryByRole('alertdialog'), 'D8: stale confirmation can dismiss itself without discarding a different chat')
      .not.toBeInTheDocument()
    expect(useSessionStore.getState().activeSessionId, 'D8: stale old decision cannot override the new foreground').toBe('__pending')
    expect(users(), 'D8: stale old decision preserves exactly the newer message').toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    expect(useChatStore.getState().pendingFirstSend, 'D8: stale old decision cannot erase the new Retry identity')
      .toStrictEqual(newRequest)
    expect(useChatStore.getState().sessionsById.__pending, 'D8: stale old decision cannot clear/change the next pending bucket')
      .toStrictEqual(newBucket)
    expect(getMessages(useChatStore.getState().sessionsById.__pending)
      .filter((message) => message.role === 'user').map(({ id, content }) => ({ id, content })),
    'D8: authoritative bucket still owns only the newer first message').toStrictEqual([{ id: NEW_ID, content: NEW_CONTENT }])
    expect(sender.send.mock.calls.map(([frame]) => frame), 'D8: stale dialog cannot cause another send or attach')
      .toStrictEqual(sentBefore)
  })
})

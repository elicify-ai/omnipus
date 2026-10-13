// Hands-on UAT row S7 (2026-10-07, train f0034f3), founder ruling 2026-10-07:
// a /stop-redirect turn is a redirect, NOT an interruption. Typed in a chat
// that is streaming a long answer, the streamed partial text must stay on
// screen and carry NO "(interrupted)" marker, and the stop sentence must not
// replace it. A plain Stop keeps "(interrupted)" exactly as before.
// What the gateway really emits when the redirected turn stops: one typed
// `error` frame (code turn_canceled, message = the catalogue sentence,
// pkg/gateway/websocket_forward_hub.go::hubError), then `done`
// (pkg/agent/stop_redirect_root.go::redirectOrdinarySession stops the turn
// through the one Stop, then continues with the instruction).
// REAL: OmnipusComposer, useSlashMenu, chat/session/connection stores,
// handleFrame. FAKE: the socket only (and the AssistantUI primitives).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { pendingRedirectSids } from '@/store/chat/runtime-state'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

import { OmnipusComposer } from './ChatScreen'

// Presentation/runtime adapter only: stop/cancel interception and frame
// construction stay real through OmnipusComposer, both hooks and the stores.
const composerRuntime = vi.hoisted(() => {
  let text = ''
  return {
    getState: () => ({ text }),
    setText: vi.fn((value: string) => { text = value }),
    send: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  }
})

import { MockButton } from '@/test/assistantUiMock'
vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  ThreadPrimitive: {
    Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  ComposerPrimitive: {
    Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
              React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
    Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string;
              'data-testid'?: string; 'aria-label'?: string;
            }) =>
              React.createElement(MockButton, {
                type: 'button', disabled, className,
                'data-testid': testId ?? 'chat-send',
                'aria-label': ariaLabel, children: children}),
    AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
              disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
            }) =>
              React.createElement(MockButton, { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment', children: children}),
  },
  AttachmentPrimitive: {
    Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
              React.createElement(MockButton, { type: 'button', className, 'aria-label': ariaLabel, children: children}),
  },
  ActionBarPrimitive: {
    Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
              React.createElement('div', { className }, children),
  },
  useComposerRuntime: vi.fn(() => composerRuntime),
  useMessage: () => ({
          id: 'msg_1',
          role: 'assistant',
          status: { type: 'complete' },
          content: [],
        }),
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'model',   label: '/model',   description: 'Change the chat model',       delivery: 'client', available_while_streaming: false },
    { name: 'agents',  label: '/agents',  description: 'Open agent selector',         delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
    { name: 'stop',    label: '/stop',    description: 'Stop only this conversation', delivery: 'client', available_while_streaming: true  },
    { name: 'stop-redirect', label: '/stop-redirect', description: 'Replace this chat\'s turn', delivery: 'client', available_while_streaming: true, argument_hint: '<instruction>' },
  ]
  const mockSkills = [
    { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
    { id: 'code-review',   name: 'Code Review',   version: '1.0', description: 'Reviews code quality',      verified: true,  status: 'active' },
    { id: 'data-analysis', name: 'Data Analysis', version: '1.0', description: 'Analyses datasets',         verified: false, status: 'active' },
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: mockSkills, isError: false, refetch: vi.fn() }
      }
      return { data: [], isError: false, refetch: vi.fn() }
    },
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

import type { ErrorFrame } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { getMessages } from '@/store/chat'

const SID = 'sess_test'
const PARTIAL = 'Here is the first long part of a very long answer. '.repeat(40)

let send: ReturnType<typeof vi.fn>

function submitCommand(text: string) {
  const input = screen.getByTestId('composer-input')
  act(() => {
    composerRuntime.setText(text)
    fireEvent.change(input, { target: { value: text } })
    fireEvent.submit(screen.getByTestId('composer-form'))
  })
}

const handle = (f: Parameters<ReturnType<typeof useChatStore.getState>['handleFrame']>[0]) =>
  act(() => { useChatStore.getState().handleFrame(f) })

function streamPartial() {
  handle({ type: 'token', session_id: SID, agent_id: 'jim', content: PARTIAL })
}

function typedError(code: 'turn_canceled' | 'turn_timed_out'): ErrorFrame {
  const message = codeToDisplay[code]
  return { type: 'error', session_id: SID, message, payload: { llm_error: { code, message, retryable: true } } }
}

// What the gateway emits when a turn is stopped: the typed error, then done.
function deliverTurnStopped() {
  handle(typedError('turn_canceled'))
  handle({ type: 'done', session_id: SID })
}

const assistants = () => getMessages(useChatStore.getState().sessionsById[SID]).filter((m) => m.role === 'assistant')

/** A second turn started from a normal send, streaming `text`. */
function startNextTurn(text: string) {
  act(() => { useChatStore.getState().sendMessage('next question') })
  handle({ type: 'token', session_id: SID, agent_id: 'jim', content: text })
}

function lastAssistant() {
  const all = assistants()
  const m = all[all.length - 1]
  expect(m, 'an assistant message exists').toBeDefined()
  return m
}

beforeEach(() => {
  send = vi.fn().mockReturnValue(true)
  composerRuntime.setText('')
  composerRuntime.send.mockClear()
  pendingRedirectSids.clear()
  act(() => {
    useConnectionStore.setState({
      connection: { send } as unknown as ReturnType<typeof useConnectionStore.getState>['connection'],
      isConnected: true, connectionError: null,
    })
    useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'jim', activeAgentType: null, attachedSessionType: null })
    useChatStore.setState(useChatStore.getInitialState(), true)
  })
})

afterEach(() => {
  pendingRedirectSids.clear()
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
    useSessionStore.setState({ activeSessionId: null })
  })
})

describe('S7 live — /stop-redirect on a streaming chat', () => {
  it('keeps the partial text, finalised done, no (interrupted) marker, no stop sentence', () => {
    render(<OmnipusComposer />)
    streamPartial()
    expect(useChatStore.getState().isStreaming, 'fixture: a turn is streaming').toBe(true)

    submitCommand('/stop-redirect now just say the word mango')
    expect(send.mock.calls.map((c) => c[0]), 'fixture: the real redirect frame left').toEqual([
      { type: 'redirect', session_id: SID, instruction: 'now just say the word mango' },
    ])
    expect(pendingRedirectSids.has(SID), 'the redirect is remembered').toBe(true)

    deliverTurnStopped()

    expect(assistants().map((m) => m.content), 'the partial text is the only assistant text, unchanged').toEqual([PARTIAL])
    const m = lastAssistant()
    expect(m.status).toBe('done')
    expect(m.isStreaming).toBe(false)
    expect(getMessageStatusSuffix(m), 'no "(interrupted)" marker').toBeNull()
    expect(useChatStore.getState().isStreaming).toBe(false)
    expect(pendingRedirectSids.has(SID), 'flag consumed').toBe(false)
  })

  it('control: a plain Stop keeps the partial text WITH "(interrupted)" and clears a pending redirect', () => {
    render(<OmnipusComposer />)
    streamPartial()
    submitCommand('/stop-redirect x')
    expect(pendingRedirectSids.has(SID)).toBe(true)
    act(() => { fireEvent.click(screen.getByTestId('stop-btn')) })
    expect(pendingRedirectSids.has(SID), 'a Stop supersedes the redirect').toBe(false)
    deliverTurnStopped()

    const m = assistants().find((a) => a.content === PARTIAL)
    expect(m, 'the partial text survives a plain Stop').toBeDefined()
    expect(getMessageStatusSuffix(m!)).toBe('(interrupted)')
  })

  it('idle chat: a redirect with no streaming turn remembers nothing', () => {
    render(<OmnipusComposer />)
    expect(useChatStore.getState().isStreaming, 'fixture: idle').toBe(false)
    submitCommand('/stop-redirect do it')
    expect(send.mock.calls.map((c) => c[0])).toEqual([{ type: 'redirect', session_id: SID, instruction: 'do it' }])
    expect(pendingRedirectSids.has(SID)).toBe(false)
  })

  it('redirect → natural done → a later turn_canceled from another tab shows "(interrupted)"', () => {
    render(<OmnipusComposer />)
    streamPartial()
    submitCommand('/stop-redirect x')
    handle({ type: 'done', session_id: SID }) // the turn had already finished
    expect(pendingRedirectSids.has(SID), 'done clears the flag').toBe(false)

    startNextTurn('second answer partial')
    handle(typedError('turn_canceled')) // Stop pressed in another tab
    handle({ type: 'done', session_id: SID })

    const m = lastAssistant()
    expect(m.content).toBe('second answer partial')
    expect(m.status).toBe('interrupted')
    expect(getMessageStatusSuffix(m)).toBe('(interrupted)')
  })

  it('a genuine non-cancel error while a redirect is pending shows the error and clears the flag', () => {
    render(<OmnipusComposer />)
    streamPartial()
    submitCommand('/stop-redirect x')
    handle(typedError('turn_timed_out'))
    handle({ type: 'done', session_id: SID })

    expect(lastAssistant().status, 'the error is shown, not hidden as a redirect').toBe('error')
    expect(pendingRedirectSids.has(SID)).toBe(false)

    startNextTurn('after the error')
    deliverTurnStopped() // later Stop from another tab
    const m = lastAssistant()
    expect(m.content).toBe('after the error')
    expect(getMessageStatusSuffix(m)).toBe('(interrupted)')
  })

  it('a socket drop mid-redirect forgets the redirect; a later Stop shows "(interrupted)"', () => {
    render(<OmnipusComposer />)
    streamPartial()
    submitCommand('/stop-redirect x')
    expect(pendingRedirectSids.has(SID)).toBe(true)
    act(() => { useChatStore.getState().clearStreamingState() })
    expect(pendingRedirectSids.has(SID), 'drop clears the flag').toBe(false)

    startNextTurn('post-reconnect partial')
    deliverTurnStopped()
    expect(getMessageStatusSuffix(lastAssistant())).toBe('(interrupted)')
  })

  it('redirect before the first token: the empty placeholder ends done and invisible — no marker, no stop sentence', () => {
    // Pinned current behaviour. The user sees their own message and then the
    // continuation; nothing is shown for the redirected-away turn (an empty
    // finished bubble renders nothing — ChatScreen isTerminalEmpty).
    render(<OmnipusComposer />)
    act(() => { useChatStore.getState().sendMessage('first question') })
    expect(useChatStore.getState().isStreaming, 'fixture: placeholder streaming, no token yet').toBe(true)
    submitCommand('/stop-redirect x')
    deliverTurnStopped()

    const m = lastAssistant()
    expect(m.content).toBe('')
    expect(m.status).toBe('done')
    expect(m.isStreaming).toBe(false)
    expect(getMessageStatusSuffix(m)).toBeNull()
    expect(assistants().filter((a) => a.status === 'error'), 'no error bubble').toHaveLength(0)
  })

  // KNOWN LIMIT: a turn_canceled carries no cause. If another tab or channel
  // presses Stop while THIS tab's redirect is pending, the first turn_canceled
  // this tab sees is attributed to the redirect, so it shows as a redirected
  // turn (no "(interrupted)") instead of a stopped one. Pinned, not endorsed.
  it('KNOWN LIMIT: another tab\'s Stop while this tab\'s redirect is pending shows as redirected', () => {
    render(<OmnipusComposer />)
    streamPartial()
    submitCommand('/stop-redirect x')
    deliverTurnStopped() // actually caused by the other tab's Stop
    const m = lastAssistant()
    expect(m.status).toBe('done')
    expect(getMessageStatusSuffix(m)).toBeNull()
  })
})

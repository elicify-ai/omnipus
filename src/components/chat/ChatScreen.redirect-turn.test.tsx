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
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

import type { ErrorFrame } from '@/lib/api/generated/asyncapi-types'
import { codeToDisplay } from '@/lib/llm-error'
import { getMessageStatusSuffix } from '@/lib/truncation'
import { getMessages } from '@/store/chat'

const SID = 'sess_test'
const PARTIAL = 'Here is the first long part of a very long answer. '.repeat(40)
const STOP_COPY = codeToDisplay.turn_canceled

let send: ReturnType<typeof vi.fn>

function submitCommand(text: string) {
  const input = screen.getByTestId('composer-input')
  act(() => {
    composerRuntime.setText(text)
    fireEvent.change(input, { target: { value: text } })
    fireEvent.submit(screen.getByTestId('composer-form'))
  })
}

function streamPartial() {
  act(() => {
    useChatStore.getState().handleFrame({ type: 'token', session_id: SID, agent_id: 'jim', content: PARTIAL })
  })
}

function deliverTurnStopped() {
  const stopped: ErrorFrame = {
    type: 'error', session_id: SID, message: STOP_COPY,
    payload: { llm_error: { code: 'turn_canceled', message: STOP_COPY, retryable: true } },
  }
  act(() => {
    useChatStore.getState().handleFrame(stopped)
    useChatStore.getState().handleFrame({ type: 'done', session_id: SID })
  })
}

const assistants = () => getMessages(useChatStore.getState().sessionsById[SID]).filter((m) => m.role === 'assistant')

beforeEach(() => {
  send = vi.fn().mockReturnValue(true)
  composerRuntime.setText('')
  composerRuntime.send.mockClear()
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
  act(() => {
    useChatStore.setState(useChatStore.getInitialState(), true)
    useConnectionStore.setState({ connection: null, isConnected: true, connectionError: null })
    useSessionStore.setState({ activeSessionId: null })
  })
})

describe('S7 live — /stop-redirect on a streaming chat', () => {
  it('keeps the partial text, shows no (interrupted) marker, and no stop-sentence replaces it', () => {
    render(<OmnipusComposer />)
    streamPartial()
    expect(useChatStore.getState().isStreaming, 'fixture: a turn is streaming').toBe(true)

    submitCommand('/stop-redirect now just say the word mango')
    expect(send.mock.calls.map((c) => c[0]), 'fixture: the real redirect frame left').toEqual([
      { type: 'redirect', session_id: SID, instruction: 'now just say the word mango' },
    ])

    deliverTurnStopped()

    const msgs = assistants()
    expect(msgs.map((m) => m.content), 'the partial text is the only assistant text, unchanged').toEqual([PARTIAL])
    expect(msgs[0].status).not.toBe('interrupted')
    expect(msgs[0].status).not.toBe('error')
    expect(getMessageStatusSuffix(msgs[0]), 'no "(interrupted)" marker').toBeNull()
    expect(useChatStore.getState().isStreaming).toBe(false)
  })

  it('control: a plain Stop keeps the partial text WITH "(interrupted)"', () => {
    render(<OmnipusComposer />)
    streamPartial()
    act(() => { fireEvent.click(screen.getByTestId('stop-btn')) })
    deliverTurnStopped()

    const partial = assistants().find((m) => m.content === PARTIAL)
    expect(partial, 'the partial text survives a plain Stop').toBeDefined()
    expect(getMessageStatusSuffix(partial!)).toBe('(interrupted)')
  })
})

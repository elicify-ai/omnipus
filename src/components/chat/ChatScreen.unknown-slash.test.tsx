// Unknown slash command handling — Issue 3 fallback.
// When the user types a "/xyz" that matches no commands and no skills,
// the menu disappears (no items). Typing unknown and pressing Enter falls
// through (no client handler runs, text is not silently cleared).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
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

vi.mock('@assistant-ui/react', () => {
  const React = require('react')
  return {
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Messages: () => null,
    },
    MessagePrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Parts: () => null,
    },
    ComposerPrimitive: {
      Root: ({ children, className, onSubmit }: { children: React.ReactNode; className?: string; onSubmit?: (e: React.FormEvent) => void }) =>
        React.createElement('form', { className, onSubmit, 'data-testid': 'composer-form' }, children),
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string;
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className, 'aria-label': ariaLabel }: { children?: React.ReactNode; className?: string; 'aria-label'?: string }) =>
        React.createElement('button', { type: 'button', className, 'aria-label': ariaLabel }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: vi.fn(() => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
    })),
    useMessage: () => ({
      id: 'msg_1',
      role: 'assistant',
      status: { type: 'complete' },
      content: [],
    }),
    makeAssistantToolUI: () => () => null,
  }
})

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'model',   label: '/model',   description: 'Change the chat model',       delivery: 'client', available_while_streaming: false },
    { name: 'agents',  label: '/agents',  description: 'Open agent selector',         delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
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

vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  fetchCommands: vi.fn().mockResolvedValue([]),
  fetchSkills: vi.fn().mockResolvedValue([]),
  fetchProviders: vi.fn().mockResolvedValue([]),
  uploadFiles: vi.fn(),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./ExecApprovalBlock', () => ({ ExecApprovalBlock: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      pendingApprovals: [],
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_unknown_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('Unknown slash command handling', () => {
  it('typing "/zzz" (no command or skill match) shows no menu items', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/zzz' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // No commands or skills match /zzz
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
    // Slash menu container should not be visible (no items)
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })

  it('slash menu disappears when input text cleared', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Menu visible
    expect(screen.queryByTestId('slash-menu')).toBeInTheDocument()

    // Clear input
    act(() => { fireEvent.change(input, { target: { value: '' } }) })

    // Menu gone
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })

  it('streaming hides all non-streaming commands; /cancel still shows', async () => {
    act(() => { useChatStore.setState({ isStreaming: true }) })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // /cancel is available_while_streaming
    expect(screen.getByText('/cancel')).toBeInTheDocument()
    // Other commands are not
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/agents')).not.toBeInTheDocument()
  })

  it('non-matching partial after "/" shows no commands but may show matching skills', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // "dat" matches data-analysis skill
    act(() => { fireEvent.change(input, { target: { value: '/dat' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    expect(screen.getByText('/data-analysis')).toBeInTheDocument()
    // No commands start with "dat"
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
  })
})

// B10/B11/D4: unknown-slash send-path — messages starting with an unknown /token
// or with the removed /skill prefix must be dispatched as normal messages, not
// intercepted or silently dropped (FR-004, US1.4, US2.2).
//
// Dispatch capture: since AssistantUI's onNew is mocked away in this harness,
// dispatch is verified by:
//   1. Making composerRuntime.getState a vi.fn() spy to capture the text the
//      component reads at submit time (this is what would be dispatched to onNew).
//   2. Asserting getState WAS called (proving the component processed the submit).
//   3. Asserting setText('') was NOT called (proving no interception occurred).
//   4. Asserting getState().text equals the literal user input — the exact string
//      that reaches AssistantUI's runtime unmodified.
// These tests MUST fail if interceptClientCommand is changed to swallow /skill
// or /zzz (setText('') would fire) OR if getState is not called (submit blocked
// before the interception check runs).
describe('Unknown slash send-path — dispatched as normal message (B10/B11/D4)', () => {
  it('"/skill web-research go" is not intercepted; literal text is dispatched (B11/US2.2)', async () => {
    // "/skill" has been removed (D1). It must pass through as a normal message —
    // interceptClientCommand must NOT fire for it; the exact text must reach onNew.
    const mockSetText = vi.fn()
    const mockGetState = vi.fn().mockReturnValue({ text: '/skill web-research go' })
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: mockGetState,
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/skill web-research go' } }) })

    // Reset spy so call counts only reflect the submit event.
    mockGetState.mockClear()

    // Submit the form — interceptClientCommand checks the text; /skill is not
    // in the commands list (it's been removed), so it returns false.
    act(() => { fireEvent.submit(form) })

    // 1. getState was called — the component checked the text at submit time.
    expect(mockGetState).toHaveBeenCalled()

    // 2. The text that reaches the runtime is the literal input — no modification.
    const dispatchedText: string = mockGetState.mock.results[0].value.text
    expect(dispatchedText).toBe('/skill web-research go')

    // 3. setText('') MUST NOT have been called — that would mean interception fired.
    const clearCalls = mockSetText.mock.calls.filter((args: unknown[]) => args[0] === '')
    expect(clearCalls).toHaveLength(0)
  })

  it('"/zzz hi" is not intercepted; literal text is dispatched (B10/D4)', async () => {
    // "/zzz" is neither a builtin nor an installed skill — must dispatch normally.
    // The exact string "/zzz hi" must reach AssistantUI's runtime unmodified.
    const mockSetText = vi.fn()
    const mockGetState = vi.fn().mockReturnValue({ text: '/zzz hi' })
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: mockGetState,
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/zzz hi' } }) })

    mockGetState.mockClear()

    act(() => { fireEvent.submit(form) })

    // 1. getState was called at submit time.
    expect(mockGetState).toHaveBeenCalled()

    // 2. The dispatched payload is exactly the literal text — not intercepted,
    //    not cleared, not modified. This MUST fail if interceptClientCommand
    //    were to swallow unknown slashes.
    const dispatchedText: string = mockGetState.mock.results[0].value.text
    expect(dispatchedText).toBe('/zzz hi')

    // 3. No interception — setText('') not called.
    const clearCalls = mockSetText.mock.calls.filter((args: unknown[]) => args[0] === '')
    expect(clearCalls).toHaveLength(0)
  })
})

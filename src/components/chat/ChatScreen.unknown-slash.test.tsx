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
describe('Unknown slash send-path — dispatched as normal message (B10/B11/D4)', () => {
  it('"/skill web-research go" is not intercepted by the client command handler (B11/US2.2)', async () => {
    // "/skill" has been removed (D1). It must pass through as a normal message —
    // interceptClientCommand must NOT fire for it.
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/skill web-research go' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/skill web-research go' } }) })

    // Submit the form — interceptClientCommand checks the text; /skill is not
    // in the commands list (it's been removed), so it returns false.
    // The form submit should NOT clear the input via mockSetText('').
    act(() => { fireEvent.submit(form) })

    // The text must NOT have been cleared by interceptClientCommand.
    // If it was intercepted, mockSetText would have been called with ''.
    // (completeSkillName also calls setText but with `/<id> ` not '')
    const clearCalls = mockSetText.mock.calls.filter((args: string[]) => args[0] === '')
    expect(clearCalls).toHaveLength(0)
  })

  it('"/zzz hi" is not intercepted by the client command handler (B10/D4)', async () => {
    // "/zzz" is neither a builtin nor an installed skill — must dispatch normally.
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/zzz hi' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const form = screen.getByTestId('composer-form')
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/zzz hi' } }) })
    act(() => { fireEvent.submit(form) })

    // No intercept: mockSetText must not have been called with '' (the intercept cleanup)
    const clearCalls = mockSetText.mock.calls.filter((args: string[]) => args[0] === '')
    expect(clearCalls).toHaveLength(0)
  })
})

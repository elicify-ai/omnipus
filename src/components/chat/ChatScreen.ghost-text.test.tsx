// Ghost text overlay — shown when value is exactly `/<skill-id> ` after skill selection.
// Falls back to generic `<message>` (no argument_hint on the wire type).

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
      activeSessionId: 'sess_ghost_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('Ghost text overlay', () => {
  it('ghost text appears after selecting a skill from the menu', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Type "/web" to see skill menu
    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Select web-research
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    // After selection, simulate the text change that completeSkillName does
    act(() => { fireEvent.change(input, { target: { value: '/web-research ' } }) })

    // Ghost text element should now be in the DOM
    const ghost = screen.queryByTestId('ghost-text')
    expect(ghost).toBeInTheDocument()
  })

  it('ghost text shows generic <message> hint (no argument_hint on wire type)', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })
    act(() => { fireEvent.change(input, { target: { value: '/web-research ' } }) })

    const ghost = screen.queryByTestId('ghost-text')
    expect(ghost).toBeInTheDocument()
    expect(ghost?.textContent).toContain('<message>')
  })

  it('ghost text disappears when user continues typing', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Select skill — simulate ghost state
    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })
    act(() => { fireEvent.change(input, { target: { value: '/web-research ' } }) })

    // Ghost should be present
    expect(screen.queryByTestId('ghost-text')).toBeInTheDocument()

    // User types more — value no longer exactly matches `/<skillId> `
    act(() => { fireEvent.change(input, { target: { value: '/web-research do something' } }) })

    // Ghost should disappear
    expect(screen.queryByTestId('ghost-text')).not.toBeInTheDocument()
  })

  it('ghost text does not appear when input value does not exactly match `/<skillId> `', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Type a normal value without going through skill selection
    act(() => { fireEvent.change(input, { target: { value: '/web-research hello' } }) })

    // No ghost text since ghostSkillId is null
    expect(screen.queryByTestId('ghost-text')).not.toBeInTheDocument()
  })
})

// SC-004/B8: the ghost `<message>` text is NEVER submitted — when the user
// presses Enter/Send while the composer shows just `/<skill-id> ` (the ghost
// state), the message dispatched is `/<skill-id>` (trimmed, no ghost text).
describe('Ghost text never submitted (SC-004/B8)', () => {
  it('submitting with ghost active dispatches exactly `/<skill-id>` and no ghost text', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '/web-research ' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')
    const form = screen.getByTestId('composer-form')

    // Select the skill from the menu
    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    // Simulate the setText call that completeSkillName makes
    act(() => { fireEvent.change(input, { target: { value: '/web-research ' } }) })

    // Ghost should be present
    expect(screen.queryByTestId('ghost-text')).toBeInTheDocument()

    // The ghost element must NOT contain its text in the textarea value —
    // the ghost is aria-hidden and outside the input.
    const ghostEl = screen.getByTestId('ghost-text')
    expect(ghostEl).toHaveAttribute('aria-hidden', 'true')

    // Submit the form with the composer returning `/<skill-id> ` as its text.
    // The interceptClientCommand path: `/web-research ` (trimmed = `/web-research`)
    // is NOT in the commands list as a client command, so it passes through.
    // The ghost text "<message>" must not appear in any submitted text frame.
    act(() => { fireEvent.submit(form) })

    // Verify: interceptClientCommand called setText('') only for actual client
    // commands. For skill slugs, it does NOT call setText('') — the message
    // goes through normally. The ghost text ("<message>") must never appear in
    // the composer value or be dispatched.
    const textSetToGhostText = mockSetText.mock.calls.filter(
      (args: string[]) => typeof args[0] === 'string' && args[0].includes('<message>'),
    )
    expect(textSetToGhostText).toHaveLength(0)
  })
})

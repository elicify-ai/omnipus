// T15: slash menu shows /cancel during streaming (FR-3a)
// Also covers: non-streaming shows all commands; commands without
// availableWhileStreaming are hidden during streaming.
//
// Traces to: docs/internal/specs/cancel-cross-channel-spec.md FR-3a

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

// ── Mocks ─────────────────────────────────────────────────────────────────────

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
        'data-testid'?: string; 'aria-label'?: string
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
    // useComposerRuntime is vi.fn() so tests that need text can override the
    // return value with mockImplementation/mockReturnValue per test.
    // The default returns empty text — matching the original behavior.
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
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
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
  createSession: vi.fn(),
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

// ── Store reset ───────────────────────────────────────────────────────────────

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
      activeSessionId: 'sess_cancel_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

// ── T15: slash menu shows /cancel during streaming ────────────────────────────

describe('T15: slash menu — /cancel available during streaming (FR-3a)', () => {
  it('shows /cancel in the slash menu when streaming and input is "/"', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    // Type "/" to trigger slash menu
    act(() => {
      fireEvent.change(input, { target: { value: '/' } })
    })

    // Slash menu must be visible (slashOpen = true requires an ArrowDown or change event)
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    // /cancel must appear
    expect(screen.getByText('/cancel')).toBeInTheDocument()
  })

  it('does NOT show non-streaming commands (/clear, /help, /session new) when streaming', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    act(() => {
      fireEvent.change(input, { target: { value: '/' } })
    })
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    // These commands must NOT appear while streaming
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/help')).not.toBeInTheDocument()
    expect(screen.queryByText('/session new')).not.toBeInTheDocument()
  })

  it('shows all commands (including /cancel) when NOT streaming', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: false })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    act(() => {
      fireEvent.change(input, { target: { value: '/' } })
    })
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    expect(screen.getByText('/cancel')).toBeInTheDocument()
    expect(screen.getByText('/clear')).toBeInTheDocument()
    expect(screen.getByText('/help')).toBeInTheDocument()
    expect(screen.getByText('/session new')).toBeInTheDocument()
  })

  it('does not show slash menu at all when streaming and there is no matching streaming-safe command', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    // Type "/clear" — no streaming-safe command matches
    act(() => {
      fireEvent.change(input, { target: { value: '/clear' } })
    })
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/cancel')).not.toBeInTheDocument()
  })
})

// NOTE: the former "Fix #1 — all uploads fail" and "#252 — upload before first
// message" tests were removed when the hand-rolled upload bridge
// (handleSendWithFiles + the hidden file-input) was replaced by the native
// AssistantUI AttachmentAdapter. The equivalent behaviors — session
// auto-creation on first upload, and surfacing a failed registration rather
// than silently dropping the file — now live in src/lib/attachment-adapter.ts
// and are covered by src/lib/attachment-adapter.test.ts.

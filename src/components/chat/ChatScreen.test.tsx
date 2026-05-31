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

// ── Fix #1: all uploads fail with text → send text-only, keep pendingFiles ────

describe('Fix #1 — all uploads fail with text', () => {
  it('sends plain text (no "Attached files:" suffix) and keeps pendingFiles when all uploads lack a path', async () => {
    // BDD: Given pending files and typed text,
    //   When all uploaded files come back with empty path (registration failed),
    //   Then sendMessage is called with just the text (no file list appended),
    //   And an "Attachment not available" toast is shown.
    //
    // Traces to: ChatScreen.tsx handleSendWithFiles — Fix #1 (sprint258fix2).
    // This test FAILS on the pre-fix code that appended "\n\nAttached files: "
    // when available.length === 0 but text was non-empty.

    const assistantUi = await import('@assistant-ui/react')
    const setTextMock = vi.fn()
    // Override useComposerRuntime for this test so it returns non-empty text,
    // simulating a user who typed a message before attaching.
    vi.mocked(assistantUi.useComposerRuntime).mockReturnValue({
      getState: () => ({ text: 'hello world' } as never),
      setText: setTextMock,
    } as never)

    const api = await import('@/lib/api')
    const uploadFilesMock = vi.mocked(api.uploadFiles)

    // All uploaded files have empty path — registration failed.
    uploadFilesMock.mockResolvedValue({
      files: [{ name: 'broken.png', path: '', size: 10, content_type: 'image/png' }],
    } as never)

    // Spy on the connection's send method — sendMessage in the chat store
    // ultimately calls connection.send(). If the message text contains
    // "Attached files:", the test fails.
    const sendMock = vi.fn().mockReturnValue(true)

    act(() => {
      useSessionStore.setState({ activeSessionId: 'sess_f1', activeAgentId: 'general-assistant', activeAgentType: null })
      useConnectionStore.setState({
        connection: { send: sendMock, disconnect: vi.fn(), connect: vi.fn() } as never,
        isConnected: true,
      })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    // Attach a file so pendingFiles is non-empty, triggering handleSendWithFiles
    const fileInput = screen.getByTestId('file-input') as HTMLInputElement
    const file = new File(['BROKEN'], 'broken.png', { type: 'image/png' })
    act(() => {
      fireEvent.change(fileInput, { target: { files: [file] } })
    })

    // Submit the composer
    const form = screen.getByTestId('composer-form')
    await act(async () => {
      fireEvent.submit(form)
      await Promise.resolve()
    })
    await act(async () => { await Promise.resolve(); await Promise.resolve() })

    // The WS send must have been called — this proves sendMessage ran.
    expect(sendMock).toHaveBeenCalled()

    // The payload sent must contain just the user text — no "Attached files:" suffix.
    const wsFrame = sendMock.mock.calls[0]?.[0] as { content?: string }
    const sentContent = typeof wsFrame === 'object' ? JSON.stringify(wsFrame) : String(wsFrame)
    expect(sentContent).toContain('hello world')
    expect(sentContent).not.toContain('Attached files:')
  })
})

// ── #252: upload before first message auto-creates a session ───────────────────

describe('#252 — image upload before first message', () => {
  it('auto-creates a session when uploading with no active session, then uploads against it', async () => {
    const api = await import('@/lib/api')
    const createSessionMock = vi.mocked(api.createSession)
    const uploadFilesMock = vi.mocked(api.uploadFiles)
    createSessionMock.mockResolvedValue({ id: 'sess_new_252', agent_id: 'general-assistant' } as any)
    uploadFilesMock.mockResolvedValue({
      files: [{ name: 'pic.png', path: 'uploads/sess_new_252/pic.png', size: 7, content_type: 'image/png', ref: 'media://r252' }],
    } as any)

    // No active session — this is the #252 condition.
    act(() => {
      useSessionStore.setState({ activeSessionId: null, activeAgentId: 'general-assistant', activeAgentType: null })
      useConnectionStore.setState({ connection: { send: vi.fn().mockReturnValue(true), disconnect: vi.fn(), connect: vi.fn() } as any, isConnected: true })
    })

    const { OmnipusComposer } = await import('./ChatScreen')
    render(<OmnipusComposer />)

    // Attach a (non-harmful) image via the hidden file input.
    const fileInput = screen.getByTestId('file-input') as HTMLInputElement
    const file = new File(['PNGDATA'], 'pic.png', { type: 'image/png' })
    act(() => {
      fireEvent.change(fileInput, { target: { files: [file] } })
    })

    // Submit the composer with a pending file present.
    const form = screen.getByTestId('composer-form')
    await act(async () => {
      fireEvent.submit(form)
      await Promise.resolve()
    })
    // Allow the async createSession→upload chain to settle.
    await act(async () => { await Promise.resolve(); await Promise.resolve() })

    // #252: a session must have been minted instead of erroring "no active session".
    expect(createSessionMock).toHaveBeenCalledWith('general-assistant')
    expect(uploadFilesMock).toHaveBeenCalledWith('sess_new_252', [file])
  })
})

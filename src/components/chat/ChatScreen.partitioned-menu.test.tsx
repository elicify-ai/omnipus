// Partitioned slash menu — FR-005
// Tests that "/" opens a menu with Commands + Skills sections, section headers,
// keyboard navigation crossing sections, and filtering.

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

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
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

// importOriginal: useSlashMenu now calls useChatAgents unconditionally (the
// "@" mention menu — src/hooks/useChatAgents.ts), which needs real
// `fetchWorkspaces`/`workspacesQueryKeys`/`isWorker` even though this file
// doesn't exercise mentions. The workspaces query stays disabled (no
// activeWorkspaceId set here), so `fetchWorkspaces` is never actually
// invoked — this just needs to exist so the module loads.
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
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): the partitioned slash menu is independent of
// the picker/model/token sub-components — stub them to null so their
// workspaces/providers query plumbing doesn't need mocking here.
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

function resetStores() {
  act(() => {
    useChatStore.setState({
      messages: [],
      isStreaming: false,
      isReplaying: false,
      toolCalls: {},
      sessionTokens: 0,
      sessionCost: 0,
    })
    useConnectionStore.setState({
      connection: null,
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('Partitioned slash menu — section headers', () => {
  it('shows "Commands" section header when "/" typed', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Commands section header must be visible
    expect(screen.getByText('Commands')).toBeInTheDocument()
  })

  it('shows "Skills" section header when "/" typed', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Skills section header must be visible
    expect(screen.getByText('Skills')).toBeInTheDocument()
  })

  it('shows both commands and skills when "/" typed', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Commands section
    expect(screen.getByText('/clear')).toBeInTheDocument()
    expect(screen.getByText('/cancel')).toBeInTheDocument()
    // Skills section
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.getByText('/code-review')).toBeInTheDocument()
  })
})

describe('Partitioned slash menu — filtering', () => {
  it('typing "/web" filters commands by prefix and shows only matching skills', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // No commands start with "web"
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    // web-research skill starts with "web"
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    // Other skills don't match
    expect(screen.queryByText('/code-review')).not.toBeInTheDocument()
  })

  it('empty commands section is hidden when no commands match', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Commands section header should not appear when no commands match
    expect(screen.queryByText('Commands')).not.toBeInTheDocument()
    // Skills header still appears
    expect(screen.getByText('Skills')).toBeInTheDocument()
  })

  it('typing "/skills" hides Commands section entirely (D9 filter)', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/skills' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Commands section must be absent
    expect(screen.queryByText('Commands')).not.toBeInTheDocument()
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    // All skills must appear
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.getByText('/code-review')).toBeInTheDocument()
    expect(screen.getByText('/data-analysis')).toBeInTheDocument()
  })

  it('menu closes when Escape pressed', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Menu is visible
    expect(screen.getByText('/clear')).toBeInTheDocument()

    // Escape closes it
    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })

    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
  })
})

// Fix 3 (bugfixes3 review): Escape must close an open menu BEFORE it
// cancels a streaming turn — previously cancel-Escape ran first, so Escape
// with the menu open mid-stream killed the generation and left the menu
// open. Captures the real `cancelStream` once so it can be restored after
// the stub — `useChatStore` is a real (unmocked) singleton store shared
// across this whole file, and a stray stub would otherwise leak into every
// test that runs after this one.
const realCancelStream = useChatStore.getState().cancelStream

describe('Escape precedence — menu-close wins over stream-cancel (Fix 3)', () => {
  afterEach(() => {
    act(() => { useChatStore.setState({ cancelStream: realCancelStream }) })
  })

  it('menu open + streaming: first Escape closes the menu only (no cancel); second Escape cancels', async () => {
    const cancelStream = vi.fn()
    act(() => { useChatStore.setState({ isStreaming: true, cancelStream }) })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    // Only available_while_streaming commands render while isStreaming is
    // true (see useSlashMenu.ts's visibleCommandItems) — "/clear" is not
    // one of them, "/cancel" is.
    expect(screen.getByText('/cancel')).toBeInTheDocument()

    // First Escape: menu closes, stream keeps running.
    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
    expect(screen.queryByText('/cancel')).not.toBeInTheDocument()
    expect(cancelStream).not.toHaveBeenCalled()

    // Second Escape: menu is already closed, so this one falls through to
    // the pre-existing cancel-Escape branch, which calls cancelStream (that
    // branch is unchanged by Fix 3 and, since it doesn't stopPropagation,
    // can also reach useCancelState's own document-level Escape listener —
    // a pre-existing, unrelated double-dispatch quirk this test doesn't
    // pin an exact count for; only that cancel actually fired).
    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
    expect(cancelStream).toHaveBeenCalled()
  })

  it('streaming with no menu open: Escape cancels immediately (the new branch does not swallow every Escape)', async () => {
    const cancelStream = vi.fn()
    act(() => { useChatStore.setState({ isStreaming: true, cancelStream }) })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Nothing typed — shouldShowSlash is false, so the menu-close guard's
    // condition never matches and Escape falls straight through to cancel.
    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
    expect(cancelStream).toHaveBeenCalled()
  })
})

describe('Partitioned slash menu — keyboard navigation across sections', () => {
  it('ArrowDown navigates through commands then skills (globalIndex tracking)', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // /ca matches /cancel command and no skills (no skill id starts with "ca")
    act(() => { fireEvent.change(input, { target: { value: '/ca' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // /cancel command is shown
    expect(screen.getByText('/cancel')).toBeInTheDocument()
    // No skills start with "ca"
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
    expect(screen.queryByText('/code-review')).not.toBeInTheDocument()
  })

  it('Enter selects the currently highlighted item', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // Type /web which matches only web-research skill (no commands)
    act(() => { fireEvent.change(input, { target: { value: '/web' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Enter selects the first (and only) item: /web-research
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(mockSetText).toHaveBeenCalledWith('/web-research ')
  })

  it('mouse click on a skill item selects it', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    const codeReviewBtn = screen.getByText('/code-review').closest('button')
    expect(codeReviewBtn).not.toBeNull()

    act(() => { fireEvent.mouseDown(codeReviewBtn!) })

    expect(mockSetText).toHaveBeenCalledWith('/code-review ')
  })
})

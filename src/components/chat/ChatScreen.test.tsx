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

// ResizeObserver is required by cmdk (used inside the ModelSelector popover);
// jsdom does not implement it. Polyfill with a noop for the tests that open
// the popover. We use vi.stubGlobal so individual tests can unstub it via
// vi.unstubAllGlobals() to test the "ResizeObserver unavailable" fallback.
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

// Static import: vi.mock() calls are hoisted before this import, so all mocks
// are in place when the module resolves. This avoids per-test dynamic import
// contention that caused intermittent timeouts under full-suite parallel load.
import { OmnipusComposer } from './ChatScreen'

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
      // The mock Send button accepts an onClick so test code can wire
      // up the assistant-ui onNew path. In production, ComposerPrimitive.Send
      // calls composer.send() internally — the onClick prop is a noop there.
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel, onClick }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string; onClick?: () => void
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          onClick,
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
  fetchProviders: vi.fn().mockResolvedValue([]),
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

  it('does NOT show non-streaming commands (/new, /clear, /help, /session new) when streaming', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    act(() => {
      fireEvent.change(input, { target: { value: '/' } })
    })
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    // These commands must NOT appear while streaming
    expect(screen.queryByText('/new')).not.toBeInTheDocument()
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/help')).not.toBeInTheDocument()
    expect(screen.queryByText('/session new')).not.toBeInTheDocument()
  })

  it('shows all commands (including /cancel and /new) when NOT streaming', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: false })
    })

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
    expect(screen.getByText('/new')).toBeInTheDocument()
  })

  it('/new slash command calls startNewSession and clears the composer', async () => {
    // /new must clear the session in-place and NOT send a literal "/new" message.
    act(() => {
      useChatStore.setState({ isStreaming: false })
      useSessionStore.setState({ activeAgentId: 'general-assistant', activeSessionId: 'sess_1' })
    })

    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')

    // Type "/new" to trigger the slash menu.
    act(() => {
      fireEvent.change(input, { target: { value: '/new' } })
    })
    act(() => {
      fireEvent.keyDown(input, { key: 'ArrowDown' })
    })

    // "/new" must appear in the menu.
    expect(screen.getByText('/new')).toBeInTheDocument()

    // Press Enter to execute the command.
    act(() => {
      fireEvent.keyDown(input, { key: 'Enter' })
    })

    // After executing /new, the active session should be cleared.
    expect(useSessionStore.getState().activeSessionId).toBeNull()
    // The active agent is preserved.
    expect(useSessionStore.getState().activeAgentId).toBe('general-assistant')
  })

  it('does not show slash menu at all when streaming and there is no matching streaming-safe command', async () => {
    act(() => {
      useChatStore.setState({ isStreaming: true })
    })

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

// ── Wave 2 / FR-008/009/010: chat composer model selector ────────────────────
//
// NOTE: The ModelSelector UI (FR-008) and the nextModel seed effect (FR-009)
// moved from OmnipusComposer → ChatControls. UI + seed tests now live in
// src/components/chat/ChatControls.test.tsx.
//
// This describe block retains only the store-level contracts that are
// independent of which component renders the picker:
//   • FR-010: sendMessage forwards nextModel as metadata.model_name
//   • FR-010 converse: omits metadata.model_name when nextModel is null
describe('OmnipusComposer — model selector store contracts (FR-010)', () => {

  it('sendMessage forwards nextModel as metadata.model_name on the WS frame (store-level contract)', () => {
    // FR-010 / Spec §13 FR-010: the chat store's sendMessage must accept
    // an opts.model_name and translate it into `metadata.model_name` in
    // the WS message frame. This test covers the contract directly,
    // bypassing the assistant-ui runtime (which is mocked in this file).
    const sentFrames: unknown[] = []
    const fakeConnection = {
      send: (frame: unknown) => {
        sentFrames.push(frame)
        return true
      },
    }
    act(() => {
      useConnectionStore.setState({
        connection: fakeConnection as never,
        isConnected: true,
      })
    })
    // Seed an active session
    act(() => {
      useSessionStore.setState({
        activeSessionId: 'sess_send_test',
        activeAgentId: 'general-assistant',
      })
    })
    // Set nextModel to a real value
    act(() => {
      useChatStore.getState().setNextModel('z-ai/glm-5-turbo')
    })
    // Send via the real store
    act(() => {
      useChatStore.getState().sendMessage('hello', { model_name: 'z-ai/glm-5-turbo' })
    })
    // The frame on the wire must include metadata.model_name
    const last = sentFrames.at(-1) as { type?: string; metadata?: { model_name?: string } }
    expect(last).toBeDefined()
    expect(last?.type).toBe('message')
    expect(last?.metadata?.model_name).toBe('z-ai/glm-5-turbo')
    // And the store must clear nextModel after send (per spec §18 Q3:
    // the picker is forward-looking, not persisted).
    expect(useChatStore.getState().nextModel).toBeNull()
  })

  it('omits metadata.model_name when the user has not picked a model this session (store-level)', () => {
    // Spec §18 Q3: the picker is forward-looking. If the user never
    // touched it, model_name is absent and the server uses the
    // agent's `model` config (the legacy path). The store must omit
    // the metadata key entirely so the wire format stays clean.
    const sentFrames: unknown[] = []
    const fakeConnection = {
      send: (frame: unknown) => {
        sentFrames.push(frame)
        return true
      },
    }
    act(() => {
      useConnectionStore.setState({
        connection: fakeConnection as never,
        isConnected: true,
      })
    })
    act(() => {
      useSessionStore.setState({
        activeSessionId: 'sess_no_pick',
        activeAgentId: 'general-assistant',
      })
    })
    // nextModel is null (the user never picked)
    act(() => {
      useChatStore.getState().setNextModel(null)
    })
    act(() => {
      useChatStore.getState().sendMessage('hello')
    })
    const last = sentFrames.at(-1) as { type?: string; metadata?: unknown }
    expect(last?.type).toBe('message')
    // No metadata key on the frame (or it's empty/undefined)
    expect(last?.metadata).toBeUndefined()
  })

})

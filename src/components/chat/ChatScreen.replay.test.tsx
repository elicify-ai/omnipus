// ChatScreen replay-mode tests — TDD row 22
// Traces to: sprint-i-historical-replay-fidelity-spec.md FR-I-014
// BDD: "send button disabled while isReplaying, enabled once done"

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

// Static import: vi.mock() calls are hoisted before this import, so all mocks
// are in place when the module resolves. This avoids per-test dynamic import
// contention that caused intermittent timeouts under full-suite parallel load.
import { OmnipusComposer } from './ChatScreen'

// ── Mocks ─────────────────────────────────────────────────────────────────────

// Mock AssistantUI primitives — ChatScreen uses ThreadPrimitive, ComposerPrimitive, etc.
// We only care about ComposerPrimitive.Send's disabled state, so render it as a real button.
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
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Input: ({ disabled, placeholder, className }: { disabled?: boolean; placeholder?: string; className?: string }) =>
        React.createElement('textarea', { disabled, placeholder, className, 'data-testid': 'composer-input' }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel, 'aria-disabled': ariaDisabled }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string; 'aria-disabled'?: boolean | string
      }) =>
        React.createElement('button', {
          type: 'button',
          disabled,
          className,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
          'aria-disabled': ariaDisabled,
        }, children),
      AddAttachment: ({ disabled, children, className }: { disabled?: boolean; children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': 'add-attachment' }, children),
      Attachments: () => null,
    },
    AttachmentPrimitive: {
      Root: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Name: () => null,
      Remove: ({ children, className }: { children?: React.ReactNode; className?: string }) =>
        React.createElement('button', { type: 'button', className }, children),
      Thumb: () => null,
    },
    MessagePartPrimitive: {
      InProgress: () => null,
    },
    ActionBarPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Copy: ({ children }: { children: React.ReactNode }) =>
        React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
    }),
    useMessage: () => ({
      id: 'msg_1',
      role: 'assistant',
      status: { type: 'complete' },
      content: [],
    }),
    makeAssistantToolUI: () => () => null,
  }
})

// Mock TanStack Query — no real server calls needed.
// Must include QueryClient because src/lib/queryClient.ts uses it at module init time.
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: () => ({ data: [], isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

// Mock TanStack Router
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: ({ children }: { children: React.ReactNode }) => children,
}))

// Mock API calls
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
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    // Isolation hardening (gap 13, bugfixes3 QA hardening wave): the
    // `importOriginal` spread above leaves the REAL fetchCommands/
    // fetchSkills reachable — harmless today only because BASE_URL resolves
    // to '' in this test environment. Explicit overrides close that gap.
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

// Mock SVG asset import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))

// Mock child components that would need their own deep deps
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// Composer Redesign (variant A1): TDD row 22 targets the send/input disabled
// state during replay, not the composer's picker/model/token sub-components —
// stub them to null so their workspaces/providers query plumbing doesn't need
// mocking here.
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

// ── Store reset ───────────────────────────────────────────────────────────────

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
      isConnected: true, // connected so we can isolate the isReplaying effect
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_replay_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

// ── TDD row 22: ChatScreen_Replay_SendDisabled ────────────────────────────────

describe('ChatScreen_Replay_SendDisabled (TDD row 22)', () => {
  it('send button is disabled while isReplaying is true', async () => {
    // Set isReplaying = true before render
    act(() => {
      useChatStore.setState({ isReplaying: true })
    })

    render(<OmnipusComposer />)

    const sendButton = screen.getByTestId('chat-send')
    expect(sendButton).toBeDisabled()
  })

  it('send button is enabled when isReplaying is false and connected', async () => {
    // isReplaying starts false, connection is up
    act(() => {
      useChatStore.setState({ isReplaying: false })
      useConnectionStore.setState({ isConnected: true })
    })

    render(<OmnipusComposer />)

    const sendButton = screen.getByTestId('chat-send')
    expect(sendButton).not.toBeDisabled()
  })

  it('composer input is disabled while isReplaying is true', async () => {
    act(() => {
      useChatStore.setState({ isReplaying: true })
    })

    render(<OmnipusComposer />)

    const input = screen.getByTestId('composer-input')
    expect(input).toBeDisabled()
  })

  it('toggling isReplaying from true to false enables the send button', async () => {
    act(() => {
      useChatStore.setState({ isReplaying: true })
    })

    render(<OmnipusComposer />)

    expect(screen.getByTestId('chat-send')).toBeDisabled()

    act(() => {
      useChatStore.setState({ isReplaying: false })
    })

    expect(screen.getByTestId('chat-send')).not.toBeDisabled()
  })
})

// ChatScreen composer-card integration test.
//
// Coverage gap found by the Composer Redesign variant A1 review: every one
// of the (at the time) 12 ChatScreen test files stubs the three composer
// sub-components (AgentPicker / ModelPicker / TokenCounter) to `null`
// (see ChatScreen.replay.test.tsx and its siblings), so deleting
// `<AgentPicker />` from the context row in src/components/chat/ChatScreen.tsx
// would keep every one of those files green while the picker silently
// vanished from the product. This file closes that gap by stubbing the
// three sub-components to non-null SENTINEL elements and asserting the
// composer card actually mounts all of its context-row slots.
//
// Scaffolding (mocks for @assistant-ui/react, @tanstack/react-query,
// @tanstack/react-router, @/lib/api, the SVG asset, and the deep child
// components) is copied from ChatScreen.replay.test.tsx's minimal
// OmnipusComposer mount setup rather than invented fresh, per the sibling
// file's precedent.
//
// NOTE: this file deliberately does NOT assert against the composer card's
// FULL className string (e.g. exact border/background/rounding utilities) —
// only the presence of its `@container` query-context class (load-bearing:
// TokenCounter's `@2xl:flex` gate below reads off THIS container, not a
// distant ancestor) plus the sentinel testids for its child slots. The
// contract under test is slot presence/placement and the @container
// context, not the card's cosmetic styling, which is free to change without
// this file needing an update.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'

// Static import: vi.mock() calls are hoisted before this import, so all mocks
// are in place when the module resolves. This avoids per-test dynamic import
// contention that caused intermittent timeouts under full-suite parallel load
// (see ChatScreen.replay.test.tsx for the same rationale).
import { OmnipusComposer } from './ChatScreen'

// ── Mocks ─────────────────────────────────────────────────────────────────────

// Mock AssistantUI primitives — ChatScreen uses ThreadPrimitive, ComposerPrimitive, etc.
// We only need the composer card's shell rendered as real DOM so its child
// slots (attach control + the three composer sub-components) are queryable.
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
vi.mock('@/lib/api', () => ({
  fetchAgents: vi.fn().mockResolvedValue([]),
  fetchSessionMessages: vi.fn().mockResolvedValue([]),
  createSession: vi.fn(),
  uploadFiles: vi.fn(),
  fetchProviders: vi.fn().mockResolvedValue([]),
}))

// Mock SVG asset import
vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))

// Mock child components that would need their own deep deps
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))

// The point of THIS file: stub the three composer sub-components to
// non-null SENTINELS (not null, unlike every other ChatScreen test file) so
// we can assert the composer card actually mounts all of its slots. The
// AgentPicker/ModelPicker stubs also surface the `disabled` prop via
// data-disabled so the agentRemoved read-only-passthrough test below can
// assert on it without needing the real components.
vi.mock('./composer/AgentPicker', () => ({
  AgentPicker: ({ disabled }: { disabled?: boolean }) => (
    <div data-testid="agent-picker-stub" data-disabled={disabled ? 'true' : 'false'} />
  ),
}))
vi.mock('./composer/ModelPicker', () => ({
  ModelPicker: ({ disabled }: { disabled?: boolean }) => (
    <div data-testid="model-picker-stub" data-disabled={disabled ? 'true' : 'false'} />
  ),
}))
vi.mock('./composer/TokenCounter', () => ({
  TokenCounter: ({ className }: { className?: string }) => (
    <div data-testid="token-counter-stub" data-cls={className} />
  ),
}))
// ActivityBar sentinel — live background activity folds below the input,
// inside the composer card (see ChatScreen.tsx's composer card JSX).
vi.mock('./ActivityBar', () => ({ ActivityBar: () => <div data-testid="activity-bar-stub" /> }))

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
      isConnected: true,
      connectionError: null,
    })
    useSessionStore.setState({
      activeSessionId: 'sess_composer_card_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

// ── Composer card slot coverage ───────────────────────────────────────────────

describe('OmnipusComposer — composer card mounts all context-row slots', () => {
  it('mounts the attach control, AgentPicker, ModelPicker, TokenCounter, and ActivityBar inside the composer card', async () => {
    render(<OmnipusComposer />)

    const card = screen.getByTestId('composer-card')
    // @container is load-bearing: TokenCounter's `@2xl:flex` gate below reads
    // off THIS container's width, not a distant ancestor's.
    expect(card.className).toContain('@container')

    const withinCard = within(card)

    // Attach control (ComposerPrimitive.AddAttachment) is present INSIDE the card.
    expect(withinCard.getByTestId('add-attachment')).toBeInTheDocument()

    // The three composer sub-components are actually mounted INSIDE the card
    // — not deleted from the context row, and not relocated outside it. This
    // is the assertion that would have caught the coverage gap: every other
    // ChatScreen test file stubs these to null, so a deleted (or relocated)
    // <AgentPicker /> would still pass there.
    expect(withinCard.getByTestId('agent-picker-stub')).toBeInTheDocument()
    expect(withinCard.getByTestId('model-picker-stub')).toBeInTheDocument()

    const tokenCounter = withinCard.getByTestId('token-counter-stub')
    expect(tokenCounter).toBeInTheDocument()
    expect(tokenCounter.getAttribute('data-cls')).toContain('hidden')
    expect(tokenCounter.getAttribute('data-cls')).toContain('@2xl:flex')

    // Live background activity (delegate spans, background bash runs) folds
    // below the input, inside the card.
    expect(withinCard.getByTestId('activity-bar-stub')).toBeInTheDocument()
  })

  it('disables the attach control and passes disabled=true through to both pickers when agentRemoved', async () => {
    render(<OmnipusComposer agentRemoved />)

    const withinCard = within(screen.getByTestId('composer-card'))

    expect(withinCard.getByTestId('add-attachment')).toBeDisabled()
    expect(withinCard.getByTestId('agent-picker-stub').getAttribute('data-disabled')).toBe('true')
    expect(withinCard.getByTestId('model-picker-stub').getAttribute('data-disabled')).toBe('true')
  })
})

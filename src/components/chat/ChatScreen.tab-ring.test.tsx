/**
 * ChatScreen.tab-ring.test.tsx — composer tab-order structural guard.
 *
 * The composer carries a DELIBERATE, operator-mandated positive tabIndex
 * ring so Tab visits the controls in a fixed order regardless of DOM
 * position. The single source of truth for the full ring is the comment in
 * ChatControls.tsx (see also src/components/layout/AppShell.tsx for the
 * skip-link=1 end of the chain, covered separately in AppShell.test.tsx, and
 * ChatControls.test.tsx for browser=7). This file locks down the
 * OmnipusComposer-owned segment of the ring:
 *
 *   skip-link=1 (AppShell.tsx, see AppShell.test.tsx)
 *   → chat input=2 (this file)
 *   → agent=3 (this file, via the tabIndex prop ChatScreen passes to AgentPicker)
 *   → model=4 (this file, via the tabIndex prop ChatScreen passes to ModelPicker)
 *   → attach=5 (this file)
 *   → send=6 (this file)
 *   → browser=7 (ChatControls.tsx, see ChatControls.test.tsx)
 *
 * A well-meaning cleanup (renumbering, dropping a tabIndex, or reordering
 * controls) should fail one of these class/attribute-level assertions rather
 * than silently drift the ring — the accepted regression-guard style in this
 * repo for structural/positional contracts (see AgentPicker.test.tsx,
 * Sidebar.m5.test.tsx for precedent).
 *
 * AgentPicker/ModelPicker are stubbed here (not their real implementations —
 * their own internal behavior is covered by composer/AgentPicker.test.tsx and
 * composer/ModelPicker.test.tsx) to a thin pass-through that echoes the
 * `tabIndex` prop it was called with, so this test asserts the CONTRACT
 * ("ChatScreen passes tabIndex=3/4 to these components") without dragging in
 * their agents/workspaces query plumbing.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
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

// ── @assistant-ui/react — same shape as ChatScreen.test.tsx's mock, EXCEPT
// Input/AddAttachment/Send additionally forward `tabIndex` onto the rendered
// DOM node, which is exactly the prop this test needs to observe. ──────────
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
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur, tabIndex }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void; tabIndex?: number;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur, tabIndex,
          'data-testid': 'chat-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId, 'aria-label': ariaLabel, onClick, tabIndex }: {
        disabled?: boolean; children?: React.ReactNode; className?: string;
        'data-testid'?: string; 'aria-label'?: string; onClick?: () => void; tabIndex?: number;
      }) =>
        React.createElement('button', {
          type: 'button', disabled, className,
          onClick, tabIndex,
          'data-testid': testId ?? 'chat-send',
          'aria-label': ariaLabel,
        }, children),
      AddAttachment: ({ disabled, children, className, 'aria-label': ariaLabel, tabIndex }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'aria-label'?: string; tabIndex?: number;
      }) =>
        React.createElement('button', { type: 'button', disabled, className, tabIndex, 'aria-label': ariaLabel, 'data-testid': 'add-attachment' }, children),
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
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: [], isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: [], isError: false, refetch: vi.fn() }
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
  uploadFiles: vi.fn(),
  fetchProviders: vi.fn().mockResolvedValue([]),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./SessionPanel', () => ({ SessionPanel: () => null }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))

// AgentPicker/ModelPicker: thin pass-throughs that echo the `tabIndex` prop
// ChatScreen calls them with, onto a testid'd node — this is the structural
// contract under test, not the pickers' own internal behavior (covered
// elsewhere, see the file header comment).
vi.mock('./composer/AgentPicker', () => ({
  AgentPicker: ({ tabIndex }: { tabIndex?: number }) =>
    React.createElement('div', { 'data-testid': 'agent-picker-mock', tabIndex }),
}))
vi.mock('./composer/ModelPicker', () => ({
  ModelPicker: ({ tabIndex }: { tabIndex?: number }) =>
    React.createElement('div', { 'data-testid': 'model-picker-mock', tabIndex }),
}))

// Static import: vi.mock() calls are hoisted before this import.
import { OmnipusComposer } from './ChatScreen'

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
      activeSessionId: 'sess_tab_ring_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('OmnipusComposer — composer tab ring (post-renumber structural guard)', () => {
  it('chat input carries tabIndex=2', () => {
    render(<OmnipusComposer />)
    expect(screen.getByTestId('chat-input')).toHaveAttribute('tabindex', '2')
  })

  it('AgentPicker is invoked with tabIndex=3', () => {
    render(<OmnipusComposer />)
    expect(screen.getByTestId('agent-picker-mock')).toHaveAttribute('tabindex', '3')
  })

  it('ModelPicker is invoked with tabIndex=4', () => {
    render(<OmnipusComposer />)
    expect(screen.getByTestId('model-picker-mock')).toHaveAttribute('tabindex', '4')
  })

  it('the attach (+) button carries tabIndex=5', () => {
    render(<OmnipusComposer />)
    expect(screen.getByTestId('add-attachment')).toHaveAttribute('tabindex', '5')
  })

  it('the send button carries tabIndex=6', () => {
    render(<OmnipusComposer />)
    expect(screen.getByTestId('chat-send')).toHaveAttribute('tabindex', '6')
  })

  it('the ring is strictly ordered 2 < 3 < 4 < 5 < 6 across the five composer controls', () => {
    render(<OmnipusComposer />)
    const order = [
      Number(screen.getByTestId('chat-input').getAttribute('tabindex')),
      Number(screen.getByTestId('agent-picker-mock').getAttribute('tabindex')),
      Number(screen.getByTestId('model-picker-mock').getAttribute('tabindex')),
      Number(screen.getByTestId('add-attachment').getAttribute('tabindex')),
      Number(screen.getByTestId('chat-send').getAttribute('tabindex')),
    ]
    expect(order).toEqual([2, 3, 4, 5, 6])
  })
})

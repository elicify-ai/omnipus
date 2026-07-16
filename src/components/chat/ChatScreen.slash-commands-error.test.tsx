// LOW S8: fetchCommands('web') query error → the slash palette must not go
// silently empty. Before this fix, an errored commands query left the
// Commands section (and, if no skills matched either, the whole menu)
// invisible with no signal to the user that anything was wrong.
// useSlashMenu now exposes `commandsError`, and ChatScreen renders a muted
// "Commands unavailable" row from it — this file proves the row actually
// reaches the DOM.

import { describe, it, expect, vi, beforeEach } from 'vitest'
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
      subscribe: vi.fn(() => vi.fn()),
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

// Commands query errors; skills query succeeds with one match so we can also
// prove the error row and a real skill item coexist in the same menu.
// Fix 1 (bugfixes3 review): also stock one scoped chat agent, so a
// mention-mode test below can prove the "Commands unavailable" row does NOT
// leak into the "@" menu — without at least one agent, "@" would never open
// the menu at all (shouldShowSlash false either way), which would make that
// assertion trivially true regardless of whether the fix exists.
vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockSkills = [
    { id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active' },
  ]
  // K.3 (bugfixes3 sign-off): `makeAgent` (src/test/factories.ts), not a
  // hand-rolled partial object literal — fills every schema-required Agent
  // field (see that file's own header comment on why a partial fixture
  // drifted from the real wire type). Imported here via `await import(...)`
  // rather than a top-level `import` because this factory function is
  // itself the body of a HOISTED `vi.mock(...)` call — referencing a
  // top-level `import`ed binding from inside it would hit vitest's mock
  // hoisting order; the dynamic import is hoisting-safe.
  const { makeAgent } = await import('@/test/factories')
  const mockAgents = [
    makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', description: 'Assistant' }),
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: undefined, isError: true, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: mockSkills, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'agents') {
        return { data: mockAgents, isError: false, refetch: vi.fn() }
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
    fetchCommands: vi.fn().mockRejectedValue(new Error('network down')),
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
      activeSessionId: 'sess_cmd_error_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('Slash menu — commands query error (LOW S8)', () => {
  it('renders a muted "Commands unavailable" row when fetchCommands errors', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    expect(screen.getByTestId('slash-menu')).toBeInTheDocument()
    expect(screen.getByTestId('slash-commands-error')).toBeInTheDocument()
    expect(screen.getByText('Commands unavailable')).toBeInTheDocument()

    // The synthetic client-only /resume command comes from useSlashMenu
    // itself, not the errored backend list, so it still renders alongside
    // the error row.
    expect(screen.getByText('/resume')).toBeInTheDocument()

    // The skills query is independent and still succeeded.
    expect(screen.getByText('/web-research')).toBeInTheDocument()
  })

  it('the menu still opens even for a prefix that matches nothing (error row keeps it visible)', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    // "/zzz" matches neither the synthetic /resume command nor web-research.
    act(() => { fireEvent.change(input, { target: { value: '/zzz' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    expect(screen.getByTestId('slash-menu')).toBeInTheDocument()
    expect(screen.getByTestId('slash-commands-error')).toBeInTheDocument()
  })

  it('the error row is hidden during the "/skills" filter (D9 already hides the whole Commands section there)', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/skills' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    expect(screen.queryByTestId('slash-commands-error')).not.toBeInTheDocument()
    expect(screen.getByText('/web-research')).toBeInTheDocument()
  })

  // Fix 1 (bugfixes3 review, highest priority): the render gate previously
  // read `commandsError && !isSkillsFilter` only — missing the
  // `!isMentionMode` clause useSlashMenu's own shouldShowSlash fallback
  // already applies. A commands-fetch error has nothing to do with the "@"
  // agent-mention menu, so the row must never appear there.
  it('the error row does NOT leak into the "@" agent-mention menu', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    // The "@" menu itself opens (one scoped agent, Mia)...
    expect(screen.getByTestId('slash-menu')).toBeInTheDocument()
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    // ...but the commands-error row must not appear alongside it.
    expect(screen.queryByTestId('slash-commands-error')).not.toBeInTheDocument()
    expect(screen.queryByText('Commands unavailable')).not.toBeInTheDocument()
  })
})

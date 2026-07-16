// "@" agent-mention menu — component-level coverage (real DOM, real
// OmnipusComposer render tree). Complements the hook-level suite in
// src/hooks/useSlashMenu.test.ts (covering gating, filtering, keyboard nav,
// selection, and the active-agent flag on the HOOK's own return value — see
// that file for the current count; not pinned here, K.2 correction,
// bugfixes3 sign-off) by proving the same behavior actually reaches the
// rendered DOM: the "Agents" section header, `agent-mention-item` rows, the
// highlight class, and the real setActiveSession/composerRuntime side
// effects wired through OmnipusComposer — mirrors the established pattern in
// ChatScreen.skills-filter.test.tsx / ChatScreen.partitioned-menu.test.tsx /
// ChatScreen.agents-command.test.tsx (mock @assistant-ui/react primitives +
// @tanstack/react-query + importOriginal-spread @/lib/api).
//
// Traces to: commit 103c5fd0 (feat(chat): @ agent-mention menu) —
// src/hooks/useSlashMenu.ts (isMentionMode/agentItems), ChatScreen.tsx
// (Agents section header, agent-mention-item rows, active marker).

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useSessionStore } from '@/store/session'
import { makeAgent } from '@/test/factories'

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
      // Forwards data-testid so this mirrors the REAL data-testid="chat-input"
      // ChatScreen.tsx passes to ComposerPrimitive.Input (see
      // ChatScreen.tab-ring.test.tsx, which does the same) — this file's
      // spec drives interactions through `[data-testid="chat-input"]`.
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur, 'data-testid': testId }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void; 'data-testid'?: string;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className,
          onChange, onKeyDown, onBlur,
          'data-testid': testId ?? 'chat-input',
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

// Command/skill fixtures — needed for the "/" regression guard (item 9)
// proving the two menu modes never mix. Mirrors
// ChatScreen.partitioned-menu.test.tsx's fixtures.
const mockCommands = [
  { name: 'clear',  label: '/clear',  description: 'Start a new conversation', delivery: 'client', available_while_streaming: false },
  { name: 'help',   label: '/help',   description: 'Show available commands',  delivery: 'client', available_while_streaming: false },
  { name: 'cancel', label: '/cancel', description: 'Cancel the current turn',  delivery: 'client', available_while_streaming: true },
]
const mockSkills = [
  { id: 'web-research', name: 'Web Research', version: '1.0', description: 'Web search and extraction', verified: true, status: 'active' },
  { id: 'code-review',  name: 'Code Review',  version: '1.0', description: 'Reviews code quality',      verified: true, status: 'active' },
]

// "@" mention menu source data — three distinct-prefix scoped agents
// (Mia/Jim/Ava) plus two agents that must NEVER render as mention rows:
// a worker (Subagent — isWorker() exclusion) and a draft-status agent (not
// active/idle — "ready to chat" exclusion). "Max Worker" deliberately also
// starts with "m" so the "@m" filter test (item 2) doubles as proof that
// worker exclusion applies BEFORE the prefix filter, not as an accident of
// the filter itself.
const mockAgents = [
  makeAgent({ id: 'mia', name: 'Mia', type: 'core', status: 'active', color: '#111111', description: 'Assistant' }),
  makeAgent({ id: 'jim', name: 'Jim', type: 'core', status: 'idle', description: 'Orchestrator' }),
  makeAgent({ id: 'ava', name: 'Ava', type: 'Main', status: 'active', description: 'Builder' }),
  makeAgent({ id: 'max', name: 'Max Worker', type: 'Subagent', status: 'active', description: 'Labour agent' }),
  makeAgent({ id: 'ray', name: 'Ray', type: 'Main', status: 'draft', description: 'Scout (draft)' }),
]

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
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
      if (Array.isArray(key) && key[0] === 'agents') {
        return { data: mockAgents, isError: false, refetch: vi.fn() }
      }
      // 'workspaces' (and anything else) — no active workspace is set in
      // these tests, so core_team scoping never engages; that scoping path
      // is covered separately (AgentPicker.test.tsx / useChatAgents.test.ts).
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

// importOriginal: useSlashMenu calls useChatAgents unconditionally (the "@"
// mention menu itself), which needs the REAL `isWorker` (this file's worker
// exclusion assertions depend on it) plus `fetchWorkspaces`/
// `workspacesQueryKeys` to exist so the module loads — useQuery is fully
// mocked above and never actually invokes these queryFns.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    fetchProviders: vi.fn().mockResolvedValue([]),
    fetchWorkspaces: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./SubagentBlock', () => ({ SubagentBlock: () => null }))
vi.mock('./markdown-text', () => ({ MarkdownText: () => null }))
vi.mock('./tools/GenericToolCall', () => ({ GenericToolCall: () => null }))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
// The mention menu is independent of the picker/model/token sub-components
// (same rationale as the sibling "/" menu test files) — stub them to null so
// their own workspaces/providers query plumbing doesn't need mocking here.
// Stubbing AgentPicker also means its auto-select-first-agent effect never
// runs, so it can't race this file's own setActiveSession assertions.
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
      activeSessionId: 'sess_mention_test',
      // Deliberately not one of mockAgents' ids — proves no row spuriously
      // renders the "active" marker in tests that don't explicitly set it
      // (see item 10, which overrides this to 'jim').
      activeAgentId: 'general-assistant',
      activeAgentType: null,
      attachedSessionType: null,
      attachedTaskTitle: null,
    })
  })
}

beforeEach(resetStores)

// K.4 (bugfixes3 sign-off): two tests below override `useComposerRuntime`'s
// return value via `.mockReturnValue(...)` (to capture a `mockSetText` spy
// for their own assertions). `.mockReturnValue` replaces the mock's
// implementation for the rest of THIS FILE's test run, not just the single
// test that sets it — vitest does not reset mock state between tests here
// (no restoreMocks/clearMocks configured in vite.config.ts) — so without
// this, a later test in this file would silently inherit a stale
// composerRuntime (and a `mockSetText` spy meant for a different test's
// assertions) from an earlier one. Restore the base factory's shape after
// EVERY test (harmless/idempotent for tests that never overrode it).
afterEach(async () => {
  const { useComposerRuntime } = await import('@assistant-ui/react')
  ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
    getState: () => ({ text: '' }),
    setText: vi.fn(),
    addAttachment: vi.fn(),
    subscribe: vi.fn(() => vi.fn()),
  })
})

describe('"@" agent-mention menu — opens and lists scoped agents', () => {
  it('typing "@" renders the slash-menu container with an "Agents" section header and one row per scoped agent', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    expect(screen.getByTestId('slash-menu')).toBeInTheDocument()
    expect(screen.getByText('Agents')).toBeInTheDocument()
    // Exactly the 3 scoped chat agents (mia/jim/ava) — worker (max) and
    // draft-status (ray) are excluded, proven separately in item 8 below.
    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(3)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
    expect(screen.getByText('@Ava')).toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — filtering', () => {
  it('typing "@m" filters to agents whose NAME prefix- or substring-matches "m" (case-insensitive)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })

    // Deferred item 4 (prefix-then-substring matching): "Mia" prefix-matches
    // "m"; "Jim" now ALSO matches via the substring rank ("jim" contains
    // "m", just not at the start) — ranked after the prefix match.
    const rows = screen.getAllByTestId('agent-mention-item')
    expect(rows).toHaveLength(2)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
    expect(screen.queryByText('@Ava')).not.toBeInTheDocument()
    // Max Worker also starts with "m" but must stay excluded — worker
    // exclusion applies before the prefix/substring filter runs.
    expect(screen.queryByText('Max Worker')).not.toBeInTheDocument()
  })

  it('"@M" (uppercase) filters identically to "@m"', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@M' } }) })

    // Deferred item 4: same prefix+substring result set as "@m" (see the
    // sibling test above) — "Mia" (prefix) and "Jim" (substring).
    const rows = screen.getAllByTestId('agent-mention-item')
    expect(rows).toHaveLength(2)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
  })

  it('typing "@zzz" (no match) hides the menu entirely', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@zzz' } }) })

    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
  })
})

describe('"@" agent-mention menu — second-character immediacy', () => {
  it('the row set is already filtered right after the second typed character, no extra keystroke needed', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    // First character: bare "@" — every scoped agent shows.
    act(() => { fireEvent.change(input, { target: { value: '@' } }) })
    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(3)

    // Second character (the first typed after "@") — filtering must be
    // visible in the SAME synchronous update, not one render/tick later.
    // Deferred item 4: "@m" now matches both "Mia" (prefix) and "Jim"
    // (substring — see the "filtering" describe block above).
    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    const rows = screen.getAllByTestId('agent-mention-item')
    expect(rows).toHaveLength(2)
    expect(screen.getByText('@Mia')).toBeInTheDocument()
    expect(screen.getByText('@Jim')).toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — selection (keyboard)', () => {
  it('Enter on the highlighted row calls setActiveSession (preserving activeSessionId), clears the composer, and closes the menu', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    // Only "Mia" matches — highlight defaults to index 0.
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(useSessionStore.getState().activeAgentId).toBe('mia')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
    // activeSessionId must be PRESERVED (same SC-005 contract as AgentPicker).
    expect(useSessionStore.getState().activeSessionId).toBe('sess_mention_test')
    expect(mockSetText).toHaveBeenCalledWith('')
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })

  it('passes the selected agent\'s type through to setActiveSession, not a hardcoded/null value', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    // "@a" matches only Ava (type "Main") — proves the type argument tracks
    // the SELECTED agent, differentiating from the "core" case above.
    act(() => { fireEvent.change(input, { target: { value: '@a' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' }) })

    expect(useSessionStore.getState().activeAgentId).toBe('ava')
    expect(useSessionStore.getState().activeAgentType).toBe('Main')
  })
})

describe('"@" agent-mention menu — selection (mouse)', () => {
  it('mousedown on an agent row selects it the same way Enter does', async () => {
    const mockSetText = vi.fn()
    const { useComposerRuntime } = await import('@assistant-ui/react')
    ;(useComposerRuntime as ReturnType<typeof vi.fn>).mockReturnValue({
      getState: () => ({ text: '' }),
      setText: mockSetText,
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    })

    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })
    const jimRow = screen.getByText('@Jim').closest('button')
    expect(jimRow).not.toBeNull()

    act(() => { fireEvent.mouseDown(jimRow!) })

    expect(useSessionStore.getState().activeAgentId).toBe('jim')
    expect(useSessionStore.getState().activeAgentType).toBe('core')
    expect(useSessionStore.getState().activeSessionId).toBe('sess_mention_test')
    expect(mockSetText).toHaveBeenCalledWith('')
    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — keyboard highlight', () => {
  it('ArrowDown moves the highlight from the first row to the second', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })
    const rowsBefore = screen.getAllByTestId('agent-mention-item')
    expect(rowsBefore).toHaveLength(3)
    // Default highlight is index 0 (Mia). data-highlighted (Fix 11) is the
    // semantic marker — prefer it over the presentational Tailwind class
    // string, which is free to change without this test needing an update.
    expect(rowsBefore[0]).toHaveAttribute('data-highlighted', 'true')
    expect(rowsBefore[1]).not.toHaveAttribute('data-highlighted')

    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    const rowsAfter = screen.getAllByTestId('agent-mention-item')
    expect(rowsAfter[0]).not.toHaveAttribute('data-highlighted')
    expect(rowsAfter[1]).toHaveAttribute('data-highlighted', 'true')
  })
})

describe('"@" agent-mention menu — leading-position gate', () => {
  it('"hello @x" (non-leading "@") never opens the mention menu', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: 'hello @x' } }) })

    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
  })
})

describe('"@" agent-mention menu — scoping exclusions', () => {
  it('excludes worker agents (type Subagent) and non-active/idle agents from the menu', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    // Only the 3 ready-to-chat, non-worker agents render.
    expect(screen.getAllByTestId('agent-mention-item')).toHaveLength(3)
    // Worker (Subagent) — excluded regardless of matching text.
    expect(screen.queryByText('Max Worker')).not.toBeInTheDocument()
    expect(screen.queryByText('@Max Worker')).not.toBeInTheDocument()
    // Draft-status (not active/idle) — excluded regardless of matching text.
    expect(screen.queryByText('Ray')).not.toBeInTheDocument()
    expect(screen.queryByText('@Ray')).not.toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — regression guard: "/" and "@" never mix', () => {
  it('input "/" still shows Commands/Skills sections and zero agent items', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    expect(screen.getByText('Commands')).toBeInTheDocument()
    expect(screen.getByText('Skills')).toBeInTheDocument()
    expect(screen.getByText('/clear')).toBeInTheDocument()
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.queryByText('Agents')).not.toBeInTheDocument()
    expect(screen.queryAllByTestId('agent-mention-item')).toHaveLength(0)
    expect(screen.queryByText('@Mia')).not.toBeInTheDocument()
  })
})

describe('"@" agent-mention menu — Escape precedence (Fix 3)', () => {
  it('menu open + streaming: first Escape closes the menu only (no cancel); second Escape cancels', () => {
    const cancelStream = vi.fn()
    const realCancelStream = useChatStore.getState().cancelStream
    act(() => { useChatStore.setState({ isStreaming: true, cancelStream }) })

    try {
      render(<OmnipusComposer />)
      const input = screen.getByTestId('chat-input')

      act(() => { fireEvent.change(input, { target: { value: '@' } }) })
      expect(screen.getByTestId('slash-menu')).toBeInTheDocument()

      // First Escape: the "@" menu closes, the stream keeps running.
      act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
      expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
      expect(cancelStream).not.toHaveBeenCalled()

      // Second Escape: menu already closed — falls through to the
      // pre-existing cancel-Escape branch, which calls cancelStream (that
      // branch is unchanged by Fix 3). bugfixes3 deferred item 5
      // (useCancelState.ts's global Escape listener now early-returns on
      // `e.defaultPrevented`) killed the double-dispatch this branch used to
      // cause — see ChatScreen.partitioned-menu.test.tsx's sibling test for
      // the full explanation. Pinned to exactly 1 call.
      act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
      expect(cancelStream).toHaveBeenCalledTimes(1)
    } finally {
      // Restore the real implementation — useChatStore is a shared
      // singleton across this whole file's test suite.
      act(() => { useChatStore.setState({ cancelStream: realCancelStream }) })
    }
  })
})

describe('"@" agent-mention menu — active-agent marker', () => {
  it('marks the currently active agent\'s row with the "active" text, and no other row', () => {
    act(() => {
      useSessionStore.setState({ activeAgentId: 'jim' })
    })
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    const jimRow = screen.getByText('@Jim').closest('button')
    const miaRow = screen.getByText('@Mia').closest('button')
    const avaRow = screen.getByText('@Ava').closest('button')
    expect(jimRow).not.toBeNull()
    expect(miaRow).not.toBeNull()
    expect(avaRow).not.toBeNull()

    expect(jimRow!.textContent).toContain('active')
    expect(miaRow!.textContent).not.toContain('active')
    expect(avaRow!.textContent).not.toContain('active')
  })
})

// Gap 7 (Fix 2, a11y HIGH): the sr-only aria-live announcement region must
// actually contain the "Now chatting with {name}" text after a real
// selection reaches the DOM, and must update (not just append/duplicate) on
// a SECOND, different selection — proving the region tracks the latest
// selection rather than a stale first value.
describe('"@" agent-mention menu — sr announcement (Fix 2)', () => {
  it('after selecting via "@", the announcement region reads "Now chatting with {name}"', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    // Before any selection, the region renders empty (no announcement yet).
    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    // Only "Mia" matches "@m" (Max/Ray excluded by scoping) — highlight
    // defaults to index 0.
    act(() => { fireEvent.keyDown(input, { key: 'Enter' } ) })

    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('Now chatting with Mia')
  })

  it('selecting a DIFFERENT agent next updates the announcement text (not stuck on the first selection)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@m' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' } ) })
    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('Now chatting with Mia')

    // Second, different selection — "@j" matches only Jim.
    act(() => { fireEvent.change(input, { target: { value: '@j' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'Enter' } ) })

    expect(screen.getByTestId('agent-mention-announcement').textContent).toBe('Now chatting with Jim')
  })
})

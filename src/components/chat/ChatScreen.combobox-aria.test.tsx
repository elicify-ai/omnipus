// bugfixes3 deferred item 2 — W3C APG combobox pattern for the shared "/"
// and "@" menu. Before this, the textarea had no ARIA semantics tying it to
// the menu, and menu rows were `tabIndex={0}` but keyboard-inert (only
// activated via onMouseDown) — Tab-focusing a row started the textarea's
// 150ms blur-close timer, which unmounted the menu and dropped focus to
// `body` (a keyboard dead end). Fixed by wiring the textarea as
// `role="combobox"` with `aria-expanded`/`aria-controls`/
// `aria-activedescendant`/`aria-autocomplete="list"`, the menu container as
// `role="listbox"`, and rows as `role="option"` with `tabIndex={-1}` (not
// tabbable — the APG pattern keeps DOM focus on the input the whole time).
//
// This file's `ComposerPrimitive.Input` mock forwards ALL extra props
// (`...rest`) onto the rendered `<textarea>`, unlike most of this directory's
// other test files (which whitelist a small, fixed prop list) — that's
// deliberate: this file exists specifically to observe the aria-* props
// ChatScreen.tsx now passes, so the mock must not silently drop them the way
// a whitelist would.

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
      // Forwards EVERY extra prop (role, aria-expanded, aria-controls,
      // aria-activedescendant, aria-autocomplete, etc.) onto the textarea —
      // see file header. Matches the REAL ComposerPrimitive.Input's own
      // contract (verified against node_modules/@assistant-ui/react/dist/
      // primitives/composer/ComposerInput.js): it destructures a fixed small
      // prop list and spreads everything else via `...rest` onto the
      // underlying textarea.
      Input: ({ onChange, onKeyDown, onBlur, ...rest }: {
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
        [key: string]: unknown;
      }) =>
        React.createElement('textarea', {
          ...rest,
          onChange, onKeyDown, onBlur,
          'data-testid': 'chat-input',
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

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
  ]
  const mockSkills = [
    { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
    { id: 'code-review',   name: 'Code Review',   version: '1.0', description: 'Reviews code quality',      verified: true,  status: 'active' },
  ]
  // F3 (test hardening): a small "@" mention-mode fixture — real, chat-eligible
  // agents (status active, type Main so isWorker() excludes none of them) so
  // the '@'-mode ARIA pass below exercises actual listbox/option rows instead
  // of an empty (unrenderable) menu.
  const mockAgents = [
    { id: 'ag-alpha', name: 'Alpha', type: 'Main', locked: false, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50 },
    { id: 'ag-bravo', name: 'Bravo', type: 'Main', locked: false, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50 },
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
      activeSessionId: 'sess_combobox_aria_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
}

beforeEach(resetStores)

describe('Composer combobox ARIA — textarea (deferred item 2)', () => {
  it('carries role="combobox" and aria-autocomplete="list" at all times', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    expect(input).toHaveAttribute('role', 'combobox')
    expect(input).toHaveAttribute('aria-autocomplete', 'list')
  })

  it('aria-expanded is false when the menu is closed, true when it opens', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    expect(input).toHaveAttribute('aria-expanded', 'false')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    expect(input).toHaveAttribute('aria-expanded', 'true')

    act(() => { fireEvent.change(input, { target: { value: '' } }) })
    expect(input).toHaveAttribute('aria-expanded', 'false')
  })

  it('aria-controls points at the listbox id only while the menu is rendered', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    expect(input).not.toHaveAttribute('aria-controls')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    expect(input).toHaveAttribute('aria-controls', 'composer-slash-menu')
    expect(screen.getByTestId('slash-menu')).toHaveAttribute('id', 'composer-slash-menu')
  })

  it('aria-activedescendant tracks the highlighted row through ArrowDown, and is absent when the menu is closed', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    expect(input).not.toHaveAttribute('aria-activedescendant')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    expect(input).toHaveAttribute('aria-activedescendant', 'composer-option-0')

    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    expect(input).toHaveAttribute('aria-activedescendant', 'composer-option-1')

    act(() => { fireEvent.keyDown(input, { key: 'Escape' }) })
    expect(input).not.toHaveAttribute('aria-activedescendant')
  })

  // F3 (test hardening, gate 6): the fixture has 5 commands (the two
  // synthetic client-only entries /resume + /workspace, then clear/help/
  // cancel) then 2 skills sorted alphabetically (Code Review, Web Research)
  // — 7 rows total, indices 0-6, with the commands→skills SECTION boundary
  // falling between index 4 and index 5. Cycling ArrowDown across that
  // boundary is exactly the case a per-section (rather than global)
  // highlight bug would miss.
  // Session-search enhancement: "/workspace" is a second synthetic
  // client-only command (useSlashMenu.ts's allCommands), inserted
  // immediately after "/resume" — shifts every subsequent index by one from
  // this test's prior fixture.
  it('aria-activedescendant always identifies the DOM element carrying aria-selected="true", stepping ArrowDown across the commands→skills section boundary, and every option id is unique', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    const initialOptions = screen.getAllByRole('option')
    expect(initialOptions).toHaveLength(7)
    // Option ids must be unique — a duplicate id would make getElementById
    // resolve to the WRONG row (the first match) without any test noticing,
    // silently invalidating every activedescendant assertion below.
    const ids = initialOptions.map((o) => o.id)
    expect(new Set(ids).size).toBe(ids.length)

    function assertActivedescendantMatchesSelectedRow() {
      const activedescendant = input.getAttribute('aria-activedescendant')
      expect(activedescendant).toBeTruthy()
      const resolved = document.getElementById(activedescendant!)
      expect(resolved).not.toBeNull()
      expect(resolved).toHaveAttribute('role', 'option')
      expect(resolved).toHaveAttribute('aria-selected', 'true')
      // Exactly one row selected at a time — activedescendant is never
      // ambiguous between two simultaneously-"selected" rows.
      expect(document.querySelectorAll('[role="option"][aria-selected="true"]')).toHaveLength(1)
      return resolved!
    }

    // Index 0 (initial highlight) — a command row.
    expect(assertActivedescendantMatchesSelectedRow().textContent).toContain('/resume')

    // Step through indices 1-4 — still inside the Commands section.
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) }) // index 1: /workspace
    assertActivedescendantMatchesSelectedRow()
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) }) // index 2: /clear
    assertActivedescendantMatchesSelectedRow()
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) }) // index 3: /help
    assertActivedescendantMatchesSelectedRow()
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) }) // index 4: /cancel — last Commands row
    expect(assertActivedescendantMatchesSelectedRow().textContent).toContain('/cancel')

    // Cross the section boundary: index 4 (Commands) -> index 5 (Skills).
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    expect(assertActivedescendantMatchesSelectedRow().textContent).toContain('/code-review')

    // Index 6 — still Skills.
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    expect(assertActivedescendantMatchesSelectedRow().textContent).toContain('/web-research')

    // Wraps back to index 0 (Commands) — the boundary is crossed in both directions.
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    expect(assertActivedescendantMatchesSelectedRow().textContent).toContain('/resume')
  })
})

describe('Composer combobox ARIA — "@" mention mode (F3 hardening: same contract as "/")', () => {
  it('the "@" agent-mention menu carries the identical combobox ARIA contract — role=combobox on the textarea, role=listbox on the menu, role=option rows, and a matching aria-activedescendant', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')

    act(() => { fireEvent.change(input, { target: { value: '@' } }) })

    expect(input).toHaveAttribute('role', 'combobox')
    expect(input).toHaveAttribute('aria-expanded', 'true')
    expect(input).toHaveAttribute('aria-controls', 'composer-slash-menu')

    const menu = screen.getByTestId('slash-menu')
    expect(menu).toHaveAttribute('role', 'listbox')
    expect(menu).toHaveAttribute('id', 'composer-slash-menu')

    // Fixture: 2 chat-eligible agents (Alpha, Bravo) — see mockAgents above.
    const options = screen.getAllByRole('option')
    expect(options).toHaveLength(2)
    for (const option of options) {
      expect(option).toHaveAttribute('tabindex', '-1')
      expect(option.id).toMatch(/^composer-option-\d+$/)
    }

    expect(input).toHaveAttribute('aria-activedescendant', 'composer-option-0')
    expect(document.getElementById('composer-option-0')).toHaveAttribute('aria-selected', 'true')
    expect(document.getElementById('composer-option-0')?.textContent).toContain('Alpha')

    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    expect(input).toHaveAttribute('aria-activedescendant', 'composer-option-1')
    expect(document.getElementById('composer-option-1')).toHaveAttribute('aria-selected', 'true')
    expect(document.getElementById('composer-option-1')?.textContent).toContain('Bravo')
    // The previously-highlighted row is no longer selected.
    expect(document.getElementById('composer-option-0')).toHaveAttribute('aria-selected', 'false')
  })
})

describe('Composer combobox ARIA — menu container and rows (deferred item 2)', () => {
  it('the menu container is role="listbox" with an aria-label', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    const menu = screen.getByTestId('slash-menu')
    expect(menu).toHaveAttribute('role', 'listbox')
    expect(menu).toHaveAttribute('aria-label')
  })

  it('rows are role="option" with a stable id, aria-selected tracking the highlight, and tabIndex=-1 (not tabbable)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    const options = screen.getAllByRole('option')
    expect(options.length).toBeGreaterThan(0)
    for (const option of options) {
      // Deferred item 2: amends the eb4de2a2 blanket tabIndex=0 stamp for
      // this widget specifically — options in a combobox's listbox popup are
      // not separately tabbable (see ChatScreen.tsx's row comment for the
      // full APG rationale).
      expect(option).toHaveAttribute('tabindex', '-1')
      expect(option.id).toMatch(/^composer-option-\d+$/)
    }
    // First option (index 0) is highlighted by default.
    expect(options[0]).toHaveAttribute('aria-selected', 'true')
    expect(options[1]).toHaveAttribute('aria-selected', 'false')

    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })
    const optionsAfter = screen.getAllByRole('option')
    expect(optionsAfter[0]).toHaveAttribute('aria-selected', 'false')
    expect(optionsAfter[1]).toHaveAttribute('aria-selected', 'true')
  })

  it('section headers are role="presentation" (not part of the listbox options)', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    const header = screen.getByText('Commands')
    expect(header).toHaveAttribute('role', 'presentation')
    // Section headers must never be queryable as options.
    expect(screen.queryAllByRole('option', { name: /Commands/ })).toHaveLength(0)
  })

  it('mouse selection (onMouseDown) still works on role="option" rows — the ARIA changes did not remove mouse selection', () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('chat-input')
    act(() => { fireEvent.change(input, { target: { value: '/' } }) })

    // Selecting "/cancel" (a client command) via mouse should clear the
    // composer back to empty and close the menu — proving onSelect still
    // runs from a role="option" row exactly as it did from the old
    // role-less row.
    const cancelRow = screen.getByText('/cancel').closest('[role="option"]')
    expect(cancelRow).not.toBeNull()
    act(() => { fireEvent.mouseDown(cancelRow!) })

    expect(screen.queryByTestId('slash-menu')).not.toBeInTheDocument()
  })
})

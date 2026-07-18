// Skills filter mode — typing "/skills" shows only the Skills section.
// Typing any "/" prefix filters both sections by prefix matching.

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

// 10 skills, zero-padded numeric suffixes ("Skill 04" not "Skill 4") — used
// by the "capped at 8" test to actually exercise the slice(0, SECTION_CAP)
// cap. Deferred item 3 sorts skills alphabetically by name BEFORE the cap
// (useSlashMenu.ts), so unpadded "Skill 4"/"Skill 10" would sort as
// "Skill 10" < "Skill 4" (lexicographic, not numeric) — zero-padding keeps
// this fixture's alphabetical order matching its numeric intent, same as
// the analogous fixture in useSlashMenu.test.ts's cap describe block.
const ALL_MOCK_SKILLS = [
  { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
  { id: 'code-review',   name: 'Code Review',   version: '1.0', description: 'Reviews code quality',      verified: true,  status: 'active' },
  { id: 'data-analysis', name: 'Data Analysis', version: '1.0', description: 'Analyses datasets',         verified: false, status: 'active' },
  { id: 'skill-04',      name: 'Skill 04',      version: '1.0', description: 'Description 4',             verified: true,  status: 'active' },
  { id: 'skill-05',      name: 'Skill 05',      version: '1.0', description: 'Description 5',             verified: true,  status: 'active' },
  { id: 'skill-06',      name: 'Skill 06',      version: '1.0', description: 'Description 6',             verified: true,  status: 'active' },
  { id: 'skill-07',      name: 'Skill 07',      version: '1.0', description: 'Description 7',             verified: true,  status: 'active' },
  { id: 'skill-08',      name: 'Skill 08',      version: '1.0', description: 'Description 8',             verified: true,  status: 'active' },
  { id: 'skill-09',      name: 'Skill 09',      version: '1.0', description: 'Description 9',             verified: true,  status: 'active' },
  { id: 'skill-10',      name: 'Skill 10',      version: '1.0', description: 'Description 10',            verified: true,  status: 'active' },
]
// Deferred item 3: even zero-padded, "Web Research" sorts LAST alphabetically
// among ALL_MOCK_SKILLS' 10 names (W > S > D > C) and never survives an
// 8-item alphabetical cap — that's real, correct, and now-deterministic
// behavior (see the "capped at 8" test below), not a bug. The three
// non-cap-related tests that only need a handful of unambiguous, always-
// visible skills use this smaller fixture instead, so they aren't
// incidentally coupled to the specific 10-item cap fixture's sort order.
const SMALL_MOCK_SKILLS = [
  { id: 'web-research',  name: 'Web Research',  version: '1.0', description: 'Web search and extraction', verified: true,  status: 'active' },
  { id: 'code-review',   name: 'Code Review',   version: '1.0', description: 'Reviews code quality',      verified: true,  status: 'active' },
  { id: 'data-analysis', name: 'Data Analysis', version: '1.0', description: 'Analyses datasets',         verified: false, status: 'active' },
]
// Mutable per-test override — defaults to the small fixture; the "capped at
// 8" test below swaps in ALL_MOCK_SKILLS for its own run only.
let skillsFixture: typeof ALL_MOCK_SKILLS = SMALL_MOCK_SKILLS

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  const mockCommands = [
    { name: 'clear',   label: '/clear',   description: 'Start a new conversation',   delivery: 'client', available_while_streaming: false },
    { name: 'help',    label: '/help',    description: 'Show available commands',     delivery: 'client', available_while_streaming: false },
    { name: 'model',   label: '/model',   description: 'Change the chat model',       delivery: 'client', available_while_streaming: false },
    { name: 'agents',  label: '/agents',  description: 'Open agent selector',         delivery: 'client', available_while_streaming: false },
    { name: 'cancel',  label: '/cancel',  description: 'Cancel the current turn',     delivery: 'client', available_while_streaming: true  },
  ]
  return {
    ...actual,
    useQuery: (opts: { queryKey: unknown[] }) => {
      const key = opts?.queryKey
      if (Array.isArray(key) && key[0] === 'commands' && key[1] === 'web') {
        return { data: mockCommands, isError: false, refetch: vi.fn() }
      }
      if (Array.isArray(key) && key[0] === 'skills') {
        return { data: skillsFixture, isError: false, refetch: vi.fn() }
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
// Composer Redesign (variant A1): the skills-filter slash menu is independent
// of the picker/model/token sub-components — stub them to null so their
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
      activeSessionId: 'sess_skills_filter_test',
      activeAgentId: 'general-assistant',
      activeAgentType: null,
    })
  })
  skillsFixture = SMALL_MOCK_SKILLS
}

beforeEach(resetStores)

describe('Skills filter mode (D9)', () => {
  it('typing "/skills" shows all skills and no commands', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/skills' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.getByText('/code-review')).toBeInTheDocument()
    expect(screen.getByText('/data-analysis')).toBeInTheDocument()
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/model')).not.toBeInTheDocument()
  })

  it('typing "/" shows both sections with all commands and skills', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Commands present
    expect(screen.getByText('/clear')).toBeInTheDocument()
    expect(screen.getByText('/cancel')).toBeInTheDocument()
    // Skills present
    expect(screen.getByText('/web-research')).toBeInTheDocument()
    expect(screen.getByText('/code-review')).toBeInTheDocument()
  })

  it('typing "/co" shows /code-review skill and no matching commands (regression: "co" prefix)', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/co' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // /code-review skill matches "co"
    expect(screen.getByText('/code-review')).toBeInTheDocument()
    // No commands start with "co"
    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
  })

  it('skills are capped at 8 results maximum — 10 skills seeded, only 8 shown, alphabetically ordered, with a "+2 more" footer', async () => {
    // Swap in the 10-entry fixture for this test only (default is the
    // 3-entry SMALL_MOCK_SKILLS — see the module-level `skillsFixture`
    // toggle and its doc comment).
    skillsFixture = ALL_MOCK_SKILLS
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: '/' } }) })
    act(() => { fireEvent.keyDown(input, { key: 'ArrowDown' }) })

    // Deferred item 2: rows are `role="option"` now (not the button's
    // implicit `role="button"`), since they're listbox options — query by
    // that role rather than "button".
    const skillOptions = screen
      .getAllByRole('option')
      .filter((el) => {
        const text = el.textContent ?? ''
        return text.match(/^\/(web-research|code-review|data-analysis|skill-\d+)/)
      })
    // Exactly 8 skill items (cap = SECTION_CAP, mock has 10).
    expect(skillOptions.length).toBe(8)
    // Deferred item 3: alphabetical by name, zero-padded so numeric order
    // matches — Code Review, Data Analysis, Skill 04..Skill 09 fill the 8
    // slots; Skill 10 and Web Research (last alphabetically: "W" > "S") are
    // the 2 pushed past the cap.
    // Note: the label and description spans are adjacent with no whitespace
    // between them in textContent (e.g. "/code-reviewCode Review — ..."), so
    // match only slug characters, not `\S+` (which would swallow into the
    // description).
    expect(skillOptions.map((el) => el.textContent?.match(/^\/([a-z0-9-]+)/)?.[1])).toEqual([
      'code-review', 'data-analysis', 'skill-04', 'skill-05', 'skill-06', 'skill-07', 'skill-08', 'skill-09',
    ])
    expect(screen.queryByText('/skill-10')).not.toBeInTheDocument()
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
    // Deferred item 3: the "+N more" footer surfaces the overflow instead of
    // silently hiding it.
    expect(screen.getByTestId('slash-menu-footer')).toHaveTextContent('+2 more')
  })

  it('menu does not show when input does not start with "/"', async () => {
    render(<OmnipusComposer />)
    const input = screen.getByTestId('composer-input')

    act(() => { fireEvent.change(input, { target: { value: 'hello world' } }) })

    expect(screen.queryByText('/clear')).not.toBeInTheDocument()
    expect(screen.queryByText('/web-research')).not.toBeInTheDocument()
  })
})

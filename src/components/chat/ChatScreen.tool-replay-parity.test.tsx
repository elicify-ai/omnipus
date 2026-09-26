/**
 * ChatScreen.tool-replay-parity.test.tsx — toolui-analysis items 2 + 4
 * (founder-approved 2026-09-26): history replay must route reloaded tool
 * calls through the SAME dedicated components the live path uses, instead of
 * the generic badge.
 *
 *   bash (+ legacy aliases)  -> BashOutputBlock   (item 2)
 *   read_file / file.read    -> FileReadBlock     (item 4)
 *   list_directory / list_dir / file.list -> FileTreeBlock (item 4)
 *   search_web / web_search  -> WebSearchBlock    (item 4)
 *   fetch_url / web_fetch    -> WebFetchBlock     (item 4)
 *
 * Harness copied from ChatScreen.issue-617-replay-outcome.test.tsx (full
 * ChatScreen render, real tool components, ResizeObserver forced undefined,
 * seeded PositionedToolCall history messages). Each case asserts the
 * DEDICATED row rendered (its toggle testid + its own header text) and that
 * the block is collapsed by default, then expands it via the same testid a
 * user would click.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen, fireEvent } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'
import { BASH_TOOL_NAMES } from './tools/BashOutput'

// ctui-gate sev-7: every toolName makeAssistantToolUI was called with at
// module import. vi.hoisted runs before the file's imports, so the array is
// already initialized when BashOutput.tsx's module-scope makeBashUI calls
// fire during import.
const mockRegisteredToolUIs = vi.hoisted(() => [] as string[])

vi.mock('@assistant-ui/react', () => {
  return {
    useThreadViewportStore: () => ({ getState: () => ({ isAtBottom: true }) }),
    ThreadPrimitive: {
      Root: ({ children, className }: { children: React.ReactNode; className?: string }) =>
        React.createElement('div', { className }, children),
      Viewport: React.forwardRef(
        (
          { children, className, style, 'data-testid': testId }: {
            children?: React.ReactNode; className?: string; style?: React.CSSProperties; 'data-testid'?: string
          },
          ref: React.Ref<HTMLDivElement>,
        ) => React.createElement('div', { ref, className, style, 'data-testid': testId }, children),
      ),
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
      Input: ({ disabled, placeholder, className, onChange, onKeyDown, onBlur }: {
        disabled?: boolean; placeholder?: string; className?: string;
        onChange?: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
        onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
        onBlur?: () => void;
      }) =>
        React.createElement('textarea', {
          disabled, placeholder, className, onChange, onKeyDown, onBlur,
          'data-testid': 'composer-input',
        }),
      Send: ({ disabled, children, className, 'data-testid': testId }: {
        disabled?: boolean; children?: React.ReactNode; className?: string; 'data-testid'?: string
      }) =>
        React.createElement('button', { type: 'button', disabled, className, 'data-testid': testId ?? 'chat-send' }, children),
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
    MessagePartPrimitive: { InProgress: () => null },
    ActionBarPrimitive: {
      Root: ({ children }: { children: React.ReactNode }) => React.createElement('div', {}, children),
      Copy: ({ children }: { children: React.ReactNode }) => React.createElement('span', {}, children),
    },
    AuiIf: () => null,
    useComposerRuntime: () => ({
      getState: () => ({ text: '' }),
      setText: vi.fn(),
      addAttachment: vi.fn(),
      subscribe: vi.fn(() => vi.fn()),
    }),
    useMessage: () => ({
      id: 'msg_streaming',
      role: 'assistant',
      status: { type: 'running' },
      content: [],
    }),
    useAttachment: vi.fn(() => ({
      id: 'att-default',
      name: 'file.txt',
      contentType: 'text/plain',
      file: undefined,
      status: { type: 'complete' },
      content: [],
    })),
    makeAssistantToolUI: (opts: { toolName: string }) => {
      mockRegisteredToolUIs.push(opts.toolName)
      return () => null
    },
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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn().mockResolvedValue([]),
    fetchAboutInfo: vi.fn().mockResolvedValue({ preview_port: 5001 }),
    createSession: vi.fn(),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
    isApiError: vi.fn().mockReturnValue(false),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('./historical-markdown', () => ({
  HistoricalMessageMarkdown: ({ content }: { content: string }) =>
    React.createElement('div', { 'data-testid': 'historical-markdown' }, content),
}))

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
// The five real tool blocks stay UNMOCKED — this file's whole point is that
// the replay loop routes through them. IframePreview (WebServeBlock's own
// preview-link renderer, an unrelated surface) is stubbed away.
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('@/components/shared/IconRenderer', () => ({ IconRenderer: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-tool-replay-parity'

function seedBucket(messages: ChatMessage[]): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
  }))
}

beforeEach(() => {
  useConnectionStore.setState({
    isConnected: true,
    liteMode: false,
    reconnectPhase: null,
    reconnectAttempt: 0,
    connectionError: null,
    connection: null,
  })
  vi.unstubAllGlobals()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  ;(globalThis as any).ResizeObserver = undefined

  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
})

function seedAssistantWithToolCall(
  callId: string,
  tool: string,
  params: Record<string, unknown>,
  result: unknown,
  status: 'success' | 'error' | 'cancelled' = 'success',
  error?: string,
): void {
  const now = new Date().toISOString()
  const assistantMsg: ChatMessage = {
    id: `msg_${callId}`,
    role: 'assistant',
    content: '',
    timestamp: now,
    status: 'done',
    tool_calls: [
      {
        id: callId,
        tool,
        params,
        status,
        result,
        ...(error !== undefined ? { error } : {}),
      } as PositionedToolCall,
    ],
  }
  seedBucket([assistantMsg])
}

async function renderScreen() {
  let container!: HTMLElement
  await act(async () => {
    const res = render(<ChatScreen />)
    container = res.container
  })
  return container
}

describe('ChatScreen replay parity — toolui-analysis item 2: bash through BashOutputBlock', () => {
  it('a reloaded bash call renders the dedicated collapsed block, expandable to the full command + output', async () => {
    seedAssistantWithToolCall('tc_bash', 'bash', { command: 'git status --porcelain' }, 'M file.go\n', 'success')
    await renderScreen()

    // Dedicated row (not the generic badge): the BashOutputBlock toggle with
    // the "bash" label, the command summary, and the Done status.
    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle.textContent).toContain('bash')
    expect(toggle.textContent).toContain('git status --porcelain')
    expect(toggle.textContent).toContain('Done')
    // Collapsed: no command/output body yet.
    expect(container_has_pre(toggle)).toBe(false)

    // Expanding shows the full command + output — same body the live path has.
    fireEvent.click(toggle)
    const pres = screen.getByTestId('bash-output-toggle').closest('div')!.parentElement!.querySelectorAll('pre')
    expect(pres.length).toBe(2)
    expect(pres[0].textContent).toBe('git status --porcelain')
    expect(pres[1].textContent).toBe('M file.go\n')
  })

  it('a reloaded legacy workspace_shell call routes through the same BashOutputBlock', async () => {
    seedAssistantWithToolCall('tc_shell', 'workspace_shell', { command: 'ls -la' }, 'total 0\n', 'success')
    await renderScreen()

    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('ls -la')
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('a reloaded FAILED bash call shows the Failed header while collapsed', async () => {
    seedAssistantWithToolCall('tc_bash_fail', 'bash', { command: 'make test' }, 'exit 1', 'error')
    await renderScreen()

    const toggle = screen.getByTestId('bash-output-toggle')
    expect(toggle.textContent).toContain('Failed')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })
})

describe('ChatScreen replay parity — item 4: read_file / list_directory', () => {
  it('a reloaded read_file call renders FileReadBlock (collapsed; expandable to the content)', async () => {
    seedAssistantWithToolCall('tc_read', 'read_file', { path: '/ws/pkg/tools/web.go' }, 'package tools\n\nfunc Name() string { return "search_web" }\n', 'success')
    await renderScreen()

    const toggle = screen.getByTestId('file-read-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // Header: basename + line count.
    expect(toggle.textContent).toContain('web.go')
    expect(toggle.textContent).toContain('4 lines')
    // Collapsed: content body absent.
    expect(screen.queryByTestId('bash-output-toggle')).toBeNull()
    const pres = document.querySelectorAll('pre')
    expect(pres.length).toBe(0)

    fireEvent.click(toggle)
    const bodyPre = screen.getByTestId('file-read-toggle').closest('div')!.parentElement!.querySelector('pre')
    expect(bodyPre?.textContent).toContain('search_web')
  })

  it('a reloaded list_directory call (backend canonical name, #898) renders FileTreeBlock', async () => {
    seedAssistantWithToolCall('tc_ls', 'list_directory', { path: '/ws/pkg/tools' }, 'web.go\nfilesystem.go\n', 'success')
    await renderScreen()

    const toggle = screen.getByTestId('file-tree-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle.textContent).toContain('/ws/pkg/tools')
    expect(toggle.textContent).toContain('2 entries')
    expect(screen.queryByTestId('file-tree-panel')).toBeNull()

    fireEvent.click(toggle)
    expect(screen.getByTestId('file-tree-panel')).toBeInTheDocument()
    expect(screen.getByTestId('file-tree-panel').textContent).toContain('web.go')
  })

  it('a reloaded legacy list_dir call routes through the same FileTreeBlock', async () => {
    seedAssistantWithToolCall('tc_ls_legacy', 'list_dir', { path: '/ws' }, 'a.go\n', 'success')
    await renderScreen()
    const toggle = screen.getByTestId('file-tree-toggle')
    expect(toggle.textContent).toContain('/ws')
    expect(toggle.textContent).toContain('1 entries')
  })
})

describe('ChatScreen replay parity — item 4: search_web / fetch_url', () => {
  it('a reloaded search_web call (backend canonical name, #898) renders WebSearchBlock', async () => {
    seedAssistantWithToolCall('tc_search', 'search_web', { query: 'go embed spa' }, '1. Omnipus agentic core\n   https://omnipus.ai\n   single Go binary, kernel sandbox\n2. Second result\\n   https://example.com\\n   another snippet', 'success')
    await renderScreen()

    const toggle = screen.getByTestId('web-search-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // WebSearchBlock header: literal tool name + query + parsed-result count.
    expect(toggle.textContent).toContain('search_web')
    expect(toggle.textContent).toContain('go embed spa')
    expect(toggle.textContent).toContain('2 results')

    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Omnipus agentic core')).toBeInTheDocument()
  })

  it('a reloaded fetch_url call renders WebFetchBlock', async () => {
    seedAssistantWithToolCall('tc_fetch', 'fetch_url', { url: 'https://omnipus.ai/docs' }, '<html>docs</html>', 'success')
    await renderScreen()

    const toggle = screen.getByTestId('web-fetch-toggle')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(toggle.textContent).toContain('omnipus.ai/docs')

    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('a reloaded legacy web_search call routes through the same WebSearchBlock', async () => {
    seedAssistantWithToolCall('tc_search_legacy', 'web_search', { query: 'x' }, 'r', 'success')
    await renderScreen()
    const toggle = screen.getByTestId('web-search-toggle')
    expect(toggle.textContent).toContain('web_search')
  })
})

/** ctui-gate fix 3c: seeds a failed read_file alongside an ACTIVE goal whose
 * record is empty (the GoalSetupFailureLine override's exact precondition),
 * proving the dedicated read_file branch pre-empts that override on replay. */
function seedReadFileFailureWithActiveGoal(): void {
  const goalFrame: GoalStatusFrame = {
    type: 'goal_status',
    session_id: SID,
    goal_id: 'goal_read_fail_test',
    condition: 'ship the release notes',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 1,
    cap: 16,
    state: 'active',
  }
  const messages: ChatMessage[] = [
    {
      id: 'u_goal',
      role: 'user',
      content: '/goal ship the release notes',
      timestamp: new Date().toISOString(),
      status: 'done',
    },
    {
      id: 'a_goal',
      role: 'assistant',
      content: '',
      timestamp: new Date().toISOString(),
      status: 'done',
      tool_calls: [
        {
          id: 'tc_read_goal',
          tool: 'read_file',
          params: { path: '/etc/hosts' },
          status: 'error',
          error: 'permission denied: /etc/hosts',
        },
      ],
    },
  ]
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...((s.sessionsById ?? {})[SID] ?? {}),
        ...bucket,
        isStreaming: false,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        sessionTokens: 0,
        sessionCost: 0,
        rateLimitEvent: null,
        lastUserMessageAt: null,
        cancelStage: null,
        lastReceivedEventTime: null,
        trimmedCount: 0,
        goalStatus: goalFrame,
      },
    },
    messages,
    isStreaming: false,
    isReplaying: false,
    replayCompletedForSession: SID,
    goalStatus: goalFrame,
  }))
}

describe('ChatScreen replay parity — ctui-gate fix 3b: remaining aliases', () => {
  const ALIAS_CASES: Array<{
    callId: string
    tool: string
    params: Record<string, unknown>
    result: string
    toggleTestId: string
    headerText: string
  }> = [
    { callId: 'tc_dot_read', tool: 'file.read', params: { path: '/ws/a.go' }, result: 'package a\n', toggleTestId: 'file-read-toggle', headerText: 'a.go' },
    { callId: 'tc_dot_list', tool: 'file.list', params: { path: '/ws' }, result: 'x.go\n', toggleTestId: 'file-tree-toggle', headerText: '/ws' },
    { callId: 'tc_web_fetch', tool: 'web_fetch', params: { url: 'https://example.com/x' }, result: '<html>x</html>', toggleTestId: 'web-fetch-toggle', headerText: 'example.com' },
    { callId: 'tc_exec', tool: 'exec', params: { command: 'echo hi' }, result: 'hi\n', toggleTestId: 'bash-output-toggle', headerText: 'echo hi' },
    { callId: 'tc_dot_shell', tool: 'workspace.shell', params: { command: 'echo dot' }, result: 'dot\n', toggleTestId: 'bash-output-toggle', headerText: 'echo dot' },
    { callId: 'tc_shell_bg', tool: 'workspace_shell_bg', params: { command: 'sleep 1' }, result: '', toggleTestId: 'bash-output-toggle', headerText: 'sleep 1' },
    { callId: 'tc_dot_shell_bg', tool: 'workspace.shell_bg', params: { command: 'sleep 2' }, result: '', toggleTestId: 'bash-output-toggle', headerText: 'sleep 2' },
  ]
  for (const c of ALIAS_CASES) {
    it(`a replayed "${c.tool}" call renders its dedicated collapsed toggle, not the generic badge`, async () => {
      seedAssistantWithToolCall(c.callId, c.tool, c.params, c.result)
      await renderScreen()
      const toggle = screen.getByTestId(c.toggleTestId)
      expect(toggle).toHaveAttribute('aria-expanded', 'false')
      expect(toggle.textContent).toContain(c.headerText)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  }

  it('BASH_TOOL_NAMES equals exactly the six names registered via makeBashUI (ctui-gate sev-7)', () => {
    const LITERAL = [
      'bash',
      'exec',
      'workspace_shell',
      'workspace.shell',
      'workspace_shell_bg',
      'workspace.shell_bg',
    ]
    expect([...BASH_TOOL_NAMES].sort()).toEqual([...LITERAL].sort())
    // Each array entry was really handed to makeAssistantToolUI (makeBashUI's
    // registrations run at module import) — and exactly once each.
    for (const name of LITERAL) {
      expect(mockRegisteredToolUIs).toContain(name)
      expect(mockRegisteredToolUIs.filter((n) => n === name).length).toBe(1)
    }
  })
})

describe('ChatScreen replay parity — ctui-gate fix 3c: failed read_file', () => {
  it('a reloaded FAILED read_file shows Failed collapsed and the reason on expand', async () => {
    seedAssistantWithToolCall('tc_read_fail', 'read_file', { path: '/etc/hosts' }, undefined, 'error', 'permission denied: /etc/hosts')
    await renderScreen()

    const toggle = screen.getByTestId('file-read-toggle')
    expect(toggle.textContent).toContain('Failed')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    // Collapsed: no body — container_has_pre now checks the ROW container
    // (the toggle's pres are its siblings), the ctui-gate 3d fix.
    expect(container_has_pre(toggle)).toBe(false)

    fireEvent.click(toggle)
    const row = screen.getByTestId('file-read-toggle').closest('div')!.parentElement!
    expect(row.querySelector('pre')?.textContent).toContain('permission denied')
  })

  it('a reloaded FAILED read_file keeps its dedicated toggle while a goal is active with an empty record — never GoalSetupFailureLine', async () => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
    seedReadFileFailureWithActiveGoal()
    await renderScreen()

    expect(screen.getByTestId('file-read-toggle')).toBeInTheDocument()
    expect(screen.queryByTestId('goal-setup-failure-line')).toBeNull()
  })
})

// Helper: does the ROW containing this toggle render any <pre>? The toggle
// button itself never contains a <pre> — the command/output bodies are its
// SIBLINGS inside the row div — so the query goes one level up from the
// toggle to the row container (ctui-gate finding 3d: the old version queried
// inside the toggle and could never see a body, so its "false" proved
// nothing).
function container_has_pre(el: HTMLElement): boolean {
  const row = el.closest('div')?.parentElement
  if (!row) return false
  return row.querySelectorAll('pre').length > 0
}

/**
 * ChatScreen.task-card-saved.test.tsx — issue #1683, round 5 / R2-4 (saved rows).
 *
 * CHECK round 2 (R2-4): no committed test mounted the task path through the
 * real ChatScreen. The direct-dispatcher test (TaskToolUI.historical.test.tsx)
 * and the badge test (TaskToolUI.replay.test.tsx) bypass the screen, so a
 * future task-only condition in the thread filter or the row component could
 * hide the card, or turn it back into the generic badge, while every test
 * stayed green. This file mounts the REAL ChatScreen over a seeded chat store
 * and checks what a person reloading a workspace chat sees for a saved task
 * call.
 *
 * Path under test: ChatScreen -> ChatMessageList (plain-list fallback, no
 * ResizeObserver) -> ChatScreen.tsx::VirtualAssistantMessageRow ->
 * HistoricalToolCallBlock.tsx::renderHistoricalToolCall -> TaskToolUI.tsx::
 * TaskToolCardBlock. Nothing between the screen and the card is mocked: the
 * generic tool component is the REAL one, so a card that falls back to the
 * generic badge is visible as `tool-call-badge`. (The live -> saved
 * transition and the virtualized list are in
 * ChatScreen.task-card-live-to-saved.test.tsx.)
 *
 * Harness: ChatScreen.tool-replay-parity.test.tsx (full ChatScreen render,
 * ResizeObserver undefined, seeded saved tool calls); the bash control proves
 * the harness can show a saved row at all, and the unknown-tool control proves
 * the generic badge is detectable in it.
 *
 * ORACLES — never the card's output:
 *  - D1 (team-lead, round 4): saved history renders the same task card as
 *    live, for all four actions. D3 headings: "I scheduled the task" (create
 *    with a schedule), "I updated the task", "I started the task",
 *    "The task is already running" (run whose result says so),
 *    "I deleted the task".
 *  - D5 (reaffirmed): "I couldn't <verb> the task, and no reason was given.
 *    Try again." appears ONLY when no reason exists anywhere; a reason counts
 *    when it is the call's error field, a string result, a {text} result, or
 *    the `reason` of a generated structured failure (delegation_denied).
 *  - D8: with a known reason the card shows "I couldn't <verb> the task.
 *    <reason>" (reason UNCHANGED), keeps the "Open tasks" link, and the alert
 *    stays role="alert".
 *
 * The saved success rows are regression protection (green when the dispatcher
 * routes task tools, which it does at HEAD 1bd56231b); the known-reason rows
 * pin D8 through the screen.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen, within } from '@testing-library/react'
import * as React from 'react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import type { DelegationFailure } from '@/lib/api/generated/asyncapi-types'

vi.mock('@assistant-ui/react', async () => (await import('@/test/assistantUiMock')).createAssistantUiMock({
  makeAssistantToolUI: () => () => null,
}))

vi.mock('@tanstack/react-query', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-query')>()
  return {
    ...actual,
    useQuery: (opts: { queryKey?: readonly unknown[] }) => ({ data: queryDataWithEmptyHistory(opts?.queryKey), isError: false, refetch: vi.fn() }),
    useMutation: () => ({ mutate: vi.fn(), isPending: false }),
    useQueryClient: () => ({ invalidateQueries: vi.fn(), removeQueries: vi.fn() }),
  }
})

// The "Open tasks" link needs a router the unit mount does not have, so Link
// renders as an <a> whose href is the route with its params and search folded
// in (the same shim the TaskToolUI unit tests use). The rest is what the
// screen itself reads from the router.
vi.mock('@tanstack/react-router', () => ({
  useRouter: () => ({ navigate: vi.fn() }),
  useSearch: () => ({}),
  Link: (props: {
    to?: unknown
    params?: Record<string, unknown>
    search?: Record<string, unknown>
    children?: React.ReactNode
  } & Record<string, unknown>) => {
    let href = typeof props.to === 'string' ? props.to : ''
    for (const [k, v] of Object.entries(props.params ?? {})) href = href.replace(`$${k}`, String(v))
    const searchEntries = Object.entries(props.search ?? {}).map(([k, v]) => [k, String(v)] as [string, string])
    const qs = searchEntries.length > 0 ? '?' + new URLSearchParams(searchEntries).toString() : ''
    const { to: _to, params: _params, search: _search, children, ...rest } = props
    return (
      <a href={href + qs} {...rest}>
        {children}
      </a>
    )
  },
}))

import { emptyHistoryPage, queryDataWithEmptyHistory, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
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
vi.mock('./IframePreview', () => ({ IframePreview: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => React.createElement('div', {}),
}))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

import { ChatScreen } from './ChatScreen'

const SID = 'test-session-task-card-saved'

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
  // No ResizeObserver selects ChatMessageList's plain-list fallback, which
  // renders every saved message through VirtualAssistantMessageRow.
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

/** One saved assistant reply holding a single tool call, as the history store holds it after a reload. */
function seedSavedCall(
  tool: string,
  params: Record<string, unknown>,
  outcome: { result?: unknown; status?: 'success' | 'error'; error?: string },
): void {
  const callId = `tc_${tool}`
  const assistantMsg: ChatMessage = {
    id: `msg_${callId}`,
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    tool_calls: [
      {
        id: callId,
        tool,
        params,
        status: outcome.status ?? 'success',
        ...(outcome.result !== undefined ? { result: outcome.result } : {}),
        ...(outcome.error !== undefined ? { error: outcome.error } : {}),
      } as PositionedToolCall,
    ],
  }
  seedBucket([assistantMsg])
}

async function renderScreen(): Promise<HTMLElement> {
  let container!: HTMLElement
  await act(async () => {
    const res = render(<ChatScreen />)
    container = res.container
  })
  return container
}

// Mon 2026-11-09 09:00 Europe/Berlin = 08:00 UTC.
const MON_0900_BERLIN_MS = Date.UTC(2026, 10, 9, 8, 0, 0)
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' },
}
const WORKSPACE_ARGS = { workspace_id: 'ws_1' }

const NO_REASON_RE = /no reason was given/i
const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

/** The element's OWN text (what getByText matched on), whitespace-normalised; a child such as the link does not count. */
function ownText(el: HTMLElement): string {
  return Array.from(el.childNodes)
    .filter((n) => n.nodeType === 3)
    .map((n) => n.textContent ?? '')
    .join('')
    .replace(/\s+/g, ' ')
    .trim()
}

describe('R2-4 — harness controls: the screen shows a saved row, and the generic badge is detectable in it', () => {
  it('a saved bash call renders its dedicated block (the harness can show a saved row at all)', async () => {
    seedSavedCall('bash', { command: 'git status --porcelain' }, { result: 'M file.go\n' })
    await renderScreen()
    expect(screen.getByTestId('bash-output-toggle')).toBeTruthy()
    expect(screen.queryByTestId('task-tool-card')).toBeNull()
  })

  it('a saved call to a tool with no dedicated block renders the generic badge (the instrument can see the failure)', async () => {
    seedSavedCall('totally_unknown_tool', { x: 1 }, { result: 'plain text result' })
    await renderScreen()
    expect(screen.getByTestId('tool-call-badge')).toBeTruthy()
    expect(screen.queryByTestId('task-tool-card')).toBeNull()
  })
})

describe('R2-4 / D1 — a saved task call on the real ChatScreen is the task card, never the generic badge', () => {
  it('create_task with a recurring schedule: the scheduled heading, the schedule line, the title', async () => {
    seedSavedCall(
      'create_task',
      { title: 'Weekly report', ...WORKSPACE_ARGS, trigger: RECURRING_TRIGGER },
      { result: { text: JSON.stringify({ task_id: 'task_1', status: 'inbox' }) } },
    )
    await renderScreen()
    const card = screen.getByTestId('task-tool-card')
    expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I scheduled the task')
    expect(within(card).getByTestId('task-tool-schedule').textContent ?? '').toMatch(/berlin/i)
    expect(card.textContent ?? '').toContain('Weekly report')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('update_task: "I updated the task"', async () => {
    seedSavedCall(
      'update_task',
      { task_id: 'task_1', title: 'Weekly report' },
      { result: { text: JSON.stringify({ task_id: 'task_1', status: 'inbox', updated_fields: ['title'] }) } },
    )
    await renderScreen()
    expect((within(screen.getByTestId('task-tool-card')).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I updated the task')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('run_task: "I started the task"; a result that says the task was already running says so instead', async () => {
    seedSavedCall(
      'run_task',
      { task_id: 'task_1' },
      { result: { text: JSON.stringify({ task_id: 'task_1', status: 'in_progress', session_id: 'sess_1' }) } },
    )
    await renderScreen()
    expect((within(screen.getByTestId('task-tool-card')).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I started the task')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('run_task whose result says it was already running: "The task is already running"', async () => {
    seedSavedCall(
      'run_task',
      { task_id: 'task_1' },
      { result: { text: JSON.stringify({ task_id: 'task_1', status: 'in_progress', session_id: 'sess_1', note: 'already running' }) } },
    )
    await renderScreen()
    expect((within(screen.getByTestId('task-tool-card')).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('The task is already running')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('delete_task: "I deleted the task"', async () => {
    seedSavedCall('delete_task', { task_id: 'task_1' }, { result: { text: JSON.stringify({ deleted: 'task_1' }) } })
    await renderScreen()
    expect((within(screen.getByTestId('task-tool-card')).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe('I deleted the task')
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })
})

describe('R2-4 / D5 + D8 — a saved failed task call on the real ChatScreen', () => {
  /**
   * D8: "I couldn't <verb> the task. <reason>" in the alert, the reason unchanged, "Open tasks" kept.
   * The failure line is LOCATED by the reason (whichever wording surrounds it) and its wording is then
   * pinned exactly, so a wrong wording fails the exact check with the text that was really rendered in
   * the message — not a locator error followed by a (truncated) screen dump.
   */
  function expectKnownReasonFailure(verb: string, reason: string, opts: { link: boolean }): void {
    const sentence = screen.getByText(reason, { exact: false })
    expect(ownText(sentence), 'D8 wording').toBe(`I couldn't ${verb} the task. ${reason}`)
    expect(sentence.closest('[role="alert"]'), 'the sentence sits in the catalogued error region (D7/D8)').not.toBeNull()
    if (opts.link) {
      const open = screen.getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', '/workspaces/ws_1/chat?panel=tasks')
    }
    expect(screen.queryByText(NO_REASON_RE)).toBeNull()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    expect(screen.queryByTestId('task-tool-schedule')).toBeNull()
    expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
  }

  it('update_task with a stored error: the D8 sentence and the Open tasks link', async () => {
    seedSavedCall(
      'update_task',
      { task_id: 'task_99', title: 'Weekly report', ...WORKSPACE_ARGS },
      { status: 'error', error: 'task not found: task_99' },
    )
    await renderScreen()
    expectKnownReasonFailure('update', 'task not found: task_99', { link: true })
  })

  it('create_task whose reason is only in a {text} result: the D8 sentence and the Open tasks link', async () => {
    const reason = 'agent "agent_zz" is not a member of this workspace.'
    seedSavedCall(
      'create_task',
      { title: 'Weekly report', ...WORKSPACE_ARGS, trigger: RECURRING_TRIGGER },
      { status: 'error', result: { text: reason } },
    )
    await renderScreen()
    expectKnownReasonFailure('create', reason, { link: true })
  })

  it('run_task refused by delegation policy (structured failure result): the D8 sentence with the denial reason', async () => {
    // Built from the GENERATED DelegationFailure type (asyncapi-types.ts) and
    // the shape of pkg/tools/result.go::DelegationDeniedResult.
    const denial: DelegationFailure = {
      error: 'delegation_denied',
      reason: 'Scout is not in your trust set, so you cannot start its task.',
      policy: 'trust_set',
      tool: 'run_task',
      target_agent_id: 'scout',
    }
    seedSavedCall('run_task', { task_id: 'task_1' }, { status: 'error', result: { ...denial }, error: denial.reason })
    await renderScreen()
    expectKnownReasonFailure('start', denial.reason, { link: false })
  })

  it('control: a saved failed call with no error and no result keeps the no-reason sentence (D5), in an alert', async () => {
    seedSavedCall('update_task', { task_id: 'task_1', ...WORKSPACE_ARGS }, { status: 'error' })
    await renderScreen()
    const sentence = screen.getByText("I couldn't update the task, and no reason was given. Try again.")
    expect(sentence.closest('[role="alert"]')).not.toBeNull()
    // The instrument tells the two outcomes apart: the D8 framing is absent.
    expect(screen.queryByText(/^I couldn't update the task\. /)).toBeNull()
    expect(screen.queryByTestId('task-tool-card')).toBeNull()
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })
})

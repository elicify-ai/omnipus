/**
 * ChatScreen.task-card-live-to-saved.test.tsx — issue #1683, round 5 / R2-4.
 *
 * CHECK round 2 (R2-4): "A future task-only condition in the screen hides the
 * card, or the live provider stops mounting the task registrations. Direct
 * dispatcher tests and import-time capture tests can remain green because they
 * bypass those edges. The new history file's negative generic-badge assertions
 * pin the saved output, not a live -> saved sequence." Decision D1 (round 4)
 * asked for exactly that sequence: the card must not turn into the generic
 * badge when a live message becomes a saved row.
 *
 * This file mounts the REAL ChatScreen inside the REAL OmnipusRuntimeProvider
 * (the real AssistantUI runtime and tool registry, the four task registrations)
 * with ResizeObserver present, so the screen takes the virtualized-list path a
 * modern browser takes: the streaming reply is the live AssistantUI row, every
 * settled reply is a saved history row. The turn is driven with the store's own
 * frames (tool_call_start / tool_call_result / done), built from the GENERATED
 * frame types — the same steps a real turn takes — and ends with `done`, which
 * bakes the call onto its message and turns the live row into a saved one.
 * Only the network edge (the WebSocket class, the REST helpers) is faked. The
 * generic tool component is the REAL one, so a card that falls back to the
 * generic badge shows up as `tool-call-badge`; a DOM watcher records a badge
 * appearing at any moment of the hand-off, and the unregistered-tool control
 * shows the watcher can see one.
 *
 * ORACLES — never the card's output:
 *  - D1 (team-lead, round 4): saved/replayed history renders the same task
 *    card as live; the card does not turn into the generic badge when a live
 *    message becomes a saved row. D3 headings: "I scheduled the task" (create
 *    with a schedule), "I updated the task", "I started the task",
 *    "The task is already running", "I deleted the task".
 *  - D5 (reaffirmed): the no-reason sentence appears ONLY when no reason exists
 *    anywhere; a reason counts when it is the call's error field, a string
 *    result, a {text} result, or the `reason` of a generated structured
 *    failure (delegation_denied).
 *  - D8: with a known reason the card shows "I couldn't <verb> the task.
 *    <reason>" (reason UNCHANGED), keeps the "Open tasks" link, and the alert
 *    stays role="alert".
 *  - Round 5b, Q1 A + Q2 C (team-lead, 2026-11-09 — the link rule): a failed
 *    call keeps "Open tasks" with or without a reason; the link goes to
 *    /workspaces/<id>/chat?panel=tasks when the call's args name a workspace
 *    and to /tasks when they do not (real run_task / delete_task args carry
 *    only task_id); "Open calendar" appears only when workspace_id is present.
 *    Round 5b REPLACES the round-5 reading "keeps the link where the call's
 *    args name the workspace" (which left the task_id-only run_task refusal
 *    asserting its sentence alone): that row now pins the /tasks link too, live
 *    and saved (strengthened, nothing removed).
 *  - Frame shapes: contracts/asyncapi.yaml (ToolCallStartFrame,
 *    ToolCallResultFrame, DoneFrame, DelegationFailure); the structured
 *    refusal is the shape of pkg/tools/result.go::DelegationDeniedResult as the
 *    gateway forwards it (Result = the object, Error = its reason).
 *
 * The success rows are regression protection (green when the registrations are
 * mounted and the dispatcher routes task tools, as at HEAD 1bd56231b) plus, for
 * the rows that name no workspace, the Round 5b /tasks link; the known-reason
 * rows pin D8 through the live adapter and the saved dispatcher.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, act, screen, waitFor, within } from '@testing-library/react'
import * as React from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useMessagePartText } from '@assistant-ui/react'
import { useChatStore, makeBucketMessages } from '@/store/chat'
import type { ChatMessage, PositionedToolCall } from '@/store/chat'
import { emptySessionState } from '@/store/chat/session'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { useToolApprovalStore } from '@/store/toolApproval'
import type {
  DelegationFailure,
  DoneFrame,
  ToolCallResultFrame,
  ToolCallStartFrame,
} from '@/lib/api/generated/asyncapi-types'
import { OmnipusRuntimeProvider } from './OmnipusRuntimeProvider'

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

// ResizeObserver PRESENT -> the virtualized list: the streaming tail is the
// live AssistantUI row, settled replies are saved history rows.
vi.stubGlobal('ResizeObserver', ResizeObserverStub)
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollTo) {
  Element.prototype.scrollTo = function () {}
  Element.prototype.scrollBy = function () {}
}

// WsLifecycle (inside OmnipusRuntimeProvider) constructs a real WsConnection on
// mount — stub the transport.
vi.mock('@/lib/ws', () => ({
  WsConnection: class {
    connect() {}
    disconnect() {}
    send() {
      return false
    }
  },
}))

vi.mock('@/lib/memory-observer', () => ({
  startMemoryObserver: () => ({ dispose: vi.fn(), getCurrentSnapshot: vi.fn() }),
  addMemoryObserver: () => () => {},
  getCurrentSnapshot: () => ({ usedJSHeapSizeBytes: null, level: 'ok', supported: false }),
}))

// The "Open tasks" link needs a router the unit mount does not have, so Link
// renders as an <a> whose href is the route with its params and search folded
// in; the rest is what the screen itself reads from the router.
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const original = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...original,
    useRouter: () => ({ navigate: vi.fn() }),
    useSearch: () => ({}),
    Link: ({ to, params, search, children, ...rest }: {
      to?: unknown
      params?: Record<string, unknown>
      search?: Record<string, unknown>
      children?: React.ReactNode
    } & Record<string, unknown>) => {
      let href = typeof to === 'string' ? to : ''
      for (const [k, v] of Object.entries(params ?? {})) href = href.replace(`$${k}`, String(v))
      const searchEntries = Object.entries(search ?? {}).map(([k, v]) => [k, String(v)] as [string, string])
      const qs = searchEntries.length > 0 ? '?' + new URLSearchParams(searchEntries).toString() : ''
      return (
        <a href={href + qs} {...rest}>
          {children}
        </a>
      )
    },
  }
})

import { emptyHistoryPage, type HistoryReadPort } from '../../../tests/chat-history-paging/legacy-fixtures'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'agent-1', name: 'Mia', color: '#6E8BA8', icon: 'lightbulb', figure: 'Omnipus', role: 'general' },
    ]),
    fetchSessionMessages: vi.fn<HistoryReadPort>(async (sessionId) => emptyHistoryPage(sessionId)),
    fetchCommands: vi.fn().mockResolvedValue([]),
    fetchSkills: vi.fn().mockResolvedValue([]),
    uploadFiles: vi.fn(),
    fetchProviders: vi.fn().mockResolvedValue([]),
  }
})

vi.mock('@/assets/logo/omnipus-avatar.svg?url', () => ({ default: 'omnipus-avatar.svg' }))
vi.mock('./RateLimitIndicator', () => ({ RateLimitIndicator: () => null }))
vi.mock('./ActivityBar', () => ({ ActivityBar: () => null }))
vi.mock('./composer/AgentPicker', () => ({ AgentPicker: () => null }))
vi.mock('./composer/ModelPicker', () => ({ ModelPicker: () => null }))
vi.mock('./composer/TokenCounter', () => ({ TokenCounter: () => null }))
vi.mock('./markdown-text', () => ({
  MarkdownText: () => {
    const { text } = useMessagePartText()
    return React.createElement('span', { 'data-testid': 'assistant-text' }, text)
  },
}))
// NOT mocked, on purpose: ./tools/GenericToolCall. The generic badge must be
// the real one so a card that regresses to it is visible.

import { ChatScreen } from './ChatScreen'

const SID = 'sess_task_card_live_to_saved'
const CALL_ID = 'call_task_live_1'

function Providers({ children }: { children: React.ReactNode }) {
  const queryClientRef = React.useRef<QueryClient | null>(null)
  if (!queryClientRef.current) {
    queryClientRef.current = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  }
  return (
    <QueryClientProvider client={queryClientRef.current}>
      <OmnipusRuntimeProvider>{children}</OmnipusRuntimeProvider>
    </QueryClientProvider>
  )
}

/**
 * Seeds the active session: a user turn, plus (when streaming) the assistant reply still in flight.
 * Every test starts from the store's own pristine empty session (`emptySessionState()`), never from
 * whatever bucket an earlier test left behind: in gate a1 this seed spread `sessionsById[SID] ?? {}`,
 * so the very first test of the file had no `history` slice and the first frame the store handled
 * threw (`history-replay.ts::handle` reads `bucket.history.viewMode`) — a harness defect that only
 * the later tests, running on a bucket the previous test had completed, hid. Order must not matter.
 */
function seedSession(messages: ChatMessage[], streaming: boolean): void {
  const bucket = makeBucketMessages(messages)
  useChatStore.setState((s) => ({
    ...s,
    sessionsById: {
      [SID]: {
        ...emptySessionState(),
        ...bucket,
        isStreaming: streaming,
        isReplaying: false,
        replayCompletedForSession: SID,
        toolCalls: {},
        toolCallOrder: [],
        textAtToolCallStart: {},
        toolCallOwnerMessageId: {},
        goalStatus: null,
        goalPills: {},
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
    isStreaming: streaming,
    isReplaying: false,
    replayCompletedForSession: SID,
    toolCalls: {},
    toolCallOrder: [],
    textAtToolCallStart: {},
    toolCallOwnerMessageId: {},
    goalStatus: null,
    goalPills: {},
    pendingAsk: null,
  }))
  useSessionStore.setState({ activeSessionId: SID, activeAgentId: 'agent-1' })
  useConnectionStore.setState({
    connection: null,
    isConnected: true,
    liteMode: false,
    connectionError: null,
    reconnectPhase: null,
    reconnectAttempt: 0,
  })
}

const NOW = () => new Date().toISOString()

/** A user turn and the assistant reply still streaming — the shape the live row renders. */
function streamingTurn(): ChatMessage[] {
  return [
    { id: `${SID}_user`, role: 'user', content: 'Please do that for me', timestamp: NOW(), status: 'done' },
    {
      id: `${SID}_assistant`,
      role: 'assistant',
      content: 'On it.',
      timestamp: NOW(),
      status: 'streaming',
      isStreaming: true,
      agentId: 'agent-1',
      turnId: 'turn-task-card',
    },
  ]
}

/** One SAVED assistant reply holding a single tool call, as the history store holds it after a reload. */
function savedTurn(tool: string, params: Record<string, unknown>, result: unknown): ChatMessage[] {
  return [
    { id: `${SID}_user`, role: 'user', content: 'Please do that for me', timestamp: NOW(), status: 'done' },
    {
      id: `${SID}_assistant`,
      role: 'assistant',
      content: '',
      timestamp: NOW(),
      status: 'done',
      agentId: 'agent-1',
      tool_calls: [{ id: `tc_${tool}`, tool, params, status: 'success', result } as PositionedToolCall],
    },
  ]
}

async function mount(): Promise<void> {
  await act(async () => {
    render(
      <Providers>
        <ChatScreen />
      </Providers>,
    )
  })
}

/** Virtualizer rows only mount with real element metrics — stub them for the duration of fn. */
async function withVirtualizerMetrics(fn: () => Promise<void>) {
  const height = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetHeight')
  const width = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetWidth')
  Object.defineProperty(HTMLElement.prototype, 'offsetHeight', { configurable: true, get: () => 800 })
  Object.defineProperty(HTMLElement.prototype, 'offsetWidth', { configurable: true, get: () => 800 })
  try {
    await fn()
  } finally {
    if (height) Object.defineProperty(HTMLElement.prototype, 'offsetHeight', height)
    if (width) Object.defineProperty(HTMLElement.prototype, 'offsetWidth', width)
  }
}

// ── frames, typed by the generated contract ─────────────────────────────────
function startFrame(tool: string, params: Record<string, unknown>): ToolCallStartFrame {
  return { type: 'tool_call_start', session_id: SID, tool, call_id: CALL_ID, params }
}

function resultFrame(
  tool: string,
  result: unknown,
  status: ToolCallResultFrame['status'],
  error?: string,
): ToolCallResultFrame {
  return {
    type: 'tool_call_result',
    session_id: SID,
    tool,
    call_id: CALL_ID,
    result,
    status,
    ...(error !== undefined ? { error } : {}),
  }
}

function sendFrames(frames: Array<ToolCallStartFrame | ToolCallResultFrame | DoneFrame>): void {
  act(() => {
    for (const frame of frames) useChatStore.getState().handleFrame(frame)
  })
}

/** The turn ends: `done` bakes the call onto its message and the live row becomes a saved one. */
function settleTurn(): void {
  sendFrames([{ type: 'done', session_id: SID, turn_id: 'turn-task-card' }])
}

// ── the generic-badge watcher ───────────────────────────────────────────────
const BADGE = '[data-testid="tool-call-badge"]'

/** Records whether a generic badge is ever ADDED to the document from now on. */
function watchForGenericBadge(): { seen: () => boolean; stop: () => void } {
  let seen = document.querySelector(BADGE) !== null
  const scan = (records: MutationRecord[]) => {
    for (const record of records) {
      for (const node of Array.from(record.addedNodes)) {
        if (node instanceof Element && (node.matches(BADGE) || node.querySelector(BADGE) !== null)) seen = true
      }
    }
  }
  const observer = new MutationObserver(scan)
  observer.observe(document.body, { childList: true, subtree: true })
  return {
    seen: () => {
      scan(observer.takeRecords())
      return seen
    },
    stop: () => observer.disconnect(),
  }
}

/** The assistant row that holds `el` — the live (streaming) one reports 'running'; a saved history row reports 'complete'. */
function rowOf(el: HTMLElement): HTMLElement {
  const row = el.closest<HTMLElement>('[data-testid="assistant-message"]')
  if (!row) throw new Error('the element is not inside an assistant row')
  return row
}

function expectLiveRow(el: HTMLElement): void {
  expect(rowOf(el), 'the card is in the LIVE (streaming) row').toHaveAttribute('data-status', 'running')
}

function expectSavedRow(el: HTMLElement): void {
  expect(rowOf(el), 'the card is in a SAVED history row').toHaveAttribute('data-status', 'complete')
  expect(rowOf(el).closest('[data-index]'), 'the saved row is mounted by the virtualizer').not.toBeNull()
}

beforeEach(() => {
  useToolApprovalStore.setState({ queue: [], resolvedIds: [] })
  useChatPreferencesStore.getState().setVerboseChatEnabled(false)
  act(() => {
    useChatStore.getState().resetSession()
  })
})

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
})

const NO_REASON_RE = /no reason was given/i
const SUCCESS_HEADING_RE = /^(I (scheduled|created|updated|started|deleted) the task|The task is already running)$/

/**
 * Finds the failure line of a refused call whichever wording it has — the no-reason sentence, the
 * D8 sentence or the bare reason — so a wrong wording fails the EXACT check that follows, with the
 * text that was really rendered in the message, instead of failing here with a screen dump.
 */
function failureLine(verb: string, reason: string): RegExp {
  const escaped = reason.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^I couldn't ${verb} the task|${escaped}`)
}

/** The element's OWN text (what getByText matched on), whitespace-normalised; a child such as the link does not count. */
function ownText(el: HTMLElement): string {
  return Array.from(el.childNodes)
    .filter((n) => n.nodeType === 3)
    .map((n) => n.textContent ?? '')
    .join('')
    .replace(/\s+/g, ' ')
    .trim()
}

// The structured refusal, built from the GENERATED DelegationFailure type (asyncapi-types.ts). The
// gateway forwards Result = this object and Error = its reason
// (pkg/gateway/websocket_forward_hub.go::hubToolExecEnd; shape: pkg/tools/result.go::DelegationDeniedResult).
const DENIAL: DelegationFailure = {
  error: 'delegation_denied',
  reason: 'Scout is not in your trust set, so you cannot start its task.',
  policy: 'trust_set',
  tool: 'run_task',
  target_agent_id: 'scout',
}
const DENIAL_SENTENCE = `I couldn't start the task. ${DENIAL.reason}`
const DENIAL_LINE = failureLine('start', DENIAL.reason)

// A plain-text refusal: the text itself is the result, and the call names its workspace.
const UPDATE_REASON = 'task not found: task_99'
const UPDATE_SENTENCE = `I couldn't update the task. ${UPDATE_REASON}`
const UPDATE_LINE = failureLine('update', UPDATE_REASON)
const UPDATE_ARGS = { task_id: 'task_99', title: 'Weekly report', workspace_id: 'ws_1' }

// Round 5b / Q2 C: the link targets the decisions name, as literals.
const WORKSPACE_TASKS_HREF = '/workspaces/ws_1/chat?panel=tasks'
const WORKSPACE_CALENDAR_HREF = '/workspaces/ws_1/calendar'
const TASKS_INDEX_HREF = '/tasks'

/** A live turn whose run_task came back refused by delegation policy, exactly as the gateway sends it. */
async function startRefusedRun(): Promise<void> {
  seedSession(streamingTurn(), true)
  await mount()
  sendFrames([startFrame('run_task', { task_id: 'task_1' }), resultFrame('run_task', DENIAL, 'error', DENIAL.reason)])
}

/** A live turn whose update_task came back refused with a plain-text reason (the text is the result). */
async function startRefusedUpdate(): Promise<void> {
  seedSession(streamingTurn(), true)
  await mount()
  sendFrames([startFrame('update_task', UPDATE_ARGS), resultFrame('update_task', UPDATE_REASON, 'error')])
}

// Mon 2026-11-09 09:00 Europe/Berlin = 08:00 UTC.
const MON_0900_BERLIN_MS = Date.UTC(2026, 10, 9, 8, 0, 0)
const RECURRING_TRIGGER = {
  type: 'recurring',
  config: { rrule: 'FREQ=WEEKLY;BYDAY=MO', dtstart_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' },
}

describe('R2-4 / D1 — a live task call becomes a saved row without turning into the generic badge', () => {
  it('control: an unregistered tool is the generic badge live AND saved — the watcher can see a badge appear', async () => {
    seedSession(streamingTurn(), true)
    await withVirtualizerMetrics(async () => {
      await mount()
      sendFrames([
        startFrame('totally_unknown_tool', { x: 1 }),
        resultFrame('totally_unknown_tool', 'plain text result', 'success'),
      ])
      const liveBadge = await screen.findByTestId('tool-call-badge')
      expectLiveRow(liveBadge)

      const watcher = watchForGenericBadge()
      try {
        settleTurn()
        await waitFor(() => {
          expect(rowOf(screen.getByTestId('tool-call-badge'))).toHaveAttribute('data-status', 'complete')
        })
        expect(watcher.seen(), 'the watcher saw the saved row add its generic badge').toBe(true)
      } finally {
        watcher.stop()
      }
    })
  })

  it('create_task with a recurring schedule: the same scheduled card live and saved, the generic badge never appears', async () => {
    seedSession(streamingTurn(), true)
    await withVirtualizerMetrics(async () => {
      await mount()
      sendFrames([
        startFrame('create_task', { title: 'Weekly report', workspace_id: 'ws_1', trigger: RECURRING_TRIGGER }),
        resultFrame('create_task', JSON.stringify({ task_id: 'task_1', status: 'inbox' }), 'success'),
      ])

      // Live: the REGISTERED task UI renders the card in the streaming row.
      const liveCard = await screen.findByTestId('task-tool-card')
      expectLiveRow(liveCard)
      const liveHeading = (within(liveCard).getByTestId('task-tool-verb').textContent ?? '').trim()
      const liveSchedule = (within(liveCard).getByTestId('task-tool-schedule').textContent ?? '').trim()
      expect(liveHeading).toBe('I scheduled the task')
      expect(liveSchedule).toMatch(/berlin/i)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()

      // The turn ends: the live row becomes a saved history row.
      const watcher = watchForGenericBadge()
      try {
        settleTurn()
        await waitFor(() => {
          expect(rowOf(screen.getByTestId('task-tool-card'))).toHaveAttribute('data-status', 'complete')
        })
        const savedCard = screen.getByTestId('task-tool-card')
        expectSavedRow(savedCard)
        expect(screen.getAllByTestId('task-tool-card'), 'one card — no live copy left behind, no duplicate').toHaveLength(1)
        expect((within(savedCard).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe(liveHeading)
        expect((within(savedCard).getByTestId('task-tool-schedule').textContent ?? '').trim()).toBe(liveSchedule)
        expect(savedCard.textContent ?? '').toContain('Weekly report')
        expect(screen.queryByTestId('tool-call-badge')).toBeNull()
        expect(watcher.seen(), 'a generic badge was never added at any point of the hand-off').toBe(false)
      } finally {
        watcher.stop()
      }
    })
  })
})

// The two halves of a refused call are separate tests on purpose: the LIVE row (the store's frames
// through the native live adapter) and the SAVED row it becomes (the baked message through the
// history dispatcher) are different code paths, and one failing must not hide the other.
describe('R2-4 / D8 — a refused task call: the live row, and the saved row it becomes', () => {
  it('run_task refused by delegation policy (structured failure, as the gateway sends it): D8 in the LIVE row', async () => {
    await withVirtualizerMetrics(async () => {
      await startRefusedRun()

      const liveText = await screen.findByText(DENIAL_LINE)
      expectLiveRow(liveText)
      expect(ownText(liveText), 'live wording (D8)').toBe(DENIAL_SENTENCE)
      expect(liveText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, live').not.toBeNull()
      // Round 5b / Q2 C: run_task's real args are task_id only, so the live row links to
      // /tasks and shows no calendar link. (Round 5 asserted the sentence alone here.)
      const open = screen.getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
      expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
      expect(screen.queryByText(NO_REASON_RE)).toBeNull()
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  })

  it('run_task refused by delegation policy: the SAVED row the live row becomes shows the same D8 sentence', async () => {
    await withVirtualizerMetrics(async () => {
      await startRefusedRun()
      await screen.findByText(DENIAL_LINE) // the live row exists first

      const watcher = watchForGenericBadge()
      try {
        settleTurn()
        await waitFor(() => {
          expect(rowOf(screen.getByText(DENIAL_LINE))).toHaveAttribute('data-status', 'complete')
        })
        const savedText = screen.getByText(DENIAL_LINE)
        expectSavedRow(savedText)
        expect(ownText(savedText), 'saved wording (D8)').toBe(DENIAL_SENTENCE)
        expect(savedText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, saved').not.toBeNull()
        // Round 5b / Q2 C: the saved row the live row becomes keeps the /tasks link and has
        // no calendar link (the args name no workspace).
        const open = screen.getByTestId('task-tool-link-open')
        expect((open.textContent ?? '').trim()).toBe('Open tasks')
        expect(open).toHaveAttribute('href', TASKS_INDEX_HREF)
        expect(screen.queryByTestId('task-tool-link-calendar')).toBeNull()
        expect(screen.queryByText(NO_REASON_RE)).toBeNull()
        expect(screen.queryByText(SUCCESS_HEADING_RE)).toBeNull()
        expect(screen.queryByTestId('tool-call-badge')).toBeNull()
        expect(watcher.seen(), 'a generic badge was never added at any point of the hand-off').toBe(false)
      } finally {
        watcher.stop()
      }
    })
  })

  it('update_task refused with a plain-text reason (the text is the result): D8 and the Open tasks link in the LIVE row', async () => {
    await withVirtualizerMetrics(async () => {
      await startRefusedUpdate()

      const liveText = await screen.findByText(UPDATE_LINE)
      expectLiveRow(liveText)
      expect(ownText(liveText), 'live wording (D8)').toBe(UPDATE_SENTENCE)
      expect(liveText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, live').not.toBeNull()
      const open = screen.getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
      expect(screen.queryByText(NO_REASON_RE)).toBeNull()
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  })

  it('update_task refused with a plain-text reason: the SAVED row keeps the same D8 sentence and the Open tasks link', async () => {
    await withVirtualizerMetrics(async () => {
      await startRefusedUpdate()
      await screen.findByText(UPDATE_LINE) // the live row exists first

      const watcher = watchForGenericBadge()
      try {
        settleTurn()
        await waitFor(() => {
          expect(rowOf(screen.getByText(UPDATE_LINE))).toHaveAttribute('data-status', 'complete')
        })
        const savedText = screen.getByText(UPDATE_LINE)
        expectSavedRow(savedText)
        expect(ownText(savedText), 'saved wording (D8)').toBe(UPDATE_SENTENCE)
        expect(savedText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, saved').not.toBeNull()
        const open = screen.getByTestId('task-tool-link-open')
        expect((open.textContent ?? '').trim()).toBe('Open tasks')
        expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
        expect(screen.queryByText(NO_REASON_RE)).toBeNull()
        expect(screen.queryByTestId('tool-call-badge')).toBeNull()
        expect(watcher.seen(), 'a generic badge was never added at any point of the hand-off').toBe(false)
      } finally {
        watcher.stop()
      }
    })
  })
})

describe('R2-4 / D1 — saved task rows in the virtualized list a modern browser mounts', () => {
  // Round 5b / Q2 C: each row also states where its links go. The create row names
  // workspace_id (both links, workspace targets — the existing success-card behaviour);
  // the others name none (the /tasks link, no calendar link). The new link columns are
  // the Round 5b addition — the heading assertions are unchanged.
  const SAVED_ROWS: Array<{
    tool: string
    params: Record<string, unknown>
    result: unknown
    heading: string
    tasksHref: string
    calendarHref: string | null
  }> = [
    {
      tool: 'create_task',
      params: { title: 'Weekly report', workspace_id: 'ws_1', trigger: RECURRING_TRIGGER },
      result: { text: JSON.stringify({ task_id: 'task_1', status: 'inbox' }) },
      heading: 'I scheduled the task',
      tasksHref: WORKSPACE_TASKS_HREF,
      calendarHref: WORKSPACE_CALENDAR_HREF,
    },
    {
      tool: 'update_task',
      params: { task_id: 'task_1', title: 'Weekly report' },
      result: { text: JSON.stringify({ task_id: 'task_1', status: 'inbox', updated_fields: ['title'] }) },
      heading: 'I updated the task',
      tasksHref: TASKS_INDEX_HREF,
      calendarHref: null,
    },
    {
      tool: 'run_task',
      params: { task_id: 'task_1' },
      result: { text: JSON.stringify({ task_id: 'task_1', status: 'in_progress', session_id: 'sess_1' }) },
      heading: 'I started the task',
      tasksHref: TASKS_INDEX_HREF,
      calendarHref: null,
    },
    {
      tool: 'run_task',
      params: { task_id: 'task_1' },
      result: { text: JSON.stringify({ task_id: 'task_1', status: 'in_progress', session_id: 'sess_1', note: 'already running' }) },
      heading: 'The task is already running',
      tasksHref: TASKS_INDEX_HREF,
      calendarHref: null,
    },
    {
      tool: 'delete_task',
      params: { task_id: 'task_1' },
      result: { text: JSON.stringify({ deleted: 'task_1' }) },
      heading: 'I deleted the task',
      tasksHref: TASKS_INDEX_HREF,
      calendarHref: null,
    },
  ]

  it.each(SAVED_ROWS)(
    '$tool saved on reload: "$heading", not the generic badge',
    async ({ tool, params, result, heading, tasksHref, calendarHref }) => {
      seedSession(savedTurn(tool, params, result), false)
      await withVirtualizerMetrics(async () => {
        await mount()
        const card = await screen.findByTestId('task-tool-card')
        expectSavedRow(card)
        expect((within(card).getByTestId('task-tool-verb').textContent ?? '').trim()).toBe(heading)
        expect(screen.queryByTestId('tool-call-badge')).toBeNull()
        const open = within(card).getByTestId('task-tool-link-open')
        expect((open.textContent ?? '').trim()).toBe('Open tasks')
        expect(open).toHaveAttribute('href', tasksHref)
        if (calendarHref === null) {
          expect(within(card).queryByTestId('task-tool-link-calendar')).toBeNull()
        } else {
          expect(within(card).getByTestId('task-tool-link-calendar')).toHaveAttribute('href', calendarHref)
        }
      })
    },
  )
})

// ── Round 6 / R3-1 — the call's error field is the ONLY reason (result null), live and saved ─────
//
// CHECK3 (R3-1): the refused-call rows above carry the reason in `result` as well as, or instead
// of, the call's `error`, so dropping the live adapter's hand-over of the recorded error
// (lib/omnipus-runtime.ts::buildContentParts) left the LIVE row green. A frame can carry the reason
// in `error` ALONE: the contract allows `result: null` on an error result
// (contracts/components/schemas/ToolCallResultFrame.yaml) and pkg/gateway/replay.go::
// buildResultFrame keeps a plain persisted reason in Error without populating Result. D5 names the
// call's error field as a reason on its own, REST or live. The LIVE row reads that field through the
// live adapter; the SAVED row it becomes reads it from the baked message through the dispatcher. They
// are separate tests for the same reason as the D8 pair above; the duplicated-reason rows stay.
/** A live turn whose update_task came back refused with the reason in the call's error field ALONE (result null). */
async function startErrorOnlyUpdate(): Promise<void> {
  seedSession(streamingTurn(), true)
  await mount()
  sendFrames([startFrame('update_task', UPDATE_ARGS), resultFrame('update_task', null, 'error', UPDATE_REASON)])
}

describe('Round 6 / R3-1 — a refused task call whose only reason is the call\'s error field (result null): the live row, and the saved row it becomes', () => {
  it('update_task, result null + error "task not found: task_99": D8 and the Open tasks link in the LIVE row', async () => {
    await withVirtualizerMetrics(async () => {
      await startErrorOnlyUpdate()
      // Instrument: the live store holds the call as the frame carried it — result null, the reason in `error` alone.
      expect(useChatStore.getState().sessionsById[SID]?.toolCalls[CALL_ID]).toMatchObject({ status: 'error', result: null, error: UPDATE_REASON })

      const liveText = await screen.findByText(UPDATE_LINE)
      expectLiveRow(liveText)
      expect(ownText(liveText), 'R3-1 live wording: the call\'s error field is the reason when result is null (D5/D8)').toBe(UPDATE_SENTENCE)
      expect(liveText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, live').not.toBeNull()
      const open = screen.getByTestId('task-tool-link-open')
      expect((open.textContent ?? '').trim()).toBe('Open tasks')
      expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
      expect(screen.queryByText(NO_REASON_RE)).toBeNull()
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  })

  it('update_task, result null + error: the SAVED row the live row becomes keeps the same D8 sentence and the Open tasks link', async () => {
    await withVirtualizerMetrics(async () => {
      await startErrorOnlyUpdate()
      await screen.findByText(UPDATE_LINE) // the live row exists first

      const watcher = watchForGenericBadge()
      try {
        settleTurn()
        await waitFor(() => {
          expect(rowOf(screen.getByText(UPDATE_LINE))).toHaveAttribute('data-status', 'complete')
        })
        const savedText = screen.getByText(UPDATE_LINE)
        expectSavedRow(savedText)
        expect(ownText(savedText), 'R3-1 saved wording (D5/D8)').toBe(UPDATE_SENTENCE)
        expect(savedText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, saved').not.toBeNull()
        const open = screen.getByTestId('task-tool-link-open')
        expect((open.textContent ?? '').trim()).toBe('Open tasks')
        expect(open).toHaveAttribute('href', WORKSPACE_TASKS_HREF)
        expect(screen.queryByText(NO_REASON_RE)).toBeNull()
        expect(screen.queryByTestId('tool-call-badge')).toBeNull()
        expect(watcher.seen(), 'a generic badge was never added at any point of the hand-off').toBe(false)
      } finally {
        watcher.stop()
      }
    })
  })
})

// ── Round 6 / R3-2 — a refused call that names a workspace offers "Open tasks" only ───────────
//
// CHECK3 (R3-2): the accepted failure view is the alert followed by ONE action, "Open tasks"
// (dev2-report.md, "Two choices, accepted by the coordinator" 12:59Z: "A failed call shows only
// 'Open tasks', with no 'Open calendar', even when it names a workspace"; reaffirmed in the CHECK3
// dispatch). The calendar-absence checks above are on calls that name NO workspace, where the
// card's workspace guard hides the link on its own, so switching the failure view's calendar link
// on passed unnoticed (CHECK3 mutant m08). update_task takes workspace_id as an optional
// parameter; UPDATE_ARGS names it. Live row and saved row are separate tests, as above.
const NO_REASON_UPDATE_SENTENCE = "I couldn't update the task, and no reason was given. Try again."

/** A live turn whose update_task (it names its workspace) came back refused with NO reason anywhere. */
async function startRefusedUpdateNoReason(): Promise<void> {
  seedSession(streamingTurn(), true)
  await mount()
  sendFrames([startFrame('update_task', UPDATE_ARGS), resultFrame('update_task', '', 'error')])
}

/** The one action a failed call offers: "Open tasks" at `href`, alone in its group, with no calendar link or text anywhere. */
function expectOnlyOpenTasksAction(href: string): void {
  const open = screen.getByTestId('task-tool-link-open')
  expect((open.textContent ?? '').trim()).toBe('Open tasks')
  expect(open).toHaveAttribute('href', href)
  const actions = Array.from(open.parentElement?.children ?? []).map((el) => (el.textContent ?? '').trim())
  expect(actions, 'R3-2: "Open tasks" is the only action a failed call offers').toEqual(['Open tasks'])
  expect(screen.queryByTestId('task-tool-link-calendar'), 'R3-2: a failed call has no calendar link').toBeNull()
  expect(screen.queryByText(/open calendar/i), 'R3-2: a failed call says nothing about opening the calendar').toBeNull()
}

describe('Round 6 / R3-2 — a refused task call that names a workspace offers "Open tasks" only: the live row, and the saved row it becomes', () => {
  const STATES: Array<{ label: string; start: () => Promise<void>; locator: RegExp | string; sentence: string }> = [
    { label: 'a plain-text reason', start: startRefusedUpdate, locator: UPDATE_LINE, sentence: UPDATE_SENTENCE },
    // The locator is the loose "no reason was given" pattern on purpose: a wrong wording then fails the EXACT
    // sentence check below with the text that was really rendered, not this lookup with a screen dump.
    { label: 'no reason', start: startRefusedUpdateNoReason, locator: NO_REASON_RE, sentence: NO_REASON_UPDATE_SENTENCE },
  ]

  it.each(STATES)('update_task with $label: the LIVE row offers Open tasks only', async ({ start, locator, sentence }) => {
    await withVirtualizerMetrics(async () => {
      await start()
      const liveText = await screen.findByText(locator)
      expectLiveRow(liveText)
      expect(ownText(liveText), 'live wording').toBe(sentence)
      expect(liveText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, live').not.toBeNull()
      expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  })

  it.each(STATES)('update_task with $label: the SAVED row the live row becomes offers Open tasks only', async ({ start, locator, sentence }) => {
    await withVirtualizerMetrics(async () => {
      await start()
      await screen.findByText(locator) // the live row exists first
      settleTurn()
      await waitFor(() => {
        expect(rowOf(screen.getByText(locator))).toHaveAttribute('data-status', 'complete')
      })
      const savedText = screen.getByText(locator)
      expectSavedRow(savedText)
      expect(ownText(savedText), 'saved wording').toBe(sentence)
      expect(savedText.closest('[role="alert"]'), 'the sentence is in the catalogued error region, saved').not.toBeNull()
      expectOnlyOpenTasksAction(WORKSPACE_TASKS_HREF)
      expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    })
  })
})

// ── Round 6 / R3-3 — a full stop closes every schedule line, live and saved ───────────────────
//
// Team-lead decision (round 6, 2026-11-09): EVERY schedule line ends with a full stop, a once
// schedule included ("Runs once — <date/time>."). Oracles, none read off the card:
//  - repeating with an end date: the D9 sentence, verbatim — "Repeats every day at 09:00 (Los
//    Angeles time), ends November 10, 2026.";
//  - repeating with no end date: that sentence with its end clause removed (D9 puts the end clause
//    last) — "Repeats every day at 09:00 (Los Angeles time).";
//  - once: the task detail panel's own string, in the viewer's zone (founder C1 B:
//    `Runs once — ${new Date(at).toLocaleString()}`), plus the full stop.
// The viewer zone is the repo-wide vitest pin (TZ UTC, en-US; vite.config.ts::test.env). The live
// card and the saved row it becomes are separate tests, as in the D8 pair above.
describe('Round 6 / R3-3 — a full stop closes every schedule line, live and saved', () => {
  const LA_ZONE = 'America/Los_Angeles'
  const LA_DTSTART = Date.UTC(2026, 10, 9, 17, 0, 0) // 9 Nov 2026 09:00 in Los Angeles (PST, UTC-8)
  const ROWS: Array<{ label: string; trigger: Record<string, unknown>; line: string }> = [
    {
      label: 'a once schedule',
      trigger: { type: 'once', config: { at_ms: MON_0900_BERLIN_MS, tz: 'Europe/Berlin' } },
      line: `Runs once — ${new Date(MON_0900_BERLIN_MS).toLocaleString()}.`,
    },
    {
      label: 'a repeating schedule with an end date (the D9 sentence)',
      trigger: { type: 'recurring', config: { rrule: 'FREQ=DAILY;UNTIL=20261111T075959Z', dtstart_ms: LA_DTSTART, tz: LA_ZONE } },
      line: 'Repeats every day at 09:00 (Los Angeles time), ends November 10, 2026.',
    },
    {
      label: 'a repeating schedule with no end date',
      trigger: { type: 'recurring', config: { rrule: 'FREQ=DAILY', dtstart_ms: LA_DTSTART, tz: LA_ZONE } },
      line: 'Repeats every day at 09:00 (Los Angeles time).',
    },
  ]

  async function startScheduledCreate(trigger: Record<string, unknown>): Promise<void> {
    seedSession(streamingTurn(), true)
    await mount()
    sendFrames([
      startFrame('create_task', { title: 'Weekly report', workspace_id: 'ws_1', trigger }),
      resultFrame('create_task', JSON.stringify({ task_id: 'task_1', status: 'inbox' }), 'success'),
    ])
  }

  it.each(ROWS)('$label: the LIVE card line ends with one full stop', async ({ trigger, line }) => {
    await withVirtualizerMetrics(async () => {
      await startScheduledCreate(trigger)
      const liveCard = await screen.findByTestId('task-tool-card')
      expectLiveRow(liveCard)
      const liveLine = (within(liveCard).getByTestId('task-tool-schedule').textContent ?? '').trim()
      expect(liveLine, 'R3-3 live: one full stop closes the schedule line').toMatch(/[^.]\.$/)
      expect(liveLine).toBe(line)
    })
  })

  it.each(ROWS)('$label: the SAVED row the live card becomes ends with one full stop', async ({ trigger, line }) => {
    await withVirtualizerMetrics(async () => {
      await startScheduledCreate(trigger)
      await screen.findByTestId('task-tool-card') // the live card exists first
      settleTurn()
      await waitFor(() => {
        expect(rowOf(screen.getByTestId('task-tool-card'))).toHaveAttribute('data-status', 'complete')
      })
      const savedCard = screen.getByTestId('task-tool-card')
      expectSavedRow(savedCard)
      expect(screen.getAllByTestId('task-tool-card'), 'one card — no live copy left behind').toHaveLength(1)
      const savedLine = (within(savedCard).getByTestId('task-tool-schedule').textContent ?? '').trim()
      expect(savedLine, 'R3-3 saved: one full stop closes the schedule line').toMatch(/[^.]\.$/)
      expect(savedLine).toBe(line)
    })
  })
})

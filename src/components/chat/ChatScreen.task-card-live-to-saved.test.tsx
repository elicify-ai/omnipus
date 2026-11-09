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

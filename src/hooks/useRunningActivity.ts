// useRunningActivity — Activity Bar data hook.
//
// Aggregates all currently-running (and recently-finished) background work
// for the ACTIVE session into a single view: subagent delegation spans
// (native agents AND external-CLI 3rd-party agents — both ride the exact
// same `subagent_start`/`subagent_end` WS event lifecycle, see chat.ts) and
// background `bash` sessions (`run_in_background: true` dispatch, plus any
// later `poll`/`read`/`kill` calls against the same session_id — see
// groupBashSessions below for why liveness is read from each call's RESULT
// payload rather than the tool_call's own completion status).
//
// v1 scope is active-session-only: it reads the chat store's foreground
// `messages`/`toolCalls` fields, which are already derived for the active
// session (see bucketToForeground in chat.ts) — no cross-session
// aggregation. Keeping it simple per the spec.
//
// Elapsed time: neither SubagentSpan nor ToolCall carries a start timestamp
// on the wire or in store state today, and this hook must NOT add one to
// those shared types (they're relied on elsewhere). Instead, the first time
// an item is observed in the running set, its key is stamped with
// Date.now() in firstSeenAtMap; durationMs for a running item is recomputed
// from that stamp on every 1Hz tick — the same setInterval-based ticking
// pattern RateLimitIndicator.tsx uses for its countdown, reused here so the
// elapsed-time display stays live without polling the server. The stamp is
// deleted once the item leaves the running set so a later re-run of the
// same key (new span/call with a reused id, however unlikely) starts fresh.
//
// The stamp map is a MODULE-LEVEL singleton, not a per-mount useRef: this
// hook lives inside ActivityBar, which is mounted inside ChatScreen — a
// different route than Settings. Navigating Chat -> Settings -> Chat
// unmounts and remounts ChatScreen/ActivityBar, which would wipe a
// component-local ref and reset a still-running task's displayed elapsed
// time toward 0 even though the real task keeps running server-side.
// Hoisting the map to module scope lets it survive that in-app
// remount (it does NOT persist across a full page reload — it isn't
// written to localStorage/anywhere durable, since it only needs to survive
// client-side navigation, not a hard refresh).

import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useChatStore } from '@/store/chat'
import type { SpanStep, SubagentSpan, SubagentSpanTerminal } from '@/store/chat'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import { fetchAgents } from '@/lib/api'
import type { Agent, ToolCall } from '@/lib/api'

export type ActivityStatus = 'running' | 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout' | 'parked'

export interface AgentActivityItem {
  kind: 'agent'
  key: string // spanId
  agentId?: string
  agentName: string
  agentType: 'native' | '3p' | 'unknown'
  agentColor?: string
  agentIcon?: string
  taskLabel: string
  status: ActivityStatus
  durationMs?: number
  steps: SpanStep[] // populated for native only — empty for 3p (issue #492: no live step detail yet)
  /**
   * The span's final result text (terminal spans only) — Fix 2 (2026-07-16):
   * SubagentBlock's thread card (now hidden from the thread by default) was
   * the only surface showing this; carried onto the item here so
   * ActivityPanel's expanded row can render it, matching SubagentBlock's own
   * "Final result" section (see SubagentBlock.tsx).
   */
  finalResult?: string
  /**
   * Populated when status === 'interrupted' (terminal spans only) — see
   * SubagentSpanTerminal.reason. Carried the same way as finalResult (Fix 2)
   * so ActivityPanel can append the human-readable reason to the row's
   * status text, mirroring SubagentBlock's own inline reason label. Render
   * via formatInterruptReason (@/lib/subagentStatus), not this raw value.
   */
  interruptReason?: SubagentSpanTerminal['reason']
}

export interface BashActivityItem {
  kind: 'bash'
  key: string // session_id — one item per background session, not per tool_call (a session spans a dispatch call plus any number of later poll/read/kill calls; see groupBashSessions)
  command: string
  status: 'running' | 'success' | 'error' | 'cancelled'
  durationMs?: number
}

/**
 * ADR-049 D2/D4/US-13/SD-C11 — a live judge-verdict push, always terminal
 * (the wire has no "judge started" frame — only the completed verdict is
 * ever pushed, so this item never appears in `running`, only
 * `recentlyFinished`; it must stay expandable even though it has no
 * `steps`, mirroring the "zero steps but a final result" span case).
 *
 * `status` reuses the shared `ActivityStatus` vocabulary so `isFailedStatus`
 * (ActivityBar.tsx) and `getSpanStatusDot` (toolStatusConfig.tsx) keep
 * working unmodified: `met: true` → 'success', `met: false` → 'error' (an
 * unmet verdict is treated as the "failure" the retained-failure
 * reachability rule (US-13 AS-7) means by "a judge call fails" — the wire
 * has no separate judge-infrastructure-failure signal at all, since a judge
 * that is merely unavailable produces no verdict/frame whatsoever, ADR D7).
 *
 * `criterionVerdicts[].text` is the raw `criterion_id` (a UUID) — the live
 * frame carries no criterion TITLE lookup (that lives on the originating
 * Task's `AcceptanceCriterion[]`, which this global, task-agnostic feed has
 * no access to), so this renders the real identifier available rather than
 * fabricating a label.
 *
 * `spend` is NOT populated: `JudgeVerdictFrame`/`JudgeVerdict` (contracts
 * C11/C20, Wave 0) carry no tokens/cost field today, only `Message.tokens`/
 * `Message.cost` do (and only on the PERSISTED transcript twin, not the live
 * push) — see this wave's report for the flagged contract gap against
 * AS-5/SC-048's "spend visible" requirement.
 */
export interface JudgeActivityItem {
  kind: 'judge'
  key: string // verdict id
  scope: 'task' | 'plan' | 'goal'
  taskId?: string
  planId?: string
  round: number
  met: boolean
  criterionVerdicts: { text: string; met: boolean; reason: string }[]
  model: string
  judgeAgentId: string
  judgedAt: string
  status: ActivityStatus
  /**
   * Always absent — a judge call has no wire-level "started" moment to
   * measure elapsed time from (see this interface's doc comment). Declared
   * (rather than omitted) so every `ActivityItem` union member shares the
   * same optional key, letting existing call sites that generically read
   * `someItem.durationMs` off the union (e.g. `ActivityPanel.tsx`,
   * `useRunningActivity.test.ts`) keep compiling without a per-call-site
   * `item.kind` narrowing check.
   */
  durationMs?: undefined
}

export type ActivityItem = AgentActivityItem | BashActivityItem | JudgeActivityItem

export interface RunningActivity {
  runningCount: number
  running: ActivityItem[]
  recentlyFinished: ActivityItem[]
}

/** Cap on how many finished items are retained for display (most-recent-first). */
const RECENTLY_FINISHED_CAP = 8

/**
 * key -> first-seen-running timestamp (ms). Module-level singleton (see file
 * header) so elapsed time survives ActivityBar unmount/remount across
 * in-app route navigation (Chat -> Settings -> Chat), not just within a
 * single mount's lifetime. Never persisted beyond the page's JS runtime.
 */
const firstSeenAtMap = new Map<string, number>()

interface ResolvedAgent {
  agentName: string
  agentType: 'native' | '3p' | 'unknown'
  agentColor?: string
  agentIcon?: string
}

/** Resolve a span's agentId against the reused agents list. Never throws — unknown/missing agentId falls back to 'unknown'. */
function resolveAgent(agentId: string | undefined, agents: Agent[]): ResolvedAgent {
  if (!agentId) return { agentName: 'Unknown agent', agentType: 'unknown' }
  const agent = agents.find((a) => a.id === agentId)
  if (!agent) return { agentName: agentId, agentType: 'unknown' }
  return {
    agentName: agent.name,
    agentType: agent.type === 'subagent_3p' ? '3p' : 'native',
    agentColor: agent.color,
    agentIcon: agent.icon,
  }
}

/**
 * Resolve the agentId to display for a span — there are two sources of
 * truth and they disagree in one real case.
 *
 * For NATIVE (non-external-CLI) delegation to a specific named target
 * agent, the backend deliberately leaves the WS frame's `agent_id` as the
 * PARENT's id, not the target's (`pkg/agent/subturn.go` ~610-641 — only
 * `DispatchKindExternalCLI` gets `agent.ID` reassigned; the comment there
 * calls native reassignment a larger refactor out of that fix's scope).
 * That makes `span.agentId` correct for untargeted delegation and for
 * external-CLI dispatch, but wrong for "delegate to agent X" when X is
 * native — it would show the parent's avatar/name instead of X's.
 *
 * Workaround: the originating `delegate` tool call (found via
 * `span.parentCallId`) carries the actual `agent_id` argument the calling
 * agent asked for, unaffected by the backend gap. Prefer that when present;
 * fall back to `span.agentId` when the call can't be found (e.g. scrolled
 * out of the loaded window) or didn't target a specific agent (untargeted
 * delegation — `span.agentId` is already correct there, no fix needed).
 *
 * `toolCallsById` must be keyed from BOTH the live flat `toolCalls` map AND
 * every finalized message's baked `message.tool_calls` (see the caller) —
 * the delegate call that started this span may belong to a turn that has
 * already finished and been baked out of the live map by the time this
 * runs, same root cause as groupBashSessions' doc comment below.
 */
function resolveSpanAgentId(span: SubagentSpan, toolCallsById: Record<string, ToolCall>): string | undefined {
  const originatingCall = toolCallsById[span.parentCallId]
  if (originatingCall?.tool === 'delegate') {
    const targetAgentId = originatingCall.params?.agent_id
    if (typeof targetAgentId === 'string' && targetAgentId.length > 0) {
      return targetAgentId
    }
  }
  return span.agentId
}

/**
 * Elapsed time for a running item, stamping its first-seen time on first
 * observation; the final duration for a finished item. Shared by the
 * agent-span and bash-call loops below (see file header for why elapsed
 * time is tracked here rather than carried on the wire).
 */
function trackDurationMs(
  key: string,
  isRunning: boolean,
  finishedDurationMs: number | undefined,
  firstSeenAtMap: Map<string, number>,
): number | undefined {
  if (!isRunning) {
    firstSeenAtMap.delete(key)
    return finishedDurationMs
  }
  if (firstSeenAtMap.get(key) === undefined) {
    firstSeenAtMap.set(key, Date.now())
  }
  return Math.max(0, Date.now() - firstSeenAtMap.get(key)!)
}

/**
 * A background `bash` session's status payload, parsed out of a `bash`
 * tool_call's `result` field.
 *
 * The call's own `tc.status` ("did this tool_call finish executing") is NOT
 * the same thing as the background session's own liveness — for a
 * `run_in_background: true` dispatch, the call itself returns almost
 * immediately with `tc.status === 'success'` (dispatching to the background
 * is a fast, synchronous operation from the call's point of view); it is
 * never `'running'` for more than a brief instant. The real "is the session
 * still running" signal lives inside the call's RESULT payload instead —
 * a JSON-encoded `pkg/tools/types.go` `ExecResponse`, emitted identically by
 * the dispatch (`executeRun`'s `runBackground` branch), `executePoll`,
 * `executeRead`, and `executeKill` in `pkg/tools/shell.go`.
 *
 * IMPORTANT — verified against the Go source, do not "correct" this back
 * without re-checking: the session-id field in this JSON payload is
 * `sessionId` (camelCase, from `ExecResponse`'s `json:"sessionId,omitempty"`
 * struct tag), NOT `session_id`. The snake_case `session_id` name is only
 * ever the tool's INPUT arg key for poll/read/kill
 * (`args["session_id"]` in `ExecTool.getSessionArg`) — it never appears in a
 * result payload. Grepping `pkg/tools/*.go` confirms `"session_id"` only
 * shows up as an arg key / log field, never as a JSON output field.
 */
interface ParsedBashSessionResult {
  sessionId: string
  status: string
}

/**
 * Parses a bash tool_call's `result` as a background-session status
 * payload. `result` is typed `unknown` on the wire (`ToolCall.result` in
 * `@/lib/api`) but arrives as the JSON-encoded `ForLLM` string in practice
 * (`pkg/agent/loop.go`'s `ToolExecEndPayload.Result: contentForLLM`);
 * tolerate an already-parsed object too, mirroring the same
 * string-or-object tolerance `BrowserNavigate.tsx`/`BrowserTool.tsx` use for
 * their own tool results.
 *
 * Never throws: returns null (skip this call for session-tracking purposes)
 * on anything that isn't valid JSON, isn't an object, or is missing a
 * non-empty `sessionId`/`status` string — per spec, a malformed or
 * unparseable result must be silently skipped, not guessed at.
 */
function parseBashSessionResult(result: unknown): ParsedBashSessionResult | null {
  let parsed: unknown
  if (typeof result === 'string') {
    try {
      parsed = JSON.parse(result)
    } catch {
      return null
    }
  } else if (result && typeof result === 'object') {
    parsed = result
  } else {
    return null
  }
  if (!parsed || typeof parsed !== 'object') return null
  const sessionId = (parsed as Record<string, unknown>).sessionId
  const status = (parsed as Record<string, unknown>).status
  if (typeof sessionId !== 'string' || sessionId.length === 0) return null
  if (typeof status !== 'string' || status.length === 0) return null
  return { sessionId, status }
}

/**
 * A bash tool_call is relevant to background-session tracking when it's
 * either the initial dispatch (`run_in_background: true` — action is
 * implicitly `"run"`) or one of the three actions that only ever operate on
 * an already-dispatched background session (`poll`/`read`/`kill`). See
 * `pkg/tools/shell.go`'s `execute()` switch and the tool's `action` enum
 * (`"run" | "poll" | "read" | "kill"`).
 */
function isSessionRelevantBashCall(tc: ToolCall): boolean {
  if (tc.tool !== 'bash') return false
  if (tc.params.run_in_background === true) return true
  const action = tc.params.action
  return action === 'poll' || action === 'read' || action === 'kill'
}

/**
 * Maps a background session's backend status
 * (`pkg/tools/session.go`'s `ProcessSession.Status`: `"running"` | `"done"`
 * | `"timeout"` | `"killed"`, set from `pkg/tools/shell.go`'s background
 * goroutine / `executeKill`) onto `BashActivityItem`'s 4-value status union.
 *
 * `"done"` and `"timeout"`/`"killed"` are NOT equivalent conceptually (a
 * normal exit vs. a hang vs. an explicit kill), but `BashActivityItem` only
 * supports `'running' | 'success' | 'error' | 'cancelled'` today, so the
 * closest-fit mapping is:
 *   - `"running"` -> `'running'`
 *   - `"done"`    -> `'success'`   (normal completion — matches the
 *                     pre-fix bash-item behavior, which never inspected
 *                     exitCode either)
 *   - `"timeout"` -> `'error'`     (closest fit: it failed to finish)
 *   - `"killed"`  -> `'cancelled'` (closest fit: something stopped it)
 *   - anything else (e.g. `session.go`'s defensive `"exited"`, which
 *     `IsDone()` treats as terminal but which no live code path actually
 *     sets — session_test.go:80 is the only place that string appears) ->
 *     `'error'`, a conservative default so an unrecognized terminal status
 *     doesn't silently vanish instead of surfacing in recentlyFinished.
 */
function mapBashSessionStatus(status: string): BashActivityItem['status'] {
  switch (status) {
    case 'running':
      return 'running'
    case 'done':
      return 'success'
    case 'timeout':
      return 'error'
    case 'killed':
      return 'cancelled'
    default:
      return 'error'
  }
}

interface BashSessionState {
  latestStatus: string
  /** Only the dispatch call carries the actual command text — poll/read/kill calls only carry session_id+action. */
  command?: string
}

/**
 * Groups session-relevant bash tool_calls by their background session id —
 * read from each call's own RESULT payload (`ParsedBashSessionResult.sessionId`),
 * not `params.session_id` (that's only present as an input arg on
 * poll/read/kill calls; the result payload is the uniform source of truth
 * present on dispatch/poll/read/kill alike).
 *
 * `orderedCalls` must already be in chronological arrival order (the same
 * assumption `recentlyFinished` ordering already relies on elsewhere in this
 * file), so a single forward scan naturally lands on the LATEST-observed
 * status per session by the time the loop ends. See the caller in
 * `useRunningActivity` for why this can't just be the live flat `toolCalls`
 * map alone: once a turn finishes, `chat.ts` "bakes" that turn's live tool
 * calls into the finalized message's own `message.tool_calls` array and
 * clears the flat map (`chat.ts`'s `draft.toolCalls = {}` on the `done`
 * frame) — so a still-running background session dispatched in an
 * already-finished turn would otherwise vanish from tracking the instant
 * that turn completes, even though the real process keeps running
 * server-side. The caller concatenates every finalized message's baked
 * `tool_calls` (in message order) with the live flat map's current entries
 * (always the newest, in-flight turn) before calling this function.
 */
function groupBashSessions(orderedCalls: ToolCall[]): Map<string, BashSessionState> {
  const sessions = new Map<string, BashSessionState>()
  for (const tc of orderedCalls) {
    if (!isSessionRelevantBashCall(tc)) continue
    const parsedResult = parseBashSessionResult(tc.result)
    if (!parsedResult) continue // malformed/unparseable/missing result — skip, don't throw, don't guess
    const isDispatch = tc.params.run_in_background === true
    const command = isDispatch
      ? (typeof tc.params.command === 'string' && tc.params.command) ||
        (typeof tc.params.description === 'string' && tc.params.description) ||
        '(unknown command)'
      : undefined
    const existing = sessions.get(parsedResult.sessionId)
    sessions.set(parsedResult.sessionId, {
      latestStatus: parsedResult.status,
      command: command ?? existing?.command,
    })
  }
  return sessions
}

export function useRunningActivity(): RunningActivity {
  const messages = useChatStore((s) => s.messages)
  const toolCalls = useChatStore((s) => s.toolCalls)
  // ADR-049 D2/D4/US-13: global (session-agnostic) judge-verdict feed — see judgeActivity.ts.
  const judgeVerdicts = useJudgeActivityStore((s) => s.verdicts)

  // Reused ['agents'] query (prefetched by AppShell) — no extra network request.
  const { data: agents = [] } = useQuery({
    queryKey: ['agents'],
    queryFn: fetchAgents,
    staleTime: 30_000,
  })

  // 1Hz re-render tick so running-item elapsed time stays live. Mirrors the
  // interval-cleanup pattern in RateLimitIndicator.tsx (effect re-subscribes
  // on unmount only — no leaked timers).
  const [, setTick] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), 1000)
    return () => clearInterval(id)
  }, [])

  const agentSpans: SubagentSpan[] = useMemo(() => messages.flatMap((m) => m.spans ?? []), [messages])

  // All tool calls the active session knows about, live turn plus every
  // already-finalized (baked) turn — see groupBashSessions' and
  // resolveSpanAgentId's doc comments for why the live flat `toolCalls` map
  // alone isn't enough: `chat.ts` bakes a finished turn's tool calls into
  // that message's own `tool_calls` array and clears the flat map, so
  // anything keyed only off the live map loses track the instant a turn
  // completes. Finalized messages are already in chronological order; the
  // live map (always the newest, in-flight turn) is concatenated/merged last
  // so it wins on any id collision (there shouldn't be one in practice).
  const allToolCalls = useMemo(
    () => [...messages.flatMap((m) => m.tool_calls ?? []), ...Object.values(toolCalls)],
    [messages, toolCalls],
  )
  const toolCallsById = useMemo(() => {
    const byId: Record<string, ToolCall> = {}
    for (const tc of allToolCalls) byId[tc.id] = tc
    return byId
  }, [allToolCalls])
  const bashSessions = useMemo(() => groupBashSessions(allToolCalls), [allToolCalls])

  const running: ActivityItem[] = []
  const recentlyFinished: ActivityItem[] = []

  for (const span of agentSpans) {
    const effectiveAgentId = resolveSpanAgentId(span, toolCallsById)
    const resolved = resolveAgent(effectiveAgentId, agents)
    // Narrow via the literal comparison (not an aliased boolean) so
    // `span.durationMs`/`finalResult`/`reason` — only present on the
    // terminal member of the SubagentSpan union — type-check in the `false`
    // branch.
    const isSpanRunning = span.status === 'running'
    const terminal = isSpanRunning ? null : (span as SubagentSpanTerminal)
    const finishedDurationMs = terminal?.durationMs
    const durationMs = trackDurationMs(span.spanId, isSpanRunning, finishedDurationMs, firstSeenAtMap)
    const item: AgentActivityItem = {
      kind: 'agent',
      key: span.spanId,
      agentId: effectiveAgentId,
      ...resolved,
      taskLabel: span.taskLabel,
      status: span.status,
      durationMs,
      steps: resolved.agentType === '3p' ? [] : span.steps,
      finalResult: terminal?.finalResult,
      interruptReason: terminal?.reason,
    }
    if (span.status === 'running') running.push(item)
    else recentlyFinished.push(item)
  }

  for (const [sessionId, session] of bashSessions) {
    const isRunning = session.latestStatus === 'running'
    // Unlike SubagentSpan, a background session carries no wire-level total
    // duration — freeze the elapsed-since-first-seen value at the moment
    // it's observed finished (one tick after this would've shown almost the
    // same number anyway), rather than leaving it undefined.
    const priorStamp = firstSeenAtMap.get(sessionId)
    const finishedDurationMs = !isRunning && priorStamp !== undefined ? Date.now() - priorStamp : undefined
    const durationMs = trackDurationMs(sessionId, isRunning, finishedDurationMs, firstSeenAtMap)
    const item: BashActivityItem = {
      kind: 'bash',
      key: sessionId,
      command: session.command ?? '(unknown command)',
      status: mapBashSessionStatus(session.latestStatus),
      durationMs,
    }
    if (isRunning) running.push(item)
    else recentlyFinished.push(item)
  }

  // ADR-049 D2/D4/US-13/SD-C11: judge verdicts are a GLOBAL feed (the wire
  // frame carries no session_id — see judgeActivity.ts), always terminal —
  // there is no "judge started" frame, only the completed verdict push, so
  // every item here goes straight to recentlyFinished, sharing the same
  // RECENTLY_FINISHED_CAP with agent/bash items via the shared slice below.
  for (const verdict of judgeVerdicts) {
    const item: JudgeActivityItem = {
      kind: 'judge',
      key: verdict.id,
      scope: verdict.scope,
      taskId: verdict.task_id,
      planId: verdict.plan_id,
      round: verdict.round,
      met: verdict.met,
      criterionVerdicts: verdict.per_criterion.map((cv) => ({
        text: cv.criterion_id,
        met: cv.met,
        reason: cv.reason,
      })),
      model: verdict.model,
      judgeAgentId: verdict.judge_agent_id,
      judgedAt: verdict.judged_at,
      status: verdict.met ? 'success' : 'error',
    }
    recentlyFinished.push(item)
  }

  return {
    runningCount: running.length,
    running,
    // Most-recent-first by arrival order. Neither type carries a real
    // timestamp (see file header), so "arrival order" is the position in
    // which each item was appended above (chronological within its own
    // source — messages/toolCalls both preserve insertion order).
    recentlyFinished: recentlyFinished.slice(-RECENTLY_FINISHED_CAP).reverse(),
  }
}

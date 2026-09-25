/**
 * Delegation event lines — pure derivation (docs/internal/specs/delegation-chat-surface-spec.md).
 *
 * One line per thing that happened, never per tool call. The inputs are the
 * records the chat store already keeps for a session: subagent spans (the
 * child's lifecycle) and the parent's `delegate` / background `bash` tool
 * calls, whether those calls are still on the live turn or already baked
 * onto a message. A reload replays the same records, so the same snapshot
 * always yields the same lines.
 *
 * Polls (`status`, `peek`, `inbox`, `inbox_ack`) are the parent looking.
 * They produce nothing, even when the payload quotes the child.
 *
 * D1: `finalResult` and `statusLine` are on the span view so callers can pass
 * the real span through, and so a test can prove the child's words were
 * available. No event field is ever copied from them, from a poll payload,
 * or from a background command's output.
 */

import type { ToolCall } from '@/lib/api'
import type { DelegationEvent, DelegationEventKind } from '@/lib/delegationEvents.types'

/** Child lifecycle values the store reduces from `subagent_state`. */
export type DelegationLifecycleState =
  | 'queued'
  | 'running'
  | 'needs_input'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'cancelled'
  | 'timed_out'
  | string

/** The span fields this derivation reads. `finalResult` / `statusLine` are ignored on purpose (D1). */
export interface DelegationSpanView {
  spanId: string
  parentCallId: string
  taskLabel: string
  agentId?: string
  childSessionId?: string
  status: 'running' | 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout' | 'parked'
  lifecycleState?: DelegationLifecycleState
  /** Child-authored. Present on the view; never copied onto an event. */
  finalResult?: string
  /** Child-authored (or the literal 'steered'). Present on the view; never copied onto an event. */
  statusLine?: string
  /**
   * Store clock (`Date.now()` at reduce time). NOT stable across a replay —
   * derivation must not read it. Present so a parity test can prove two
   * snapshots with different clocks still match.
   */
  lastUpdateAt?: string
}

export interface DelegationMessageView {
  id: string
  timestamp: string
  spans?: DelegationSpanView[]
  toolCalls?: ToolCall[]
}

/**
 * One session's derivation input. Live calls are the in-flight turn (not yet
 * baked onto a message). A rebuilt session has those same calls on the
 * messages and an empty live map — both shapes must derive the same events.
 */
export interface DelegationEventSource {
  sessionId: string
  messages: DelegationMessageView[]
  liveToolCalls?: Record<string, ToolCall>
  liveToolCallOrder?: string[]
  /** Live call id → assistant message id. Unknown ids fall back to the last message. */
  toolCallOwnerMessageId?: Record<string, string>
  /** agent id → display name. Missing ids fall back to the id itself. */
  agentNames?: Record<string, string>
}

const POLL_ACTIONS = new Set(['status', 'peek', 'inbox', 'inbox_ack'])

interface PlacedCall {
  call: ToolCall
  messageIndex: number
  anchorMessageId?: string
}

interface PlacedSpan {
  span: DelegationSpanView
  messageIndex: number
  anchorMessageId: string
}

interface Draft {
  event: DelegationEvent
  messageIndex: number
  seq: number
}

interface BashStory {
  command: string
  launchCallId?: string
  firstCallId?: string
  terminalCallId?: string
  terminalStatus?: string
  exitCode?: number
}

/** JSON object at the start of a tool result. Delegate `run` appends a prose line after the JSON. */
export function parseLeadingJson(result: unknown): Record<string, unknown> | null {
  if (result && typeof result === 'object' && !Array.isArray(result)) {
    return result as Record<string, unknown>
  }
  if (typeof result !== 'string') return null
  const trimmed = result.trim()
  if (!trimmed.startsWith('{')) return null
  const firstLine = trimmed.split('\n', 1)[0] ?? trimmed
  for (const piece of firstLine === trimmed ? [trimmed] : [trimmed, firstLine]) {
    try {
      const value: unknown = JSON.parse(piece)
      if (value && typeof value === 'object' && !Array.isArray(value)) {
        return value as Record<string, unknown>
      }
    } catch {
      // Trailing prose makes the whole string invalid; the first line is tried next.
    }
  }
  return null
}

function stringParam(call: ToolCall, key: string): string | undefined {
  const value = call.params[key]
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

function jsonString(json: Record<string, unknown> | null, key: string): string | undefined {
  if (!json) return undefined
  const value = json[key]
  return typeof value === 'string' && value.trim() !== '' ? value : undefined
}

/** `delegate` with no action is `run` — the same default the tool applies. */
function delegateAction(call: ToolCall): string | null {
  if (call.tool !== 'delegate') return null
  const raw = call.params.action
  if (raw == null || raw === '') return 'run'
  return typeof raw === 'string' ? raw : null
}

function resultText(result: unknown): string {
  if (typeof result === 'string') return result
  if (result && typeof result === 'object') {
    try {
      return JSON.stringify(result)
    } catch {
      return ''
    }
  }
  return ''
}

/**
 * A cancel tool result is not always "the child was stopped". The tool also
 * reports success for "already finished" and "nothing to cancel". Only the
 * wordings that mean a stop actually landed count.
 */
function cancelLanded(result: unknown): boolean {
  const text = resultText(result)
  if (/no action needed/i.test(text)) return false
  if (/already terminal/i.test(text)) return false
  if (/hard-cancelled/i.test(text)) return true
  if (/cooperatively cancelled/i.test(text)) return true
  if (/has been dropped and will never run/i.test(text)) return true
  return false
}

function runLaunchState(call: ToolCall | undefined): 'queued' | 'immediate' | 'unknown' {
  if (!call || call.status !== 'success') return 'unknown'
  const state = parseLeadingJson(call.result)?.state
  if (state === 'queued') return 'queued'
  if (typeof state === 'string') return 'immediate'
  return 'unknown'
}

function runSessionId(call: ToolCall | undefined): string | undefined {
  if (!call) return undefined
  return jsonString(parseLeadingJson(call.result), 'session_id')
}

/**
 * Still in the queue, or queued and ended before it ever ran: no "started"
 * line (D3 — a queued worker does not announce itself). A successful finish
 * means it did run, even if a lifecycle frame was missed.
 */
function birthKind(span: DelegationSpanView, launch: 'queued' | 'immediate' | 'unknown'): 'delegated' | 'started' | null {
  if (span.status === 'running' && span.lifecycleState === 'queued') return null
  if (span.status === 'running' && span.lifecycleState == null && launch === 'queued') return null
  const neverRan =
    launch === 'queued' &&
    (span.lifecycleState === 'queued' || span.lifecycleState == null) &&
    span.status !== 'success'
  if (neverRan) return null
  return launch === 'queued' ? 'started' : 'delegated'
}

function displayName(agentId: string | undefined, names: Record<string, string> | undefined): string {
  if (!agentId) return 'Unknown agent'
  return names?.[agentId] || agentId
}

function agentIdFor(span: DelegationSpanView | undefined, runCall: ToolCall | undefined): string | undefined {
  if (runCall) {
    const fromRun = stringParam(runCall, 'agent_id')
    if (fromRun) return fromRun
  }
  return span?.agentId
}

function refusalReason(call: ToolCall): string | undefined {
  const json = parseLeadingJson(call.result)
  const code = json?.error
  if (code === 'delegation_denied' || code === 'skill_not_found') {
    return jsonString(json, 'reason') ?? jsonString(json, 'message')
  }
  if (typeof call.result === 'string' && call.result.trim() !== '') return call.result.trim()
  if (typeof call.error === 'string' && call.error.trim() !== '') return call.error.trim()
  return undefined
}

function withDefined(event: DelegationEvent): DelegationEvent {
  const out: DelegationEvent = { id: event.id, kind: event.kind, sessionId: event.sessionId, at: event.at }
  if (event.anchorMessageId !== undefined) out.anchorMessageId = event.anchorMessageId
  if (event.agentName !== undefined) out.agentName = event.agentName
  if (event.title !== undefined) out.title = event.title
  if (event.childSessionId !== undefined) out.childSessionId = event.childSessionId
  if (event.reason !== undefined) out.reason = event.reason
  if (event.command !== undefined) out.command = event.command
  if (event.exitCode !== undefined) out.exitCode = event.exitCode
  return out
}

function baseEvent(
  source: DelegationEventSource,
  placed: { anchorMessageId?: string },
  id: string,
  kind: DelegationEventKind,
): DelegationEvent {
  return withDefined({
    id,
    kind,
    sessionId: source.sessionId,
    at: 0,
    anchorMessageId: placed.anchorMessageId,
  })
}

function placeCalls(source: DelegationEventSource): PlacedCall[][] {
  const groups: PlacedCall[][] = source.messages.map(() => [])
  const indexById = new Map<string, { messageIndex: number; callIndex: number }>()
  source.messages.forEach((message, messageIndex) => {
    for (const call of message.toolCalls ?? []) {
      const callIndex = groups[messageIndex].length
      groups[messageIndex].push({ call, messageIndex, anchorMessageId: message.id })
      indexById.set(call.id, { messageIndex, callIndex })
    }
  })

  const live = source.liveToolCalls ?? {}
  const seen = new Set(source.liveToolCallOrder ?? [])
  const order = [...(source.liveToolCallOrder ?? [])]
  for (const id of Object.keys(live)) {
    if (!seen.has(id)) order.push(id)
  }
  for (const id of order) {
    const call = live[id]
    if (!call) continue
    const existing = indexById.get(call.id)
    if (existing) {
      const prev = groups[existing.messageIndex][existing.callIndex]
      groups[existing.messageIndex][existing.callIndex] = { ...prev, call }
      continue
    }
    const anchor = source.toolCallOwnerMessageId?.[call.id]
    let messageIndex = anchor ? source.messages.findIndex((message) => message.id === anchor) : -1
    if (messageIndex < 0) messageIndex = source.messages.length - 1
    if (messageIndex < 0) {
      groups.push([])
      messageIndex = 0
    }
    groups[messageIndex].push({
      call,
      messageIndex,
      anchorMessageId: source.messages[messageIndex]?.id,
    })
  }
  return groups
}

function placeSpans(source: DelegationEventSource): PlacedSpan[] {
  const spans: PlacedSpan[] = []
  source.messages.forEach((message, messageIndex) => {
    for (const span of message.spans ?? []) {
      spans.push({ span, messageIndex, anchorMessageId: message.id })
    }
  })
  return spans
}

function indexById(groups: PlacedCall[][]): Map<string, PlacedCall> {
  const byId = new Map<string, PlacedCall>()
  for (const group of groups) {
    for (const placed of group) byId.set(placed.call.id, placed)
  }
  return byId
}

function landedCancelTargets(groups: PlacedCall[][]): Set<string> {
  const targets = new Set<string>()
  for (const group of groups) {
    for (const placed of group) {
      if (delegateAction(placed.call) !== 'cancel') continue
      if (placed.call.status !== 'success') continue
      if (!cancelLanded(placed.call.result)) continue
      const sessionId = stringParam(placed.call, 'session_id')
      if (sessionId) targets.add(sessionId)
    }
  }
  return targets
}

function isBackgroundBash(call: ToolCall): boolean {
  if (call.tool !== 'bash') return false
  if (call.params.run_in_background === true) return true
  const action = call.params.action
  return action === 'poll' || action === 'read' || action === 'kill'
}

function parseBash(result: unknown): { sessionId: string; status: string; exitCode?: number } | null {
  const json = parseLeadingJson(result)
  const sessionId = jsonString(json, 'sessionId')
  const status = jsonString(json, 'status')
  if (!sessionId || !status) return null
  const exitCode = json?.exitCode
  return {
    sessionId,
    status,
    exitCode: typeof exitCode === 'number' && Number.isFinite(exitCode) ? exitCode : undefined,
  }
}

function bashStories(groups: PlacedCall[][]): Map<string, BashStory> {
  const stories = new Map<string, BashStory>()
  for (const group of groups) {
    for (const placed of group) {
      if (!isBackgroundBash(placed.call)) continue
      const parsed = parseBash(placed.call.result)
      if (!parsed) continue
      const story = stories.get(parsed.sessionId) ?? { command: '(unknown command)' }
      if (!story.firstCallId) story.firstCallId = placed.call.id
      if (placed.call.params.run_in_background === true) {
        story.launchCallId = placed.call.id
        const command = stringParam(placed.call, 'command')
        if (command) story.command = command
      }
      if (parsed.status !== 'running' && !story.terminalCallId) {
        story.terminalCallId = placed.call.id
        story.terminalStatus = parsed.status
        story.exitCode = parsed.exitCode
      }
      stories.set(parsed.sessionId, story)
    }
  }
  for (const story of stories.values()) {
    if (!story.launchCallId) story.launchCallId = story.firstCallId
  }
  return stories
}

function bashKind(story: BashStory): 'bash_finished' | 'bash_failed' | null {
  if (!story.terminalStatus) return null
  const clean = story.terminalStatus === 'done' && (story.exitCode === undefined || story.exitCode === 0)
  return clean ? 'bash_finished' : 'bash_failed'
}

function pushDraft(drafts: Draft[], event: DelegationEvent, messageIndex: number, seq: number): void {
  drafts.push({ event, messageIndex, seq })
}

/**
 * Per-span, not per child session. A follow-up runs another generation in the
 * same child session under a new span; collapsing those onto the session id
 * would drop the second finish. The span id is what replay preserves.
 */
function lifecycleId(kind: string, span: DelegationSpanView): string {
  return `${kind}:${span.spanId}`
}

function childSessionOf(span: DelegationSpanView, runCall: ToolCall | undefined): string | undefined {
  return span.childSessionId || runSessionId(runCall)
}

function birthEvent(
  source: DelegationEventSource,
  kind: 'delegated' | 'started',
  span: PlacedSpan,
  runCall: ToolCall | undefined,
  anchor: { anchorMessageId?: string },
): DelegationEvent {
  const title = span.span.taskLabel.trim() || (runCall ? stringParam(runCall, 'label') : undefined)
  return withDefined({
    ...baseEvent(source, anchor, lifecycleId(kind, span.span), kind),
    agentName: displayName(agentIdFor(span.span, runCall), source.agentNames),
    title,
    childSessionId: childSessionOf(span.span, runCall),
  })
}

function terminalKind(
  span: DelegationSpanView,
  cancelled: Set<string>,
  childSessionId: string | undefined,
): 'finished' | 'stopped' | null {
  if (span.status === 'running' || span.status === 'parked') return null
  if (span.status === 'success') return 'finished'
  // Same session the rest of the derivation uses: the span's own id, or the
  // run call's result `session_id` when a pre-ADR-091 transcript omitted it.
  if (childSessionId && cancelled.has(childSessionId)) return null
  return 'stopped'
}

function spanEventsForMessage(
  source: DelegationEventSource,
  spans: PlacedSpan[],
  callsById: Map<string, PlacedCall>,
  cancelled: Set<string>,
  birthEmitted: Set<string>,
): DelegationEvent[] {
  const events: DelegationEvent[] = []
  for (const placed of spans) {
    const runCall = callsById.get(placed.span.parentCallId)?.call
    const childSessionId = childSessionOf(placed.span, runCall)
    if (!birthEmitted.has(placed.span.spanId)) {
      const kind = birthKind(placed.span, runLaunchState(runCall))
      if (kind) {
        birthEmitted.add(placed.span.spanId)
        events.push(birthEvent(source, kind, placed, runCall, placed))
      }
    }
    const terminal = terminalKind(placed.span, cancelled, childSessionId)
    if (!terminal) continue
    const title = terminal === 'finished' ? placed.span.taskLabel.trim() || undefined : undefined
    events.push(
      withDefined({
        ...baseEvent(source, placed, lifecycleId(terminal, placed.span), terminal),
        agentName: displayName(agentIdFor(placed.span, runCall), source.agentNames),
        title,
        childSessionId,
      }),
    )
  }
  return events
}

function parentAction(
  source: DelegationEventSource,
  kind: 'steered' | 'answered' | 'cancelled' | 'follow_up',
  placed: PlacedCall,
  spansByChild: Map<string, PlacedSpan>,
  callsById: Map<string, PlacedCall>,
): DelegationEvent {
  const namedSession = stringParam(placed.call, 'session_id')
  const childSessionId = (kind === 'follow_up' ? runSessionId(placed.call) : undefined) ?? namedSession
  const span =
    (childSessionId ? spansByChild.get(childSessionId) : undefined) ??
    (namedSession ? spansByChild.get(namedSession) : undefined)
  const runCall = span ? callsById.get(span.span.parentCallId)?.call : undefined
  const title = kind === 'follow_up' ? stringParam(placed.call, 'label') ?? stringParam(placed.call, 'task') : undefined
  return withDefined({
    ...baseEvent(source, placed, `${kind}:${placed.call.id}`, kind),
    agentName: displayName(agentIdFor(span?.span, runCall), source.agentNames),
    title,
    childSessionId,
  })
}

function refusedEvent(source: DelegationEventSource, placed: PlacedCall): DelegationEvent {
  return withDefined({
    ...baseEvent(source, placed, `refused:${placed.call.id}`, 'refused'),
    reason: refusalReason(placed.call),
  })
}

function eventsFromCall(
  source: DelegationEventSource,
  placed: PlacedCall,
  spansByParent: Map<string, PlacedSpan>,
  spansByChild: Map<string, PlacedSpan>,
  callsById: Map<string, PlacedCall>,
  birthEmitted: Set<string>,
  bashByCall: Map<string, BashStory[]>,
): DelegationEvent[] {
  const events: DelegationEvent[] = []
  const action = delegateAction(placed.call)
  if (action && !POLL_ACTIONS.has(action)) {
    events.push(
      ...delegateEvents(source, action, placed, spansByParent, spansByChild, callsById, birthEmitted),
    )
  }
  for (const story of bashByCall.get(placed.call.id) ?? []) {
    if (story.launchCallId === placed.call.id) {
      events.push(
        withDefined({
          ...baseEvent(source, placed, `bash_launched:${bashSessionId(placed)}`, 'bash_launched'),
          command: story.command,
        }),
      )
    }
    const kind = story.terminalCallId === placed.call.id ? bashKind(story) : null
    if (kind) {
      events.push(
        withDefined({
          ...baseEvent(source, placed, `${kind}:${bashSessionId(placed)}`, kind),
          command: story.command,
          exitCode: kind === 'bash_failed' ? story.exitCode : undefined,
        }),
      )
    }
  }
  return events
}

function bashSessionId(placed: PlacedCall): string {
  return parseBash(placed.call.result)?.sessionId ?? placed.call.id
}

function delegateEvents(
  source: DelegationEventSource,
  action: string,
  placed: PlacedCall,
  spansByParent: Map<string, PlacedSpan>,
  spansByChild: Map<string, PlacedSpan>,
  callsById: Map<string, PlacedCall>,
  birthEmitted: Set<string>,
): DelegationEvent[] {
  if (action === 'run') return runEvents(source, placed, spansByParent, birthEmitted)
  if (placed.call.status !== 'success') return []
  if (action === 'cancel') {
    // No cascadeCount: the cancel result text never says how many descendants
    // stopped (pkg/tools/delegate_run.go executeCancel). Omit rather than guess.
    return cancelLanded(placed.call.result) ? [parentAction(source, 'cancelled', placed, spansByChild, callsById)] : []
  }
  if (action === 'steer') return [parentAction(source, 'steered', placed, spansByChild, callsById)]
  if (action === 'respond') return [parentAction(source, 'answered', placed, spansByChild, callsById)]
  if (action === 'follow_up') return [parentAction(source, 'follow_up', placed, spansByChild, callsById)]
  return []
}

function runEvents(
  source: DelegationEventSource,
  placed: PlacedCall,
  spansByParent: Map<string, PlacedSpan>,
  birthEmitted: Set<string>,
): DelegationEvent[] {
  const span = spansByParent.get(placed.call.id)
  if (placed.call.status === 'error' && !span) return [refusedEvent(source, placed)]
  if (!span || birthEmitted.has(span.span.spanId)) return []
  const kind = birthKind(span.span, runLaunchState(placed.call))
  if (!kind) return []
  birthEmitted.add(span.span.spanId)
  return [birthEvent(source, kind, span, placed.call, placed)]
}

function bashIndex(stories: Map<string, BashStory>): Map<string, BashStory[]> {
  const byCall = new Map<string, BashStory[]>()
  for (const story of stories.values()) {
    const ids = new Set<string>()
    if (story.launchCallId) ids.add(story.launchCallId)
    if (story.terminalCallId) ids.add(story.terminalCallId)
    for (const id of ids) {
      const list = byCall.get(id) ?? []
      list.push(story)
      byCall.set(id, list)
    }
  }
  return byCall
}

function stampAndDedupe(drafts: Draft[], messages: DelegationMessageView[]): DelegationEvent[] {
  const ordered = [...drafts].sort(
    (a, b) => a.messageIndex - b.messageIndex || a.seq - b.seq || a.event.id.localeCompare(b.event.id),
  )
  const seen = new Set<string>()
  const events: DelegationEvent[] = []
  for (const draft of ordered) {
    if (seen.has(draft.event.id)) continue
    seen.add(draft.event.id)
    const stamp = Date.parse(messages[draft.messageIndex]?.timestamp ?? '')
    const at = (Number.isFinite(stamp) ? stamp : 0) + draft.seq
    events.push({ ...draft.event, at })
  }
  return events
}

/**
 * Ordered delegation lines for one session. Pure: no clock, no module state.
 * `at` is the anchor message's timestamp plus the line's position on that
 * message, so two snapshots with the same records share timestamps even when
 * the store's own "last update" clock differs between a live reduce and a replay.
 */
export function deriveDelegationEvents(source: DelegationEventSource): DelegationEvent[] {
  if (!source.sessionId) return []
  const groups = placeCalls(source)
  const spans = placeSpans(source)
  const callsById = indexById(groups)
  const cancelled = landedCancelTargets(groups)
  const bashByCall = bashIndex(bashStories(groups))
  const spansByParent = new Map(spans.map((span) => [span.span.parentCallId, span]))
  const spansByChild = new Map<string, PlacedSpan>()
  for (const span of spans) {
    if (span.span.childSessionId) spansByChild.set(span.span.childSessionId, span)
  }
  const birthEmitted = new Set<string>()
  const drafts: Draft[] = []

  groups.forEach((group, messageIndex) => {
    let seq = 0
    for (const placed of group) {
      for (const event of eventsFromCall(
        source,
        placed,
        spansByParent,
        spansByChild,
        callsById,
        birthEmitted,
        bashByCall,
      )) {
        pushDraft(drafts, event, messageIndex, seq)
        seq += 1
      }
    }
    const onMessage = spans.filter((span) => span.messageIndex === messageIndex)
    for (const event of spanEventsForMessage(source, onMessage, callsById, cancelled, birthEmitted)) {
      pushDraft(drafts, event, messageIndex, seq)
      seq += 1
    }
  })

  return stampAndDedupe(drafts, source.messages)
}

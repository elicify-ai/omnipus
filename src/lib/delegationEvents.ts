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
export interface DelegationSpanView { // not-wire-format: read-only UI projection of a store span for line derivation, built client-side and never serialised over REST/WS
  spanId: string
  parentCallId: string
  taskLabel: string
  agentId?: string
  childSessionId?: string
  status: 'running' | 'success' | 'error' | 'cancelled' | 'interrupted' | 'timeout' | 'parked'
  lifecycleState?: DelegationLifecycleState
  /**
   * Sticky record from the store: a runnable lifecycle state was reduced onto
   * this span at least once. The last state is not enough — a child that ran
   * and then failed ends on `failed`, same as one that never left the queue (D10).
   */
  hasRun?: boolean
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

export interface DelegationMessageView { // not-wire-format: read-only UI projection of a chat message for line derivation, built client-side and never sent over the gateway boundary
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
export interface DelegationEventSource { // not-wire-format: in-memory input bundle for the pure derivation function, assembled in the browser and never serialised
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

interface PlacedCall { // not-wire-format: internal derivation helper pairing a tool call with its message index, local to this module, never on the wire
  call: ToolCall
  messageIndex: number
  anchorMessageId?: string
}

interface PlacedSpan { // not-wire-format: internal derivation helper pairing a span with its message index, local to this module, never on the wire
  span: DelegationSpanView
  messageIndex: number
  anchorMessageId: string
}

interface Draft { // not-wire-format: internal pre-stamp event record used only inside the derivation pass, never serialised or sent anywhere
  event: DelegationEvent
  messageIndex: number
  seq: number
}

interface BashStory { // not-wire-format: internal accumulator of one background command's observed polls during derivation, local only, never on the wire
  command: string
  launchCallId?: string
  firstCallId?: string
  terminalCallId?: string
  terminalStatus?: string
  exitCode?: number
  /** A poll or kill (or a preview that literally contained exitCode) reported it. A read never does. */
  exitKnown?: boolean
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

/** Lifecycle states that prove the child actually left the queue and ran (D10). */
const RAN_LIFECYCLE = new Set(['running', 'needs_input', 'paused', 'completed'])

/**
 * Follow-up generation N ≥ 2. The gateway's span id is
 * `span_<originalRunCallId>_g<N>` (F5). Generation 1 keeps `span_<callId>`
 * and must still announce. A follow-up span must not.
 */
function isFollowUpGeneration(spanId: string): boolean {
  const match = /_g(\d+)$/.exec(spanId)
  return match != null && Number(match[1]) >= 2
}

function generationNumber(spanId: string): number {
  const match = /_g(\d+)$/.exec(spanId)
  return match ? Number(match[1]) : 1
}

/**
 * A queued launch that never left the queue (D10) — no line at all.
 * `hasRun` is the sticky record. The last state is not enough: `failed` /
 * `cancelled` / `timed_out` is also how a child that did run then ended.
 * A success, or a state that is itself only reachable after starting, did run
 * even when an older snapshot never carried the flag.
 */
function queuedNeverRan(span: DelegationSpanView, launch: 'queued' | 'immediate' | 'unknown'): boolean {
  if (launch !== 'queued') return false
  if (span.hasRun) return false
  if (span.status === 'success') return false
  if (span.lifecycleState != null && RAN_LIFECYCLE.has(span.lifecycleState)) return false
  return true
}

/**
 * Still in the queue: no line (D3). Queued and ended before it ran: no line
 * (D10). A follow-up generation does not announce a second "Delegated" (D9).
 */
function birthKind(span: DelegationSpanView, launch: 'queued' | 'immediate' | 'unknown'): 'delegated' | 'started' | null {
  if (isFollowUpGeneration(span.spanId)) return null
  if (queuedNeverRan(span, launch)) return null
  if (span.status === 'running' && span.lifecycleState === 'queued') return null
  if (span.status === 'running' && span.lifecycleState == null && launch === 'queued') return null
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

function callError(call: ToolCall): string | undefined {
  return typeof call.error === 'string' && call.error.trim() !== '' ? call.error.trim() : undefined
}

function refusalReason(call: ToolCall): string | undefined {
  const json = parseLeadingJson(call.result)
  const code = json?.error
  if (code === 'delegation_denied' || code === 'skill_not_found') {
    // A denial payload with neither reason nor message still has the frame's
    // error string (pkg/gateway/websocket_forward_hub.go::hubToolExecEnd).
    return jsonString(json, 'reason') ?? jsonString(json, 'message') ?? callError(call)
  }
  if (typeof call.result === 'string' && call.result.trim() !== '') return call.result.trim()
  return callError(call)
}

/** Drop keys whose value is undefined so a live event and a replayed one compare equal. */
function withDefined(event: DelegationEvent): DelegationEvent {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(event)) {
    if (value !== undefined) out[key] = value
  }
  return out as unknown as DelegationEvent
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

/**
 * Each landed cancel suppresses the stopped line of one generation: the
 * earliest not-yet-covered non-success span of that child. A later
 * generation that fails on its own still gets its stopped line.
 */
function suppressedSpanIds(spans: PlacedSpan[], groups: PlacedCall[][], callsById: Map<string, PlacedCall>): Set<string> {
  const remaining = new Map<string, number>()
  for (const group of groups) {
    for (const placed of group) {
      if (delegateAction(placed.call) !== 'cancel') continue
      if (placed.call.status !== 'success') continue
      if (!cancelLanded(placed.call.result)) continue
      const sessionId = stringParam(placed.call, 'session_id')
      if (!sessionId) continue
      remaining.set(sessionId, (remaining.get(sessionId) ?? 0) + 1)
    }
  }
  const suppressed = new Set<string>()
  for (const placed of spans) {
    const runCall = callsById.get(placed.span.parentCallId)?.call
    const childSessionId = childSessionOf(placed.span, runCall)
    if (!childSessionId) continue
    if (placed.span.status === 'running' || placed.span.status === 'parked' || placed.span.status === 'success') continue
    const left = remaining.get(childSessionId) ?? 0
    if (left <= 0) continue
    suppressed.add(placed.span.spanId)
    remaining.set(childSessionId, left - 1)
  }
  return suppressed
}

function isBackgroundBash(call: ToolCall): boolean {
  if (call.tool !== 'bash') return false
  if (call.params.run_in_background === true) return true
  const action = call.params.action
  return action === 'poll' || action === 'read' || action === 'kill'
}

function finiteExit(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

/** Truncation sentinels keep sessionId and status at the front of `preview`. */
function bashPreview(result: unknown): string | null {
  if (!result || typeof result !== 'object' || Array.isArray(result)) return null
  const record = result as Record<string, unknown>
  const sentinel = record._truncated === true || record._truncated_client === true || record._ref === true
  return sentinel && typeof record.preview === 'string' ? record.preview : null
}

function bashFromText(text: string): { sessionId: string; status: string; exitCode?: number } | null {
  const sessionId = /"sessionId"\s*:\s*"([^"\\]+)"/.exec(text)?.[1]
  const status = /"status"\s*:\s*"([^"\\]+)"/.exec(text)?.[1]
  if (!sessionId || !status) return null
  const exitMatch = /"exitCode"\s*:\s*(-?\d+)/.exec(text)
  const exitCode = exitMatch ? Number(exitMatch[1]) : undefined
  return { sessionId, status, exitCode: finiteExit(exitCode) }
}

function parseBash(result: unknown): { sessionId: string; status: string; exitCode?: number } | null {
  const json = parseLeadingJson(result)
  const sessionId = jsonString(json, 'sessionId')
  const status = jsonString(json, 'status')
  if (sessionId && status) {
    return { sessionId, status, exitCode: finiteExit(json?.exitCode) }
  }
  const preview = bashPreview(result)
  if (preview) return bashFromText(preview)
  if (typeof result === 'string') return bashFromText(result)
  return null
}

/** poll and kill always marshal exitCode (0 is omitted). read never does. */
function reportsExit(call: ToolCall): boolean {
  const action = call.params.action
  return action === 'poll' || action === 'kill'
}

function statusSpecificity(status: string): number {
  if (status === 'killed' || status === 'canceled' || status === 'cancelled' || status === 'timeout') return 2
  return 1
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
      if (parsed.status !== 'running') {
        if (!story.terminalCallId) {
          story.terminalCallId = placed.call.id
          story.terminalStatus = parsed.status
        } else if (story.terminalStatus && statusSpecificity(parsed.status) > statusSpecificity(story.terminalStatus)) {
          story.terminalStatus = parsed.status
        }
        // A read of a finished command has no exit code. Do not treat that
        // absence as success — take the code from whichever later poll or
        // kill reports it (pkg/tools/shell_bg.go::executeRead vs executePoll).
        if (reportsExit(placed.call)) {
          story.exitCode = parsed.exitCode ?? 0
          story.exitKnown = true
        } else if (parsed.exitCode !== undefined) {
          story.exitCode = parsed.exitCode
          story.exitKnown = true
        }
      }
      stories.set(parsed.sessionId, story)
    }
  }
  for (const story of stories.values()) {
    if (!story.launchCallId) story.launchCallId = story.firstCallId
  }
  return stories
}

function bashKind(story: BashStory): 'bash_finished' | 'bash_failed' | 'bash_stopped' | null {
  const status = story.terminalStatus
  if (!status) return null
  // pkg/tools/session.go: StatusKilled and StatusCanceled are on purpose.
  // StatusTimeout stays a failure. "canceled" is the Go spelling.
  if (status === 'killed' || status === 'canceled' || status === 'cancelled') return 'bash_stopped'
  if (status === 'done' || status === 'exited') {
    if (!story.exitKnown) return null
    return story.exitCode === 0 ? 'bash_finished' : 'bash_failed'
  }
  return 'bash_failed'
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

function terminalKind(span: DelegationSpanView, suppressed: Set<string>): 'finished' | 'stopped' | null {
  if (span.status === 'running' || span.status === 'parked') return null
  if (span.status === 'success') return 'finished'
  // A landed cancel covers the generation it hit. Later generations still
  // get their own stopped line (gap 2).
  if (suppressed.has(span.spanId)) return null
  return 'stopped'
}

function maybeEmitBirth(
  source: DelegationEventSource,
  placed: PlacedSpan,
  runCall: ToolCall | undefined,
  anchor: { anchorMessageId?: string },
  birthEmitted: Set<string>,
): DelegationEvent | null {
  if (birthEmitted.has(placed.span.spanId)) return null
  const kind = birthKind(placed.span, runLaunchState(runCall))
  if (!kind) return null
  birthEmitted.add(placed.span.spanId)
  return birthEvent(source, kind, placed, runCall, anchor)
}

function spanTerminalEvent(
  source: DelegationEventSource,
  placed: PlacedSpan,
  callsById: Map<string, PlacedCall>,
  suppressed: Set<string>,
): DelegationEvent | null {
  const runCall = callsById.get(placed.span.parentCallId)?.call
  if (queuedNeverRan(placed.span, runLaunchState(runCall))) return null
  const childSessionId = childSessionOf(placed.span, runCall)
  const terminal = terminalKind(placed.span, suppressed)
  if (!terminal) return null
  const title = terminal === 'finished' ? placed.span.taskLabel.trim() || undefined : undefined
  return withDefined({
    ...baseEvent(source, placed, lifecycleId(terminal, placed.span), terminal),
    agentName: displayName(agentIdFor(placed.span, runCall), source.agentNames),
    title,
    childSessionId,
  })
}

function spanEventsForMessage(
  source: DelegationEventSource,
  spans: PlacedSpan[],
  callsById: Map<string, PlacedCall>,
  suppressed: Set<string>,
  birthEmitted: Set<string>,
  terminalEmitted: Set<string>,
): DelegationEvent[] {
  const events: DelegationEvent[] = []
  for (const placed of spans) {
    const runCall = callsById.get(placed.span.parentCallId)?.call
    const birth = maybeEmitBirth(source, placed, runCall, placed, birthEmitted)
    if (birth) events.push(birth)
    if (terminalEmitted.has(placed.span.spanId)) continue
    const terminal = spanTerminalEvent(source, placed, callsById, suppressed)
    if (terminal) events.push(terminal)
  }
  return events
}

/**
 * follow_up's result is prose, not JSON
 * (pkg/tools/delegate_followup.go::spawnCorrectiveFollowUp). A 3P corrective
 * session mints a new id that is only in that sentence.
 */
function followUpSessionId(result: unknown): string | undefined {
  const match = /dispatched for session (\S+) at generation \d+/.exec(resultText(result))
  if (!match?.[1]) return undefined
  return match[1].replace(/[),.:;]+$/, '')
}

/** Same 60-rune cap pkg/gateway/replay.go::resolveTaskLabel uses for a task-text title. An explicit label is kept whole. */
const TASK_LABEL_RUNES = 60

function truncateRunes(value: string, max: number): string {
  const runes = Array.from(value)
  return runes.length <= max ? value : runes.slice(0, max).join('')
}

function followUpTitle(call: ToolCall): string | undefined {
  const label = stringParam(call, 'label')
  if (label) return label
  // "text" is the documented field; "task" is the deprecated alias (delegate.go::Parameters).
  const text = stringParam(call, 'text')
  if (text) return truncateRunes(text, TASK_LABEL_RUNES)
  const task = stringParam(call, 'task')
  if (task) return truncateRunes(task, TASK_LABEL_RUNES)
  return undefined
}

function parentAction(
  source: DelegationEventSource,
  kind: 'steered' | 'answered' | 'cancelled' | 'follow_up',
  placed: PlacedCall,
  spansByChild: Map<string, PlacedSpan>,
  callsById: Map<string, PlacedCall>,
): DelegationEvent {
  const namedSession = stringParam(placed.call, 'session_id')
  const fromProse = kind === 'follow_up' ? followUpSessionId(placed.call.result) : undefined
  const childSessionId = fromProse ?? (kind === 'follow_up' ? runSessionId(placed.call) : undefined) ?? namedSession
  const span =
    (childSessionId ? spansByChild.get(childSessionId) : undefined) ??
    (namedSession ? spansByChild.get(namedSession) : undefined)
  const runCall = span ? callsById.get(span.span.parentCallId)?.call : undefined
  const title = kind === 'follow_up' ? followUpTitle(placed.call) : undefined
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
  if (!span) return []
  const birth = maybeEmitBirth(source, span, placed.call, placed, birthEmitted)
  return birth ? [birth] : []
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
function primarySpans(spans: PlacedSpan[]): Map<string, PlacedSpan> {
  const byParent = new Map<string, PlacedSpan>()
  for (const placed of spans) {
    if (isFollowUpGeneration(placed.span.spanId)) continue
    if (!byParent.has(placed.span.parentCallId)) byParent.set(placed.span.parentCallId, placed)
  }
  return byParent
}

function spansByMessageIndex(spans: PlacedSpan[]): Map<number, PlacedSpan[]> {
  const byMessage = new Map<number, PlacedSpan[]>()
  for (const placed of spans) {
    const list = byMessage.get(placed.messageIndex)
    if (list) list.push(placed)
    else byMessage.set(placed.messageIndex, [placed])
  }
  return byMessage
}

function messageInterleavesFollowUp(group: PlacedCall[], onMessage: PlacedSpan[]): boolean {
  if (onMessage.some((placed) => isFollowUpGeneration(placed.span.spanId))) return true
  return group.some((placed) => delegateAction(placed.call) === 'follow_up' && placed.call.status === 'success')
}

export function deriveDelegationEvents(source: DelegationEventSource): DelegationEvent[] {
  if (!source.sessionId) return []
  const groups = placeCalls(source)
  const spans = placeSpans(source)
  const callsById = indexById(groups)
  const suppressed = suppressedSpanIds(spans, groups, callsById)
  const bashByCall = bashIndex(bashStories(groups))
  const spansByParent = primarySpans(spans)
  const spansByChild = new Map<string, PlacedSpan>()
  for (const span of spans) {
    if (span.span.childSessionId) spansByChild.set(span.span.childSessionId, span)
  }
  const byMessage = spansByMessageIndex(spans)
  const birthEmitted = new Set<string>()
  const terminalEmitted = new Set<string>()
  const drafts: Draft[] = []

  groups.forEach((group, messageIndex) => {
    let seq = 0
    const onMessage = byMessage.get(messageIndex) ?? []
    const interleave = messageInterleavesFollowUp(group, onMessage)
    const push = (event: DelegationEvent) => {
      pushDraft(drafts, event, messageIndex, seq)
      seq += 1
    }
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
        push(event)
      }
      if (!interleave) continue
      const action = delegateAction(placed.call)
      const terminalSpan = terminalSpanForCall(action, placed, onMessage, terminalEmitted)
      if (!terminalSpan) continue
      const terminal = spanTerminalEvent(source, terminalSpan, callsById, suppressed)
      if (!terminal) continue
      terminalEmitted.add(terminalSpan.span.spanId)
      push(terminal)
    }
    for (const event of spanEventsForMessage(source, onMessage, callsById, suppressed, birthEmitted, terminalEmitted)) {
      push(event)
    }
  })

  return stampAndDedupe(drafts, source.messages)
}

function terminalSpanForCall(
  action: string | null,
  placed: PlacedCall,
  onMessage: PlacedSpan[],
  terminalEmitted: Set<string>,
): PlacedSpan | undefined {
  if (action === 'run') {
    return onMessage.find(
      (span) => span.span.parentCallId === placed.call.id && !isFollowUpGeneration(span.span.spanId),
    )
  }
  if (action !== 'follow_up' || placed.call.status !== 'success') return undefined
  return onMessage
    .filter((span) => isFollowUpGeneration(span.span.spanId) && !terminalEmitted.has(span.span.spanId))
    .sort((a, b) => generationNumber(a.span.spanId) - generationNumber(b.span.spanId))[0]
}

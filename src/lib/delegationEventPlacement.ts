/**
 * Where a delegation line sits in the thread.
 *
 * A line whose event id names a tool call that has a known place in the
 * answer (a baked `textOffset`, or a live text snapshot) is drawn at that
 * call, after the text that preceded it. The finish line uses the same call
 * as the line that opened the delegation, so the two stay together.
 * Anything we cannot place keeps the older rule: after the anchor message,
 * or at the end of the thread when the anchor is missing.
 *
 * The event type has no call-id field. The id the derivation already stamps
 * is the join: `delegated|started|finished|stopped:<spanId>`,
 * `steered|answered|cancelled|follow_up|refused:<callId>`,
 * `bash_*:<bash session id or call id>`. A span id resolves through the
 * message's own span (`parentCallId`).
 */
import type { DelegationEvent, DelegationEventKind } from './delegationEvents.types'
import { parseLeadingJson } from './delegationEvents'

function byTime(a: DelegationEvent, b: DelegationEvent): number {
  // `at` is anchor-message time plus sequence, not wall-clock event time — order only, never render it.
  if (a.at !== b.at) return a.at - b.at
  return a.id < b.id ? -1 : a.id > b.id ? 1 : 0
}

export function delegationEventsAfterMessage(
  events: readonly DelegationEvent[],
  messageId: string,
): DelegationEvent[] {
  return events.filter((event) => event.anchorMessageId === messageId).sort(byTime)
}

export function delegationEventsAtEnd(
  events: readonly DelegationEvent[],
  messageIds: ReadonlySet<string>,
): DelegationEvent[] {
  return events
    .filter((event) => !event.anchorMessageId || !messageIds.has(event.anchorMessageId))
    .sort(byTime)
}

/** The fields placement reads off a chat message. Extra store fields are ignored. */
export interface DelegationPlacementMessage { // not-wire-format: render-only view of spans and tool calls already on the chat message; never serialised
  spans?: readonly { spanId: string; parentCallId: string }[]
  toolCalls?: readonly DelegationPlacementCall[]
}

export interface DelegationPlacementCall { // not-wire-format: render-only view of one baked or live tool call; never serialised
  id: string
  tool?: string
  params?: Record<string, unknown>
  result?: unknown
  textOffset?: number
}

const SPAN_KINDS = new Set<DelegationEventKind>(['delegated', 'started', 'finished', 'stopped'])
const CALL_KINDS = new Set<DelegationEventKind>(['steered', 'answered', 'cancelled', 'follow_up', 'refused'])
const BASH_KINDS = new Set<DelegationEventKind>(['bash_launched', 'bash_finished', 'bash_failed', 'bash_stopped'])

export interface AnchoredDelegationSplit { // not-wire-format: render placement result; never serialised
  /** Call id → lines that belong in that call's slot, earliest first. */
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>
  /** Lines for this message that stay after the bubble. */
  trailing: DelegationEvent[]
}

function eventToken(event: DelegationEvent): string | undefined {
  const prefix = `${event.kind}:`
  if (!event.id.startsWith(prefix)) return undefined
  const token = event.id.slice(prefix.length)
  return token.length > 0 ? token : undefined
}

function bashSessionId(result: unknown): string | undefined {
  const json = parseLeadingJson(result)
  if (!json) return undefined
  if (typeof json.sessionId === 'string' && json.sessionId.trim() !== '') return json.sessionId
  const sentinel = json._truncated === true || json._truncated_client === true || json._ref === true
  if (!sentinel || typeof json.preview !== 'string') return undefined
  return /"sessionId"\s*:\s*"([^"\\]+)"/.exec(json.preview)?.[1]
}

function bashCallId(token: string, calls: readonly DelegationPlacementCall[]): string | undefined {
  const matches = calls.filter(
    (call) => call.tool === 'bash' && (call.id === token || bashSessionId(call.result) === token),
  )
  const launch = matches.find((call) => call.params?.run_in_background === true)
  return (launch ?? matches[0])?.id
}

function callIdForEvent(event: DelegationEvent, message: DelegationPlacementMessage): string | undefined {
  const token = eventToken(event)
  if (!token) return undefined
  if (SPAN_KINDS.has(event.kind)) {
    return message.spans?.find((span) => span.spanId === token)?.parentCallId
  }
  if (CALL_KINDS.has(event.kind)) return token
  if (BASH_KINDS.has(event.kind)) return bashCallId(token, message.toolCalls ?? [])
  return undefined
}

/**
 * Live tool calls sit in the streaming bubble before they are baked with a
 * `textOffset`. A snapshot keyed to this message is a known position.
 * An owner recorded for a different message is not.
 */
export function liveSnapshotIds(
  messageId: string,
  snapshots: Readonly<Record<string, string>> | undefined,
  owners?: Readonly<Record<string, string>>,
): Set<string> {
  const ids = new Set<string>()
  if (!snapshots) return ids
  for (const id of Object.keys(snapshots)) {
    const owner = owners?.[id]
    if (owner === undefined || owner === messageId) ids.add(id)
  }
  return ids
}

export function splitAnchoredDelegationEvents(
  events: readonly DelegationEvent[],
  messageId: string,
  message: DelegationPlacementMessage,
  livePositionedIds?: ReadonlySet<string>,
): AnchoredDelegationSplit {
  const positioned = new Set<string>(livePositionedIds)
  for (const call of message.toolCalls ?? []) {
    if (typeof call.textOffset === 'number') positioned.add(call.id)
  }
  const byCall = new Map<string, DelegationEvent[]>()
  const trailing: DelegationEvent[] = []
  for (const event of delegationEventsAfterMessage(events, messageId)) {
    const callId = callIdForEvent(event, message)
    if (!callId || !positioned.has(callId)) {
      trailing.push(event)
      continue
    }
    const group = byCall.get(callId)
    if (group) group.push(event)
    else byCall.set(callId, [event])
  }
  return { byCall, trailing }
}

/**
 * A call can count as placed (a live snapshot) and still never be drawn,
 * because the streaming bubble has no tool-call part for it. Those lines
 * go back after the bubble instead of disappearing. `claimed` is the set
 * of call ids a tool group actually rendered.
 */
export function appendUnclaimedCalls(
  trailing: readonly DelegationEvent[],
  byCall: ReadonlyMap<string, readonly DelegationEvent[]>,
  claimed: ReadonlySet<string>,
): DelegationEvent[] {
  const extra: DelegationEvent[] = []
  for (const [callId, events] of byCall) {
    if (!claimed.has(callId)) extra.push(...events)
  }
  if (extra.length === 0) return [...trailing]
  return [...trailing, ...extra].sort(byTime)
}

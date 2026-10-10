/**
 * The only reader of session-core main / needs_attention / ack fields.
 *
 * The generated wire fields exist (contract regenerated) and are handled here
 * and nowhere else: Session.type = "main", Session.needs_attention,
 * WorkspaceMemberConfig.main_session_id, SessionStateFrame.attention_bound
 * (the server frame the bound is read from), and the acknowledging
 * AttachSessionFrame's ack_attention/attention_bound, which attachAckFields
 * produces. This file does not parse raw JSON and
 * does not declare a look-alike wire type. A missing or unusable value is
 * unavailable (undefined / false / 'unknown' / an empty object), never a
 * guessed main id, never a fabricated off, and never an acknowledgement:
 * callers must still show unavailable / Retry for unavailable reads. When the
 * contract changes again, only this module changes; every other nav module
 * keeps calling these functions.
 */
import type { Session as WireSession, WorkspaceMemberConfig } from '@/lib/api/generated/openapi-types'
import type { SessionStateFrame } from '@/lib/api/generated/asyncapi-types'

/** Validated main session id for a workspace member, or unavailable. */
export function mainSessionIdOfMember(member: unknown): string | undefined {
  const id = (member as WorkspaceMemberConfig | null | undefined)?.main_session_id
  if (typeof id !== 'string') return undefined
  if (id.trim() === '') return undefined
  return id
}

/** True only when the session is a validated main. Unavailable reads are false. */
export function isMainSession(session: unknown): boolean {
  return (session as WireSession | null | undefined)?.type === 'main'
}

/**
 * Attention on a main: on, off, or unknown when the value or its source
 * is missing. Unavailable is unknown, never a fabricated off.
 */
export function sessionAttention(session: unknown): 'on' | 'off' | 'unknown' {
  const value = (session as WireSession | null | undefined)?.needs_attention
  if (value === true) return 'on'
  if (value === false) return 'off'
  return 'unknown'
}

/**
 * Integer attention bound on a server frame — the attach answer's
 * SessionStateFrame, which carries the bound for both the incremental
 * catch-up and the full snapshot — or unavailable. Only a true integer is a
 * bound; a string — even a numeric one — and a missing value are
 * unavailable. Never a client-invented bound, and never read off the
 * client-sent AttachSessionFrame.
 */
export function attentionBoundOfFrame(frame: unknown): number | undefined {
  const bound = (frame as SessionStateFrame | null | undefined)?.attention_bound
  return Number.isInteger(bound) ? bound : undefined
}

/**
 * Fields to add to an attach_session for a shown-commit acknowledgement.
 * Unavailable means attach nothing — an empty object, not a guessed ack.
 * The bound argument is the integer from attentionBoundOfFrame, never a
 * client-invented string.
 */
export function attachAckFields(bound: unknown): Record<string, unknown> {
  if (!Number.isInteger(bound)) return {}
  return { ack_attention: true, attention_bound: bound }
}

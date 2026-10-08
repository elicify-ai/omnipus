/**
 * The only reader of session-core main / needs_attention / ack fields.
 *
 * Production default is unavailable until the generated wire fields exist.
 * Awaited generated names, read only here once regeneration adds them:
 * Session.type = "main", Session.needs_attention,
 * WorkspaceMemberConfig.main_session_id,
 * AttachSessionFrame.ack_attention and AttachSessionFrame.attention_bound.
 * Callers must show unavailable / Retry, never a guessed main id and never
 * an acknowledgement. This file does not parse raw JSON and does not declare
 * a look-alike wire type. When the contract is regenerated, only this module
 * changes; every other nav module keeps calling these functions.
 */

/** Validated main session id for a workspace member, or unavailable. */
export function mainSessionIdOfMember(_member: unknown): string | undefined {
  void _member
  return undefined
}

/** True only when the session is a validated main. Unavailable reads are false. */
export function isMainSession(_session: unknown): boolean {
  void _session
  return false
}

/**
 * Attention on a main: on, off, or unknown when the value or its source
 * is missing. Unavailable is unknown, never a fabricated off.
 */
export function sessionAttention(_session: unknown): 'on' | 'off' | 'unknown' {
  void _session
  return 'unknown'
}

/**
 * Integer attention bound on a server snapshot or attach frame, or unavailable.
 * Production does not read the frame: there is no generated integer field yet,
 * so every frame is unavailable. A string is never a bound.
 */
export function attentionBoundOfFrame(_frame: unknown): number | undefined {
  void _frame
  return undefined
}

/**
 * Fields to add to an attach_session for a shown-commit acknowledgement.
 * Unavailable means attach nothing — an empty object, not a guessed ack.
 * The bound argument is the integer from attentionBoundOfFrame, never a
 * client-invented string.
 */
export function attachAckFields(_bound: unknown): Record<string, unknown> {
  void _bound
  return {}
}

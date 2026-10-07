/**
 * The only reader of session-core main / needs_attention / ack fields.
 *
 * Production default is unavailable until the generated wire fields exist.
 * Callers must show unavailable / Retry, never a guessed main id and never
 * an acknowledgement. This file does not parse raw JSON and does not declare
 * a look-alike wire type. When the contract is regenerated, only this module
 * changes; every other nav module keeps calling these four functions.
 */

/** Validated main session id for a workspace member, or unavailable. */
export function mainSessionIdOfMember(_member: unknown): string | undefined {
  return undefined
}

/** True only when the session is a validated main. Unavailable reads are false. */
export function isMainSession(_session: unknown): boolean {
  return false
}

/**
 * Attention on a main: on, off, or unknown when the value or its source
 * is missing. Unavailable is unknown, never a fabricated off.
 */
export function sessionAttention(_session: unknown): 'on' | 'off' | 'unknown' {
  return 'unknown'
}

/**
 * Fields to add to an attach_session for a shown-commit acknowledgement.
 * Unavailable means attach nothing — an empty object, not a guessed ack.
 */
export function attachAckFields(_bound: unknown): Record<string, unknown> {
  return {}
}

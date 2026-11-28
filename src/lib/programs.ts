// programs.ts — browser-side rules and wording for connected programs
// (spec cli-credentials S1). The gateway is the authority for every rule
// here; the browser checks the token rules first only so the person gets the
// same message without a round trip (FR-003, Machine-Verifiable Constraints).

import { isApiError, parseServerErrorField } from '@/lib/api-error'
import type { ProgramConnection, ProgramEntry } from '@/lib/api/generated/openapi-types'

export const PROGRAM_TOKEN_MIN_BYTES = 8
export const PROGRAM_TOKEN_MAX_BYTES = 16_384

/**
 * The promise on the secure card and the Settings paste dialog (FR-003,
 * FR-025: the card says the token is shared).
 */
export const PROGRAM_SECRET_PROMISE =
  'The token never appears in the chat and is hidden in command output. Every agent you switch this program on for can use it in its commands.'

/** The note in the disconnect dialog (US-7 AS-3, S-53). */
export function disconnectNote(program: string): string {
  return `This removes the token from Omnipus. The token stays valid at ${program} until you revoke it there.`
}

/** The text a program card shows when the person's own API token, not the browser, submitted it (US-8 AS-5). */
export const PROGRAM_CARD_BROWSER_ONLY = 'Open Omnipus in your browser to enter this token.'

/** What a lost save answer means (UI table, paste dialog). */
export const PROGRAM_SAVE_UNKNOWN = 'The result of saving is unknown. The list shows the current state.'

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

/** Surrounding whitespace is not part of a token (D-1 row 6). */
export function normalizeProgramToken(value: string): string {
  return value.trim()
}

/**
 * The token value rules (D-1). Returns the gateway's own message for the
 * first rule the value breaks, or null. Takes the value as typed; the rules
 * apply to the trimmed value.
 */
export function programTokenError(raw: string): string | null {
  const value = normalizeProgramToken(raw)
  if (value.length === 0) return 'Enter a token.'
  if (value.includes('\u0000')) return 'The token contains a character that cannot be stored.'
  if (/[\r\n]/.test(value)) return 'The token contains a line break. Paste the token on one line.'
  const bytes = byteLength(value)
  if (bytes < PROGRAM_TOKEN_MIN_BYTES) return 'The token is too short. A token has at least 8 characters.'
  if (bytes > PROGRAM_TOKEN_MAX_BYTES) return 'The token is too long. A token has at most 16,384 bytes.'
  return null
}

/** The row state badge text (UI table, "Row state badge"). */
export function connectionBadgeText(connection: ProgramConnection): string {
  switch (connection.state) {
    case 'not_connected':
      return 'Not connected'
    case 'checking':
      return 'Checking the account…'
    case 'connected':
      return connection.account_label ? `Connected as ${connection.account_label}` : 'Connected'
    case 'saved_not_checked':
      return 'Saved, not checked'
    case 'check_failed':
      return 'Check failed'
  }
}

/** True when the entry holds a saved token, whatever its check state. */
export function hasCredential(entry: ProgramEntry): boolean {
  return entry.connection.state !== 'not_connected'
}

/** True while any entry's account check is running, so the list keeps reading. */
export function anyChecking(entries: readonly ProgramEntry[] | undefined): boolean {
  return (entries ?? []).some((entry) => entry.connection.state === 'checking')
}

/**
 * The gateway's own value-free refusal text (the Programs routes answer with
 * the spec's exact sentences), else the client's generic message.
 */
export function programErrorMessage(error: unknown, fallback: string): string {
  if (isApiError(error)) return parseServerErrorField(error.body) ?? error.userMessage
  if (error instanceof Error && error.message) return error.message
  return fallback
}

/** A save whose answer was lost: transport failure, timeout or a gateway hop. 503 is the store's definite "busy". */
export function isLostAnswer(error: unknown): boolean {
  if (isApiError(error)) return [0, 408, 502, 504].includes(error.status)
  return error instanceof TypeError
}

/** "gh, git" — a comma list typed into the custom-program form, split and trimmed. */
export function splitList(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((part) => part.trim())
    .filter((part) => part.length > 0)
}

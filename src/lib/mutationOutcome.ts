/**
 * Was a failed write refused, or is its result unknown?
 *
 * omnipus-system-agent spec §6.3 asks every changed surface to show a
 * "not confirmed" state (with Retry) when a write's outcome is uncertain,
 * distinct from a clear refusal. A write is UNCERTAIN when the request may
 * have reached the server and been applied without the browser learning so:
 * a transport failure (ApiError status 0), a timeout (408), a gateway
 * failure (502/503/504), or a configuration write that reports itself only
 * partly saved. Everything else the server answered (4xx, 500 with "nothing
 * saved") is a REFUSAL whose message is shown as is.
 */
import { isApiError } from '@/lib/api-error'
import { ConfigurationSaveError } from '@/lib/api/configuration'

export type MutationFailureKind = 'uncertain' | 'refused' // not-wire-format: in-browser classification of a failed write

const UNCERTAIN_STATUSES = new Set([0, 408, 502, 503, 504])

export function classifyMutationFailure(error: unknown): MutationFailureKind {
  if (error instanceof ConfigurationSaveError) {
    return error.state.persistence_status === 'partial' ? 'uncertain' : 'refused'
  }
  if (isApiError(error)) return UNCERTAIN_STATUSES.has(error.status) ? 'uncertain' : 'refused'
  // fetch() rejects with a TypeError when the request never got an answer.
  if (error instanceof TypeError) return 'uncertain'
  return 'refused'
}

/** A safe, human message for a refused write. */
export function mutationFailureMessage(error: unknown): string {
  if (isApiError(error)) return error.userMessage
  if (error instanceof Error && error.message) return error.message
  return 'The change was refused.'
}

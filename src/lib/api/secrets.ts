// secrets.ts: the secure secret card's REST half and stored secret names
// (omnipus-system-agent K5, K6, K49; FR-062, FR-063, FR-073, FR-079, FR-241).
//
// Values travel ONLY in submitSecretRequest's body, straight from the card's
// isolated inputs to the authenticated submit; nothing here logs, stores or
// echoes a value. Every response is value-free and validated against the
// generated schema (Hard Constraint #8).

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  SecretNameEntry as SecretNameEntrySchema,
  SecretRequestState as SecretRequestStateSchema,
} from '@/lib/api/generated/schemas'
import type {
  SecretNameEntry,
  SecretOptionsUpdateRequest,
  SecretRequestState,
  SecretSubmitRequest,
} from '@/lib/api/generated/openapi-types'
import { isApiError } from '../api-error'
import { request } from './http'

/**
 * The outcome of a submit: the server's resulting state, or — on 409 — the
 * request's CURRENT state (already submitted, cancelled, stopped…), which the
 * server sends as the 409 body. Any other failure is thrown.
 */
export type SecretSubmitOutcome = // not-wire-format: in-browser union over two generated SecretRequestState responses
  | { kind: 'accepted'; state: SecretRequestState }
  | { kind: 'not-pending'; state: SecretRequestState }

function stateFrom409Body(body: string | undefined): SecretRequestState | null {
  if (!body) return null
  try {
    const parsed = SecretRequestStateSchema.safeParse(JSON.parse(body))
    return parsed.success ? (parsed.data as SecretRequestState) : null
  } catch {
    return null
  }
}

export async function submitSecretRequest(requestId: string, body: SecretSubmitRequest): Promise<SecretSubmitOutcome> {
  try {
    const state = await request<SecretRequestState>(
      `/secret-requests/${encodeURIComponent(requestId)}/submit`,
      { method: 'POST', body: JSON.stringify(body) },
      SecretRequestStateSchema as ZodType<SecretRequestState>,
    )
    return { kind: 'accepted', state }
  } catch (error) {
    if (isApiError(error) && error.status === 409) {
      const state = stateFrom409Body(error.body)
      if (state) return { kind: 'not-pending', state }
    }
    throw error
  }
}

export function getSecretRequest(requestId: string): Promise<SecretRequestState> {
  return request<SecretRequestState>(
    `/secret-requests/${encodeURIComponent(requestId)}`,
    undefined,
    SecretRequestStateSchema as ZodType<SecretRequestState>,
  )
}

export function listSecrets(): Promise<SecretNameEntry[]> {
  return request<SecretNameEntry[]>('/secrets', undefined, z.array(SecretNameEntrySchema) as ZodType<SecretNameEntry[]>)
}

export function updateSecretOptions(reference: string, body: SecretOptionsUpdateRequest): Promise<SecretNameEntry> {
  return request<SecretNameEntry>(
    `/secrets/${encodeURIComponent(reference)}/options`,
    { method: 'PATCH', body: JSON.stringify(body) },
    SecretNameEntrySchema as ZodType<SecretNameEntry>,
  )
}

export const secretsQueryKeys = {
  list: () => ['secrets'] as const,
}

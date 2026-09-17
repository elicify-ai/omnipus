import type { ZodType } from 'zod'
import type { ConfigurationMutationState } from './generated/openapi-types'
import { ConfigurationMutationState as MutationStateSchema } from './generated/schemas'
import { isApiError } from '../api-error'
import { ApiSchemaError, request } from './http'

export class ConfigurationSaveError extends Error {
  readonly state: ConfigurationMutationState

  constructor(state: ConfigurationMutationState, cause?: unknown) {
    const message = state.persistence_status === 'complete'
      ? 'Changes were saved but are not active. Reload before making further changes.'
      : state.persistence_status === 'partial'
        ? 'Some changes were saved, but configuration is incomplete. Reload before making further changes.'
        : 'Changes were not saved. Reload before trying again.'
    super(message, { cause })
    this.name = 'ConfigurationSaveError'
    this.state = state
  }
}

function parseMutationState(value: unknown) {
  // Resource responses also carry configuration fields. Validate only the
  // generated state envelope, leaving resource validation to its own schema.
  const body = value !== null && typeof value === 'object' ? value as Record<string, unknown> : {}
  const { revision, persistence_status, activation_status, changed_fields, error_stage, message } = body
  return MutationStateSchema.safeParse({ revision, persistence_status, activation_status, changed_fields, error_stage, message })
}

/** A successful HTTP save is complete only when the new configuration is active. */
export async function requestConfiguration<T>(path: string, init: RequestInit, schema: ZodType<T>): Promise<T> {
  let result: T
  try {
    result = await request<T>(path, init, schema)
  } catch (error) {
    if (isApiError(error) && error.status === 500 && error.body) {
      let body: unknown
      try { body = JSON.parse(error.body) } catch { throw error }
      const parsed = parseMutationState(body)
      if (parsed.success) throw new ConfigurationSaveError(parsed.data, error)
    }
    throw error
  }
  const parsed = parseMutationState(result)
  if (!parsed.success) {
    throw new ApiSchemaError(path, parsed.error.issues.map(issue => ({ path: issue.path, message: issue.message })), result)
  }
  if (parsed.data.persistence_status !== 'complete' || parsed.data.activation_status !== 'active') {
    throw new ConfigurationSaveError(parsed.data)
  }
  return result
}

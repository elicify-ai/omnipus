// mutationOutcome.test.ts — spec §6.3 "Mutation uncertain" vs a refusal.
import { describe, it, expect } from 'vitest'
import { ApiError } from '@/lib/api-error'
import { ConfigurationSaveError } from '@/lib/api/configuration'
import { classifyMutationFailure } from './mutationOutcome'

describe('classifyMutationFailure', () => {
  it.each([0, 408, 502, 503, 504])('status %i is uncertain', (status) => {
    expect(classifyMutationFailure(new ApiError(status))).toBe('uncertain')
  })
  it.each([400, 403, 409, 422, 500])('status %i is a refusal', (status) => {
    expect(classifyMutationFailure(new ApiError(status))).toBe('refused')
  })
  it('a fetch TypeError is uncertain', () => {
    expect(classifyMutationFailure(new TypeError('Failed to fetch'))).toBe('uncertain')
  })
  it('a partly saved configuration is uncertain; nothing saved is a refusal', () => {
    const base = { revision: 'r', activation_status: 'not_applied', changed_fields: [] }
    expect(classifyMutationFailure(new ConfigurationSaveError({ ...base, persistence_status: 'partial' } as never))).toBe('uncertain')
    expect(classifyMutationFailure(new ConfigurationSaveError({ ...base, persistence_status: 'none' } as never))).toBe('refused')
  })
})

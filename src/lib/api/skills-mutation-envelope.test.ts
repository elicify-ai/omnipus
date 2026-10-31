// RED reconciliation — r15 §5.8, C-4/C-5/C-6/C-9, S-11.19.
// Real adapters + generated schemas; only the network edge is replaced.
// A mocked adapter or a safeParse-only counter assertion proves nothing.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Skill } from './generated/openapi-types'
import { ConfigurationMutationState, ConfigurationMutationFailureState } from './generated/schemas'
import { ApiSchemaError, resetApiSchemaErrorCount, getApiSchemaErrorCount } from './http'
import { ConfigurationSaveError } from './configuration'
import * as skills from './skills'

const revision = 'fdd9b7afa7635d0f8028957f85c713170c8083e2aebf37ee45677f2e05881eda'
const state = { revision, activation_status: 'active', persistence_status: 'complete', changed_fields: ['skills'], message: 'saved' }
const failure = { revision, activation_status: 'not_attempted', persistence_status: 'none', changed_fields: [], error_stage: 'manifest_write', message: 'not saved' }
const copied: Skill = { id: 'plan-copy', name: 'plan-copy', description: 'own copy', source: 'global', status: 'active', verified: false, version: '0.0.0', revision }

beforeEach(() => {
  document.cookie = 'csrf=skills-test-token; path=/'
  resetApiSchemaErrorCount()
})
afterEach(() => {
  document.cookie = 'csrf=; Max-Age=0; path=/'
  vi.unstubAllGlobals()
  resetApiSchemaErrorCount()
})

function reply(body: unknown, status = 200) {
  const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(JSON.stringify(body), {
    status, headers: { 'Content-Type': 'application/json' },
  }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

async function copyAdapter() {
  const candidate = (skills as Record<string, unknown>).copySkill
  expect(candidate, 'BLOCKED: copySkill request adapter required by r15 C-4 is not implemented').toBeTypeOf('function')
  return candidate as (id: string, newId: string) => Promise<Skill>
}

describe('skills mutation envelopes and real request paths', () => {
  it('a valid ConfigurationMutationState delete envelope parses (C-6)', () => {
    expect(ConfigurationMutationState.parse(state)).toEqual(state)
  })
  it('a valid ConfigurationMutationFailureState 500 body parses (C-9)', () => {
    expect(ConfigurationMutationFailureState.parse(failure)).toEqual(failure)
  })
  it('message-only success is rejected by the real delete adapter and counted exactly once', async () => {
    const network = reply({ message: 'saved' })
    await expect(skills.deleteSkill('x', revision)).rejects.toBeInstanceOf(ApiSchemaError)
    expect(network).toHaveBeenCalledTimes(1)
    expect(getApiSchemaErrorCount()).toBe(1)
  })
  it('copySkill sends the exact copy request and returns the validated Skill', async () => {
    const network = reply({ ...copied, ...state }, 201)
    const copy = await copyAdapter()
    const result = await copy('plan', 'plan-copy')
    expect(network).toHaveBeenCalledTimes(1)
    const [url, init] = network.mock.calls[0]
    expect(String(url)).toBe('/api/v1/skills/plan/copy')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({ new_id: 'plan-copy' })
    expect(result).toMatchObject(copied)
    expect(getApiSchemaErrorCount()).toBe(0)
  })
  it('copySkill rejects a fabricated message-only 201 body', async () => {
    const network = reply({ message: 'copied' }, 201)
    const copy = await copyAdapter()
    await expect(copy('plan', 'plan-copy')).rejects.toBeInstanceOf(ApiSchemaError)
    expect(network).toHaveBeenCalledTimes(1)
    expect(getApiSchemaErrorCount()).toBe(1)
  })
  it('installSkillBySlug forwards replace_local_changes and returns actual response data (S-9.3)', async () => {
    const installed = { ...copied, id: 'x', name: 'x' }
    const network = reply({ ...installed, ...state })
    // Test the new optional argument without a compile-time dependency on a
    // missing production signature. This is a function boundary, not a wire type.
    const install = skills.installSkillBySlug as (id: string, version?: string, rev?: string, replace?: boolean) => Promise<Skill>
    const result = await install('x', undefined, revision, true)
    expect(network).toHaveBeenCalledTimes(1)
    const [url, init] = network.mock.calls[0]
    expect(String(url)).toBe('/api/v1/skills/install')
    expect(init?.method).toBe('POST')
    expect(JSON.parse(String(init?.body))).toEqual({ slug: 'x', revision, replace_local_changes: true })
    expect(result).toMatchObject(installed)
  })
  it('valid delete success reaches the request and returns the exact envelope (positive control)', async () => {
    const network = reply(state)
    expect(await skills.deleteSkill('x', revision)).toEqual(state)
    expect(network).toHaveBeenCalledTimes(1)
    expect(String(network.mock.calls[0][0])).toBe(`/api/v1/skills/x?revision=${revision}`)
    expect(network.mock.calls[0][1]?.method).toBe('DELETE')
    expect(getApiSchemaErrorCount()).toBe(0)
  })
  it('the real delete adapter preserves the declared 500 failure state', async () => {
    const network = reply(failure, 500)
    let thrown: unknown
    try { await skills.deleteSkill('x', revision) } catch (error) { thrown = error }
    expect(network).toHaveBeenCalledTimes(1)
    expect(thrown).toBeInstanceOf(ConfigurationSaveError)
    expect((thrown as ConfigurationSaveError).state).toEqual(failure)
  })
})

// e2e-agent-model.test.ts — design.md §1 row 9 and §7 R10.
// The helper module does not exist yet. The expected objects are the central
// pair from design.md §4.1. api-contract.md §6 names the function.

import { describe, expect, it } from 'vitest'

type Fields = { provider: string; model: string; fallback_models?: { provider: string; model: string }[] }

const LIVE: Fields = {
  provider: 'minimax-coding-plan',
  model: 'MiniMax-M3.1-Flash',
  fallback_models: [{ provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash' }],
}

const MOCK: Fields = {
  provider: 'openrouter',
  model: 'deepseek/deepseek-v4.1-flash',
}

// import.meta.glob matches files that exist at transform time. A direct
// import() of a module that is not written yet fails the suite before any
// assertion runs, which is not the missing behaviour. An empty map is.
const helperLoaders = import.meta.glob('./e2e-agent-model.ts') as Record<
  string,
  () => Promise<{ e2eAgentModelFields?: (kind: 'live' | 'mock') => Fields }>
>

async function loadHelper(): Promise<(kind: 'live' | 'mock') => Fields> {
  const load = helperLoaders['./e2e-agent-model.ts']
  if (load === undefined) {
    throw new Error(
      'BLOCKED: tests/e2e/fixtures/e2e-agent-model.ts::e2eAgentModelFields not implemented — required by design.md §7 R10',
    )
  }
  const mod = await load()
  if (typeof mod.e2eAgentModelFields !== 'function') {
    throw new Error(
      'BLOCKED: tests/e2e/fixtures/e2e-agent-model.ts::e2eAgentModelFields not implemented — required by design.md §7 R10',
    )
  }
  return mod.e2eAgentModelFields
}

describe('e2eAgentModelFields (design.md §7 R10)', () => {
  it('adds fallback_models for a live pair', async () => {
    const fields = await loadHelper()
    expect(fields('live')).toEqual(LIVE)
    expect(JSON.stringify(fields('live'))).not.toContain('test-key-')
    expect(JSON.stringify(fields('live'))).not.toContain('API_KEY')
  })

  it('uses the fallback pair for a mock gateway and does not add fallback_models', async () => {
    const fields = await loadHelper()
    const got = fields('mock')
    expect(got).toEqual(MOCK)
    expect(Object.prototype.hasOwnProperty.call(got, 'fallback_models')).toBe(false)
  })

  it('rejects an unknown kind and names the two legal kinds', async () => {
    const fields = await loadHelper()
    expect(() => fields('other' as 'live')).toThrow(/live[\s\S]*mock|mock[\s\S]*live/)
  })
})

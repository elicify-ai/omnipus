// e2e-model.test.ts — design.md §4.2 and §7 R8.
// Expected values are the central-file shape in design.md §4.1, not the
// current {"model":...} document and not whatever the module exports today.
// The pure functions are specified in /workspace/ci-minimax/api-contract.md §6.

import { describe, expect, it } from 'vitest'

const SPEC = `{
  "primary": { "provider": "minimax-coding-plan", "model": "MiniMax-M3.1-Flash-Preview", "api_key_ref": "MINIMAX_API_KEY" },
  "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
}`

const PARSED = {
  primary: { provider: 'minimax-coding-plan', model: 'MiniMax-M3.1-Flash-Preview', apiKeyRef: 'MINIMAX_API_KEY' },
  fallback: { provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash', apiKeyRef: 'OPENROUTER_API_KEY' },
}

type Side = { provider: string; model: string; apiKeyRef: string }
type Pair = { primary: Side; fallback: Side }
type ModelModule = {
  parseE2EModel?: (raw: string) => Pair
  applyPrimaryEnvOverride?: (pair: Pair, env: NodeJS.ProcessEnv, log?: (line: string) => void) => Pair
  E2E_PRIMARY?: Side
  E2E_FALLBACK?: Side
}

async function loadModelModule(): Promise<ModelModule> {
  const mod = (await import('./e2e-model.js')) as ModelModule
  const missing = ['parseE2EModel', 'applyPrimaryEnvOverride', 'E2E_PRIMARY', 'E2E_FALLBACK'].filter(
    (name) => typeof (mod as Record<string, unknown>)[name] === 'undefined',
  )
  if (missing.length > 0) {
    throw new Error(
      `BLOCKED: tests/e2e/fixtures/e2e-model.ts is missing ${missing.join(', ')} — required by design.md §4.2 and §7 R8`,
    )
  }
  return mod
}

describe('parseE2EModel (design.md §4.1)', () => {
  it('parses the primary and fallback sides', async () => {
    const { parseE2EModel } = await loadModelModule()
    expect(parseE2EModel!(SPEC)).toEqual(PARSED)
  })

  it('trims every field', async () => {
    const { parseE2EModel } = await loadModelModule()
    const raw = `{
      "primary": { "provider": " minimax-coding-plan ", "model": " MiniMax-M3.1-Flash-Preview ", "api_key_ref": " MINIMAX_API_KEY " },
      "fallback": { "provider": " openrouter ", "model": " deepseek/deepseek-v4.1-flash ", "api_key_ref": " OPENROUTER_API_KEY " }
    }`
    expect(parseE2EModel!(raw)).toEqual(PARSED)
  })

  it('rejects the legacy {"model":...} shape', async () => {
    const { parseE2EModel } = await loadModelModule()
    expect(() => parseE2EModel!('{"model":"deepseek/deepseek-v4.1-flash"}')).toThrow(/old shape/)
  })

  it('rejects a whitespace-only primary model by name', async () => {
    const { parseE2EModel } = await loadModelModule()
    const raw = `{
      "primary": { "provider": "minimax-coding-plan", "model": "   ", "api_key_ref": "MINIMAX_API_KEY" },
      "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
    }`
    expect(() => parseE2EModel!(raw)).toThrow(/\.primary\.model/)
  })

  it('rejects a document with no fallback object', async () => {
    const { parseE2EModel } = await loadModelModule()
    const raw = '{"primary":{"provider":"minimax-coding-plan","model":"MiniMax-M3.1-Flash-Preview","api_key_ref":"MINIMAX_API_KEY"}}'
    expect(() => parseE2EModel!(raw)).toThrow(/\.fallback/)
  })

  it('rejects a primary pair equal to the fallback pair', async () => {
    const { parseE2EModel } = await loadModelModule()
    const raw = `{
      "primary": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" },
      "fallback": { "provider": "openrouter", "model": "deepseek/deepseek-v4.1-flash", "api_key_ref": "OPENROUTER_API_KEY" }
    }`
    expect(() => parseE2EModel!(raw)).toThrow(/same provider\/model/)
  })
})

describe('applyPrimaryEnvOverride (design.md §4.2)', () => {
  it('logs when the job-start override differs, and the env value wins', async () => {
    const { parseE2EModel, applyPrimaryEnvOverride } = await loadModelModule()
    const lines: string[] = []
    const got = applyPrimaryEnvOverride!(parseE2EModel!(SPEC), {
      OMNIPUS_E2E_PRIMARY_PROVIDER: 'env-prov',
      OMNIPUS_E2E_PRIMARY_MODEL: 'env-model',
    }, (line) => lines.push(line))
    expect(got.primary).toEqual({ provider: 'env-prov', model: 'env-model', apiKeyRef: 'MINIMAX_API_KEY' })
    expect(got.fallback).toEqual(PARSED.fallback)
    const joined = lines.join('\n')
    expect(joined).toContain('OMNIPUS_E2E_PRIMARY_PROVIDER')
    expect(joined).toContain('env-prov')
    expect(joined).toContain('minimax-coding-plan')
    expect(joined).toContain('OMNIPUS_E2E_PRIMARY_MODEL')
    expect(joined).toContain('env-model')
    expect(joined).toContain('MiniMax-M3.1-Flash-Preview')
    expect(joined).toContain('environment variable won')
  })

  it('fails closed when only one of the two override variables is set', async () => {
    const { parseE2EModel, applyPrimaryEnvOverride } = await loadModelModule()
    expect(() =>
      applyPrimaryEnvOverride!(parseE2EModel!(SPEC), { OMNIPUS_E2E_PRIMARY_PROVIDER: 'env-prov' }),
    ).toThrow(/OMNIPUS_E2E_PRIMARY_PROVIDER[\s\S]*OMNIPUS_E2E_PRIMARY_MODEL|OMNIPUS_E2E_PRIMARY_MODEL[\s\S]*OMNIPUS_E2E_PRIMARY_PROVIDER/)
  })

  it('leaves the pair unchanged when neither override is set', async () => {
    const { parseE2EModel, applyPrimaryEnvOverride } = await loadModelModule()
    const lines: string[] = []
    const got = applyPrimaryEnvOverride!(parseE2EModel!(SPEC), {}, (line) => lines.push(line))
    expect(got).toEqual(PARSED)
    expect(lines).toEqual([])
  })
})

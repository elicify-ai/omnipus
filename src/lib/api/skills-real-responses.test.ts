// r15 S-11.19 bridge: Go producer saves REAL responses, then this file feeds
// the exact bytes through generated schemas and real adapters. Run serially
// after TestRestS1119_ResponseCorpus with the same SKILLS_REST_CORPUS path.
import { readFileSync } from 'node:fs'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { Skill, ConfigurationMutationFailureState } from './generated/schemas'
import { ConfigurationSaveError } from './configuration'
import * as adapters from './skills'

beforeEach(() => { document.cookie = 'csrf=real-response-test; path=/' })
afterEach(() => { document.cookie = 'csrf=; Max-Age=0; path=/'; vi.unstubAllGlobals() })

// not-wire-format: test-run receipt, not a gateway/SPA product carrier.
interface Receipt {
  leg: string; method: string; path: string; query: string; request_body: string;
  body: string; status: number; expected_status: number; schema: string; slug: string; revision: string
}

it.each(['install-cleanup', 'install-partial', 'copy-partial', 'delete-rollback', 'delete-rollback-failed'])(
  'real response %s validates and has the same outcome through the real adapter', async (leg) => {
    const path = process.env.SKILLS_REST_CORPUS
    expect(path, 'BLOCKED: generate the real response corpus with the targeted Go producer first').toBeDefined()
    const rows: Receipt[] = JSON.parse(readFileSync(path!, 'utf8'))
    expect(rows.map(row => row.leg).sort(), 'all five operation-specific real responses required; no stale or partial receipt').toEqual(
      ['install-cleanup', 'install-partial', 'copy-partial', 'delete-rollback', 'delete-rollback-failed'].sort(),
    )
    const row = rows.find(item => item.leg === leg)!
    expect(row.status, row.body).toBe(row.expected_status)
    const body: unknown = JSON.parse(row.body)
    const parsed = row.schema === 'Skill' ? Skill.parse(body) : ConfigurationMutationFailureState.parse(body)
    const network = vi.fn<typeof fetch>().mockResolvedValue(new Response(row.body, {
      status: row.status, headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', network)
    let call: Promise<unknown>
    if (leg.startsWith('install')) {
      const install = adapters.installSkillBySlug as (id: string, version?: string, revision?: string, replace?: boolean) => Promise<unknown>
      call = install(row.slug, undefined, row.revision, true)
    } else if (leg === 'copy-partial') {
      const copy = (adapters as Record<string, unknown>).copySkill
      expect(copy, 'BLOCKED: real copy adapter missing').toBeTypeOf('function')
      call = (copy as (id: string, to: string) => Promise<unknown>)('src', 'dst')
    } else {
      call = adapters.deleteSkill(row.slug, row.revision)
    }
    if (row.status === 200) {
      expect(await call).toMatchObject(parsed)
    } else {
      let thrown: unknown
      try { await call } catch (error) { thrown = error }
      expect(thrown).toBeInstanceOf(ConfigurationSaveError)
      expect((thrown as ConfigurationSaveError).state).toEqual(parsed)
    }
    expect(network).toHaveBeenCalledTimes(1)
    const [url, init] = network.mock.calls[0]
    expect(String(url)).toBe(`/api/v1${row.path.replace('/api/v1', '')}${row.query ? `?${row.query}` : ''}`)
    expect(init?.method).toBe(row.method)
    if (row.request_body) expect(JSON.parse(String(init?.body))).toEqual(JSON.parse(row.request_body))
  },
)

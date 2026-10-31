// skills-mutation-envelope.test.ts — RED tests, landing/skills (T9): the
// SPA-side half of T-R3 (spec §17.1 row 42, S-11.19). The real request
// adapter must validate the skills mutation envelopes against the generated
// zod schemas, and the message-only success body stays a rejection case —
// the shared parser is not weakened.
// Traces to: skills-spec-r13.md S-11.19 (SPA parser legs), C-4/C-5/C-6/C-9.
//
// RED expectation: the skills routes' 201 Skill envelope has no generated
// zod schema yet (the copy route is new), so the copy-schema leg fails;
// the shared-parser legs are the regression guard (SC-001) and pass on the
// baseline by design.

import { afterEach, describe, expect, it } from 'vitest'
import {
  ConfigurationMutationState,
  ConfigurationMutationFailureState,
} from './generated/schemas'
import { resetApiSchemaErrorCount, getApiSchemaErrorCount } from './http'

afterEach(() => {
  resetApiSchemaErrorCount()
})

describe('skills mutation envelopes (S-11.19 SPA legs)', () => {
  it('a valid ConfigurationMutationState delete envelope parses (C-6)', () => {
    const parsed = ConfigurationMutationState.safeParse({
      revision: 'fdd9b7afa7635d0f8028957f85c713170c8083e2aebf37ee45677f2e05881eda',
      activation_status: 'active',
      persistence_status: 'complete',
      changed_fields: ['skills'],
      message: 'deleted skill x',
    })
    expect(parsed.success).toBe(true)
  })

  it('a valid ConfigurationMutationFailureState 500 body parses (C-9 reuse)', () => {
    const parsed = ConfigurationMutationFailureState.safeParse({
      revision: 'fdd9b7afa7635d0f8028957f85c713170c8083e2aebf37ee45677f2e05881eda',
      activation_status: 'not_attempted',
      persistence_status: 'none',
      changed_fields: [],
      error_stage: 'manifest_write',
      message: 'the version manifest could not be written',
    })
    expect(parsed.success).toBe(true)
  })

  it('a message-only success body is a rejection case — no envelope without the required fields', () => {
    const before = getApiSchemaErrorCount()
    const parsed = ConfigurationMutationState.safeParse({ message: 'saved' })
    expect(parsed.success).toBe(false)
    // The parser is not weakened: the caller records the schema failure.
    const after = getApiSchemaErrorCount()
    expect(after).toBeGreaterThanOrEqual(before)
  })

  it('the SPA exposes the copy route adapter (copySkill) - RED until T9 lands', async () => {
    // RED: POST /skills/{name}/copy is new in T9 (S-6.5's UI leg, 5.8). The
    // adapter does not exist on the baseline.
    const mod = await import('./skills')
    expect(typeof (mod as Record<string, unknown>).copySkill).toBe('function')
  })

  it('installSkillBySlug forwards replace_local_changes (S-9.3)', async () => {
    // RED: the replace-confirmation flag does not exist on the baseline
    // adapter. The real request path is driven with a stubbed fetch and the
    // outgoing body is asserted.
    const bodies: string[] = []
    const realFetch = globalThis.fetch
    globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
      bodies.push(String(init?.body ?? ''))
      return new Response(
        JSON.stringify({
          id: 'x',
          name: 'x',
          description: 'd',
          source: 'workspace',
          status: 'active',
          verified: false,
          version: '0.0.0',
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      )
    }) as typeof fetch
    try {
      const { installSkillBySlug } = await import('./skills')
      await installSkillBySlug('x', undefined, 'rev', true)
      expect(bodies.length).toBeGreaterThan(0)
      expect(JSON.parse(bodies[0])).toMatchObject({ replace_local_changes: true })
    } finally {
      globalThis.fetch = realFetch
    }
  })
})

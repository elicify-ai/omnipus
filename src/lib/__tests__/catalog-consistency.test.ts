// catalog-consistency.test.ts — ADR-031 Track 1, US-7 / FR-007 / FR-019 / SC-002.
//
// The blocking onboarding≡Settings consistency guarantee. Both surfaces
// (onboarding ModelKeyStep — see -onboarding.test.tsx "US-7 / FR-019" block —
// and Settings ProvidersSection — see ProvidersSection.test.tsx #24-settings)
// render `entry.label` / `entry.subtitle` / `entry.logoSlug` VERBATIM from the
// single shared PROVIDER_CATALOG. Because both read the same object, their
// terminology + logos are identical by construction (SC-002 "0 diffs").
//
// This suite locks the catalog's internal consistency — the invariants that make
// that shared render trustworthy: uniqueness, well-formed labels, wire derivation
// matching FR-005, and every display field non-empty. A regression that corrupts
// a label/subtitle/wire here fails BOTH surfaces at once, which is the point.

import { describe, it, expect } from 'vitest'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'

const PLAN_ACCESS_LABEL: Record<string, string> = {
  'standard-api': 'Standard API',
  'coding-plan': 'Coding Plan',
}

// FR-005 wire derivation rule, replicated here as the oracle.
function expectedWire(id: string): 'anthropic' | 'openai-compatible' {
  if (/-anthropic$/.test(id) || id === 'anthropic' || id === 'anthropic-messages' || id === 'bedrock') {
    return 'anthropic'
  }
  return 'openai-compatible'
}

describe('provider catalog — cross-surface consistency (SC-002 / FR-019)', () => {
  it('is non-empty and has a stable size (~30 curated user-facing entries)', () => {
    expect(PROVIDER_CATALOG.length).toBeGreaterThanOrEqual(25)
    expect(PROVIDER_CATALOG.length).toBeLessThanOrEqual(40)
  })

  it('has unique ids (each entry = one billable endpoint)', () => {
    const ids = PROVIDER_CATALOG.map((e) => e.id)
    expect(new Set(ids).size).toBe(ids.length)
  })

  it('every entry has all display fields non-empty (nothing renders blank)', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(e.id, `id for ${JSON.stringify(e)}`).toBeTruthy()
      expect(e.company, `company for ${e.id}`).toBeTruthy()
      expect(e.label, `label for ${e.id}`).toBeTruthy()
      expect(e.subtitle, `subtitle for ${e.id}`).toBeTruthy()
      expect(e.logoSlug, `logoSlug for ${e.id}`).toBeTruthy()
      expect(e.endpointHint, `endpointHint for ${e.id}`).toBeTruthy()
    }
  })

  it('plan is one of the shipped enum values (no legacy "anthropic"/"token" plan)', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(['standard-api', 'coding-plan'], `plan for ${e.id}`).toContain(e.plan)
    }
  })

  it('label encodes an access descriptor: plan label for openai-compatible, wire descriptor for anthropic (US-6)', () => {
    // FR-006 access-type = plan; BUT the dual-wire Chinese providers expose an
    // OpenAI-compatible AND an Anthropic-compatible endpoint at the SAME
    // plan+region — so strict "plan-only" labels collide (z-ai vs z-ai-anthropic
    // would both be "Zhipu / GLM — Standard API (International)"). The catalog
    // therefore uses the wire descriptor ("Anthropic-compatible") as the access
    // label for anthropic-wire variants, which keeps labels unique. The exact
    // wording is flagged for the /ux-psychology review; the invariant here is
    // that the label carries a MEANINGFUL access descriptor, never blank.
    for (const e of PROVIDER_CATALOG) {
      if (e.wire === 'anthropic') {
        expect(e.label, `anthropic-wire label for ${e.id}`).toMatch(/Anthropic/)
      } else {
        expect(e.label, `openai-wire label for ${e.id}`).toContain(PLAN_ACCESS_LABEL[e.plan])
      }
    }
  })

  it('labels are unique (no two rows share a title — the dual-wire disambiguation works)', () => {
    const labels = PROVIDER_CATALOG.map((e) => e.label)
    const dupes = labels.filter((l, i) => labels.indexOf(l) !== i)
    expect(dupes, `duplicate labels: ${dupes.join(', ')}`).toEqual([])
  })

  it('subtitle names the endpoint (contains endpointHint)', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(e.subtitle, `subtitle for ${e.id}`).toContain(e.endpointHint)
    }
  })

  it('wire is derived per FR-005 (openai-compatible unless anthropic-suffixed / in the anthropic set)', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(e.wire, `wire for ${e.id}`).toBe(expectedWire(e.id))
    }
  })

  it('region, when present, is one of the closed enum values', () => {
    for (const e of PROVIDER_CATALOG) {
      if (e.region !== undefined) {
        expect(['intl', 'china', 'us'], `region for ${e.id}`).toContain(e.region)
      }
    }
  })
})

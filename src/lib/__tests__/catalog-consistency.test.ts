// catalog-consistency.test.ts — provider-ux-fixes-plan FIX-4/FIX-5/FIX-6 +
// the original ADR-031 Track 1, US-7 / FR-007 / FR-019 / SC-002 guarantee.
//
// The blocking onboarding≡Settings consistency guarantee. Both surfaces
// (onboarding ModelKeyStep — see -onboarding.test.tsx "US-7 / FR-019" block —
// and Settings ProvidersSection — see ProvidersSection.test.tsx #24-settings)
// render `entry.label` / `entry.subtitle` / `entry.logoSlug` VERBATIM from the
// single shared PROVIDER_CATALOG. Because both read the same object, their
// terminology + logos are identical by construction (SC-002 "0 diffs").
//
// This suite locks the catalog's internal consistency — the invariants that make
// that shared render trustworthy: uniqueness, well-formed labels, wire derivation,
// and every display field non-empty. A regression that corrupts a label/subtitle/
// wire here fails BOTH surfaces at once, which is the point.
//
// [C2] Cross-surface DOM-render parity is in the companion file:
// src/lib/__tests__/onboarding-settings-parity.test.tsx
// That file renders ProvidersSection with a mocked provider and asserts the DOM
// shows the same catalog label/subtitle that -onboarding.test.tsx asserts on the
// onboarding side.
//
// FIX-5 wire-merge: the catalog was reduced from ~30 entries to 23 by merging
// the 7 pure anthropic-wire sibling entries into their primary entry's optional
// `anthropic_id` field (e.g. `z-ai-anthropic` → `z-ai.anthropic_id`). Wire is no
// longer a separate catalog row — see the anthropic_id invariants below.
//
// Traces to: docs/internal/specs/provider-ux-fixes-plan.md FIX-4/FIX-5/FIX-6;
// connectors-providers-redesign-spec.md §7 SC-002 / US-7 / FR-019 (superseded
// where in conflict, per the plan's header).

import { describe, it, expect } from 'vitest'
import { PROVIDER_CATALOG } from '@/lib/generated/providerCatalog'

describe('provider catalog — cross-surface consistency (FIX-4/5/6, SC-002 / FR-019)', () => {
  it('is non-empty and has a stable size (23 curated user-facing entries post wire-merge)', () => {
    expect(PROVIDER_CATALOG.length).toBe(23)
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

  // FIX-4: real terminology — "Standard API" and "Anthropic-compatible" are
  // retired wording; labels use "Pay-as-you-go API" / plain company name for
  // standard-api entries, and always contain "Coding Plan" for coding-plan
  // entries (never the other way around).
  it('[FIX-4] no label contains the retired "Standard API" or "Anthropic-compatible" wording', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(e.label, `label for ${e.id}`).not.toMatch(/Standard API/)
      expect(e.label, `label for ${e.id}`).not.toMatch(/Anthropic-compatible/)
    }
  })

  it('[FIX-4] label contains "Coding Plan" if and only if plan === coding-plan', () => {
    for (const e of PROVIDER_CATALOG) {
      if (e.plan === 'coding-plan') {
        expect(e.label, `coding-plan label for ${e.id}`).toMatch(/Coding Plan/)
      } else {
        expect(e.label, `standard-api label for ${e.id}`).not.toMatch(/Coding Plan/)
      }
    }
  })

  it('labels are unique (no two rows share a title)', () => {
    const labels = PROVIDER_CATALOG.map((e) => e.label)
    const dupes = labels.filter((l, i) => labels.indexOf(l) !== i)
    expect(dupes, `duplicate labels: ${dupes.join(', ')}`).toEqual([])
  })

  it('subtitle names the endpoint (contains endpointHint)', () => {
    for (const e of PROVIDER_CATALOG) {
      expect(e.subtitle, `subtitle for ${e.id}`).toContain(e.endpointHint)
    }
  })

  // FIX-5: post wire-merge, only the `anthropic` company entry itself carries
  // wire: 'anthropic' as its primary wire — every other entry (including
  // dual-wire providers) is 'openai-compatible' as its primary id, with the
  // Anthropic-compatible sibling reachable only via `anthropic_id`.
  it('[FIX-5] wire is openai-compatible for every primary id except the anthropic company entry', () => {
    for (const e of PROVIDER_CATALOG) {
      if (e.id === 'anthropic') {
        expect(e.wire, `wire for ${e.id}`).toBe('anthropic')
      } else {
        expect(e.wire, `wire for ${e.id}`).toBe('openai-compatible')
      }
    }
  })

  it('[FIX-5] anthropic_id, when present, is unique, non-empty, and distinct from the entry id', () => {
    const anthropicIds = PROVIDER_CATALOG.map((e) => e.anthropic_id).filter(
      (v): v is string => !!v,
    )
    expect(new Set(anthropicIds).size).toBe(anthropicIds.length)
    for (const e of PROVIDER_CATALOG) {
      if (e.anthropic_id) {
        expect(e.anthropic_id).not.toBe(e.id)
        expect(e.anthropic_id.length).toBeGreaterThan(0)
      }
    }
  })

  it('[FIX-5] anthropic_id values never collide with a primary catalog id', () => {
    const primaryIds = new Set(PROVIDER_CATALOG.map((e) => e.id))
    for (const e of PROVIDER_CATALOG) {
      if (e.anthropic_id) {
        expect(primaryIds.has(e.anthropic_id), `${e.anthropic_id} collides with a primary id`).toBe(false)
      }
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

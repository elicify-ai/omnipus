// popularProvidersExistInRealCatalog.test.ts — drift guard for the exact
// defect this file was added to fix: the hand-maintained SPA fixture
// (providers-catalog.json) carried `moonshot` as a popular-tier provider id
// while the shipped catalog (pkg/providers/catalog/data/providers_catalog.json,
// the file the binary actually embeds) had been renamed to `moonshotai` by
// upstream. Every SPA test that asserted against the fixture's `moonshot`
// id passed, and proved nothing about the real thing — the id production
// serves does not exist in the fixture that stood in for it.
//
// SCOPE: this checks only `tier: "popular"` fixture ids, not the whole
// 190-entry document. That is a deliberate, evidence-based boundary, not
// laziness: providersCatalog.ts's own header documents the fixture as "a
// hand-built stand-in shaped exactly like the real snapshot" used to
// exercise catalog-shape behaviour (grouping, tiers, sign-in variants,
// letter headers, EU/coding-plan region padding, …), not a mirror of
// production. A direct count confirms this — 129 of the fixture's 190 ids
// (bytedance-ark-cn, mistral-coding-plan, hyperbolic-eu, …) have no real
// catalog counterpart at all, by design, spread across dozens of vendors'
// invented -eu/-pro/-coding-plan variants. A guard asserting full-catalog id
// parity would fail on all 129 and teach nobody to trust it.
//
// The 12 `tier: "popular"` ids are different in kind: they are the vendors
// the onboarding wizard actually surfaces first, and other SPA code keys
// real behaviour off their exact id (brand icons via company name, sign-in
// defaults, `PROVIDERS_REQUIRING_ENDPOINT` lookups). Before the `moonshot`
// bug, all 12 already matched a real production id with zero exceptions —
// that is a genuine invariant, not a coincidence, and this test pins it so
// the next silent upstream rename fails loudly here instead of shipping
// unnoticed.

import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { CATALOG_PROVIDERS } from './providersCatalog'

const REPO_ROOT = resolve(__dirname, '..', '..', '..')
const REAL_CATALOG_PATH = resolve(REPO_ROOT, 'pkg/providers/catalog/data/providers_catalog.json')

interface RealCatalogDoc {
  providers: Array<{ id: string }>
}

function loadRealCatalogIds(): Set<string> {
  const raw = readFileSync(REAL_CATALOG_PATH, 'utf8')
  const doc = JSON.parse(raw) as RealCatalogDoc
  return new Set(doc.providers.map((p) => p.id))
}

describe('every popular-tier fixture id exists in the real shipped catalog', () => {
  it('finds a real catalog match for each tier:"popular" id in providers-catalog.json', () => {
    const realIds = loadRealCatalogIds()
    const popularFixtureIds = CATALOG_PROVIDERS.filter((p) => p.tier === 'popular').map((p) => p.id)

    expect(popularFixtureIds.length, 'fixture has no popular-tier providers — fixture changed shape').toBeGreaterThan(0)

    const orphaned = popularFixtureIds.filter((id) => !realIds.has(id))
    expect(
      orphaned,
      `these popular-tier fixture ids do not exist in the real catalog ` +
        `(pkg/providers/catalog/data/providers_catalog.json) — a hand-maintained ` +
        `SPA fixture id has drifted from a production rename: ${orphaned.join(', ')}`,
    ).toEqual([])
  })
})

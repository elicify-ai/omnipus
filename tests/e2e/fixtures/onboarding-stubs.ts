/**
 * onboarding-stubs.ts — the shared route stubs that put the SPA back on the
 * onboarding wizard (ADR-068 T068-31).
 *
 * WHY STUBS AND NOT A REAL FRESH INSTALL
 *
 * The Playwright suite runs `workers: 1` against ONE shared gateway that
 * `global-setup.ts` already onboarded (it seeds the openrouter provider every
 * other spec asserts on). Driving the real `POST /onboarding/complete` a second
 * time would either 409 or re-write that shared install's provider and default
 * model out from under `providers.spec.ts`. So the wizard's four inputs are
 * stubbed at the network edge and nothing on the gateway is mutated:
 *
 *   GET  /api/v1/state                    → onboarding_complete: false
 *   GET  /api/v1/providers/catalog        → the 190-entry fixture
 *   POST /api/v1/onboarding/probe-provider→ success, echoing `probed_model`
 *   POST /api/v1/onboarding/complete      → a valid LoginResponse, body captured
 *
 * What is still REAL, and therefore still under test: the whole SPA — the
 * beforeLoad guard, the three-step wizard, the shared `ProviderPicker` and its
 * second-level `ProviderDetailPanel`, the probe sequencing that gates *Finish*,
 * and the exact JSON body the SPA emits on completion. The assertions in
 * onboarding.spec.ts are about the SPA's behaviour and the shape of the request
 * it sends; a stub cannot fake either of those.
 *
 * The stubbed responses are the CONTRACT shapes (contracts/components/schemas/
 * AppState.yaml, ProbeProviderResponse.yaml, LoginResponse.yaml). Every one is
 * re-validated by the SPA's own zod edge (`request(..., Schema)`), so a stub
 * that drifts from the contract fails the test rather than passing it.
 */

import type { Page, Route } from '@playwright/test'
import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'

const __dirname_ = path.dirname(fileURLToPath(import.meta.url))

/**
 * The 190-entry providers catalog every ADR-068 SPA test renders against
 * (spec §Development). Read from the SAME file `src/test/fixtures/
 * providers-catalog.json` the vitest suite uses, so the e2e and unit layers
 * can never disagree about what the picker is showing.
 */
export const PROVIDERS_CATALOG = JSON.parse(
  fs.readFileSync(
    path.join(__dirname_, '../../../src/test/fixtures/providers-catalog.json'),
    'utf-8',
  ),
) as {
  providers: Array<{
    id: string
    company: string
    tier: string
    auth_methods?: string[]
    models?: Array<{ id: string }>
  }>
}

/** A syntactically valid BearerToken (contracts/components/schemas/BearerToken.yaml). */
const FAKE_TOKEN = `omnipus_${'a1b2c3d4'}_${'0'.repeat(64)}`

/** Everything an onboarding run observed on the wire. */
export interface OnboardingWireLog {
  /** Bodies of every POST /onboarding/complete, newest last. */
  completions: Array<Record<string, unknown>>
  /** Bodies of every POST /onboarding/probe-provider, newest last. */
  probes: Array<Record<string, unknown>>
  /** Every GET /providers/catalog response status the browser actually saw. */
  catalogStatuses: number[]
}

/**
 * Install the four stubs and return the wire log the test asserts on.
 * Must be called BEFORE `page.goto` — the route guard fetches `/state` during
 * `beforeLoad`, which runs on the very first render.
 */
export async function stubOnboarding(
  page: Page,
  opts: { catalog?: 'fixture' | 'real' } = {},
): Promise<OnboardingWireLog> {
  const log: OnboardingWireLog = { completions: [], probes: [], catalogStatuses: [] }

  await page.route('**/api/v1/state', async (route: Route) => {
    if (route.request().method() !== 'GET') return route.continue()
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ onboarding_complete: false }),
    })
  })

  // `catalog: 'real'` leaves GET /providers/catalog to the gateway — that is how
  // FR-037's "the SPA reads the catalog from the GET, not a bundle" row is
  // tested. Every other row wants the deterministic 190-entry fixture.
  if (opts.catalog !== 'real') {
    await page.route('**/api/v1/providers/catalog', async (route: Route) => {
      log.catalogStatuses.push(200)
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        headers: { ETag: '"e2e-catalog-v1"' },
        body: JSON.stringify(PROVIDERS_CATALOG),
      })
    })
  }

  await page.route('**/api/v1/onboarding/probe-provider', async (route: Route) => {
    const body = route.request().postDataJSON() as Record<string, unknown>
    log.probes.push(body)
    const model = typeof body.model === 'string' ? body.model : ''
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      // FR-036: `probed_model` echoes the model the probe actually exercised.
      // Finish compares it to the model on screen, so echoing the REQUEST's
      // model is what makes the success path real rather than assumed.
      body: JSON.stringify({ success: true, models: [model], probed_model: model }),
    })
  })

  await page.route('**/api/v1/onboarding/complete', async (route: Route) => {
    log.completions.push(route.request().postDataJSON() as Record<string, unknown>)
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ token: FAKE_TOKEN, username: 'e2e-admin' }),
    })
  })

  return log
}

/** The catalog row for an id — used to derive expectations from the fixture. */
export function catalogRow(id: string) {
  const row = PROVIDERS_CATALOG.providers.find((p) => p.id === id)
  if (!row) throw new Error(`fixture has no provider "${id}" — the fixture changed shape`)
  return row
}

/**
 * The Popular band, in catalog order: one entry per distinct `company`, the
 * FIRST popular-tier provider of that company. This is the derivation
 * `ProviderPicker.test.tsx::popularIdsInCatalogOrder` pins against the rendered
 * tiles, so the e2e rows and the unit rows can never disagree about which tile
 * is "the third one".
 */
export function popularTiles(): Array<{ id: string; company: string }> {
  const seen = new Set<string>()
  const out: Array<{ id: string; company: string }> = []
  for (const p of PROVIDERS_CATALOG.providers) {
    if (p.tier !== 'popular') continue
    if (seen.has(p.company)) continue
    seen.add(p.company)
    out.push({ id: p.id, company: p.company })
  }
  if (out.length < 3) {
    throw new Error(`fixture has ${out.length} Popular companies — expected the 8 of FR-022`)
  }
  return out
}

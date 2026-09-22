/**
 * ProviderDetailPanel.bedrockServedShape.test.tsx — issue #800 (Bedrock
 * region contract), orchestrator-review defect.
 *
 * A real running gateway's GET /api/v1/providers/catalog was returning
 * amazon-bedrock with `regions` absent (0 entries), although the parsed
 * catalog Document had 22 regions (Provider.Regions) and models carried
 * InferenceProfiles — pkg/providers/catalog/served.go's private marshal
 * shapes (`providerJSON` / `modelJSON`) simply had no field for either.
 * `ProviderDetailPanel.awsRegion.test.tsx`'s own header already names the
 * gap this closes: those tests, and every other suite in this directory,
 * render against `src/test/fixtures/providers-catalog.json` — a
 * HAND-MAINTAINED fixture (4 regions, not the real 22) that never goes
 * through Go's own served-body marshal at all, so a dropped field there is
 * invisible to a hand fixture almost by construction.
 *
 * This suite instead reads the ACTUAL committed catalog asset
 * (`pkg/providers/catalog/data/providers_catalog.json` — the same file
 * `served.go` marshals byte-for-byte-equivalent field names from, proven
 * server-side by `served_test.go`'s
 * `TestServed_EmbeddedSnapshot_BedrockRegionsAndInferenceProfiles`) and
 * feeds it to the panel in the served JSON's own snake_case shape
 * (`CatalogProvider`/`CatalogModel`, generated from
 * contracts/components/schemas/) — proving the field names line up
 * end-to-end, from Go's private marshal struct tags through the OpenAPI
 * contract to the component that reads `resolvedVariant.regions`.
 *
 * The one field the raw asset does not carry is `locality` — ADR-067
 * FR-039 derives it server-side, in served.go's own providerToJSON, not in
 * the committed data file. Bedrock is a cloud protocol (never "local"), so
 * stamping it here mirrors exactly what the gateway adds before this shape
 * ever reaches the wire, without re-deriving the whole locality rule in a
 * test.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { toCompanyRows } from './provider-picker-model'
import { ProviderDetailPanel } from './ProviderDetailPanel'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'

const REPO_ROOT = resolve(__dirname, '..', '..', '..')
const CATALOG_PATH = resolve(REPO_ROOT, 'pkg/providers/catalog/data/providers_catalog.json')

interface RealCatalogDoc {
  providers: CatalogProvider[]
}

function loadRealCatalogProvider(id: string): CatalogProvider {
  const raw = readFileSync(CATALOG_PATH, 'utf8')
  const doc = JSON.parse(raw) as RealCatalogDoc
  const provider = doc.providers.find((row) => row.id === id)
  if (!provider) throw new Error(`real catalog has no provider ${id}`)
  // served.go's providerToJSON always sets `locality` (required, no
  // omitempty) — the raw asset predates that derivation, so it is stamped
  // here exactly as the gateway would for a cloud-protocol row.
  return { ...provider, locality: 'cloud' }
}

function bedrockCompanyRow() {
  const rows = toCompanyRows([loadRealCatalogProvider('amazon-bedrock')])
  if (rows.length !== 1) throw new Error('expected exactly one company row for amazon-bedrock')
  return rows[0]
}

describe('ProviderDetailPanel against the real served-shaped amazon-bedrock row (issue #800)', () => {
  it('sanity check: the real catalog asset carries 22 AWS regions and a model with inference_profiles', () => {
    const provider = loadRealCatalogProvider('amazon-bedrock')
    expect(provider.regions).toHaveLength(22)
    expect(
      (provider.models ?? []).some((m) => (m.inference_profiles ?? []).length > 0),
    ).toBe(true)
  })

  it('renders the AWS region select with all 22 catalog regions — snake_case field names line up end to end', () => {
    render(<ProviderDetailPanel company={bedrockCompanyRow()} locale="en-US" />)
    const select = screen.getByTestId('provider-detail-panel-aws-region') as HTMLSelectElement
    const values = Array.from(select.options).map((o) => o.value)
    expect(values).toHaveLength(22)
    expect(values).toContain('us-east-1')
    expect(values).toContain('us-gov-west-1')
    expect(select.value).toBe('us-east-1') // CatalogProvider.region's default, echoed as the select's default
  })

  it('does not render the single-button sibling region group next to the real AWS select (UX fix)', () => {
    render(<ProviderDetailPanel company={bedrockCompanyRow()} locale="en-US" />)
    expect(screen.getByTestId('provider-detail-panel-aws-region')).toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-regions')).not.toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-region-us-east-1')).not.toBeInTheDocument()
  })
})

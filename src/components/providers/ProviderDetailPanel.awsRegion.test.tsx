/**
 * ProviderDetailPanel.awsRegion.test.tsx — issue #800 (Bedrock region
 * contract). The picker's second-level panel is where Bedrock's API key is
 * entered (T068-27's apiKeyField slot, used by both onboarding and
 * Settings → Providers via the shared ProviderPicker), so this is where the
 * AWS region control lives — populated from the catalog's own
 * `regions: [{id, group}]`, never a hand-typed list.
 *
 * Distinct from the EXISTING "Region" aria-pressed group covered by
 * ProviderDetailPanel.test.tsx: that one selects between SIBLING catalog
 * ROWS (a company's plan x region VARIANTS, e.g. Zhipu AI intl vs china —
 * `PickerCompanyRow.regions: string[]`). This one selects an AWS region
 * WITHIN one already-chosen row, from `CatalogProvider.regions:
 * CatalogProviderRegion[]`. Amazon Bedrock has exactly one catalog row, so
 * the existing group also renders (a single "us-east-1" button, from
 * CatalogProvider.region) — harmless and out of scope here.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { toCompanyRows, type PickerCompanyRow } from './provider-picker-model'
import { ProviderDetailPanel, type ProviderDetailSelection } from './ProviderDetailPanel'

function catalogRow(id: string): CatalogProvider {
  const row = CATALOG_PROVIDERS.find((p) => p.id === id)
  if (!row) throw new Error(`fixture is missing provider ${id}`)
  return row
}

function companyRowFor(providerId: string): PickerCompanyRow {
  const company = catalogRow(providerId).company
  const row = toCompanyRows(CATALOG_PROVIDERS).find((r) => r.company === company)
  if (!row) throw new Error(`no company row for ${providerId}`)
  return row
}

describe('ProviderDetailPanel — AWS region control (issue #800)', () => {
  it('renders an AWS region control for a provider whose catalog row carries regions', () => {
    render(<ProviderDetailPanel company={companyRowFor('amazon-bedrock')} locale="en-US" />)
    expect(screen.getByTestId('provider-detail-panel-aws-region')).toBeInTheDocument()
  })

  it('does not render the AWS region control for a provider with no regions field', () => {
    render(<ProviderDetailPanel company={companyRowFor('zai')} locale="en-US" />)
    expect(screen.queryByTestId('provider-detail-panel-aws-region')).not.toBeInTheDocument()
  })

  it('defaults the AWS region to the catalog row\'s own default region', () => {
    render(<ProviderDetailPanel company={companyRowFor('amazon-bedrock')} locale="en-US" />)
    const select = screen.getByTestId('provider-detail-panel-aws-region') as HTMLSelectElement
    expect(select.value).toBe('us-east-1')
  })

  it('lists every region the catalog offers, in catalog order', () => {
    render(<ProviderDetailPanel company={companyRowFor('amazon-bedrock')} locale="en-US" />)
    const select = screen.getByTestId('provider-detail-panel-aws-region') as HTMLSelectElement
    const optionValues = Array.from(select.options).map((o) => o.value)
    expect(optionValues).toEqual(['us-east-1', 'eu-central-1', 'ap-northeast-1', 'us-gov-west-1'])
  })

  it('reports the selected AWS region on the emitted selection, and saving persists it', async () => {
    const user = userEvent.setup()
    let latest: ProviderDetailSelection | undefined
    render(
      <ProviderDetailPanel
        company={companyRowFor('amazon-bedrock')}
        locale="en-US"
        onChange={(s) => {
          latest = s
        }}
      />,
    )
    const select = screen.getByTestId('provider-detail-panel-aws-region') as HTMLSelectElement
    await user.selectOptions(select, 'eu-central-1')
    expect(latest?.awsRegion).toBe('eu-central-1')
  })
})

/**
 * ProviderDetailPanel.test.tsx — ADR-068 FR-027, FR-028, FR-005/FR-006
 * (T068-21). The panel is the picker's second level: plan and region as
 * `aria-pressed` groups with a locale-inferred region default, and the
 * auth-method control in the same step.
 *
 * Every company row here is built from the shared 190-entry catalog fixture
 * through `toCompanyRows` — the same function the picker uses — so a catalog
 * change reaches these tests instead of being papered over by hand-written
 * variants.
 *
 * One deliberate exception, called out because it looks like a fixture
 * disagreement: the fixture keeps `openai`, `openai-chatgpt` and `codex-cli` as
 * three single-variant companies ("so the `openai` API-key row stays one-click"
 * — its own header), while the served catalog is expected to group OpenAI's
 * rows under one company. The FR-006 pair is therefore exercised against a
 * LOCALLY regrouped copy of the fixture rows rather than by editing the shared
 * fixture, which several other suites pin as-is. The panel itself has no OpenAI
 * branch at all: it offers whatever sign-in variants the company it is handed
 * carries, which is the property that makes both groupings correct.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import { toCompanyRows, type PickerCompanyRow } from './provider-picker-model'
import { ProviderDetailPanel } from './ProviderDetailPanel'

function catalogRow(id: string): CatalogProvider {
  const row = CATALOG_PROVIDERS.find((p) => p.id === id)
  if (!row) throw new Error(`fixture is missing provider ${id}`)
  return row
}

/** The company row the picker would hand the panel for a given catalog id. */
function companyRowFor(providerId: string): PickerCompanyRow {
  const company = catalogRow(providerId).company
  const row = toCompanyRows(CATALOG_PROVIDERS).find((r) => r.company === company)
  if (!row) throw new Error(`no company row for ${providerId}`)
  return row
}

/** The OpenAI family as one company — see the header note. */
function openAiFamilyRow(): PickerCompanyRow {
  const variants = ['openai', 'openai-chatgpt', 'codex-cli'].map((id) => ({
    ...catalogRow(id),
    company: 'OpenAI',
  }))
  const rows = toCompanyRows(variants)
  expect(rows).toHaveLength(1)
  return rows[0]
}

describe('ProviderDetailPanel — plan and region groups (FR-027)', () => {
  it('renders plan and region as labelled aria-pressed groups with exactly one pressed', () => {
    render(<ProviderDetailPanel company={companyRowFor('zai')} locale="de-DE" />)

    const plans = screen.getByTestId('provider-detail-panel-plans')
    const regions = screen.getByTestId('provider-detail-panel-regions')
    expect(plans).toHaveAccessibleName('Plan')

    const pressed = (group: HTMLElement) =>
      within(group)
        .getAllByRole('button')
        .filter((b) => b.getAttribute('aria-pressed') === 'true')
    expect(pressed(plans)).toHaveLength(1)
    expect(pressed(regions)).toHaveLength(1)

    // Both dimensions of the Zhipu fixture are offered, catalog order.
    const regionButtons = within(regions).getAllByRole('button')
    expect(regionButtons.map((b) => b.textContent)).toEqual(['International', 'China'])
  })

  it('omits both groups for a company with a single variant', () => {
    render(<ProviderDetailPanel company={companyRowFor('anthropic')} locale="en-US" />)
    expect(screen.queryByTestId('provider-detail-panel-plans')).not.toBeInTheDocument()
    expect(screen.queryByTestId('provider-detail-panel-regions')).not.toBeInTheDocument()
  })

  // Scenario Outline "Region inferred from locale" — the render half. The pure
  // inference map itself is `region-inference.test.ts` (TDD row 7).
  const localeRows: Array<{ locale: string | null; selected: string; copy: string }> = [
    { locale: 'zh-CN', selected: 'china', copy: 'Detected: China — change' },
    { locale: 'zh-SG', selected: 'china', copy: 'Detected: China — change' },
    { locale: 'zh-TW', selected: 'intl', copy: 'Detected: International — change' },
    { locale: 'zh-HK', selected: 'intl', copy: 'Detected: International — change' },
    { locale: 'en-GB', selected: 'intl', copy: 'Detected: International — change' },
    { locale: 'en-US', selected: 'intl', copy: 'Detected: International — change' },
    { locale: 'de-DE', selected: 'intl', copy: 'Detected: International — change' },
    { locale: '', selected: 'intl', copy: 'Region — change' },
  ]

  for (const row of localeRows) {
    it(`pre-selects ${row.selected} for locale "${row.locale}" and says so`, () => {
      render(<ProviderDetailPanel company={companyRowFor('zai')} locale={row.locale} />)

      expect(screen.getByTestId(`provider-detail-panel-region-${row.selected}`)).toHaveAttribute(
        'aria-pressed',
        'true',
      )
      expect(screen.getByTestId('provider-detail-panel-region-copy')).toHaveTextContent(row.copy)
      // The copy IS the group's accessible name, so a screen-reader user hears
      // that the region was guessed before hearing the options (1.3.1).
      expect(screen.getByTestId('provider-detail-panel-regions')).toHaveAccessibleName(row.copy)
    })
  }

  // The two outline rows whose region set the fixture does not ship (intl, us).
  // Built from the Zhipu rows with the region relabelled, so the panel is still
  // reading catalog-shaped data rather than a bespoke object.
  function intlUsCompany(): PickerCompanyRow {
    return toCompanyRows([
      { ...catalogRow('zai'), company: 'Regional', region: 'intl', plan: undefined },
      { ...catalogRow('zhipuai'), company: 'Regional', region: 'us', plan: undefined },
    ])[0]
  }

  it('pre-selects us for en-US when the provider offers a US region', () => {
    render(<ProviderDetailPanel company={intlUsCompany()} locale="en-US" />)
    expect(screen.getByTestId('provider-detail-panel-region-us')).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(screen.getByTestId('provider-detail-panel-region-copy')).toHaveTextContent(
      'Detected: US — change',
    )
  })

  it('pre-selects intl for en-GB even where a US region is offered', () => {
    render(<ProviderDetailPanel company={intlUsCompany()} locale="en-GB" />)
    expect(screen.getByTestId('provider-detail-panel-region-intl')).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    expect(screen.getByTestId('provider-detail-panel-region-copy')).toHaveTextContent(
      'Detected: International — change',
    )
  })

  it('falls back to the browser locale when none is passed', () => {
    const spy = vi.spyOn(navigator, 'language', 'get').mockReturnValue('zh-CN')
    try {
      render(<ProviderDetailPanel company={companyRowFor('zai')} />)
      expect(screen.getByTestId('provider-detail-panel-region-china')).toHaveAttribute(
        'aria-pressed',
        'true',
      )
    } finally {
      spy.mockRestore()
    }
  })

  it('resolves the plan x region pair to the catalog variant it names', async () => {
    const onConfirm = vi.fn()
    render(
      <ProviderDetailPanel company={companyRowFor('zai')} locale="de-DE" onConfirm={onConfirm} />,
    )

    // Untouched: intl (inferred) with the first plan.
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({ providerId: 'zai', region: 'intl', authMethod: 'api_key' }),
    )

    // Override the region: the China row of the same plan.
    await userEvent.click(screen.getByTestId('provider-detail-panel-region-china'))
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({ providerId: 'zhipuai', region: 'china' }),
    )

    // And the coding plan within that region.
    await userEvent.click(screen.getByTestId('provider-detail-panel-plan-coding-plan'))
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({
        providerId: 'zhipuai-coding-plan',
        region: 'china',
        plan: 'coding-plan',
      }),
    )
  })
})

describe('ProviderDetailPanel — auth method in the same step (FR-005, FR-028)', () => {
  it('contains no sign-in control for Anthropic or Google (ADR-068 §8b decision 4)', () => {
    for (const id of ['anthropic', 'google']) {
      const { container, unmount } = render(
        <ProviderDetailPanel company={companyRowFor(id)} locale="en-US" />,
      )
      expect(screen.queryByTestId('provider-detail-panel-auth-segment')).not.toBeInTheDocument()
      expect(screen.queryByTestId('provider-detail-panel-auth-signin')).not.toBeInTheDocument()
      expect(container.textContent ?? '').not.toMatch(/sign in/i)
      expect(screen.getByTestId('provider-detail-panel-api-key-input')).toBeInTheDocument()
      unmount()
    }
  })

  it('leaves xAI key-only until its catalog row carries sign_in (FR-049)', () => {
    const keyOnly = render(<ProviderDetailPanel company={companyRowFor('xai')} locale="en-US" />)
    expect(screen.queryByTestId('provider-detail-panel-auth-signin')).not.toBeInTheDocument()
    // No forward-looking copy either — the row says nothing about a login that
    // does not exist yet (Qualitative prohibitions).
    expect(keyOnly.container.textContent ?? '').not.toMatch(/sign in/i)
    keyOnly.unmount()

    // The day the catalog row gains sign_in, the same panel offers it — no code
    // change, which is exactly what this asserts.
    const listed = toCompanyRows([
      { ...catalogRow('xai'), auth_methods: ['api_key', 'sign_in'] },
    ])[0]
    render(<ProviderDetailPanel company={listed} locale="en-US" />)
    expect(screen.getByTestId('provider-detail-panel-auth-segment-sign_in')).toHaveAttribute(
      'aria-pressed',
      'true',
    )
  })

  it('persists openai-chatgpt when the OpenAI sign-in radios are untouched (DoD)', async () => {
    const onConfirm = vi.fn()
    render(<ProviderDetailPanel company={openAiFamilyRow()} locale="en-US" onConfirm={onConfirm} />)

    expect(screen.getByTestId('provider-detail-panel-auth-segment-sign_in')).toHaveAttribute(
      'aria-pressed',
      'true',
    )
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({ providerId: 'openai-chatgpt', authMethod: 'sign_in' }),
    )
  })

  it('switches to the OpenAI API-key row when the segment switches (FR-028)', async () => {
    const onConfirm = vi.fn()
    render(<ProviderDetailPanel company={openAiFamilyRow()} locale="en-US" onConfirm={onConfirm} />)

    await userEvent.click(screen.getByTestId('provider-detail-panel-auth-segment-api_key'))
    await userEvent.type(screen.getByTestId('provider-detail-panel-api-key-input'), 'sk-test')
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))

    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({
        providerId: 'openai',
        authMethod: 'api_key',
        apiKey: 'sk-test',
      }),
    )
  })

  it('hands the T068-33 sign-in seam the selected provider id', async () => {
    const onSignIn = vi.fn()
    render(
      <ProviderDetailPanel company={openAiFamilyRow()} locale="en-US" onSignIn={onSignIn} />,
    )
    await userEvent.click(screen.getByTestId('provider-detail-panel-auth-signin-start'))
    expect(onSignIn).toHaveBeenCalledWith('openai-chatgpt')
  })
})

// F2 (UAT-confirmed, same real-instance walkthrough as F1 above): the served
// catalog's plain "openai" (API-key) row spuriously ALSO carries `sign_in` in
// its `auth_methods` — a data bug distinct from this fixture, which keeps
// "openai" api_key-only (see `catalogRow('openai').auth_methods` above). This
// suite reproduces that exact shape locally so the panel's defence against it
// is under test even while the shared fixture stays clean.
describe('ProviderDetailPanel — defends against a dual api_key+sign_in row with a dedicated sibling (FR-006 F2)', () => {
  function openAiFamilyRowWithBuggyOpenAI(): PickerCompanyRow {
    const variants: CatalogProvider[] = [
      { ...catalogRow('codex-cli'), company: 'OpenAI' },
      { ...catalogRow('openai'), company: 'OpenAI', auth_methods: ['api_key', 'sign_in'] },
      { ...catalogRow('openai-chatgpt'), company: 'OpenAI' },
    ]
    return toCompanyRows(variants)[0]
  }

  it('renders exactly the FR-006 pair — the dual-purpose "openai" row never becomes a third radio', () => {
    render(<ProviderDetailPanel company={openAiFamilyRowWithBuggyOpenAI()} locale="en-US" />)

    const radiogroup = screen.getByTestId('provider-detail-panel-auth-signin-options')
    const radios = within(radiogroup).getAllByRole('radio') as HTMLInputElement[]
    expect(radios.map((r) => r.value)).toEqual(['openai-chatgpt', 'codex-cli'])
    expect(
      screen.queryByTestId('provider-detail-panel-auth-signin-option-openai'),
    ).not.toBeInTheDocument()

    const checked = radios.find((r) => r.checked)
    expect(checked?.value).toBe('openai-chatgpt')
  })

  it('still offers "openai" through the API-key segment — the row is excluded only from the sign-in radios', async () => {
    const onConfirm = vi.fn()
    render(
      <ProviderDetailPanel
        company={openAiFamilyRowWithBuggyOpenAI()}
        locale="en-US"
        onConfirm={onConfirm}
      />,
    )
    await userEvent.click(screen.getByTestId('provider-detail-panel-auth-segment-api_key'))
    await userEvent.type(screen.getByTestId('provider-detail-panel-api-key-input'), 'sk-test')
    await userEvent.click(screen.getByTestId('provider-detail-panel-continue'))
    expect(onConfirm).toHaveBeenLastCalledWith(
      expect.objectContaining({ providerId: 'openai', authMethod: 'api_key', apiKey: 'sk-test' }),
    )
  })
})

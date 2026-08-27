/**
 * openai-signin-default.realcatalog.test.tsx — UAT-confirmed spec violation,
 * ADR-068 FR-006 (amended 2026-08-23, ADR-068 §8b): "The OpenAI pair MUST be
 * offered as `openai-chatgpt` (default — in-app sign-in, no CLI needed) and
 * `codex-cli` second."
 *
 * A real running instance's onboarding step 3 pre-selected `codex-cli`
 * instead, and the popular OpenAI tile carried
 * `data-testid="picker-popular-codex-cli"`. A related finding (F2) on the
 * same panel: a THIRD, unlabelled radio for the plain `openai` (API-key) row
 * also rendered, because the served catalog's `openai` row spuriously also
 * carries `sign_in` in its `auth_methods`.
 *
 * Root cause traced to the served catalog document
 * (`pkg/providers/catalog/data/providers_catalog.json`, served byte-for-byte
 * by GET /providers/catalog): its `providers[]` array is sorted
 * alphabetically by id (an assembly-job presentation detail — that file is a
 * daily-synced copy of a SEPARATE repository, `omnipus-provider-catalog`,
 * not something this project's SPA/backend release owns), which
 * accidentally puts `codex-cli` (sorts under "c") ahead of `openai` /
 * `openai-chatgpt` (sort under "o"), and the `openai` row's `auth_methods`
 * wrongly includes `sign_in`.
 *
 * Every OTHER suite in this directory (AuthMethodControl.test.tsx,
 * ProviderDetailPanel.test.tsx) already asserts `openai-chatgpt` is the
 * default — but they render against `src/test/fixtures/providers-catalog.json`,
 * a HAND-MAINTAINED stand-in that its own header says is not kept in sync
 * with the real snapshot. That gap is exactly how the wrong order/extra
 * option shipped uncaught: every existing test that exercises
 * AuthMethodControl/ProviderDetailPanel passed throughout.
 *
 * This test closes that gap by reading the ACTUAL committed catalog document
 * as-is — deliberately NOT "fixed" here, because fixing the data file would
 * require a release of the separate `omnipus-provider-catalog` repository —
 * and proving the SPA layer (`toCompanyRows`'s `primary`,
 * `ProviderDetailPanel`'s `signInOptionsFor`) produces the FR-006-correct
 * result despite the upstream data bug, exactly as the real onboarding flow
 * would.
 */

import { describe, it, expect } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { toCompanyRows } from './provider-picker-model'
import { ProviderDetailPanel, signInOptionsFor } from './ProviderDetailPanel'
import { SIGN_IN_HELPER_COPY } from './AuthMethodControl'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'

const REPO_ROOT = resolve(__dirname, '..', '..', '..')
const CATALOG_PATH = resolve(REPO_ROOT, 'pkg/providers/catalog/data/providers_catalog.json')

interface RealCatalogDoc {
  providers: CatalogProvider[]
}

function loadRealCatalog(): RealCatalogDoc {
  const raw = readFileSync(CATALOG_PATH, 'utf8')
  return JSON.parse(raw) as RealCatalogDoc
}

function openAiCompanyRow() {
  const doc = loadRealCatalog()
  const rows = toCompanyRows(doc.providers)
  const row = rows.find((r) => r.company === 'OpenAI')
  if (!row) throw new Error('real catalog has no "OpenAI" company row')
  return row
}

describe('OpenAI sign-in default against the REAL (still catalog-order-buggy) backend document', () => {
  it('sanity check: the real document still lists codex-cli ahead of openai/openai-chatgpt, and "openai" still spuriously carries sign_in — this test guards the SPA layer, not the data', () => {
    const doc = loadRealCatalog()
    const ids = doc.providers.filter((p) => p.company === 'OpenAI').map((p) => p.id)
    expect(ids.indexOf('codex-cli')).toBeLessThan(ids.indexOf('openai'))
    const openai = doc.providers.find((p) => p.id === 'openai')
    expect(openai?.auth_methods).toContain('sign_in')
  })

  it('the picker\'s primary variant for the OpenAI tile is "openai" (tier: popular), not "codex-cli"', () => {
    const row = openAiCompanyRow()
    // This is exactly what `data-testid={picker-popular-${row.primary.id}}`
    // renders in ProviderPicker.tsx — the UAT-observed
    // "picker-popular-codex-cli" testid traces to this value.
    expect(row.primary.id).toBe('openai')
    expect(row.primary.tier).toBe('popular')
  })

  it('signInOptionsFor excludes the spurious "openai" row and orders openai-chatgpt before codex-cli (F1 + F2)', () => {
    const row = openAiCompanyRow()
    const options = signInOptionsFor(row.variants)
    expect(options.map((o) => o.providerId)).toEqual(['openai-chatgpt', 'codex-cli'])
  })

  it('renders exactly two sign-in radios, openai-chatgpt checked and first, with FR-006 helper lines — no unlabelled third "openai" option', () => {
    render(<ProviderDetailPanel company={openAiCompanyRow()} locale="en-US" />)

    // FR-005: sign-in pre-selected over api_key when offered.
    expect(screen.getByTestId('provider-detail-panel-auth-segment-sign_in')).toHaveAttribute(
      'aria-pressed',
      'true',
    )

    const radiogroup = screen.getByTestId('provider-detail-panel-auth-signin-options')
    const radios = within(radiogroup).getAllByRole('radio') as HTMLInputElement[]
    // F2: exactly the FR-006 pair — no third "openai" radio.
    expect(radios.map((r) => r.value)).toEqual(['openai-chatgpt', 'codex-cli'])
    expect(screen.queryByTestId('provider-detail-panel-auth-signin-option-openai')).not.toBeInTheDocument()

    const checked = radios.find((r) => r.checked)
    expect(checked?.value).toBe('openai-chatgpt')

    const chatgptOption = screen.getByTestId(
      'provider-detail-panel-auth-signin-option-openai-chatgpt',
    )
    expect(within(chatgptOption).getByText(SIGN_IN_HELPER_COPY['openai-chatgpt'])).toBeInTheDocument()

    const codexOption = screen.getByTestId('provider-detail-panel-auth-signin-option-codex-cli')
    const codexRadio = within(codexOption).getByRole('radio') as HTMLInputElement
    expect(codexRadio.checked).toBe(false)
    expect(within(codexOption).getByText(SIGN_IN_HELPER_COPY['codex-cli'])).toBeInTheDocument()
  })

  it('a single dual-purpose variant with no dedicated sign-in sibling (e.g. xAI\'s shape) is left untouched — FR-049\'s "no code change here" guarantee', () => {
    // xAI's real catalog row is a single provider carrying BOTH api_key and
    // sign_in on the one entry — this is a DIFFERENT shape from OpenAI's
    // multi-row company and must not be affected by the F2 exclusion rule
    // (that rule only drops a dual-purpose variant when a DEDICATED
    // sign_in-only sibling exists in the same company; a lone dual-purpose
    // row has no such sibling and is the company's only path to sign-in at
    // all, so removing it would leave sign-in permanently unreachable).
    const dualOnly: CatalogProvider = {
      id: 'xai',
      name: 'xAI',
      company: 'xAI',
      api: 'https://api.x.ai/v1',
      protocol: 'openai-compatible',
      tier: 'popular',
      auth_methods: ['api_key', 'sign_in'],
      aliases: ['grok'],
      locality: 'cloud',
      models: [],
    }
    expect(signInOptionsFor([dualOnly]).map((o) => o.providerId)).toEqual(['xai'])
  })
})

import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import type { ProvidersCatalog } from '@/lib/api/generated/openapi-types'
import { toCompanyRows } from './provider-picker-model'
import { ProviderDetailPanel, signInOptionsFor } from './ProviderDetailPanel'

// WU-24 founder removal/KEEP decision, 2026-10-14 / L1 plan section 10.
// Use the real committed catalog, not the hand-maintained test catalog that
// could manufacture a green by simply omitting the unwanted providers.
function realCompanyRows() {
  const path = resolve(__dirname, '../../../pkg/providers/catalog/data/providers_catalog.json')
  const catalog = JSON.parse(readFileSync(path, 'utf8')) as ProvidersCatalog
  return toCompanyRows(catalog.providers)
}

function openAICompany() {
  const row = realCompanyRows().find((candidate) => candidate.company === 'OpenAI')
  if (!row) throw new Error('KEEP control: real catalog is missing the OpenAI company')
  return row
}

const retiredIds = ['codex' + '-cli', 'github' + '-copilot']

describe('WU-24 — provider sign-in has no subprocess model path', () => {
  it('offers no retired id in any company and exactly HTTP ChatGPT sign-in for OpenAI', () => {
    const offered = realCompanyRows().flatMap((row) => signInOptionsFor(row.variants))
    expect(offered.filter((option) => retiredIds.includes(option.providerId))).toEqual([])
    expect(signInOptionsFor(openAICompany().variants).map((option) => option.providerId)).toEqual([
      'openai-chatgpt',
    ])
  })

  it('renders the real OpenAI provider panel without a CLI option or terminal-login helper', () => {
    render(<ProviderDetailPanel company={openAICompany()} locale="en-US" />)
    const signIn = screen.getByTestId('provider-detail-panel-auth-signin')
    expect(within(signIn).getByRole('button', { name: /^Sign in$/ })).toBeEnabled()
    for (const id of retiredIds) {
      expect(screen.queryByTestId(`provider-detail-panel-auth-signin-option-${id}`)).not.toBeInTheDocument()
    }
    // One retained HTTP sign-in needs no method chooser. This assertion catches
    // a removed option rendered without its old identifier/test id as well.
    expect(within(signIn).queryAllByRole('radio')).toHaveLength(0)
    expect(within(signIn).queryByText(/official Codex app|sign in inside it|Copilot subscription/i)).not.toBeInTheDocument()
  })

  it('keeps the HTTP ChatGPT sign-in action reachable and sends its exact provider identity', () => {
    const onSignIn = vi.fn()
    render(<ProviderDetailPanel company={openAICompany()} locale="en-US" onSignIn={onSignIn} />)
    fireEvent.click(screen.getByTestId('provider-detail-panel-auth-signin-start'))
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(onSignIn).toHaveBeenCalledWith('openai-chatgpt')
  })
})

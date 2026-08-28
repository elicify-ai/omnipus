/**
 * ProviderRow.test.tsx — ADR-068 FR-034/FR-038 T068-26.
 *
 * Covers the six-value `Provider.status` switch this row renders (icon +
 * text, never colour alone) and the sign-in row's Sign-in/Manage action word
 * (FR-034): "Sign in" for a row that has never connected (or errored),
 * "Manage" once it has (`signed_in` / `expired`). `ProvidersSection.test.tsx`
 * covers what clicking Manage actually opens (dispatch to ManageSignInDialog
 * / ReSignInDialog / SignInDialog) — this file is the row's own rendering
 * contract in isolation.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ProviderRow, cliKindOf, isSignInCapable, type ProviderRowProps } from './ProviderRow'
import type { CatalogProvider, Provider } from '@/lib/api/generated/openapi-types'

function baseProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 'anthropic',
    name: 'anthropic',
    display_name: 'Anthropic',
    status: 'connected',
    auth_method: 'api_key',
    dependents: [],
    backs_default: false,
    models: [],
    ...overrides,
  }
}

function renderRow(overrides: Partial<ProviderRowProps> = {}) {
  const onConfigure = vi.fn()
  const onSignIn = vi.fn()
  const onManage = vi.fn()
  const utils = render(
    <ProviderRow
      provider={baseProvider()}
      title="Anthropic"
      showIcon
      onConfigure={onConfigure}
      onSignIn={onSignIn}
      onManage={onManage}
      {...overrides}
    />,
  )
  return { ...utils, onConfigure, onSignIn, onManage }
}

describe('ProviderRow — six-value status badge (FR-038)', () => {
  it('connected: icon + "Connected"', () => {
    renderRow({ provider: baseProvider({ status: 'connected' }) })
    const badge = screen.getByTestId('connected-badge-anthropic')
    expect(badge).toHaveTextContent('Connected')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('disconnected (not sign-in capable): icon + "Not configured"', () => {
    renderRow({ provider: baseProvider({ status: 'disconnected' }) })
    const badge = screen.getByTestId('disconnected-badge-anthropic')
    expect(badge).toHaveTextContent('Not configured')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('error: icon + "Error"', () => {
    renderRow({ provider: baseProvider({ status: 'error', error: 'upstream 403' }) })
    const badge = screen.getByTestId('error-badge-anthropic')
    expect(badge).toHaveTextContent('Error')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('unknown-provider: icon + "Unknown provider"', () => {
    renderRow({ provider: baseProvider({ status: 'unknown-provider' }) })
    const badge = screen.getByTestId('unknown-provider-badge-anthropic')
    expect(badge).toHaveTextContent('Unknown provider')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('signed_in with an account label: "Signed in · <label>"', () => {
    renderRow({
      provider: baseProvider({
        id: 'openai-chatgpt',
        status: 'signed_in',
        auth_method: 'sign_in',
        account_label: 'acct_7f3a',
      }),
    })
    const badge = screen.getByTestId('signed-in-badge-openai-chatgpt')
    expect(badge).toHaveTextContent('Signed in · acct_7f3a')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('signed_in without an account label: "Signed in" (no dangling separator)', () => {
    renderRow({
      provider: baseProvider({ id: 'openai-chatgpt', status: 'signed_in', auth_method: 'sign_in' }),
    })
    const badge = screen.getByTestId('signed-in-badge-openai-chatgpt')
    expect(badge).toHaveTextContent('Signed in')
    expect(badge.textContent).not.toContain('·')
  })

  it('expired: icon + "Session expired"', () => {
    renderRow({
      provider: baseProvider({ id: 'codex-cli', status: 'expired', auth_method: 'sign_in' }),
    })
    const badge = screen.getByTestId('expired-badge-codex-cli')
    expect(badge).toHaveTextContent('Session expired')
    expect(badge.querySelector('svg')).toBeInTheDocument()
  })

  it('disconnected + sign-in capable (never connected): "Not signed in"', () => {
    renderRow({
      provider: baseProvider({ id: 'codex-cli', status: 'disconnected', auth_method: 'sign_in' }),
    })
    const badge = screen.getByTestId('not-signed-in-badge-codex-cli')
    expect(badge).toHaveTextContent('Not signed in')
  })
})

describe('ProviderRow — Sign-in / Manage action (FR-034)', () => {
  it('a never-connected sign-in row reads "Sign in" and calls onSignIn', () => {
    const { onSignIn, onManage } = renderRow({
      provider: baseProvider({ id: 'codex-cli', status: 'disconnected', auth_method: 'sign_in' }),
    })
    const btn = screen.getByTestId('sign-in-btn-codex-cli')
    expect(btn).toHaveTextContent('Sign in')
    btn.click()
    expect(onSignIn).toHaveBeenCalledTimes(1)
    expect(onManage).not.toHaveBeenCalled()
  })

  it('an errored sign-in row still reads "Sign in" (not Manage)', () => {
    renderRow({
      provider: baseProvider({ id: 'codex-cli', status: 'error', auth_method: 'sign_in', error: 'boom' }),
    })
    expect(screen.getByTestId('sign-in-btn-codex-cli')).toHaveTextContent('Sign in')
  })

  it('a signed_in row reads "Manage" and calls onManage, not onSignIn', () => {
    const { onSignIn, onManage } = renderRow({
      provider: baseProvider({
        id: 'openai-chatgpt',
        status: 'signed_in',
        auth_method: 'sign_in',
        account_label: 'acct_7f3a',
      }),
    })
    const btn = screen.getByTestId('manage-btn-openai-chatgpt')
    expect(btn).toHaveTextContent('Manage')
    btn.click()
    expect(onManage).toHaveBeenCalledTimes(1)
    expect(onSignIn).not.toHaveBeenCalled()
  })

  it('an expired row reads "Manage" and calls onManage', () => {
    const { onManage } = renderRow({
      provider: baseProvider({ id: 'codex-cli', status: 'expired', auth_method: 'sign_in' }),
    })
    const btn = screen.getByTestId('manage-btn-codex-cli')
    expect(btn).toHaveTextContent('Manage')
    btn.click()
    expect(onManage).toHaveBeenCalledTimes(1)
  })

  it('a non-sign-in connected row reads "Edit" via onConfigure, never Sign in/Manage', () => {
    const { onConfigure } = renderRow({ provider: baseProvider({ status: 'connected' }) })
    const btn = screen.getByTestId('configure-btn-anthropic')
    expect(btn).toHaveTextContent('Edit')
    btn.click()
    expect(onConfigure).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('sign-in-btn-anthropic')).not.toBeInTheDocument()
    expect(screen.queryByTestId('manage-btn-anthropic')).not.toBeInTheDocument()
  })
})

describe('cliKindOf / isSignInCapable', () => {
  const codexEntry: CatalogProvider = {
    id: 'codex-cli',
    name: 'Codex CLI',
    company: 'Codex CLI',
    api: 'codex',
    protocol: 'cli',
    tier: 'standard',
    auth_methods: ['sign_in'],
    aliases: [],
    locality: 'local',
    cli_kind: 'codex',
    models: [],
  }

  it('resolves cli_kind from the catalog entry first', () => {
    expect(cliKindOf(baseProvider({ id: 'codex-cli' }), codexEntry)).toBe('codex')
  })

  it('falls back to the configured row own cli_kind when no entry resolved', () => {
    expect(cliKindOf(baseProvider({ id: 'codex-cli', cli_kind: 'copilot' }))).toBe('copilot')
  })

  it('is undefined for a device_code row (no cli_kind either side)', () => {
    expect(cliKindOf(baseProvider({ id: 'openai-chatgpt', auth_method: 'sign_in' }))).toBeUndefined()
  })

  it('isSignInCapable prefers the catalog entry auth_methods over the row field', () => {
    expect(isSignInCapable(baseProvider({ auth_method: 'api_key' }), { ...codexEntry, auth_methods: ['sign_in'] })).toBe(true)
  })
})

import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import type { CatalogProvider, Provider } from '@/lib/api/generated/openapi-types'
import { ProviderRow } from '@/components/settings/ProviderRow'
import { AuthMethodControl } from './AuthMethodControl'
import { toCompanyRows } from './provider-picker-model'
import { ProviderDetailPanel } from './ProviderDetailPanel'

// Founder WU-24 follow-up, 2026-10-14: HTTP ChatGPT has one sign-in
// method (device code). These are independent generated-type UI inputs,
// deliberately NOT the server's embedded catalog or its hand-maintained copy.
const HTTP_CHATGPT: CatalogProvider = {
  id: 'openai-chatgpt',
  name: 'ChatGPT',
  company: 'OpenAI',
  api: 'https://chatgpt.com/backend-api/codex',
  protocol: 'openai-compatible',
  tier: 'standard',
  auth_methods: ['sign_in'],
  aliases: [],
  locality: 'cloud',
  models: [],
}

const OPENAI_API: CatalogProvider = {
  ...HTTP_CHATGPT,
  id: 'openai',
  name: 'OpenAI API',
  api: 'https://api.openai.com/v1',
  tier: 'popular',
  auth_methods: ['api_key'],
}

const removedIds = ['codex' + '-cli', 'github' + '-copilot']

function assertNoLocalLoginControls(root: HTMLElement) {
  expect(within(root).queryAllByRole('radio')).toHaveLength(0)
  expect(within(root).queryByRole('button', { name: /existing Codex login|import.*login|check sign-in/i })).not.toBeInTheDocument()
  expect(within(root).queryByText(/run in a terminal|official Codex app|sign in inside it|Copilot subscription/i)).not.toBeInTheDocument()
}

describe('WU-24 — real provider controls, independent of server catalog cleanup', () => {
  it('AuthMethodControl offers one in-app sign-in action targeting HTTP ChatGPT', () => {
    const onSignIn = vi.fn()
    const onMethodChange = vi.fn()
    const onProviderChange = vi.fn()
    render(
      <AuthMethodControl
        apiKeyOffered={false}
        companyName="OpenAI"
        signInOptions={[{ providerId: HTTP_CHATGPT.id, label: HTTP_CHATGPT.name }]}
        onSignIn={onSignIn}
        onMethodChange={onMethodChange}
        onSignInProviderChange={onProviderChange}
      />,
    )
    const control = screen.getByTestId('auth-method-control')
    const signIn = within(control).getByRole('button', { name: /^Sign in$/ })
    expect(signIn).toBeEnabled()
    assertNoLocalLoginControls(control)
    fireEvent.click(signIn)
    expect(onSignIn).toHaveBeenCalledExactlyOnceWith('openai-chatgpt')
    expect(onMethodChange).toHaveBeenCalledExactlyOnceWith('sign_in')
    expect(onProviderChange).toHaveBeenCalledExactlyOnceWith('openai-chatgpt')
  })

  it('ProviderDetailPanel keeps API credentials separate from the sole HTTP sign-in method', () => {
    const company = toCompanyRows([OPENAI_API, HTTP_CHATGPT])[0]
    if (!company) throw new Error('independent OpenAI fixture did not produce a company row')
    const onSignIn = vi.fn()
    render(<ProviderDetailPanel company={company} locale="en-US" onSignIn={onSignIn} />)
    const panel = screen.getByTestId('provider-detail-panel')
    expect(within(panel).getByTestId('provider-detail-panel-auth-segment-sign_in')).toHaveAttribute('aria-pressed', 'true')
    expect(within(panel).getByTestId('provider-detail-panel-auth-segment-api_key')).toBeInTheDocument()
    assertNoLocalLoginControls(panel)
    fireEvent.click(within(panel).getByRole('button', { name: /^Sign in$/ }))
    expect(onSignIn).toHaveBeenCalledExactlyOnceWith('openai-chatgpt')
  })

  it.each(removedIds)('ProviderRow preserves the loud unknown error for %s without a login/recovery action', (id) => {
    // This is the valid configured-row projection the server now returns for
    // an unknown kind, not a synthetic obsolete catalog row or migration.
    const error = `unknown provider "${id}"`
    const provider: Provider = {
      id,
      name: id,
      status: 'unknown-provider',
      auth_method: 'sign_in',
      models: [],
      dependents: [],
      backs_default: false,
      error,
    }
    const onSignIn = vi.fn()
    const onManage = vi.fn()
    render(
      <ProviderRow
        provider={provider}
        title={id}
        showIcon={false}
        onConfigure={vi.fn()}
        onSignIn={onSignIn}
        onManage={onManage}
      />,
    )
    const row = screen.getByTestId(`provider-row-${id}`)
    expect(within(row).getByText('Unknown provider', { exact: true })).toBeInTheDocument()
    expect(within(row).getByText(error, { exact: true })).toBeInTheDocument()
    expect(within(row).queryByRole('button', { name: /^(Sign in|Manage|Check sign-in)$/ })).not.toBeInTheDocument()
    expect(onSignIn).not.toHaveBeenCalled()
    expect(onManage).not.toHaveBeenCalled()
  })

  it('ProviderRow keeps first-time HTTP ChatGPT sign-in reachable', () => {
    const onSignIn = vi.fn()
    const provider: Provider = {
      id: HTTP_CHATGPT.id,
      name: HTTP_CHATGPT.name,
      status: 'disconnected',
      auth_method: 'sign_in',
      models: [],
      dependents: [],
      backs_default: false,
    }
    render(
      <ProviderRow
        provider={provider}
        entry={HTTP_CHATGPT}
        title="ChatGPT"
        showIcon={false}
        onConfigure={vi.fn()}
        onSignIn={() => onSignIn(provider.id)}
      />,
    )
    const row = screen.getByTestId('provider-row-openai-chatgpt')
    expect(within(row).getByText('Not signed in', { exact: true })).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: /^Sign in$/ }))
    expect(onSignIn).toHaveBeenCalledExactlyOnceWith('openai-chatgpt')
    assertNoLocalLoginControls(row)
  })
})

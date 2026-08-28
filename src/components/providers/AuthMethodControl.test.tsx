/**
 * AuthMethodControl.test.tsx — ADR-068 spec TDD plan row 26.
 *
 * Scenarios covered:
 *   • Scenario Outline "Auth methods offered per provider" (all five rows)
 *   • Scenario "OpenAI sign-in offers two named providers", as AMENDED by
 *     ADR-068 §8b decision 2 / FR-006: `openai-chatgpt` is the DEFAULT of the
 *     pair and the withdrawn "relies on OpenAI's stated tolerance" caveat must
 *     appear nowhere. The scenario prose in the spec body still carries the
 *     pre-amendment wording (codex-cli default, tolerance label); FR-006 as
 *     amended is the authority and is what these assertions follow.
 *   • The FR-028 half testable without a router: switching the segment reveals
 *     the key field and hides the sign-in radios in place — no navigation, no
 *     unmount of the surrounding panel.
 *
 * The sign-in options every case uses are read out of the shared 190-entry
 * catalog fixture rather than typed here, so a catalog that stopped offering
 * `sign_in` on a row would fail these tests instead of passing them by
 * accident.
 */

import * as React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { CATALOG_PROVIDERS } from '@/test/fixtures/providersCatalog'
import type { CatalogProvider } from '@/lib/api/generated/openapi-types'
import {
  AuthMethodControl,
  SIGN_IN_HELPER_COPY,
  signInHelperCopy,
  type SignInOption,
} from './AuthMethodControl'

/** The exact caveat ADR-068 §8b withdrew. It must never reach the DOM. */
const WITHDRAWN_CAVEAT = "relies on OpenAI's stated tolerance"

function catalogRow(id: string): CatalogProvider {
  const row = CATALOG_PROVIDERS.find((p) => p.id === id)
  if (!row) throw new Error(`fixture is missing provider ${id}`)
  return row
}

/** Sign-in options exactly as `ProviderDetailPanel` derives them: catalog order. */
function signInOptionsFor(...ids: string[]): SignInOption[] {
  return ids
    .map(catalogRow)
    .filter((row) => row.auth_methods.includes('sign_in'))
    .map((row) => ({ providerId: row.id, label: row.name }))
}

const KEY_FIELD = <input data-testid="key-field" aria-label="API key" type="password" />

describe('AuthMethodControl — auth methods offered per provider (FR-005)', () => {
  // The outline's five rows. `signInIds` is what the CATALOG offers for that
  // company; the two OpenAI sign-in rows are the pair FR-006 names.
  const rows: Array<{
    name: string
    signInIds: string[]
    expectSignIn: boolean
  }> = [
    { name: 'anthropic', signInIds: [], expectSignIn: false },
    { name: 'google', signInIds: [], expectSignIn: false },
    { name: 'xai', signInIds: [], expectSignIn: false },
    { name: 'github-copilot', signInIds: ['github-copilot'], expectSignIn: true },
    { name: 'openai', signInIds: ['openai-chatgpt', 'codex-cli'], expectSignIn: true },
  ]

  for (const row of rows) {
    it(`${row.name}: sign-in control is ${row.expectSignIn ? 'present and pre-selected' : 'absent from the DOM'}`, () => {
      render(
        <AuthMethodControl
          signInOptions={signInOptionsFor(...row.signInIds)}
          companyName={row.name}
          apiKeyField={KEY_FIELD}
        />,
      )

      if (row.expectSignIn) {
        const signIn = screen.getByTestId('auth-method-control-segment-sign_in')
        // FR-005: pre-selected where present.
        expect(signIn).toHaveAttribute('aria-pressed', 'true')
        expect(screen.getByTestId('auth-method-control-segment-api_key')).toHaveAttribute(
          'aria-pressed',
          'false',
        )
        // Sign-in is the selected method, so the key field is not rendered yet.
        expect(screen.queryByTestId('key-field')).not.toBeInTheDocument()
        expect(screen.getByTestId('auth-method-control-signin-start')).toBeInTheDocument()
      } else {
        // Absent, not merely disabled — no segment, no radios, no button, and
        // no forward-looking copy anywhere (Qualitative prohibitions; xAI stays
        // key-only until its catalog row carries sign_in, FR-049).
        expect(screen.queryByTestId('auth-method-control-segment')).not.toBeInTheDocument()
        expect(screen.queryByTestId('auth-method-control-signin')).not.toBeInTheDocument()
        expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
        expect(screen.queryByText(/sign in/i)).not.toBeInTheDocument()
        // The API-key field is the whole control.
        expect(screen.getByTestId('key-field')).toBeInTheDocument()
      }
    })
  }

  it('reports the resolved default method to its caller without any interaction', () => {
    const onMethodChange = vi.fn()
    const { unmount } = render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('openai-chatgpt', 'codex-cli')}
        onMethodChange={onMethodChange}
        apiKeyField={KEY_FIELD}
      />,
    )
    expect(onMethodChange).toHaveBeenCalledWith('sign_in')
    unmount()

    const keyOnly = vi.fn()
    render(<AuthMethodControl signInOptions={[]} onMethodChange={keyOnly} apiKeyField={KEY_FIELD} />)
    expect(keyOnly).toHaveBeenCalledWith('api_key')
  })

  it('offers no sign-in control when the company has only sign-in rows and no key row', () => {
    render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('github-copilot')}
        apiKeyOffered={false}
        apiKeyField={KEY_FIELD}
      />,
    )
    // One method means no segment to switch: a single-button segmented control
    // would claim a choice the operator does not have.
    expect(screen.queryByTestId('auth-method-control-segment')).not.toBeInTheDocument()
    expect(screen.getByTestId('auth-method-control-signin-start')).toBeInTheDocument()
    expect(screen.queryByTestId('key-field')).not.toBeInTheDocument()
  })
})

describe('AuthMethodControl — the OpenAI pair (FR-006 as amended, ADR-068 §8b)', () => {
  function renderPair(overrides: Partial<React.ComponentProps<typeof AuthMethodControl>> = {}) {
    return render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('openai-chatgpt', 'codex-cli')}
        companyName="OpenAI"
        apiKeyField={KEY_FIELD}
        {...overrides}
      />,
    )
  }

  it('shows two named radio options with openai-chatgpt first and pre-selected', () => {
    renderPair()

    const radios = screen.getAllByRole('radio') as HTMLInputElement[]
    expect(radios.map((r) => r.value)).toEqual(['openai-chatgpt', 'codex-cli'])
    expect(radios[0].checked).toBe(true)
    expect(radios[1].checked).toBe(false)

    // Labels are the catalog rows' own names, not SPA constants.
    expect(screen.getByText(catalogRow('openai-chatgpt').name)).toBeInTheDocument()
    expect(screen.getByText(catalogRow('codex-cli').name)).toBeInTheDocument()
  })

  it('carries the amended helper copy and never the withdrawn caveat', () => {
    const { container } = renderPair()

    expect(screen.getByText("Uses your ChatGPT plan's included usage")).toBeInTheDocument()
    expect(screen.getByText('Drives the official Codex app; sign in inside it')).toBeInTheDocument()
    expect(container.textContent ?? '').not.toContain(WITHDRAWN_CAVEAT)
    expect(SIGN_IN_HELPER_COPY['openai-chatgpt']).not.toContain(WITHDRAWN_CAVEAT)
    expect(signInHelperCopy('codex-cli')).not.toContain(WITHDRAWN_CAVEAT)

    // Each helper line is programmatically tied to its own radio (1.3.1).
    const chatgpt = screen.getByDisplayValue('openai-chatgpt')
    const describedBy = chatgpt.getAttribute('aria-describedby')
    expect(describedBy).toBeTruthy()
    expect(document.getElementById(describedBy as string)?.textContent).toBe(
      "Uses your ChatGPT plan's included usage",
    )
  })

  it('signs in as openai-chatgpt when the radios are never touched (DoD)', async () => {
    const onSignIn = vi.fn()
    const onSignInProviderChange = vi.fn()
    renderPair({ onSignIn, onSignInProviderChange })

    expect(onSignInProviderChange).toHaveBeenCalledWith('openai-chatgpt')

    await userEvent.click(screen.getByTestId('auth-method-control-signin-start'))
    expect(onSignIn).toHaveBeenCalledWith('openai-chatgpt')
  })

  it('switches the persisted provider to codex-cli when the operator picks it', async () => {
    const onSignIn = vi.fn()
    const onSignInProviderChange = vi.fn()
    renderPair({ onSignIn, onSignInProviderChange })

    await userEvent.click(screen.getByDisplayValue('codex-cli'))
    expect(onSignInProviderChange).toHaveBeenLastCalledWith('codex-cli')
    expect((screen.getByDisplayValue('codex-cli') as HTMLInputElement).checked).toBe(true)
    expect((screen.getByDisplayValue('openai-chatgpt') as HTMLInputElement).checked).toBe(false)

    await userEvent.click(screen.getByTestId('auth-method-control-signin-start'))
    expect(onSignIn).toHaveBeenCalledWith('codex-cli')
  })

  it('labels the radio group and names the vendor on the sign-in segment', () => {
    renderPair()
    const group = screen.getByRole('radiogroup')
    expect(group).toHaveAccessibleName('Sign-in method')
    expect(screen.getByTestId('auth-method-control-segment-sign_in')).toHaveTextContent(
      'Sign in with OpenAI',
    )
    expect(screen.getByRole('group', { name: 'Authentication method' })).toBeInTheDocument()
  })
})

describe('AuthMethodControl — segment switching stays in place (FR-028)', () => {
  it('reveals the key field and hides the sign-in radios without navigation', async () => {
    const onMethodChange = vi.fn()
    render(
      <div data-testid="surrounding-panel">
        <AuthMethodControl
          signInOptions={signInOptionsFor('openai-chatgpt', 'codex-cli')}
          companyName="OpenAI"
          apiKeyField={KEY_FIELD}
          onMethodChange={onMethodChange}
        />
      </div>,
    )

    expect(screen.getByRole('radiogroup')).toBeInTheDocument()
    expect(screen.queryByTestId('key-field')).not.toBeInTheDocument()

    await userEvent.click(screen.getByTestId('auth-method-control-segment-api_key'))

    expect(onMethodChange).toHaveBeenLastCalledWith('api_key')
    expect(screen.getByTestId('key-field')).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
    expect(screen.queryByTestId('auth-method-control-signin-start')).not.toBeInTheDocument()
    // The surrounding panel never unmounted — nothing navigated.
    expect(screen.getByTestId('surrounding-panel')).toBeInTheDocument()
    expect(screen.getByTestId('auth-method-control-segment-api_key')).toHaveAttribute(
      'aria-pressed',
      'true',
    )

    // And back again, with the radio choice intact.
    await userEvent.click(screen.getByTestId('auth-method-control-segment-sign_in'))
    expect(screen.getByRole('radiogroup')).toBeInTheDocument()
    expect((screen.getByDisplayValue('openai-chatgpt') as HTMLInputElement).checked).toBe(true)
  })

  it('is operable by keyboard alone', async () => {
    render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('openai-chatgpt', 'codex-cli')}
        apiKeyField={KEY_FIELD}
      />,
    )

    await userEvent.tab()
    expect(screen.getByTestId('auth-method-control-segment-sign_in')).toHaveFocus()
    await userEvent.tab()
    expect(screen.getByTestId('auth-method-control-segment-api_key')).toHaveFocus()
    await userEvent.keyboard('{Enter}')
    expect(screen.getByTestId('key-field')).toBeInTheDocument()
  })
})

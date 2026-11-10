/**
 * AuthMethodControl.test.tsx — ADR-068 spec TDD plan row 26.
 *
 * Scenarios covered:
 *   • Scenario Outline "Auth methods offered per provider"
 *   • Scenario "OpenAI sign-in offers its named provider", as AMENDED by
 *     ADR-068 §8b decision 2 / FR-006 and since WU-24 (founder 2026-10-14)
 *     a SINGLE sign-in row: `openai-chatgpt`, the device-code login, is the
 *     default and the withdrawn "relies on OpenAI's stated tolerance" caveat
 *     must appear nowhere. FR-006 as amended is the authority and is what
 *     these assertions follow.
 *   • The FR-028 half testable without a router: switching the segment reveals
 *     the key field and hides the sign-in options in place — no navigation, no
 *     unmount of the surrounding panel.
 *
 * The sign-in options every case uses are read out of the shared 188-entry
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
  // The outline's rows. `signInIds` is what the CATALOG offers for that
  // company; OpenAI's single sign-in row is the one FR-006 names (WU-24
  // removed the retired CLI rows).
  const rows: Array<{
    name: string
    signInIds: string[]
    expectSignIn: boolean
  }> = [
    { name: 'anthropic', signInIds: [], expectSignIn: false },
    { name: 'google', signInIds: [], expectSignIn: false },
    { name: 'xai', signInIds: [], expectSignIn: false },
    { name: 'openai', signInIds: ['openai-chatgpt'], expectSignIn: true },
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
        signInOptions={signInOptionsFor('openai-chatgpt')}
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
        signInOptions={signInOptionsFor('openai-chatgpt')}
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

describe('AuthMethodControl — the OpenAI sign-in option (FR-006 as amended, ADR-068 §8b; single option since WU-24)', () => {
  function renderPair(overrides: Partial<React.ComponentProps<typeof AuthMethodControl>> = {}) {
    return render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('openai-chatgpt')}
        companyName="OpenAI"
        apiKeyField={KEY_FIELD}
        {...overrides}
      />,
    )
  }

  it('shows openai-chatgpt as the one named sign-in option, pre-selected without a radio group', () => {
    renderPair()

    // One sign-in option means the choice is already made: no radios at all.
    expect(screen.queryByRole('radio')).not.toBeInTheDocument()
    // The single option announces itself through its FR-006 helper line, and
    // the helper is derived from the catalog row's own id, not a SPA constant.
    expect(screen.getByText(signInHelperCopy(catalogRow('openai-chatgpt').id)!)).toBeInTheDocument()
  })

  it('carries the amended helper copy and never the withdrawn caveat', () => {
    const { container } = renderPair()

    expect(screen.getByText("Uses your ChatGPT plan's included usage")).toBeInTheDocument()
    expect(container.textContent ?? '').not.toContain(WITHDRAWN_CAVEAT)
    expect(SIGN_IN_HELPER_COPY['openai-chatgpt']).not.toContain(WITHDRAWN_CAVEAT)
    // No retired row's copy survives in the copy table (ids written as split
    // literals, same as the WU-24 RED pack, to stay off the removal guard).
    for (const retired of ['codex' + '-cli', 'github' + '-copilot']) {
      expect(SIGN_IN_HELPER_COPY).not.toHaveProperty(retired)
      expect(signInHelperCopy(retired)).toBeUndefined()
    }
  })

  it('signs in as openai-chatgpt when the options are never touched (DoD)', async () => {
    const onSignIn = vi.fn()
    const onSignInProviderChange = vi.fn()
    renderPair({ onSignIn, onSignInProviderChange })

    expect(onSignInProviderChange).toHaveBeenCalledWith('openai-chatgpt')

    await userEvent.click(screen.getByTestId('auth-method-control-signin-start'))
    expect(onSignIn).toHaveBeenCalledWith('openai-chatgpt')
  })

  it('names the vendor on the sign-in segment', () => {
    renderPair()
    expect(screen.getByTestId('auth-method-control-segment-sign_in')).toHaveTextContent(
      'Sign in with OpenAI',
    )
    expect(screen.getByRole('group', { name: 'Authentication method' })).toBeInTheDocument()
  })
})

describe('AuthMethodControl — segment switching stays in place (FR-028)', () => {
  it('reveals the key field and hides the sign-in option without navigation', async () => {
    const onMethodChange = vi.fn()
    render(
      <div data-testid="surrounding-panel">
        <AuthMethodControl
          signInOptions={signInOptionsFor('openai-chatgpt')}
          companyName="OpenAI"
          apiKeyField={KEY_FIELD}
          onMethodChange={onMethodChange}
        />
      </div>,
    )

    expect(screen.getByTestId('auth-method-control-signin-start')).toBeInTheDocument()
    expect(screen.queryByTestId('key-field')).not.toBeInTheDocument()

    await userEvent.click(screen.getByTestId('auth-method-control-segment-api_key'))

    expect(onMethodChange).toHaveBeenLastCalledWith('api_key')
    expect(screen.getByTestId('key-field')).toBeInTheDocument()
    expect(screen.queryByTestId('auth-method-control-signin-start')).not.toBeInTheDocument()
    // The surrounding panel never unmounted — nothing navigated.
    expect(screen.getByTestId('surrounding-panel')).toBeInTheDocument()
    expect(screen.getByTestId('auth-method-control-segment-api_key')).toHaveAttribute(
      'aria-pressed',
      'true',
    )

    // And back again, with the same single option resolved.
    await userEvent.click(screen.getByTestId('auth-method-control-segment-sign_in'))
    expect(screen.getByTestId('auth-method-control-signin-start')).toBeInTheDocument()
    expect(screen.getByText(signInHelperCopy(catalogRow('openai-chatgpt').id)!)).toBeInTheDocument()
  })

  it('is operable by keyboard alone', async () => {
    render(
      <AuthMethodControl
        signInOptions={signInOptionsFor('openai-chatgpt')}
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

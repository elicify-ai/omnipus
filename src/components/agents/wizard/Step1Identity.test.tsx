// Step1Identity.test.tsx — the four-state provider-catalog fix.
//
// Bug: the Step 1 model picker collapsed four genuinely different
// provider-catalog states into one message — "Connect a provider in
// Settings to pick a model" with an empty dropdown — true for only ONE of
// the four:
//   1. providers query LOADING          → was shown as "no provider" (false)
//   2. providers query FAILED           → was shown as "no provider" (false)
//   3. provider connected, catalog EMPTY (backend `warning`) → was shown as
//      "no provider" (false — a provider IS connected)
//   4. genuinely NO provider connected  → the one case where it was true
//
// Motivating incident: CI logged the gateway's upstream fetch to
// openrouter.ai failing 9 times in a row with `context canceled` (zero
// successes) while the /providers endpoint itself was healthy — a direct
// curl from the same worker returned 200 in 0.46s — and the picker still
// told the user no provider was connected. This file drives Step1Identity
// through the full `CreateAgentWizard` (mirrors the render harness used by
// the other `Step1Identity.*.test.tsx` files) with the REAL `ModelSelector`
// mounted (not stubbed), so the assertions exercise the actual message the
// user sees, not a mock's approximation of it.

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { CreateAgentWizard } from '../CreateAgentWizard'
import { useUiStore } from '@/store/ui'
import type { Provider } from '@/lib/api/generated/openapi-types'

// cmdk (used by the real ModelSelector's <Command>) needs ResizeObserver and
// scrollIntoView, neither available in jsdom — same stub as
// model-selector.test.tsx.
beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, toasts: [] })
  })
})

function connectedProvider(overrides: Partial<Provider> = {}): Provider {
  return {
    id: 'openrouter',
    name: 'openrouter',
    display_name: 'OpenRouter',
    status: 'connected',
    auth_method: 'api_key',
    dependents: [],
    backs_default: false,
    models: [],
    has_models_endpoint: true,
    has_api_key: true,
    ...overrides,
  }
}

function renderWizard(props: Partial<Parameters<typeof CreateAgentWizard>[0]> = {}) {
  return render(
    <CreateAgentWizard
      initialType={props.initialType ?? 'Main'}
      onSubmit={props.onSubmit ?? vi.fn().mockResolvedValue(undefined)}
      onClose={props.onClose ?? vi.fn()}
      connectedProviders={props.connectedProviders ?? []}
      providersLoading={props.providersLoading ?? false}
      {...(props.providersError !== undefined ? { providersError: props.providersError } : {})}
      {...(props.onRetryProviders ? { onRetryProviders: props.onRetryProviders } : {})}
    />,
  )
}

describe('Step1Identity — model picker, four provider-catalog states', () => {
  it('state 1 (loading): stays clickable and shows loading inside the popover, never the "connect a provider" claim', () => {
    renderWizard({ providersLoading: true, connectedProviders: [] })
    const trigger = screen.getByTestId('wizard-model')

    // The trigger must remain the REAL combobox while the catalog loads. It
    // used to be swapped for a non-interactive placeholder wearing the same
    // test id, which silently swallowed the user's click for the 0.1-4.5s the
    // fetch takes — the root cause of the create-agent e2e failure
    // (root-caused 2026-08-14). aria-busy carries the loading signal that the
    // placeholder used to carry, without a second element.
    expect(trigger).toHaveAttribute('role', 'combobox')
    expect(trigger).toHaveAttribute('aria-busy', 'true')
    expect(trigger).not.toHaveTextContent(/connect a provider/i)

    fireEvent.click(trigger)
    expect(screen.getByText(/loading models/i)).toBeInTheDocument()
  })

  it('state 2 (error): shows the real fetch-failure message and a working Retry, never the "connect a provider" claim', () => {
    const onRetryProviders = vi.fn()
    renderWizard({
      providersLoading: false,
      providersError: 'Get "https://openrouter.ai/api/v1/models": context canceled',
      connectedProviders: [],
      onRetryProviders,
    })
    expect(screen.getByTestId('wizard-model')).toHaveTextContent(/context canceled/)
    expect(screen.getByTestId('wizard-model')).not.toHaveTextContent(/connect a provider/i)
    screen.getByTestId('wizard-model-retry').click()
    expect(onRetryProviders).toHaveBeenCalledTimes(1)
  })

  it('state 3 (connected, empty catalog with backend warning): shows the provider\'s own warning text, never the "connect a provider" claim', () => {
    renderWizard({
      providersLoading: false,
      connectedProviders: [
        connectedProvider({ models: [], warning: 'could not fetch upstream model list: status 429' }),
      ],
    })
    expect(screen.getByTestId('wizard-model')).toHaveTextContent(
      /model list unavailable: could not fetch upstream model list: status 429/i,
    )
    expect(screen.getByTestId('wizard-model')).not.toHaveTextContent(/connect a provider/i)
  })

  it('state 3b (connected, empty catalog, no warning): an honest "no models available" message, not "connect a provider" (no provider IS connected)', () => {
    renderWizard({
      providersLoading: false,
      connectedProviders: [connectedProvider({ models: [] })],
    })
    expect(screen.getByTestId('wizard-model')).toHaveTextContent(/connected provider has no models available/i)
    expect(screen.getByTestId('wizard-model')).not.toHaveTextContent(/connect a provider/i)
  })

  it('state 4 (no provider connected at all): keeps today\'s exact message — the one true case', () => {
    renderWizard({ providersLoading: false, connectedProviders: [] })
    expect(screen.getByTestId('wizard-model')).toHaveTextContent(/connect a provider in settings to pick a model/i)
  })
})

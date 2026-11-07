/**
 * Issue #1620 T6 / CHECK F2. Team-lead's Round 3 product decision is the oracle:
 * an unfinished or failed default-model request is not proof of no default.
 * Exercise real query state and the real section-to-dialog path; mock only
 * the API/network edges. Each test starts with an empty query cache.
 */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import * as api from '@/lib/api'
import type { AppState, DefaultModel, Provider } from '@/lib/api/generated/openapi-types'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { ProvidersSection } from './ProvidersSection'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn(),
    fetchProvidersCatalog: vi.fn(),
    getDefaultModel: vi.fn(),
    fetchAppState: vi.fn(),
    deleteProvider: vi.fn(),
  }
})

const DEFAULT_PAIR: DefaultModel = { provider: 'openrouter', model: 'openai/gpt-5.4' }
const APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}
const REMOVED: Provider = {
  id: 'workshop',
  name: 'workshop',
  display_name: 'Workshop',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: false,
  custom: true,
  locality: 'local',
  models: ['workshop-model'],
  dependents: [{ id: 'scout', name: 'Scout', role: 'primary' }],
}
const OTHER_PROVIDER: Provider = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: true,
  models: ['anthropic/claude-3.5-haiku', 'openai/gpt-5.4'],
  dependents: [],
}

let client: QueryClient
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  vi.mocked(api.fetchProviders).mockResolvedValue([REMOVED, OTHER_PROVIDER])
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE)
})

afterEach(() => {
  cleanup()
  client.clear()
})

async function openRemoval() {
  await screen.findByTestId('provider-row-workshop')
  fireEvent.click(screen.getByTestId('configure-btn-workshop'))
  fireEvent.click(await screen.findByTestId('remove-provider-btn-workshop'))
  const dialog = await screen.findByRole('alertdialog')
  const primary = within(dialog).getByTestId('dependent-group-primary')
  expect(within(primary).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Scout'])
  return { dialog, primary }
}

it('says Checking the default model and disables Remove only while the cold request is pending', async () => {
  let resolveDefault: (pair: DefaultModel) => void = () => { throw new Error('default request resolver not installed') }
  const pending = new Promise<DefaultModel>((resolve) => { resolveDefault = resolve })
  vi.mocked(api.getDefaultModel).mockReturnValue(pending)
  render(<QueryClientProvider client={client}><ProvidersSection /></QueryClientProvider>)
  const { dialog, primary } = await openRemoval()
  expect(api.getDefaultModel).toHaveBeenCalledTimes(1)

  expect.soft(primary.querySelector('p')?.textContent,
    'pending default-model request needs its own primary-agents heading').toBe('Checking the default model…')
  expect.soft(dialog, 'pending is not evidence that agents lose their model')
    .not.toHaveTextContent(/left without a model/i)
  expect.soft(dialog, 'do not invent a fallback before the request settles').not.toHaveTextContent(/fall back/i)
  expect.soft(dialog).not.toHaveTextContent('OpenRouter')
  expect.soft(dialog).not.toHaveTextContent('openai/gpt-5.4')
  expect.soft(within(dialog).getByRole('button', { name: /^Remove$/ }),
    'removal must wait for the default-model request to settle').toBeDisabled()
  expect(api.deleteProvider).not.toHaveBeenCalled()

  // Complete the SAME request: the loading gate must lift, and the dialog
  // must then describe the real selected pair rather than stay unknown.
  await act(async () => { resolveDefault(DEFAULT_PAIR) })
  await waitFor(() => expect(primary).toHaveTextContent(/fall back to the default provider OpenRouter/))
  expect(primary).toHaveTextContent('openai/gpt-5.4')
  expect(within(primary).queryByText('Checking the default model…', { exact: true })).not.toBeInTheDocument()
  expect(within(dialog).getByRole('button', { name: /^Remove$/ })).toBeEnabled()
})

it('admits it could not check the destination after a cold request error and keeps Remove enabled', async () => {
  vi.mocked(api.getDefaultModel).mockRejectedValue(new api.ApiError(503, 'default-model lookup unavailable'))
  render(<QueryClientProvider client={client}><ProvidersSection /></QueryClientProvider>)
  // This existing card is a positive control that the real query has settled
  // into error, not loading, and there is no cached default-model pair.
  expect(await screen.findByTestId('default-model-error')).toHaveTextContent('Could not load the default model. Please try again.')
  const { dialog, primary } = await openRemoval()
  expect(api.getDefaultModel).toHaveBeenCalledTimes(1)

  expect.soft(primary.querySelector('p')?.textContent,
    'failed default-model lookup needs the explicit unknown-destination heading')
    .toBe("We could not check the default model, so we can't tell where these agents will go.")
  expect.soft(dialog, 'an error is not evidence that agents lose their model')
    .not.toHaveTextContent(/left without a model/i)
  expect.soft(dialog, 'a failed lookup must not name an unverified fallback').not.toHaveTextContent(/fall back/i)
  expect.soft(dialog).not.toHaveTextContent('OpenRouter')
  expect.soft(dialog).not.toHaveTextContent('openai/gpt-5.4')
  expect.soft(within(dialog).getByRole('button', { name: /^Remove$/ }),
    'the product decision allows removal after the lookup has failed').toBeEnabled()
  expect(api.deleteProvider).not.toHaveBeenCalled()
})

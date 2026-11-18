/**
 * Issue #1620 T5 / CHECK F1: the Settings flow must carry the queried default
 * pair into the real removal dialog. Oracle: issue Wanted 1 and team-lead's
 * Round 3 dispatch, not the component's output. Only API/network edges are
 * mocked; ProvidersSection, its queries, and RemoveProviderDialog stay real.
 */
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
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
  locality: 'cloud',
  models: ['workshop-model'],
  dependents: [{ id: 'scout', name: 'Scout', role: 'primary' }],
}
const DEFAULT_PROVIDER: Provider = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  auth_method: 'api_key',
  backs_default: true,
  // The selected default is intentionally not this provider's first model.
  models: ['anthropic/claude-3.5-haiku', 'openai/gpt-5.4'],
  dependents: [],
}

let client: QueryClient
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  vi.mocked(api.fetchProviders).mockResolvedValue([REMOVED, DEFAULT_PROVIDER])
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  vi.mocked(api.getDefaultModel).mockResolvedValue(DEFAULT_PAIR)
  vi.mocked(api.fetchAppState).mockResolvedValue(APP_STATE)
  vi.mocked(api.deleteProvider).mockResolvedValue({
    deleted: true,
    dependents: REMOVED.dependents,
    default_changed: false,
  })
})

afterEach(() => {
  cleanup()
  client.clear()
})

it('names the queried default and conversation destination when Settings removes a custom provider with a primary agent', async () => {
  render(<QueryClientProvider client={client}><ProvidersSection /></QueryClientProvider>)

  // Positive control: the section has received the selected pair before the
  // removal dialog opens. This card alone cannot prove the dialog wiring.
  expect(await screen.findByTestId('default-model-provider')).toHaveTextContent('OpenRouter')
  expect(screen.getByTestId('default-model-model')).toHaveTextContent('openai/gpt-5.4')
  await screen.findByTestId('provider-row-workshop')
  fireEvent.click(screen.getByTestId('configure-btn-workshop'))
  fireEvent.click(await screen.findByTestId('remove-provider-btn-workshop'))

  const dialog = await screen.findByRole('alertdialog')
  const primary = within(dialog).getByTestId('dependent-group-primary')
  expect(within(primary).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Scout'])
  expect.soft(primary, 'the real parent must pass the available default to the dialog')
    .toHaveTextContent(/fall back to the default provider/)
  expect.soft(primary, 'name the destination provider, not the removed endpoint').toHaveTextContent('OpenRouter')
  expect.soft(primary, 'name the selected model, not the first listed model').toHaveTextContent('openai/gpt-5.4')
  expect.soft(dialog, 'a working default does not leave agents without a model')
    .not.toHaveTextContent(/left without a model/i)
  expect.soft(within(dialog).queryByText('Their conversations will go to the default provider OpenRouter instead.', { exact: true }),
    'custom-endpoint removal must disclose the conversation destination').toBeVisible()

  fireEvent.click(within(dialog).getByRole('button', { name: /^Remove$/ }))
  await waitFor(() => expect(api.deleteProvider).toHaveBeenCalledExactlyOnceWith('workshop', undefined))
})

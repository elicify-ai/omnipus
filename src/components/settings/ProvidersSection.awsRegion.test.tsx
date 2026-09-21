/**
 * ProvidersSection.awsRegion.test.tsx — issue #800 (Bedrock region
 * contract). Settings → Providers is the OTHER place Bedrock's API key is
 * entered (onboarding's coverage lives in
 * ProviderDetailPanel.awsRegion.test.tsx); this file proves the
 * ProviderConfigSheet — the ONE sheet both "connect a new provider" and
 * "configure an already-added row" land in — renders an AWS region control
 * beside the API key input, populated from the catalog's own
 * `regions: [{id, group}]`, and that Save sends it as
 * ProviderUpdateRequest.region.
 */

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn(),
    fetchProvidersCatalog: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    deleteProvider: vi.fn(),
    getDefaultModel: vi.fn(),
    putDefaultModel: vi.fn(),
    checkEntitlement: vi.fn(),
    signOutProvider: vi.fn(),
    fetchSignInStatus: vi.fn(),
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { ProvidersSection } from './ProvidersSection'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'

// Platform mode pins useStepUp() to 'confirm' (ConfirmDialog, no password) —
// the same mode ProvidersSection.test.tsx's save-confirmation suite uses.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ProvidersSection />
    </QueryClientProvider>,
  )
}

const CONFIGURED_BASE = {
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
}

const ANTHROPIC_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  has_models_endpoint: true,
  models: [],
}

const BEDROCK_PROVIDER = {
  ...CONFIGURED_BASE,
  id: 'amazon-bedrock',
  name: 'amazon-bedrock',
  display_name: 'Amazon Bedrock',
  has_models_endpoint: true,
  models: ['anthropic.claude-sonnet-4-5-v1:0'],
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchProviders).mockResolvedValue([ANTHROPIC_PROVIDER] as never)
  vi.mocked(api.fetchProvidersCatalog).mockResolvedValue(PROVIDERS_CATALOG)
  vi.mocked(api.getDefaultModel).mockResolvedValue(null as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('ProvidersSection — AWS region control (issue #800)', () => {
  it('renders no AWS region control for a provider with no regions field', async () => {
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-anthropic'))
    fireEvent.click(screen.getByTestId('configure-btn-anthropic'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))
    expect(screen.queryByTestId('aws-region-input-anthropic')).not.toBeInTheDocument()
  })

  it('renders the AWS region control, defaulted to the catalog row\'s own default region', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([BEDROCK_PROVIDER] as never)
    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-amazon-bedrock'))
    fireEvent.click(screen.getByTestId('configure-btn-amazon-bedrock'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    const select = screen.getByTestId('aws-region-input-amazon-bedrock') as HTMLSelectElement
    expect(select).toBeInTheDocument()
    expect(select.value).toBe('us-east-1')
    const optionValues = Array.from(select.options).map((o) => o.value)
    expect(optionValues).toEqual(['us-east-1', 'eu-central-1', 'ap-northeast-1', 'us-gov-west-1'])
  })

  it('sends the selected AWS region as ProviderUpdateRequest.region on Save', async () => {
    vi.mocked(api.fetchProviders).mockResolvedValue([BEDROCK_PROVIDER] as never)
    vi.mocked(api.configureProvider).mockResolvedValue(BEDROCK_PROVIDER as never)

    renderSection()
    await waitFor(() => screen.getByTestId('configure-btn-amazon-bedrock'))
    fireEvent.click(screen.getByTestId('configure-btn-amazon-bedrock'))
    await waitFor(() => screen.getByTestId('provider-config-sheet'))

    fireEvent.change(screen.getByTestId('aws-region-input-amazon-bedrock'), { target: { value: 'eu-central-1' } })
    fireEvent.change(screen.getByTestId('api-key-input-amazon-bedrock'), { target: { value: 'sk-bedrock-secret' } })
    fireEvent.click(screen.getByTestId('save-provider-amazon-bedrock'))

    fireEvent.click(await screen.findByTestId('confirm-accept'))

    await waitFor(() => {
      expect(api.configureProvider).toHaveBeenCalledTimes(1)
      const custom = vi.mocked(api.configureProvider).mock.calls[0][6]
      expect(custom).toEqual(expect.objectContaining({ region: 'eu-central-1' }))
    })
  })
})

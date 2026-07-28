/**
 * IntegrationsSection.test.tsx — Spec-6 U5 (FR-12.1 / FR-12.2).
 *
 * Covers the provider-picker UI and that a sensitive change is gated by the
 * re-auth consent dialog: a configure action opens the ReAuthDialog, and only a
 * successful re-auth replays the consent token into configureIntegrationProvider.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchIntegrationProviders: vi.fn(),
    configureIntegrationProvider: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

const CATALOGUE = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave Search', configured: false, requires_key: true, active: false },
    { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo', configured: true, requires_key: false, active: true },
  ],
  voice: [
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs Scribe', configured: false, requires_key: true, active: false },
  ],
  active_search: 'duckduckgo',
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderSection() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchIntegrationProviders).mockResolvedValue(CATALOGUE as never)
})

describe('IntegrationsSection', () => {
  it('lists search and voice providers from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByText('Brave Search')).toBeInTheDocument()
      expect(screen.getByText('DuckDuckGo')).toBeInTheDocument()
      expect(screen.getByText('ElevenLabs Scribe')).toBeInTheDocument()
    })
    // Section headings present.
    expect(screen.getByText(/web search/i)).toBeInTheDocument()
    expect(screen.getByText(/voice input/i)).toBeInTheDocument()
  })

  it('marks the active provider', async () => {
    renderSection()
    await waitFor(() => {
      expect(screen.getByTestId('active-duckduckgo')).toBeInTheDocument()
    })
  })

  it('D14: renders a "Needs configuration" badge for a keyless, not-yet-configured provider (SearXNG / audio-model)', async () => {
    // D14 regression. Root cause: the badge ternary branched only on
    // `configured` / `requires_key`, with no else-case for
    // `!configured && !requires_key`. SearXNG (requiresKey:false;
    // `configured` derives from `BaseURL != ""`, empty by default) and
    // audio-model (requiresKey:false; `configured` derives from
    // `voice.model_name`, empty by default) both fall through BOTH branches
    // on a fresh install and render no badge at all — inert-looking rows
    // next to every other provider.
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      search: [
        {
          id: 'searxng',
          kind: 'search',
          display_name: 'SearXNG',
          configured: false,
          requires_key: false,
          active: false,
        },
      ],
      voice: [
        {
          id: 'audio-model',
          kind: 'voice',
          display_name: 'Audio Model (provider)',
          configured: false,
          requires_key: false,
          active: false,
        },
      ],
      active_search: 'duckduckgo',
    } as never)
    renderSection()
    await waitFor(() => {
      expect(screen.getByText('SearXNG')).toBeInTheDocument()
      expect(screen.getByText('Audio Model (provider)')).toBeInTheDocument()
    })
    expect(screen.getByTestId('needs-config-searxng')).toBeInTheDocument()
    expect(screen.getByTestId('needs-config-searxng')).toHaveTextContent(/needs configuration/i)
    expect(screen.getByTestId('needs-config-audio-model')).toBeInTheDocument()
    // Neither row should ALSO show the "Configured"/"Needs API key" badges.
    expect(screen.queryByTestId('active-searxng')).not.toBeInTheDocument()
  })

  it('opens the re-auth dialog before configuring (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    // Expand Brave's key form, type a key, and click Save & activate.
    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    // The re-auth dialog must appear; the PUT must NOT have fired yet.
    await waitFor(() => {
      expect(screen.getByTestId('reauth-confirm')).toBeInTheDocument()
    })
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('replays the consent token into configureIntegrationProvider after re-auth', async () => {
    vi.mocked(api.reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(CATALOGUE as never)

    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    await waitFor(() => screen.getByTestId('reauth-password-input'))
    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(api.reAuth).toHaveBeenCalledWith('mypassword')
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave',
        { kind: 'search', api_key: 'BSA-secret', active: true },
        'reauth_tok',
      )
    })
  })

  it('shows an error when the providers query fails', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockRejectedValue(new Error('boom'))
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/failed to load integrations/i)).toBeInTheDocument()
    })
  })
})

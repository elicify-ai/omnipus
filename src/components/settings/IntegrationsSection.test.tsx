/**
 * IntegrationsSection.test.tsx — Spec-6 U5 (FR-12.1), ADR-0010 WP3.
 *
 * Covers the provider-picker UI and that a sensitive change goes through the
 * step-up gate: a configure action opens ConfirmDialog (platform/confirm
 * mode, pinned here via a mocked AppState identity.mode), and only a confirm
 * calls configureIntegrationProvider. The password-mode (local edition)
 * equivalent lives in IntegrationsSection.password.test.tsx.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
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
    fetchAppState: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { IntegrationsSection } from './IntegrationsSection'

// Platform mode (identity.mode: 'platform') pins useStepUp() to 'confirm' —
// ConfirmDialog, no consent token. See useStepUp.test.tsx for the mode
// selection itself.
const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

const CATALOGUE = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave Search', configured: false, requires_key: true, active: false, usable: false },
    { id: 'duckduckgo', kind: 'search', display_name: 'DuckDuckGo', configured: true, requires_key: false, active: true, usable: true },
  ],
  voice: [
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs Scribe', configured: false, requires_key: true, active: false },
  ],
  active_search: 'duckduckgo',
  default_search: 'duckduckgo',
  fallback_search: null,
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
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

describe('IntegrationsSection', () => {
  it('lists search and voice providers from the API', async () => {
    renderSection()
    await waitFor(() => {
      expect(within(screen.getByTestId('search-row-brave')).getByText('Brave Search')).toBeInTheDocument()
      expect(within(screen.getByTestId('search-row-duckduckgo')).getByText('DuckDuckGo')).toBeInTheDocument()
      expect(screen.getByText('ElevenLabs Scribe')).toBeInTheDocument()
    })
    // Section headings present.
    expect(screen.getByText(/web search/i)).toBeInTheDocument()
    expect(screen.getByText(/voice input/i)).toBeInTheDocument()
  })

  it('shows the stored DuckDuckGo default on its choice card, not as an old Active badge', async () => {
    renderSection()
    expect(await screen.findByTestId('default-search-card')).toHaveTextContent('DuckDuckGo')
    expect(screen.queryByTestId('active-duckduckgo')).not.toBeInTheDocument()
  })

  it('D14: keeps the keyless, not-yet-configured voice-provider badge after removing SearXNG', async () => {
    // The keyless search service that originally shared this regression was
    // removed by #1056. Audio Model still needs voice.model_name; the voice
    // badge must not fall through when configured and requires_key are false.
    vi.mocked(api.fetchIntegrationProviders).mockResolvedValueOnce({
      search: CATALOGUE.search,
      voice: [{
        id: 'audio-model', kind: 'voice', display_name: 'Audio Model (provider)',
        configured: false, requires_key: false, active: false,
      }],
      default_search: 'duckduckgo', fallback_search: null,
    } as never)
    renderSection()
    expect(await screen.findByText('Audio Model (provider)')).toBeInTheDocument()
    expect(screen.getByTestId('needs-config-audio-model')).toHaveTextContent(/needs configuration/i)
    expect(screen.queryByText('Configured')).not.toBeInTheDocument()
  })

  // FR-OB-041/042: exactly Cancel and one confirm, no input of any kind, and
  // the confirm is not the destructive variant.
  it('opens the confirmation before configuring (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    // Expand Brave's key form, type a key, and click Save key; roles stay unchanged.
    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    // The confirmation must appear; the PUT must NOT have fired yet.
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Update this integration?')
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveTextContent('Cancel')
    expect(screen.getByRole('button', { name: 'Update integration' })).toHaveTextContent('Update integration')
    expect(dialog.querySelectorAll('input, textarea, select')).toHaveLength(0)
    expect(screen.getByRole('button', { name: 'Update integration' }).className).not.toMatch(/color-error/)
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('confirming performs the save', async () => {
    vi.mocked(api.configureIntegrationProvider).mockResolvedValue(CATALOGUE as never)

    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    fireEvent.click(await screen.findByRole('button', { name: 'Update integration' }))

    await waitFor(() => {
      // Confirm mode (platform edition) calls with no consent token — the
      // third argument is undefined, not omitted, since gate() always calls
      // run(token) positionally.
      expect(api.configureIntegrationProvider).toHaveBeenCalledWith(
        'brave',
        { kind: 'search', api_key: 'BSA-secret' },
        undefined,
      )
    })
  })

  it('cancelling performs nothing', async () => {
    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    fireEvent.click(screen.getByTestId('addkey-brave'))
    fireEvent.change(screen.getByTestId('key-input-brave'), { target: { value: 'BSA-secret' } })
    fireEvent.click(screen.getByTestId('save-brave'))

    fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }))

    await waitFor(() => {
      expect(screen.queryByRole('alertdialog')).toBeNull()
    })
    expect(api.configureIntegrationProvider).not.toHaveBeenCalled()
  })

  it('shows an error when the providers query fails', async () => {
    vi.mocked(api.fetchIntegrationProviders).mockRejectedValue(new Error('boom'))
    renderSection()
    await waitFor(() => {
      expect(screen.getByText(/failed to load integrations/i)).toBeInTheDocument()
    })
  })
})

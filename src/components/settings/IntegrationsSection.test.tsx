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
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
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

  // FR-OB-041/042: exactly Cancel and one confirm, no input of any kind, and
  // the confirm is not the destructive variant.
  it('opens the confirmation before configuring (does NOT call PUT directly)', async () => {
    renderSection()
    await waitFor(() => screen.getByText('Brave Search'))

    // Expand Brave's key form, type a key, and click Save & activate.
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
        { kind: 'search', api_key: 'BSA-secret', active: true },
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

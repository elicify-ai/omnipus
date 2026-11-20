/**
 * voiceInput.providers.red.test.tsx — Settings redesign #1519, RED
 * (FR-053, FR-025; BDD "Voice input lives in Providers").
 *
 * Oracles (spec, never code):
 *   - FR-053: Providers holds a Voice input group — Active transcriber (a
 *     Save-batch value) and one key row per voice service; a key row opens a
 *     side panel with the key field and show/hide only (no Test, no Remove).
 *   - FR-025: the Password badge (icon + the word) sits on Active
 *     transcriber.
 *
 * RED on landing: the old IntegrationsSection has a Voice Input block with
 * inline key editing, but no Active transcriber row, no Password badge and
 * no side panel — every assertion here fails for that spec reason.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const VOICE = [
  { id: 'local-whisper', display_name: 'Local Whisper', requires_key: false, active: true, configured: true },
  { id: 'cloud-transcribe', display_name: 'Cloud Transcribe', requires_key: true, active: false, configured: false },
]

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchIntegrationProviders: vi.fn(async () => ({ voice: VOICE, search: [] })),
    fetchAppState: vi.fn(async () => ({
      onboarding_complete: true,
      identity: { mode: 'local', edition: 'core', signed_in: true },
    })),
    updateIntegrationProvider: vi.fn(async () => ({})),
    removeIntegrationProvider: vi.fn(async () => ({})),
    testIntegrationProvider: vi.fn(async () => ({ ok: true })),
  }
})
vi.mock('@/store/ui', () => ({ useUiStore: () => ({ addToast: vi.fn() }) }))

import { IntegrationsSection } from './IntegrationsSection'

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <IntegrationsSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  // The SPA refuses a state-changing call without the CSRF cookie.
  document.cookie = 'csrf=voice-input-test'
})

describe('FR-053 — Providers holds a Voice input group', () => {
  it('shows the Voice input group with Active transcriber and a row per voice service', async () => {
    renderSection()
    expect(
      await screen.findByText(/voice input/i, { selector: 'div,span,h3' }, { timeout: 5000 }),
      'FR-053 RED: the Voice input group must exist in Providers',
    ).toBeInTheDocument()
    expect(
      await screen.findByLabelText(/active transcriber/i),
      'FR-053 RED: Active transcriber must be a row of the Voice input group (FR-053 replaces the old per-provider voice controls)',
    ).toBeInTheDocument()
    // The service name also feeds the Active transcriber choice, so match
    // "at least one row per service" rather than exactly one node.
    expect(screen.getAllByText('Local Whisper').length, 'FR-053: a row per voice service').toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Cloud Transcribe').length).toBeGreaterThanOrEqual(1)
  })

  it('marks Active transcriber with the Password badge — icon and the word (FR-025)', async () => {
    renderSection()
    await screen.findByLabelText(/active transcriber/i, {}, { timeout: 5000 })
    expect(
      screen.getByText('Password'),
      'FR-025 RED: Active transcriber carries the Password badge (icon and word)',
    ).toBeInTheDocument()
  })

  it('a key row opens a side panel with the key field and show/hide only — no Test, no Remove (FR-053)', async () => {
    renderSection()
    await screen.findByText(/voice input/i, { selector: 'div,span,h3' }, { timeout: 5000 })
    await userEvent.setup().click(screen.getByRole('button', { name: /add key/i }))
    const panel = await screen.findByRole('dialog', {}, { timeout: 5000 })
    // The key field and its show/hide toggle both name the key, so assert
    // "at least one field labelled as the key" rather than exactly one node.
    expect(
      within(panel).getAllByLabelText(/api key/i).length,
      'FR-053 RED: the side panel holds the key field (the old UI edits keys inline)',
    ).toBeGreaterThanOrEqual(1)
    expect(within(panel).getByRole('button', { name: /show|hide/i })).toBeInTheDocument()
    expect(
      within(panel).queryByRole('button', { name: /^test$/i }),
      'FR-053: the panel has no Test button',
    ).not.toBeInTheDocument()
    expect(
      within(panel).queryByRole('button', { name: /remove/i }),
      'FR-053: the panel has no Remove button',
    ).not.toBeInTheDocument()
  })
})

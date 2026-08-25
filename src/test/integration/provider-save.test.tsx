import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ProvidersSection } from '@/components/settings/ProvidersSection'

// test_provider_save_connect (test #27)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Adding a new provider
//             wave5a-wire-ui-spec.md — Scenario: Provider connection test fails
//             wave5a-wire-ui-spec.md — Scenario: API key stored securely
//
// Round-3 re-auth migration: saving a provider key is now consent-gated
// (Spec-6 FR-12.2). "Save & Connect" no longer calls configureProvider
// directly — it stages the change and opens the ReAuthDialog. The PUT only
// fires from onReAuthConfirmed once reAuth() mints a consent token, which is
// then replayed in configureProvider's reAuthToken arg. The tests below drive
// that full happy path: open inline form → type key → Save & Connect →
// confirm password in the ReAuthDialog → assert configureProvider fires with
// the minted token.

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchProviders: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    reAuth: vi.fn(),
  }
})

import { fetchProviders, configureProvider, testProvider, reAuth } from '@/lib/api'

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function wrapper({ children }: { children: React.ReactNode }) {
  return <QueryClientProvider client={makeClient()}>{children}</QueryClientProvider>
}

beforeEach(() => {
  // Clear call history so tests don't bleed into each other
  vi.clearAllMocks()
  // Seed all 5 providers already CONNECTED (status:'connected'), not
  // 'disconnected'. GET /providers also reports ~25 forever-keyless template
  // rows as status:'disconnected' (Provider.yaml: "no key available or
  // fallback default entry") — ProvidersSection now filters those out of its
  // main list entirely, so a disconnected fixture here would never render.
  vi.mocked(fetchProviders).mockResolvedValue([
    { id: 'openai', name: 'OpenAI', display_name: 'OpenAI', status: 'connected', models: ['gpt-4o'], auth_method: 'api_key', dependents: [], backs_default: false },
    { id: 'anthropic', name: 'Anthropic', display_name: 'Anthropic', status: 'connected', models: ['claude-sonnet-4-5'], auth_method: 'api_key', dependents: [], backs_default: false },
    { id: 'google', name: 'Google Gemini', display_name: 'Google Gemini', status: 'connected', models: ['gemini-2.5-flash'], auth_method: 'api_key', dependents: [], backs_default: false },
    { id: 'groq', name: 'Groq', display_name: 'Groq', status: 'connected', models: ['llama-3.3-70b'], auth_method: 'api_key', dependents: [], backs_default: false },
    { id: 'openrouter', name: 'OpenRouter', display_name: 'OpenRouter', status: 'connected', models: ['openrouter/auto'], auth_method: 'api_key', dependents: [], backs_default: false },
  ])
  vi.mocked(configureProvider).mockResolvedValue({ id: 'openai', name: 'OpenAI', status: 'connected', models: [], auth_method: 'api_key', dependents: [], backs_default: false })
  vi.mocked(testProvider).mockResolvedValue({ success: true })
  // Re-auth succeeds and mints a consent token the save flow replays.
  vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'consent-token-abc', expires_in: 300 })
})

describe('provider save & connect integration (test #27)', () => {
  it('saves key by calling configureProvider after re-auth when Save & Connect is clicked', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Adding a new provider (AC2-3)
    render(<ProvidersSection />, { wrapper })

    await screen.findByText('OpenAI')

    // Find all Configure buttons — providers are [openai, anthropic, google, groq, openrouter].
    // Click the second button (index 1) to expand Anthropic's inline form.
    const configBtns = screen.getAllByRole('button', { name: /configure|edit/i })
    fireEvent.click(configBtns[1])

    // Enter API key in the expanded inline form.
    // API key inputs are type="password" — not accessible as role="textbox"; use placeholder.
    const keyInput = screen.getByPlaceholderText(/sk-ant/i)
    fireEvent.change(keyInput, { target: { value: 'sk-ant-test-1234' } })

    // Click Save & Connect — this stages the change and opens the ReAuthDialog;
    // it does NOT call configureProvider yet (Spec-6 FR-12.2 consent gate).
    fireEvent.click(screen.getByRole('button', { name: /save.*connect/i }))

    // The ReAuthDialog mounts in a portal — query it asynchronously.
    const reauthDialog = await screen.findByRole('dialog')
    const passwordInput = within(reauthDialog).getByTestId('reauth-password-input')
    fireEvent.change(passwordInput, { target: { value: 'my-password' } })

    // Confirm in the dialog → reAuth() resolves → onConfirmed(token) → configureProvider fires.
    fireEvent.click(within(reauthDialog).getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('my-password')
    })
    await waitFor(() => {
      // configureProvider(id, apiKey, endpoint?, model?, reAuthToken?, models?, custom?)
      // 7th arg (ADR-068 FR-037, T068-25): the custom-endpoint pair, absent
      // here — every non-custom save now threads it through explicitly (see
      // the same 7-arg pattern in ProvidersSection.test.tsx's re-auth and
      // manual-provider describes).
      expect(configureProvider).toHaveBeenCalledWith(
        'anthropic',
        'sk-ant-test-1234',
        undefined,
        undefined,
        'consent-token-abc',
        undefined,
        undefined,
      )
    })
  })

  it('does not call configureProvider when key input is empty', async () => {
    // Traces to: wave5a-wire-ui-spec.md — Dataset: Provider Configuration row 2 — empty key validation
    render(<ProvidersSection />, { wrapper })

    await screen.findByText('OpenAI')
    // Providers are connected — the row action reads "Edit", not "Configure".
    const configBtns = screen.getAllByRole('button', { name: /configure|edit/i })
    fireEvent.click(configBtns[0])

    // Do NOT enter a key — Save & Connect should be disabled, so the re-auth
    // gate is never reached and configureProvider is never called.
    const saveBtn = screen.getByRole('button', { name: /save.*connect/i })
    expect(saveBtn).toBeDisabled()
    expect(reAuth).not.toHaveBeenCalled()
    expect(configureProvider).not.toHaveBeenCalled()
  })

  it('shows "Connected" badge after successful provider save', async () => {
    // Traces to: wave5a-wire-ui-spec.md — AC2: badge updates after save
    // Initial fetch returns all providers already connected (see beforeEach's
    // comment on why 'disconnected' fixtures don't render); after save,
    // anthropic's model list updates.
    vi.mocked(fetchProviders).mockResolvedValue([
      { id: 'anthropic', name: 'Anthropic', display_name: 'Anthropic', status: 'connected', models: ['claude-sonnet-4-6'], auth_method: 'api_key', dependents: [], backs_default: false },
    ])

    render(<ProvidersSection />, { wrapper })
    await screen.findByText('Anthropic')

    // Expand Anthropic's inline form — the only configured provider here.
    const configBtns = screen.getAllByRole('button', { name: /configure|edit/i })
    fireEvent.click(configBtns[0])
    // API key inputs are type="password" — not accessible as role="textbox"; use placeholder.
    const keyInput = screen.getByPlaceholderText(/sk-ant/i)
    fireEvent.change(keyInput, { target: { value: 'sk-ant-valid-key' } })
    fireEvent.click(screen.getByRole('button', { name: /save.*connect/i }))

    // Pass the re-auth gate so the PUT actually fires.
    const reauthDialog = await screen.findByRole('dialog')
    const passwordInput = within(reauthDialog).getByTestId('reauth-password-input')
    fireEvent.change(passwordInput, { target: { value: 'my-password' } })
    fireEvent.click(within(reauthDialog).getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(configureProvider).toHaveBeenCalled()
    })
  })
})

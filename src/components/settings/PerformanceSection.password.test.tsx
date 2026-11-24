/**
 * PerformanceSection.password.test.tsx — Spec-3 FR-6.6, ADR-0010 WP3; the
 * redesign's FR-026/FR-027/FR-029 keep the substance.
 *
 * The password-mode counterpart to PerformanceSection.test.tsx's confirm-mode
 * coverage: with AppState.identity.mode = 'local', the shell's password prompt
 * is the real ReAuthDialog, and the section's one Save asks it before the PUT
 * goes out (FR-026: one prompt per Save, before any write) and sends the PUT
 * exactly once carrying the minted consent token (FR-027). Cancel/dismissal
 * sends nothing and keeps every draft (FR-029, D-11 row 5).
 *
 * The old file drove the removed autosave model (a 700 ms debounce triggered
 * the PUT through useStepUp's own dialog); FR-010 replaces autosave with the
 * section Save bar, and the prompt moved to the shell (FR-026), so this
 * harness supplies the shell boundary and drives the prompt's outcome by
 * hand — the same dialog-first, token-once oracle at the boundary a
 * component test owns.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

const addToast = vi.fn()

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchPerformanceSettings: vi.fn(),
    updatePerformanceSettings: vi.fn(),
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import type { SettingsShellApi } from './SettingsShellContext'
import { SettingsShellContext } from './SettingsShellContext'
import { PerformanceSection } from './PerformanceSection'

// Local mode (identity.mode: 'local') — the shell's prompt renders the real
// ReAuthDialog; at this boundary the harness owns the prompt's outcome.
const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
  tools_on_demand: true,
}

/** A prompt recorder whose outcome the test resolves by hand. */
function manualPrompt() {
  const prompts: string[][] = []
  let resolvePrompt: (token: string | null) => void = () => {}
  const requestPassword = vi.fn(
    (labels: string[]) =>
      new Promise<string | null>((resolve) => {
        prompts.push(labels)
        resolvePrompt = resolve
      }),
  )
  return {
    prompts,
    requestPassword,
    resolveWith: (token: string | null) =>
      act(async () => {
        resolvePrompt(token)
      }),
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

async function renderSectionWithShell(requestPassword: (labels: string[]) => Promise<string | null>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SettingsShellContext.Provider
        value={
          {
            announce: () => {},
            setDraftCount: () => {},
            offerRestart: () => {},
            refreshPendingRestart: () => {},
            requestPassword,
            saveBarVisible: false,
            setSaveBarVisible: () => {},
          } as SettingsShellApi
        }
      >
        <PerformanceSection />
      </SettingsShellContext.Provider>
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — password mode (local edition)', () => {
  it('the prompt precedes the PUT; completing it sends the PUT once with the minted consent token (FR-026, FR-027)', async () => {
    vi.mocked(api.updatePerformanceSettings).mockResolvedValue({
      ...SETTINGS,
      max_parallel_agents: 8,
    } as never)
    const prompt = manualPrompt()

    renderSectionWithShell(prompt.requestPassword)
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))

    // Prompt first: the shell is asked for the one password before anything
    // is sent (FR-026; the old flow's dialog-first property), listing the
    // password-marked setting by its visible label.
    await waitFor(() => expect(prompt.prompts.length).toBe(1))
    expect(prompt.prompts[0]).toEqual(['Max parallel agents'])
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()

    // The re-auth completes; the minted consent token rides the one PUT.
    await prompt.resolveWith('reauth_tok')
    await waitFor(() => {
      expect(api.updatePerformanceSettings).toHaveBeenCalledTimes(1)
      expect(vi.mocked(api.updatePerformanceSettings).mock.calls[0]).toEqual([
        { max_parallel_agents: 8 },
        'reauth_tok',
      ])
    })
    await screen.findByText(/Saved at/)
  })

  it('dismissing the prompt leaves the change unsaved and the draft on the bar (FR-029, D-11 row 5)', async () => {
    const prompt = manualPrompt()
    renderSectionWithShell(prompt.requestPassword)
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    fireEvent.click(await screen.findByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(prompt.prompts.length).toBe(1))

    // Cancel/dismiss resolves null: nothing is stored and the draft stays.
    await prompt.resolveWith(null)
    await waitFor(() => {
      expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    })
    expect(api.updatePerformanceSettings).not.toHaveBeenCalled()
  })
})

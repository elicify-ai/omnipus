// PerformanceSection.noToolsOnDemand.test.tsx — order 48 (S-44; ST-Q4 B;
// C-22 row 2). Oracle: skills-tools-redesign-spec.md S-44 — "Settings →
// Limits shows no 'Load tools on demand' switch, and saving Max parallel
// agents there still succeeds"; C-22 row 2 removes the PerformanceSection
// row, the CONFIG_KEYS entry, the label, the default and the field-path
// entry, while the other Limits values keep working.
//
// RED today: this tree is the Settings base — the switch still renders
// (PerformanceSection.tsx 'Load tools on demand'), so the absence assertion
// fails against the real section.
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
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import type { AppState } from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

// The landed PerformanceSettings carries no tools_on_demand — the mock
// already reflects the post-removal shape (C-22 row 2).
const SETTINGS = {
  max_parallel_agents: 4,
  effective_max_parallel_agents: 4,
  max_parallel_agents_configured: true,
}

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
      <PerformanceSection />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(api.fetchPerformanceSettings).mockResolvedValue(SETTINGS as never)
  vi.mocked(api.fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — no tools-on-demand control after the removal (order 48)', () => {
  it('S-44: Limits shows no "Load tools on demand" switch', async () => {
    renderSection()
    // The section loaded (its other content is there) before asserting the
    // absence — the tripwire for a section that failed to render at all.
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    expect(screen.queryByText(/load tools on demand/i)).toBeNull()
  })

  it('S-44: saving Max parallel agents still succeeds — the change path survives the removal', async () => {
    renderSection()
    // Load with real timers, then control the autosave debounce (600 ms).
    await waitFor(() => screen.getByLabelText('Max parallel agents'))
    vi.useFakeTimers()
    fireEvent.change(screen.getByLabelText('Max parallel agents'), { target: { value: '8' } })
    await act(async () => {
      vi.advanceTimersByTime(700)
    })
    vi.useRealTimers()
    // The re-auth confirm — the section's real save path — is offered for a
    // Max-parallel change: the other Limits values keep working (C-22 row 2).
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Change the performance settings?')
    expect(screen.getByRole('button', { name: 'Change performance settings' })).toBeTruthy()
  })
})

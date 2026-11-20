/**
 * PerformanceSection.autoDefault.test.tsx — FR-069.
 *
 * The single most visible artefact of deleting the computed concurrency
 * default. There is no longer a memory-derived recommendation: when nothing is
 * configured the backend returns a PHYSICAL OS-thread-safety backstop (2000)
 * alongside max_parallel_agents_configured=false, and 2000 is not a number
 * anything in the process is recommending. Left unhandled, the Settings panel
 * rendered it under the words "Recommended", telling every operator on a fresh
 * install that the system advises two thousand parallel agents.
 *
 * These tests are in their own file rather than added to
 * PerformanceSection.test.tsx because that suite's shared SETTINGS fixture
 * declares max_parallel_agents_configured: true — the configured branch. This
 * file is the unconfigured branch, and mixing the two into one fixture is how
 * a "no 2000 anywhere" assertion quietly stops being exercised.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
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
    isApiError: actual.isApiError,
  }
})

import * as api from '@/lib/api'
import { PerformanceSection } from './PerformanceSection'

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
})

afterEach(() => {
  vi.useRealTimers()
})

describe('PerformanceSection — the unconfigured (memory-bounded) state, FR-069', () => {
  it('renders the unconfigured state as an empty field, the in-use value explained by memory, and 2000 nowhere as a recommendation', async () => {
    vi.useRealTimers()
    // Exactly what a fresh install's GET /api/v1/performance returns: nothing
    // configured, so the effective value IS the physical backstop.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 2000,
      effective_max_parallel_agents: 2000,
      max_parallel_agents_configured: false,
      tools_on_demand: true,
    } as never)

    const { container } = renderSection()
    const input = await screen.findByLabelText('Max parallel agents')

    // The Save-bar oracle (settings-redesign-spec D-10 row 9): EMPTY means
    // automatic — "empty means no fixed limit". The field never carries the
    // backstop. (The old oracle asserted the previous panel's copy
    // "automatic — bounded by available memory"; D-10 row 9 replaces it.)
    expect((input as HTMLInputElement).value).toBe('')

    // FR-069 (browser-workspace-ownership-spec) substance: the backstop is
    // never presented as a recommendation, a capacity claim, or a default
    // the system chose. The panel states what is in use and why ("Currently
    // in use: 2000 parallel agents · without a limit, the number is bounded
    // by available memory" — the accepted implementation of "empty means
    // automatic"), and 2000 appears in no recommendation frame.
    expect(container.textContent).toMatch(/Currently in use: 2000 parallel agents/)
    expect(container.textContent).toMatch(/bounded by available memory/i)
    // "Recommended:" with the colon, not the bare word — the unrelated Tool
    // loading card legitimately says "Smaller messages, lower token use.
    // Recommended." and a bare /Recommended/ would make this test fail for a
    // reason that has nothing to do with concurrency.
    expect(container.textContent).not.toMatch(/Recommended:/i)
    expect(container.textContent).not.toMatch(/Live system recommendation/i)

    // And the deleted formula must not be described anywhere either.
    expect(container.textContent).not.toMatch(/3\.5 MB/)
    expect(container.textContent).not.toMatch(/auto-detected/i)
  })

  it('still shows an operator their OWN configured value', async () => {
    vi.useRealTimers()
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 40,
      effective_max_parallel_agents: 40,
      max_parallel_agents_configured: true,
      tools_on_demand: true,
    } as never)

    const { container } = renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    // WITHOUT this second case the first one passes against a panel that
    // stopped showing the operator's setting at all — "renders the automatic
    // text and never an integer" is satisfied by a panel that renders the
    // automatic text unconditionally, which would be a worse bug than the one
    // being fixed.
    expect(screen.getByText(/Currently in use: 40 parallel agents/)).toBeInTheDocument()
    expect(container.textContent).not.toMatch(/automatic — bounded by available memory/i)
    expect((screen.getByLabelText('Max parallel agents') as HTMLInputElement).value).toBe('40')
  })

  it('falls back to the integer rendering when an older backend omits the flag', async () => {
    vi.useRealTimers()
    // Forward-compatibility: the field is optional on the wire. A response
    // without it must keep the previous behaviour rather than silently
    // switching every install to the automatic text — which is why the
    // component tests `!== false` rather than a truthy check.
    vi.mocked(api.fetchPerformanceSettings).mockResolvedValue({
      max_parallel_agents: 12,
      effective_max_parallel_agents: 12,
      tools_on_demand: true,
    } as never)

    renderSection()
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    expect(screen.getByText(/Currently in use: 12 parallel agents/)).toBeInTheDocument()
  })
})

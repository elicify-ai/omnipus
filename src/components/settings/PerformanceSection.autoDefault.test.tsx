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
    reAuth: vi.fn(),
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
  it('renders "automatic — bounded by available memory" and shows 2000 NOWHERE', async () => {
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
    await waitFor(() => screen.getByLabelText('Max parallel agents'))

    expect(screen.getByText(/automatic — bounded by available memory/i)).toBeInTheDocument()

    // The load-bearing negative. The backstop must not reach the operator's
    // eyes in ANY form — not as a recommendation, not as an "effective value
    // in use", not prefilled into the input.
    expect(container.textContent).not.toMatch(/2000/)
    // "Recommended:" with the colon, not the bare word — the unrelated Tool
    // loading card legitimately says "Smaller messages, lower token use.
    // Recommended." and a bare /Recommended/ would make this test fail for a
    // reason that has nothing to do with concurrency.
    expect(container.textContent).not.toMatch(/Recommended:/i)
    expect((screen.getByLabelText('Max parallel agents') as HTMLInputElement).value).not.toBe('2000')

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

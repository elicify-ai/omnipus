/**
 * AgentProfile.providerPair.test.tsx — H6 provider-pair tests.
 *
 * Split out of AgentProfile.test.tsx (file-budget guard, 2026-10-14): that
 * file is grandfathered in scripts/budgets/ and may only SHRINK, so the
 * duplicate-provider tests added for the (provider, model) selection-identity
 * fix live here. Assertions were moved verbatim — none were changed.
 *
 * Shared fixtures (mockCoreAgent etc.) come from agentProfileTestFixtures.ts.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import { mockCoreAgent } from './agentProfileTestFixtures'
import { fetchAgent, updateAgent, fetchProviders } from '@/lib/api'

// cmdk (inside the ModelSelector popover) needs ResizeObserver and
// scrollIntoView in jsdom.
class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}
if (typeof globalThis.ResizeObserver === 'undefined') {
  vi.stubGlobal('ResizeObserver', ResizeObserverStub)
}
if (typeof Element !== 'undefined' && !Element.prototype.scrollIntoView) {
  Element.prototype.scrollIntoView = function () {}
}

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => mockNavigate,
    useParams: () => ({}),
  }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgent: vi.fn(),
    updateAgent: vi.fn(),
    fetchProviders: vi.fn(),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderProfile(agentId: string) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <AgentProfile agentId={agentId} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(mockCoreAgent)
  vi.mocked(updateAgent).mockReset().mockResolvedValue(mockCoreAgent)
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([] as never)
})

// ── H6 R2 (review round 1) — duplicate-provider persistence ───────────────────
//
// The same slug served by TWO usable providers, grok-local listed FIRST so
// any first-match resolution would name grok-local for a pick made under
// openai-chatgpt. These tests pin the SAVED wire payload (updateAgent), not
// just component state, and the chosen row on reopen.

const H6_DUAL_PROVIDERS = [
  { id: 'grok-local', name: 'grok-local', display_name: 'grok-local', status: 'connected', models: ['gpt-6.1-sol', 'grok-4'], auth_method: 'api_key', dependents: [], backs_default: false },
  { id: 'openai-chatgpt', name: 'openai-chatgpt', display_name: 'ChatGPT (subscription)', status: 'signed_in', models: ['gpt-6.1-sol', 'gpt-5.6'], auth_method: 'sign_in', dependents: [], backs_default: false },
]

/** The open popover's rows for one slug, in fixture (render) order. */
const h6RowsFor = (model: string) =>
  screen.getAllByRole('option').filter((el) => (el.textContent ?? '').includes(model))

/** The profile's PRIMARY model picker trigger (no testid — aria-labelled). */
const h6PrimaryTrigger = () =>
  screen
    .getAllByRole('combobox')
    .filter((el) => el.tagName === 'BUTTON' && /^Model selector/.test(el.getAttribute('aria-label') ?? ''))[0]

describe('AgentProfile — H6 R2 duplicate-provider picks persist the clicked provider', () => {
  it('a fallback added via the SECOND provider\'s row is saved with THAT provider, once, with other entries preserved', async () => {
    vi.mocked(fetchProviders).mockResolvedValue(H6_DUAL_PROVIDERS as never)
    vi.mocked(updateAgent).mockResolvedValue(mockCoreAgent)
    vi.mocked(updateAgent).mockClear()
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      fallback_models: [{ model: 'grok-4', provider: 'grok-local' }],
    })
    renderProfile(mockCoreAgent.id)
    await screen.findByText(mockCoreAgent.name)
    await waitFor(() => {
      expect(screen.getAllByText(/Fallback models/i).length).toBeGreaterThanOrEqual(1)
    })

    // Add gpt-6.1-sol via openai-chatgpt's row (the SECOND listing).
    fireEvent.click(screen.getAllByTestId('fallback-add-trigger')[0])
    const rows = await vi.waitFor(() => h6RowsFor('gpt-6.1-sol'))
    expect(rows).toHaveLength(2)
    fireEvent.click(rows[1])

    // Chip shows the clicked provider immediately.
    await waitFor(() => {
      expect(screen.getAllByTestId('fallback-chip-provider-gpt-6.1-sol')[0]).toHaveTextContent(/chatgpt/i)
    })

    // Wait out the 1500ms autosave debounce.
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(([id]) => id === mockCoreAgent.id)
        expect(calls.length).toBeGreaterThan(0)
      },
      { timeout: 6000 },
    )

    // Exactly ONE autosave carries the new fallback, and it carries the
    // CLICKED provider — never an intermediate first-listing value, and no
    // duplicate entry for the same model.
    type FallbackPayload = { fallback_models?: Array<{ model: string; provider: string }> }
    const callsWithNewFallback = vi.mocked(updateAgent).mock.calls.filter((call) => {
      const payload = call[1] as FallbackPayload
      return (
        Array.isArray(payload.fallback_models) &&
        payload.fallback_models.some((f) => f.model === 'gpt-6.1-sol')
      )
    })
    expect(callsWithNewFallback).toHaveLength(1)
    const saved = callsWithNewFallback[0][1] as FallbackPayload
    expect(saved.fallback_models).toEqual([
      { model: 'grok-4', provider: 'grok-local' },
      { model: 'gpt-6.1-sol', provider: 'openai-chatgpt' },
    ])
  })

  it('picking the PRIMARY model under one provider then the OTHER (same slug) moves the check and saves the final provider', async () => {
    vi.mocked(fetchProviders).mockResolvedValue(H6_DUAL_PROVIDERS as never)
    // The PUT contract returns the COMPLETE updated agent; the save-success
    // path patches the query cache with it and the hydration effect then
    // re-hydrates from that snapshot. Echo the payload — a static response
    // would re-hydrate the form back to the pre-pick model/provider and the
    // reopened picker would legitimately show no chosen row.
    vi.mocked(updateAgent).mockImplementation(async (_id, payload) => {
      const p = payload as { model?: string; provider?: string }
      return { ...mockCoreAgent, updated_at: '2026-10-13T00:00:00Z', model: p.model ?? mockCoreAgent.model, provider: p.provider }
    })
    vi.mocked(updateAgent).mockClear()
    // A fixed updated_at on BOTH the GET fixture and the PUT echo engages the
    // hydration effect's monotonic guard: the save echo is incorporated, and
    // the invalidateQueries refetch — which here re-serves the initial
    // fixture — carries the SAME timestamp and is skipped as an echo instead
    // of re-hydrating model/provider back to '' over the user's picks.
    vi.mocked(fetchAgent).mockResolvedValue({
      ...mockCoreAgent,
      model: '',
      provider: '',
      updated_at: '2026-10-13T00:00:00Z',
    })
    renderProfile(mockCoreAgent.id)
    await screen.findByText(mockCoreAgent.name)

    // Pick gpt-6.1-sol under openai-chatgpt.
    fireEvent.click(h6PrimaryTrigger())
    let rows = await vi.waitFor(() => h6RowsFor('gpt-6.1-sol'))
    expect(rows).toHaveLength(2)
    fireEvent.click(rows[1])
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(([id]) => id === mockCoreAgent.id)
        expect(calls.some(([, p]) => (p as { provider?: string }).provider === 'openai-chatgpt')).toBe(true)
      },
      { timeout: 6000 },
    )

    // Pick the SAME slug under grok-local — model unchanged, provider flips.
    fireEvent.click(h6PrimaryTrigger())
    rows = await vi.waitFor(() => h6RowsFor('gpt-6.1-sol'))
    fireEvent.click(rows[0])
    await waitFor(
      () => {
        const calls = vi.mocked(updateAgent).mock.calls.filter(([id]) => id === mockCoreAgent.id)
        // The FIRST save carries the full pair atomically...
        const firstWithModel = calls.find(
          ([, p]) => (p as { model?: string }).model === 'gpt-6.1-sol',
        )
        expect((firstWithModel?.[1] as { provider?: string }).provider).toBe('openai-chatgpt')
        // ...and the LAST save carries the flipped provider. buildAgentUpdate
        // diffs against the saved baseline, so the provider-only flip sends
        // provider only — the model side of the pair is already saved.
        const last = calls.at(-1)!
        expect((last[1] as { provider?: string }).provider).toBe('grok-local')
      },
      { timeout: 6000 },
    )

    // Reopen: the check sits on grok-local's row only.
    fireEvent.click(h6PrimaryTrigger())
    await vi.waitFor(() => h6RowsFor('gpt-6.1-sol'))
    const reopened = h6RowsFor('gpt-6.1-sol')
    expect(reopened).toHaveLength(2)
    expect(reopened[0]).toHaveAttribute('data-chosen')
    expect(reopened[1]).not.toHaveAttribute('data-chosen')
  })
})

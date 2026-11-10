/**
 * AgentProfile.fallbackPairs.test.tsx — #1340 / #1341 founder rule: "provider
 * and model always come in pairs, never alone".
 *
 * The server refuses (400) an agent update whose fallback_models carries an
 * entry missing its provider or model (AgentUpdateRequest.fallback_models is
 * the generated FallbackModelPair[]). The profile autosaves the whole chain,
 * so a legacy UNPAIRED fallback (an old install) must never be re-sent, must
 * not make an unrelated save fail, and must be shown as incomplete so the
 * person can fix or remove it.
 */
import { it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { AgentProfile } from './AgentProfile'
import { mockCoreAgent } from './agentProfileTestFixtures'
import { fetchAgent, updateAgent, fetchProviders } from '@/lib/api'

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

vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return { ...actual, useNavigate: () => vi.fn(), useParams: () => ({}) }
})

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, fetchAgent: vi.fn(), updateAgent: vi.fn(), fetchProviders: vi.fn() }
})

// One connected provider that lists `paired-model` but NOT `legacy-model`.
const PROVIDERS = [
  { id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter', status: 'connected', models: ['paired-model'], auth_method: 'api_key', dependents: [], backs_default: false },
]

// An old install: one legacy entry saved WITHOUT its provider, one full pair.
const legacyAgent = {
  ...mockCoreAgent,
  fallback_models: [
    { model: 'legacy-model' },
    { model: 'paired-model', provider: 'openrouter' },
  ],
}

function renderProfile() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AgentProfile agentId={legacyAgent.id} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.mocked(fetchAgent).mockReset().mockResolvedValue(legacyAgent)
  vi.mocked(updateAgent).mockReset().mockResolvedValue(legacyAgent)
  vi.mocked(fetchProviders).mockReset().mockResolvedValue(PROVIDERS as never)
})

it('#1340: an unrelated edit never sends an unpaired fallback — every sent entry has both provider and model', async () => {
  renderProfile()
  await screen.findByText(legacyAgent.name)

  const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
  fireEvent.change(description, { target: { value: 'Edited description' } })

  await waitFor(
    () => expect(vi.mocked(updateAgent).mock.calls.length).toBeGreaterThan(0),
    { timeout: 6000 },
  )
  for (const [, payload] of vi.mocked(updateAgent).mock.calls) {
    const chain = (payload as { fallback_models?: Array<{ model?: string; provider?: string }> }).fallback_models
    if (chain === undefined) continue // an omitted chain leaves the stored one unchanged
    for (const entry of chain) {
      expect(entry.provider ?? '', `fallback ${entry.model} was sent without a provider`).not.toBe('')
      expect(entry.model ?? '').not.toBe('')
    }
  }
})

it('#1340: a saved unpaired fallback is shown as incomplete, can be removed, and offers no empty provider choice', async () => {
  renderProfile()
  await screen.findByText(legacyAgent.name)

  // Shown clearly as incomplete, with the way out named.
  const warning = (await screen.findAllByTestId('fallback-chip-warning-legacy-model'))[0]
  expect(warning).toHaveTextContent(/incomplete/i)
  expect(screen.getAllByTestId('fallback-incomplete-notice')[0]).toBeVisible()
  // The full pair is not flagged.
  expect(screen.queryAllByTestId('fallback-chip-warning-paired-model')).toHaveLength(0)
  // The picker has no "—" / empty provider choice for any entry.
  expect(screen.queryAllByTestId('fallback-provider-option-empty-paired-model')).toHaveLength(0)
  expect(screen.queryAllByTestId('fallback-provider-option-empty-legacy-model')).toHaveLength(0)
  // It can be removed.
  expect(screen.getAllByTestId('fallback-chip-remove-legacy-model')[0]).toBeEnabled()
})

// ── CHECK F1–F3 follow-up (#1340): preservation, no guessing, removal ──────

type SentChain = Array<{ model?: string; provider?: string }> | undefined

function lastUpdate(): { id: string; payload: Record<string, unknown> } {
  const calls = vi.mocked(updateAgent).mock.calls
  const [id, payload] = calls[calls.length - 1]
  return { id: id as string, payload: payload as unknown as Record<string, unknown> }
}

it('#1340 F1: an unrelated edit while a fallback is incomplete omits the chain entirely — the stored chain is never erased', async () => {
  renderProfile()
  await screen.findByText(legacyAgent.name)

  const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
  fireEvent.change(description, { target: { value: 'Edited description' } })

  await waitFor(
    () => expect(vi.mocked(updateAgent).mock.calls.length).toBeGreaterThan(0),
    { timeout: 6000 },
  )
  const { id, payload } = lastUpdate()
  expect(id).toBe(legacyAgent.id)
  expect(payload.description).toBe('Edited description')
  // Omitted — not [] (which would clear the saved chain) and not a partial
  // chain (which would drop the complete pair or resend the unpaired one).
  expect(payload).not.toHaveProperty('fallback_models')
  for (const [, sent] of vi.mocked(updateAgent).mock.calls) {
    expect(sent as unknown as Record<string, unknown>).not.toHaveProperty('fallback_models')
  }
})

it('#1340 F2: hydration never invents a provider, even when a connected provider offers the unpaired model', async () => {
  // Here the connected provider DOES list `legacy-model`, so any guess on
  // hydration would silently pair it with openrouter.
  vi.mocked(fetchProviders).mockReset().mockResolvedValue([
    { ...PROVIDERS[0], models: ['paired-model', 'legacy-model'] },
  ] as never)
  // The agent arrives only after the provider catalog has loaded, so a
  // lookup at hydration time would have the catalog available.
  vi.mocked(fetchAgent).mockReset().mockImplementation(
    () => new Promise((resolve) => setTimeout(() => resolve(legacyAgent), 50)),
  )
  renderProfile()
  await screen.findByText(legacyAgent.name)

  // Still shown as incomplete, with the explicit choice still owed.
  expect((await screen.findAllByTestId('fallback-chip-warning-legacy-model'))[0]).toHaveTextContent(/incomplete/i)
  expect(screen.getAllByTestId('fallback-provider-option-placeholder-legacy-model').length).toBeGreaterThan(0)
  expect((screen.getAllByTestId('fallback-provider-select-legacy-model')[0] as HTMLSelectElement).value).toBe('')

  // And a save does not send an invented pair.
  const description = (await screen.findAllByTestId('agent-description-input'))[0] as HTMLTextAreaElement
  fireEvent.change(description, { target: { value: 'Edited description' } })
  await waitFor(
    () => expect(vi.mocked(updateAgent).mock.calls.length).toBeGreaterThan(0),
    { timeout: 6000 },
  )
  expect(lastUpdate().payload).not.toHaveProperty('fallback_models')
})

it('#1340 F3: Remove on the incomplete fallback removes it, and the next save sends exactly the remaining complete pair', async () => {
  renderProfile()
  await screen.findByText(legacyAgent.name)
  await screen.findAllByTestId('fallback-chip-warning-legacy-model')

  fireEvent.click(screen.getAllByTestId('fallback-chip-remove-legacy-model')[0])

  await waitFor(() => {
    expect(screen.queryAllByTestId('fallback-chip-remove-legacy-model')).toHaveLength(0)
    expect(screen.queryAllByTestId('fallback-incomplete-notice')).toHaveLength(0)
  })
  expect(screen.getAllByTestId('fallback-chip-remove-paired-model').length).toBeGreaterThan(0)

  await waitFor(
    () => expect(vi.mocked(updateAgent).mock.calls.length).toBeGreaterThan(0),
    { timeout: 6000 },
  )
  const { id, payload } = lastUpdate()
  expect(id).toBe(legacyAgent.id)
  expect(payload.fallback_models as SentChain).toEqual([{ model: 'paired-model', provider: 'openrouter' }])
})

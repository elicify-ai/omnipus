// RR1 pr-test F3 / SFH N1: real global-policy adapter, consumer, editor,
// autosave hook and indicator. Only network reads and unrelated shell sections
// are substituted. The PUT always runs through the real request() boundary.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { GlobalToolPolicies } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => ({
  ...await importOriginal<typeof import('@/lib/api')>(),
  fetchConfig: vi.fn(), fetchCredentials: vi.fn(), fetchBuiltinTools: vi.fn(),
  fetchGlobalToolPolicies: vi.fn(), fetchSandboxConfig: vi.fn(),
  fetchSandboxStatus: vi.fn(), fetchAppState: vi.fn(),
}))
vi.mock('./DiagnosticsSection', () => ({ DiagnosticsSection: () => null }))
vi.mock('./SandboxSection', () => ({ SandboxSection: () => null }))
vi.mock('./AuditLogViewer', () => ({ AuditLogViewer: () => null }))
vi.mock('./PromptGuardSection', () => ({ PromptGuardSection: () => null }))
vi.mock('./ExecProxyStatusCard', () => ({ ExecProxyStatusCard: () => null }))
vi.mock('./SkillTrustSection', () => ({ SkillTrustSection: () => null }))

import * as api from '@/lib/api'
import { SecuritySection } from './SecuritySection'

// Exact OpenAPI 503 example; docs/security.md::Set tool policies specifies
// retaining this server recovery message as an error, never a Saved result.
const RECOVERY = 'The settings were saved, but could not be applied to running agents. Retry applying the change, or restart Omnipus.'
const INTERNAL = 'INTERNAL_RELOAD_ONLY: registry creation failed: PRIVATE_OPERATOR_CONTEXT'
const A: GlobalToolPolicies = { policies: { bash: 'allow' } }
const B: GlobalToolPolicies = { policies: { bash: 'deny' } }
const fetchMock = vi.fn<typeof fetch>()
const clients: QueryClient[] = []

function jsonResponse(body: unknown, status = 503) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function deferredResponse() {
  let resolve!: (response: Response) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<Response>((resolveResponse, rejectResponse) => {
    resolve = resolveResponse
    reject = rejectResponse
  })
  return { promise, resolve, reject }
}

beforeEach(() => {
  vi.clearAllMocks()
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
  document.cookie = 'csrf=rr1-policy-proof; Path=/'
  vi.mocked(api.fetchConfig).mockResolvedValue({ security: { rate_limits: {} }, gateway: {}, agents: { defaults: {} } } as never)
  vi.mocked(api.fetchCredentials).mockResolvedValue([])
  vi.mocked(api.fetchBuiltinTools).mockResolvedValue([{ name: 'bash', description: 'Run a shell command', category: 'code', scope: 'core', source: 'builtin' }])
  vi.mocked(api.fetchGlobalToolPolicies).mockResolvedValue(structuredClone(A))
  vi.mocked(api.fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
  vi.mocked(api.fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: true } as never)
  vi.mocked(api.fetchAppState).mockResolvedValue({ onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } } as never)
})

afterEach(() => {
  cleanup()
  for (const client of clients.splice(0)) client.clear()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; Path=/'
})

async function openGlobalPolicies() {
  expect(vi.isMockFunction(api.updateGlobalToolPolicies), 'the changed adapter must stay real').toBe(false)
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  clients.push(client)
  const rendered = render(<QueryClientProvider client={client}><SecuritySection /></QueryClientProvider>)
  fireEvent.click(await screen.findByTestId('advanced-disclosure-trigger'))
  const category = await screen.findByTestId(/^category-pill-/)
  expect(category.textContent, 'positive control: persisted map A was hydrated').toBe('Allow')
  fireEvent.click(category)
  const row = await screen.findByTestId('tool-row-bash')
  expect(within(row).getByRole('button', { name: /^Deny$/ })).toBeEnabled()
  expect(fetchMock, 'hydration must not send a PUT').not.toHaveBeenCalled()
  return { ...rendered, row }
}

function expectPut(number: number, body: GlobalToolPolicies) {
  expect(fetchMock).toHaveBeenNthCalledWith(number, '/api/v1/security/tool-policies', expect.objectContaining({
    method: 'PUT', credentials: 'include', body: JSON.stringify(body),
  }))
}

async function queueAWhileBIsPending() {
  const first = deferredResponse()
  const second = deferredResponse()
  fetchMock.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise)
  const rendered = await openGlobalPolicies()
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  fireEvent.click(within(rendered.row).getByRole('button', { name: /^Deny$/ }))
  await act(async () => { await vi.advanceTimersByTimeAsync(500) })
  expect(fetchMock).toHaveBeenCalledTimes(1)
  expectPut(1, B)
  fireEvent.click(within(rendered.row).getByRole('button', { name: /^Allow$/ }))
  await act(async () => { await vi.advanceTimersByTimeAsync(500) })
  expect(fetchMock, 'queue latest A without overlapping the held B request').toHaveBeenCalledTimes(1)
  // Both debounce callbacks have run. Use real timers for actual network-body
  // completion; no clock threshold is used as proof that a save happened.
  vi.useRealTimers()
  return { ...rendered, first, second }
}

describe('global policy recovery through the real settings consumer', () => {
  it('renders the exact saved-but-not-active recovery message and never the internal reload detail', async () => {
    const body = { error: RECOVERY, details: { reload_error: INTERNAL } }
    expect(JSON.stringify(body)).toContain(INTERNAL) // Non-vacuous leak control.
    fetchMock.mockResolvedValueOnce(jsonResponse(body))
    const { row, container } = await openGlobalPolicies()
    fireEvent.click(within(row).getByRole('button', { name: /^Deny$/ }))
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(RECOVERY)
    expect(container.innerHTML).not.toContain(INTERNAL)
    expect(screen.queryByText('Saved', { exact: true })).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expectPut(1, B)
    expect(api.fetchGlobalToolPolicies, 'failed activation is not a successful-save callback').toHaveBeenCalledTimes(1)
  })

  it('sends queued A after persisted B fails activation and retains that failure until A has an actual outcome', async () => {
    const { first, second, container } = await queueAWhileBIsPending()
    await act(async () => { first.resolve(jsonResponse({ error: RECOVERY, details: { reload_error: INTERNAL } })) })
    await waitFor(() => expect(fetchMock, 'the saved B baseline must not suppress the queued correction back to A').toHaveBeenCalledTimes(2))
    expectPut(2, A)
    expect(screen.getByRole('alert').textContent, 'first failure stays visible while the corrective PUT is still pending').toBe(RECOVERY)
    expect(container.innerHTML).not.toContain(INTERNAL)
    expect(screen.queryByText('Saved', { exact: true })).not.toBeInTheDocument()
    expect(api.fetchGlobalToolPolicies).toHaveBeenCalledTimes(1)
    await act(async () => { second.resolve(jsonResponse(A, 200)) })
    expect(await screen.findByText('Saved', { exact: true })).toBeInTheDocument()
    expect(screen.queryByText(RECOVERY, { exact: true })).not.toBeInTheDocument()
    expect(fetchMock).toHaveBeenCalledTimes(2)
    await waitFor(() => expect(api.fetchGlobalToolPolicies).toHaveBeenCalledTimes(2))
  })

  it.each(['generic 503', 'network error'] as const)('does not treat a %s as a committed B write', async (kind) => {
    const { first } = await queueAWhileBIsPending()
    await act(async () => {
      if (kind === 'generic 503') first.resolve(jsonResponse({ error: 'Service is temporarily unavailable.' }))
      else first.reject(new TypeError('Failed to fetch'))
    })
    const alert = await screen.findByRole('alert')
    expect(alert.textContent).toBe(kind === 'generic 503' ? 'Service is temporarily unavailable.' : 'Network unavailable. Check your connection.')
    expect(fetchMock, 'A is already the persisted baseline when B never committed').toHaveBeenCalledTimes(1)
    expectPut(1, B)
    expect(api.fetchGlobalToolPolicies).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('Saved', { exact: true })).not.toBeInTheDocument()
  })
})

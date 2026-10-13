// U2-C08–C10. FR-241, K6/K49, §6.3 "Agent editor private secrets list";
// US-7 AS-7 / BDD-7.12. UI half only: command injection is a backend/UAT row.
import { it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
// Real QueryClient, with automatic retries disabled to make the visible
// Error/Retry path deterministic. Same fixture pattern as Sidebar.retry.
const editorClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
import type { SecretNameEntry } from '@/lib/api/generated/openapi-types'
import { deferred, installNetwork, jsonResponse, loadPrivateSecrets, requests, resetSharedState } from '../../../tests/l1-ui-red-u23/support'
import type { ComponentType } from 'react'

const privateEntry: SecretNameEntry = {
  reference: 'private_sales_mia_WEATHER_TOKEN.v2', kind: 'agent_private', version: 2,
  recipient: { kind: 'agent_private', lines: ['Sales / Mia', 'WEATHER_TOKEN'] },
  owner: { workspace_id: 'sales', agent_id: 'mia' }, consumers: ['Mia in Sales'], state: 'selected',
}
const entries: SecretNameEntry[] = [
  privateEntry,
  { ...privateEntry, reference: 'private_sales_mia_WEATHER_TOKEN.v1', version: 1, state: 'superseded', consumers: [], available_in_channel_turns: false },
  { ...privateEntry, reference: 'private_research_mia_API_KEY.v1', version: 1, owner: { workspace_id: 'research', agent_id: 'mia' }, recipient: { kind: 'agent_private', lines: ['Research / Mia', 'API_KEY'] }, available_in_channel_turns: true },
  { ...privateEntry, reference: 'private_sales_jim_ONLY_JIM.v1', owner: { workspace_id: 'sales', agent_id: 'jim' } },
  { ...privateEntry, reference: 'openai_provider_key.v1', kind: 'provider_key', owner: undefined, recipient: { kind: 'provider_key', lines: ['https://api.openai.com/v1'] } },
]
let Editor: ComponentType<{ agentId: string }>
function mount(agentId = 'mia') {
  return render(<QueryClientProvider client={editorClient}><Editor agentId={agentId} /></QueryClientProvider>)
}
beforeEach(async () => {
  resetSharedState()
  Editor = await loadPrivateSecrets()
  installNetwork(call => {
    if (call.path === '/api/v1/secrets' && call.method === 'GET') return jsonResponse(entries)
    throw new Error(`Unexpected ${call.method} ${call.path}`)
  })
})
afterEach(() => { editorClient.clear(); vi.unstubAllGlobals() })

it('K6 / FR-241: lists only this agent’s private versions with owner/state metadata and no secret fields', async () => {
  mount()
  await screen.findAllByText(/Sales \/ Mia.*WEATHER_TOKEN/)
  const rows = screen.getAllByRole('listitem')
  expect(rows).toHaveLength(3)
  expect(rows[0]).toHaveTextContent(/WEATHER_TOKEN.*version 2/)
  expect(rows[1]).toHaveTextContent(/WEATHER_TOKEN.*version 1/)
  expect(rows[2]).toHaveTextContent(/API_KEY.*version 1/)
  expect(screen.queryByText(/ONLY_JIM/)).not.toBeInTheDocument()
  expect(screen.queryByText('https://api.openai.com/v1')).not.toBeInTheDocument()
  expect(document.body.textContent).toContain('sales')
  expect(document.body.textContent).toContain('research')
  expect(document.body.textContent).toMatch(/selected|in use/i)
  expect(document.body.textContent).toMatch(/superseded|replaced/i)
  expect(document.querySelector('input[type="password"]')).toBeNull()
  expect(requests.map(c => ({ path: c.path, method: c.method, credentials: c.credentials }))).toEqual([
    { path: '/api/v1/secrets', method: 'GET', credentials: 'include' },
  ])
})

it('FR-241 / K6 / BDD-7.12: omitted availability flag and explicit false are off; true is on', async () => {
  mount()
  await screen.findAllByText(/Sales \/ Mia.*WEATHER_TOKEN/)
  const switches = screen.getAllByLabelText('Available in channel conversations')
  expect(switches.map(toggle => toggle.getAttribute('aria-checked'))).toEqual(['false', 'false', 'true'])
  expect(requests.filter(c => c.method !== 'GET')).toEqual([])
})

it('FR-241 / K49: deliberate toggle sends only availability to the exact reference and reflects confirmed state', async () => {
  const confirmation = deferred<Response>()
  installNetwork(call => call.method === 'GET' ? jsonResponse([privateEntry]) : confirmation.promise)
  mount()
  const toggle = await screen.findByLabelText('Available in channel conversations')
  fireEvent.click(toggle)
  await waitFor(() => expect(requests.filter(c => c.method === 'PATCH')).toHaveLength(1))
  const patch = requests.find(c => c.method === 'PATCH')!
  expect({ path: patch.path, body: JSON.parse(patch.body!), credentials: patch.credentials }).toEqual({
    path: `/api/v1/secrets/${encodeURIComponent(privateEntry.reference)}/options`,
    body: { available_in_channel_turns: true }, credentials: 'include',
  })
  expect(patch.headers.get('X-CSRF-Token')).toBe('l1-red-csrf')
  expect(toggle).toHaveAttribute('aria-checked', 'false') // no unconfirmed optimistic success
  await act(async () => confirmation.resolve(jsonResponse({ ...privateEntry, available_in_channel_turns: true })))
  await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
  expect(screen.getByText(/saved/i)).toBeInTheDocument()
  expect(requests.filter(c => c.method === 'PATCH')).toHaveLength(1)
})

it('FR-241 / K49: off toggle sends false, not a deletion or a credential write', async () => {
  installNetwork(call => call.method === 'GET' ? jsonResponse([{ ...privateEntry, available_in_channel_turns: true }]) : jsonResponse({ ...privateEntry, available_in_channel_turns: false }))
  mount()
  const toggle = await screen.findByLabelText('Available in channel conversations')
  expect(toggle).toHaveAttribute('aria-checked', 'true')
  fireEvent.click(toggle)
  await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
  expect(requests.filter(c => c.method !== 'GET').map(c => ({ path: c.path, method: c.method, body: JSON.parse(c.body!) }))).toEqual([
    { path: `/api/v1/secrets/${privateEntry.reference}/options`, method: 'PATCH', body: { available_in_channel_turns: false } },
  ])
})

it('§6.3: lost toggle response is not confirmed, retains the last known flag, and is never replayed automatically', async () => {
  const lookup = deferred<Response>()
  let reads = 0
  installNetwork(call => {
    if (call.method === 'PATCH') return Promise.reject(new TypeError('response lost'))
    reads++
    return reads === 1 ? jsonResponse([privateEntry]) : lookup.promise
  })
  mount()
  const toggle = await screen.findByLabelText('Available in channel conversations')
  fireEvent.click(toggle)
  await screen.findByText(/not confirmed/i)
  expect(screen.queryByText(/^saved$/i)).not.toBeInTheDocument()
  expect(toggle).toHaveAttribute('aria-checked', 'false')
  expect(requests.filter(c => c.method === 'PATCH')).toHaveLength(1)
  await act(async () => lookup.resolve(jsonResponse([privateEntry])))
  expect(requests.filter(c => c.method === 'PATCH')).toHaveLength(1)
})

it('§6.3: pending list is Loading, not No private secrets', async () => {
  const pending = deferred<Response>()
  installNetwork(() => pending.promise)
  mount()
  // §6.3 requires a loading state, not the literal word Loading.
  expect(document.querySelector('[aria-busy="true"], [role="status"]')).not.toBeNull()
  expect(screen.queryByText(/no private secrets/i)).not.toBeInTheDocument()
  await act(async () => pending.resolve(jsonResponse([])))
  expect(await screen.findByText('No private secrets')).toBeInTheDocument()
})

it('§6.3: a true empty private list is explicit, even when integrations/another agent exist', async () => {
  installNetwork(() => jsonResponse(entries.slice(3)))
  mount()
  expect(await screen.findByText('No private secrets')).toBeInTheDocument()
  expect(screen.queryByLabelText('Available in channel conversations')).not.toBeInTheDocument()
})

it('§6.3: failed initial list shows error and Retry, never a false empty list; Retry reads again', async () => {
  let reads = 0
  installNetwork(() => ++reads === 1 ? jsonResponse({ error: 'secret list unavailable' }, 503) : jsonResponse([privateEntry]))
  mount()
  const retry = await screen.findByRole('button', { name: /retry/i })
  expect(screen.queryByText('No private secrets')).not.toBeInTheDocument()
  expect(document.body.textContent).toMatch(/unavailable|error|could not|failed/i)
  fireEvent.click(retry)
  const restoredRows = await screen.findAllByText(/Sales \/ Mia.*WEATHER_TOKEN/)
  expect(restoredRows).toHaveLength(1)
  expect(restoredRows[0]).toBeInTheDocument()
  expect(requests.filter(c => c.method === 'GET')).toHaveLength(2)
})

it('§6.3: cached metadata survives a failed refresh with an explicit stale hint', async () => {
  let fail = false
  installNetwork(() => fail ? jsonResponse({ error: 'refresh unavailable' }, 503) : jsonResponse([privateEntry]))
  mount()
  await screen.findAllByText(/Sales \/ Mia.*WEATHER_TOKEN/)
  fail = true
  await act(async () => { await editorClient.invalidateQueries() })
  expect(screen.getByText(/Sales \/ Mia.*WEATHER_TOKEN/)).toBeInTheDocument()
  expect(document.body.textContent).toMatch(/out of date|cached|stale/i)
  expect(screen.queryByText('No private secrets')).not.toBeInTheDocument()
})

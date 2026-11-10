// Independent oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1105, exact result table, and QA acceptance Safety/concurrency and
// Leakage/UI/reachability. Keep the section, API and generated validators real;
// replace only browser fetch at the gateway boundary.

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { useUiStore } from '@/store/ui'
import {
  heldReply,
  jsonReply,
  keyRemovedCatalogue,
  savedSearchCatalogue,
  searchSettingsHarness,
} from '../../../tests/helpers/search-settings-fixture'

const checkPath = '/api/v1/integrations/providers/tavily/check'
const removalPath = '/api/v1/integrations/providers/tavily'
const statuses = [
  ['success', 'Connection works.'],
  ['auth_error', 'The service rejected your key. Edit it and try again.'],
  ['rate_limited', 'The service is limiting requests. Wait and try again.'],
  ['timeout', 'The service did not respond in time. Try again.'],
  ['network_error', 'Could not reach the service. Try again.'],
  ['provider_error', 'The service could not complete the check. Try again.'],
  ['invalid_response', 'The service returned an unexpected response. Try again.'],
] as const

async function mountTavily(h: ReturnType<typeof searchSettingsHarness>) {
  h.render()
  return screen.findByTestId('search-row-tavily')
}

afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  useUiStore.setState({ toasts: [] })
  document.cookie = 'csrf=; Max-Age=0; Path=/'
})

describe('#1105 Check a web-search connection', () => {
  it('puts Check connection between Edit key and Remove key, explains cost and Ready, and never runs on mount', async () => {
    const h = searchSettingsHarness()
    const row = await mountTavily(h)
    const buttons = within(row).getAllByRole('button').map((button) => button.textContent)
    expect(buttons).toEqual(['Edit key', 'Check connection', 'Remove key'])
    expect(screen.getByText('Runs one small search with your saved key. Your service may charge for it.')).toBeInTheDocument()
    expect(screen.getByText('Ready means the key is loaded. Check connection tests it with the service.')).toBeInTheDocument()
    expect(h.calls('POST', checkPath)).toHaveLength(0)
    expect(h.calls('POST', '/api/v1/integrations/providers/exa/check')).toHaveLength(0)
  })

  it('uses the real POST adapter with a closed empty object and no password step-up', async () => {
    const h = searchSettingsHarness('local')
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    await waitFor(() => expect(h.calls('POST', checkPath)).toHaveLength(1))
    const [, init] = h.calls('POST', checkPath)[0]
    expect(JSON.parse(String(init?.body))).toEqual({})
    expect(new Headers(init?.headers).get('X-Reauth-Token')).toBeNull()
    expect(init?.credentials).toBe('include')
    expect(h.calls('POST', '/api/v1/auth/reauth')).toHaveLength(0)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(await within(row).findByText('Connection works.')).toBeInTheDocument()
  })

  it('shows per-service Checking connection pending state and disables duplicate/conflicting row actions', async () => {
    const h = searchSettingsHarness()
    const hold = heldReply()
    h.checkReply = () => hold.promise
    const row = await mountTavily(h)
    const check = within(row).getByRole('button', { name: 'Check connection' })
    fireEvent.click(check)
    fireEvent.click(check)
    expect(within(row).getByText('Checking connection…')).toBeInTheDocument()
    expect(check).toBeDisabled()
    expect(within(row).getByRole('button', { name: 'Edit key' })).toBeDisabled()
    expect(within(row).getByRole('button', { name: 'Remove key' })).toBeDisabled()
    expect(within(screen.getByTestId('search-row-exa')).getByRole('button', { name: 'Check connection' })).toBeEnabled()
    expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    await act(async () => { hold.resolve(jsonReply({ provider_id: 'tavily', status: 'success', checked_at: '2026-09-30T12:00:00Z' })) })
    expect(await within(row).findByText('Connection works.')).toBeInTheDocument()
    expect(within(row).queryByText('Checking connection…')).not.toBeInTheDocument()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
  })

  it.each(statuses)('renders exact %s copy inline, refetches readiness, and never disables or reassigns from a check result', async (status, message) => {
    const h = searchSettingsHarness()
    h.checkReply = () => jsonReply({ provider_id: 'tavily', status, checked_at: '2026-09-30T12:00:00Z' })
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    expect(await within(row).findByText(message)).toBeInTheDocument()
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    expect(within(row).getByText('Ready')).toBeInTheDocument()
    expect(screen.getByTestId('default-search-card')).toHaveTextContent('Tavily')
    expect(screen.getByTestId('fallback-search-card')).toHaveTextContent('DuckDuckGo')
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    expect(useUiStore.getState().toasts).toEqual([])
  })

  it('a failed attempt also refetches actual backend usability rather than trusting the old Ready badge', async () => {
    const h = searchSettingsHarness()
    h.checkReply = () => {
      const fresh = savedSearchCatalogue()
      fresh.search = fresh.search.map((row) => row.id === 'tavily' ? { ...row, usable: false } : row)
      h.replaceCatalogue(fresh)
      return jsonReply({ provider_id: 'tavily', status: 'auth_error', checked_at: '2026-09-30T12:00:00Z' })
    }
    const row = await mountTavily(h)
    expect(within(row).getByText('Ready')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    expect(within(row).queryByText('Ready')).not.toBeInTheDocument()
    expect(within(row).getByText('Key not reaching search')).toBeInTheDocument()
    expect(within(row).getByText('The service rejected your key. Edit it and try again.')).toBeInTheDocument()
  })

  it('disables incomplete setup with exact help, including a saved key that is not runtime-usable', async () => {
    const initial = savedSearchCatalogue()
    initial.search = initial.search.map((row) => row.id === 'exa' ? { ...row, usable: false } : row)
    const h = searchSettingsHarness('platform', initial)
    h.render()
    const missing = await screen.findByTestId('search-row-brave')
    const unloaded = screen.getByTestId('search-row-exa')
    for (const row of [missing, unloaded]) {
      expect(within(row).getByRole('button', { name: 'Check connection' })).toBeDisabled()
      expect(within(row).getByText('Add or save a key before checking.')).toBeInTheDocument()
    }
    expect(within(screen.getByTestId('search-row-duckduckgo')).queryByRole('button', { name: 'Check connection' })).not.toBeInTheDocument()
    expect(h.fetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(0)
  })

  it('uses Retry-After for exact local cooldown copy without retrying the check automatically', async () => {
    const h = searchSettingsHarness()
    h.checkReply = () => jsonReply({ error: 'local check cooldown' }, 429, { 'Retry-After': '30' })
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    expect(await within(row).findByText('You can check again in 30 seconds.')).toBeInTheDocument()
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    expect(within(row).getByRole('button', { name: 'Check connection' })).toBeDisabled()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument()
  })

  it('allows a fresh manual check after the 30-second cooldown, not a cached result or automatic attempt', async () => {
    // Only the external clock is replaced. Real query/mutation/HTTP/UI logic
    // remains in the boundary, and all pending replies resolve normally.
    const h = searchSettingsHarness()
    const row = await mountTavily(h)
    // Load the catalogue on the real clock; install the clock edge BEFORE
    // clicking Check, so the actual admission/cooldown timers are controlled.
    vi.useFakeTimers()
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(within(row).getByText('Connection works.')).toBeInTheDocument()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(29_000) })
    expect(within(row).getByRole('button', { name: 'Check connection' })).toBeDisabled()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    await act(async () => { await vi.advanceTimersByTimeAsync(2_000) })
    expect(h.calls('POST', checkPath)).toHaveLength(1)
    expect(within(row).getByRole('button', { name: 'Check connection' })).toBeEnabled()
    h.checkReply = () => jsonReply({ provider_id: 'tavily', status: 'auth_error', checked_at: '2026-09-30T12:00:31Z' })
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    await act(async () => { await vi.advanceTimersByTimeAsync(1) })
    expect(h.calls('POST', checkPath)).toHaveLength(2)
    expect(within(row).getByText('The service rejected your key. Edit it and try again.')).toBeInTheDocument()
    expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument()
  })

  it('saving a replacement key clears an old result and never starts an automatic diagnostic', async () => {
    const h = searchSettingsHarness()
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    expect(await within(row).findByText('Connection works.')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Edit key' }))
    fireEvent.change(screen.getByTestId('key-input-tavily'), { target: { value: 'new-key' } })
    fireEvent.click(screen.getByTestId('save-tavily'))
    fireEvent.click(screen.getByRole('button', { name: 'Update integration' }))
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    await waitFor(() => expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument())
    expect(JSON.parse(String(h.calls('PUT', removalPath)[0][1]?.body))).toEqual({ kind: 'search', api_key: 'new-key' })
    expect(h.calls('POST', checkPath)).toHaveLength(1)
  })

  it('removing the key clears the old diagnostic and never checks the removed service automatically', async () => {
    const h = searchSettingsHarness()
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    expect(await within(row).findByText('Connection works.')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Remove key' }))
    fireEvent.click(within(screen.getByRole('alertdialog', { name: 'Remove the Tavily key?' })).getByRole('button', { name: 'Remove key' }))
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    await waitFor(() => expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument())
    expect(within(row).getByRole('button', { name: 'Add key' })).toBeEnabled()
    expect(within(row).getByRole('button', { name: 'Check connection' })).toBeDisabled()
    expect(h.calls('POST', checkPath)).toHaveLength(1)
  })

  it('suppresses a late check result after a newer catalogue generation removed the key in another tab', async () => {
    const h = searchSettingsHarness()
    const hold = heldReply()
    h.checkReply = () => hold.promise
    const row = await mountTavily(h)
    fireEvent.click(within(row).getByRole('button', { name: 'Check connection' }))
    expect(within(row).getByText('Checking connection…')).toBeInTheDocument()
    h.replaceCatalogue(keyRemovedCatalogue())
    await act(async () => { await h.client.invalidateQueries({ queryKey: ['integrations'] }) })
    expect(within(row).getByRole('button', { name: 'Add key' })).toBeInTheDocument()
    await act(async () => { hold.resolve(jsonReply({ provider_id: 'tavily', status: 'success', checked_at: '2026-09-30T12:00:00Z' })) })
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers').length).toBeGreaterThanOrEqual(2))
    expect(within(row).queryByText('Connection works.')).not.toBeInTheDocument()
    expect(within(row).queryByText('Ready')).not.toBeInTheDocument()
    expect(within(row).getByRole('button', { name: 'Add key' })).toBeEnabled()
  })
})

// Independent oracle: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-1104-1105/design.md,
// Decision #1104, Exact UI and QA acceptance Consent/cancel, Roles/ownership,
// Leakage/UI/reachability. Expected copy is fixed before running the component.

import { afterEach, describe, expect, it, vi } from 'vitest'
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useUiStore } from '@/store/ui'
import {
  heldReply,
  jsonReply,
  keyRemovedCatalogue,
  savedSearchCatalogue,
  searchSettingsHarness,
} from '../../../tests/helpers/search-settings-fixture'

const removalPath = '/api/v1/integrations/providers/tavily'
const confirmationBody = 'This deletes the saved key from Omnipus and switches off Tavily. It does not revoke the key with Tavily.'
const defaultRoleCopy = 'Tavily will remain your default search service. Omnipus will not choose a replacement. Add a key or change that choice to fix it.'

async function openRemoval(harness: ReturnType<typeof searchSettingsHarness>) {
  harness.render()
  const row = await screen.findByTestId('search-row-tavily')
  fireEvent.click(within(row).getByRole('button', { name: 'Remove key' }))
  return screen.getByRole('alertdialog', { name: 'Remove the Tavily key?' })
}

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  useUiStore.setState({ toasts: [] })
  document.cookie = 'csrf=; Max-Age=0; Path=/'
})

describe('#1104 Remove a saved web-search key', () => {
  it('keeps Edit key and exposes Remove key only for saved keyed search services', async () => {
    const h = searchSettingsHarness()
    h.render()
    const row = await screen.findByTestId('search-row-tavily')
    expect(within(row).getByRole('button', { name: 'Edit key' })).toBeEnabled()
    expect(within(row).getByRole('button', { name: 'Remove key' })).toBeEnabled()
    expect(within(screen.getByTestId('search-row-exa')).getByRole('button', { name: 'Remove key' })).toBeEnabled()
    expect(within(screen.getByTestId('search-row-brave')).queryByRole('button', { name: 'Remove key' })).not.toBeInTheDocument()
    expect(within(screen.getByTestId('search-row-duckduckgo')).queryByRole('button', { name: 'Remove key' })).not.toBeInTheDocument()
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    expect(h.calls('POST', `${removalPath}/check`)).toHaveLength(0)
  })

  it('shows exact confirmation and retained default copy without sending a mutation on open or Cancel', async () => {
    const h = searchSettingsHarness('platform')
    const dialog = await openRemoval(h)
    expect(dialog).toHaveAccessibleDescription(`${confirmationBody} ${defaultRoleCopy}`)
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeEnabled()
    expect(within(dialog).getByRole('button', { name: 'Remove key' })).toBeEnabled()
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    expect(h.calls('POST', '/api/v1/auth/reauth')).toHaveLength(0)
    expect(useUiStore.getState().toasts).toEqual([])
  })

  it('explains an assigned fallback but never claims it chose a replacement', async () => {
    const catalogue = savedSearchCatalogue()
    catalogue.default_search = 'duckduckgo'
    catalogue.fallback_search = 'tavily'
    const h = searchSettingsHarness('platform', catalogue)
    const dialog = await openRemoval(h)
    expect(dialog).toHaveAccessibleDescription(`${confirmationBody} Tavily will remain your fallback search service. Omnipus will not choose a replacement. Add a key or change that choice to fix it.`)
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
  })

  it('uses only one platform confirmation and one PUT with clear_api_key true, no key or role fields', async () => {
    const h = searchSettingsHarness('platform')
    const dialog = await openRemoval(h)
    expect(screen.getAllByRole('alertdialog')).toHaveLength(1)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove key' }))
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    const [, init] = h.calls('PUT', removalPath)[0]
    expect(JSON.parse(String(init?.body))).toEqual({ kind: 'search', clear_api_key: true })
    expect(new Headers(init?.headers).get('X-Reauth-Token')).toBeNull()
    expect(new Headers(init?.headers).get('X-CSRF-Token')).toBe('search-settings-test-csrf')
    expect(h.calls('POST', '/api/v1/auth/reauth')).toHaveLength(0)
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.queryByRole('dialog', { name: 'Confirm your password' })).not.toBeInTheDocument()
    await waitFor(() => expect(useUiStore.getState().toasts.map(({ message, variant }) => ({ message, variant }))).toEqual([
      { message: 'Tavily key removed.', variant: 'success' },
    ]))
  })

  it('local mode confirms removal first, then mints password consent, then sends exactly one removal', async () => {
    const h = searchSettingsHarness('local')
    const dialog = await openRemoval(h)
    expect(dialog).toHaveAccessibleDescription(`${confirmationBody} ${defaultRoleCopy}`)
    expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove key' }))
    const password = await screen.findByTestId('reauth-password-input')
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    expect(h.calls('POST', '/api/v1/auth/reauth')).toHaveLength(0)
    fireEvent.change(password, { target: { value: 'correcthorse' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    const [, consent] = h.calls('POST', '/api/v1/auth/reauth')[0]
    expect(JSON.parse(String(consent?.body))).toEqual({ password: 'correcthorse' })
    const [, change] = h.calls('PUT', removalPath)[0]
    expect(JSON.parse(String(change?.body))).toEqual({ kind: 'search', clear_api_key: true })
    expect(new Headers(change?.headers).get('X-Reauth-Token')).toBe('fresh-single-use-consent')
  })

  it('cancelling the local password step-up sends no removal and is not an error toast', async () => {
    const h = searchSettingsHarness('local')
    const dialog = await openRemoval(h)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove key' }))
    fireEvent.click(await screen.findByTestId('reauth-cancel'))
    await waitFor(() => expect(screen.queryByTestId('reauth-password-input')).not.toBeInTheDocument())
    expect(h.calls('PUT', removalPath)).toHaveLength(0)
    expect(useUiStore.getState().toasts).toEqual([])
  })

  it('disables conflicting actions and duplicate accepts while Removing is pending', async () => {
    const h = searchSettingsHarness('platform')
    const hold = heldReply()
    h.removeReply = () => hold.promise
    const dialog = await openRemoval(h)
    const confirm = within(dialog).getByRole('button', { name: 'Remove key' })
    fireEvent.click(confirm)
    fireEvent.click(confirm)
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    expect(screen.getByText('Removing…')).toBeInTheDocument()
    const row = screen.getByTestId('search-row-tavily')
    expect(within(row).getByRole('button', { name: 'Edit key', hidden: true })).toBeDisabled()
    expect(within(row).getByRole('button', { name: 'Check connection', hidden: true })).toBeDisabled()
    for (const button of within(dialog).getAllByRole('button')) expect(button).toBeDisabled()
    h.replaceCatalogue(keyRemovedCatalogue())
    await act(async () => { hold.resolve(jsonReply(keyRemovedCatalogue())) })
    await waitFor(() => expect(screen.queryByText('Removing…')).not.toBeInTheDocument())
    expect(h.calls('PUT', removalPath)).toHaveLength(1)
  })

  it('success refetches catalogue, closes the editor and keeps the retained default Fix path', async () => {
    const h = searchSettingsHarness('platform')
    h.render()
    const row = await screen.findByTestId('search-row-tavily')
    fireEvent.click(within(row).getByRole('button', { name: 'Edit key' }))
    expect(screen.getByTestId('key-input-tavily')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Remove key' }))
    fireEvent.click(within(screen.getByRole('alertdialog', { name: 'Remove the Tavily key?' })).getByRole('button', { name: 'Remove key' }))
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    expect(screen.queryByTestId('key-input-tavily')).not.toBeInTheDocument()
    const card = screen.getByTestId('default-search-card')
    expect(within(card).getByText('Tavily: key missing — searches will fail')).toBeInTheDocument()
    expect(screen.getByTestId('fallback-search-card')).toHaveTextContent('DuckDuckGo')
    expect(within(screen.getByTestId('search-row-tavily')).getByRole('button', { name: 'Add key' })).toBeEnabled()
    expect(within(screen.getByTestId('search-row-tavily')).queryByRole('button', { name: 'Remove key' })).not.toBeInTheDocument()
    fireEvent.click(within(card).getByRole('button', { name: 'Fix' }))
    expect(screen.getByTestId('key-input-tavily')).toBeInTheDocument()
  })

  it('retains the removed fallback and shows the exact not-running warning plus Fix', async () => {
    const initial = savedSearchCatalogue()
    initial.default_search = 'duckduckgo'
    initial.fallback_search = 'tavily'
    const h = searchSettingsHarness('platform', initial)
    const dialog = await openRemoval(h)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove key' }))
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    const card = screen.getByTestId('fallback-search-card')
    expect(within(card).getByText('Tavily: key missing — the fallback will not run')).toBeInTheDocument()
    fireEvent.click(within(card).getByRole('button', { name: 'Fix' }))
    expect(screen.getByTestId('key-input-tavily')).toBeInTheDocument()
    expect(screen.getByTestId('default-search-card')).toHaveTextContent('DuckDuckGo')
  })

  it('a partial failure refetches persisted state and never shows a removal-success toast', async () => {
    const h = searchSettingsHarness('platform')
    const message = 'Could not remove the saved key. The service has been switched off. Try again.'
    h.removeReply = () => {
      h.replaceCatalogue(keyRemovedCatalogue())
      return jsonReply({ error: message }, 500)
    }
    const dialog = await openRemoval(h)
    fireEvent.click(within(dialog).getByRole('button', { name: 'Remove key' }))
    await waitFor(() => expect(h.calls('GET', '/api/v1/integrations/providers')).toHaveLength(2))
    expect(screen.getByTestId('default-search-card')).toHaveTextContent('Tavily: key missing — searches will fail')
    expect(useUiStore.getState().toasts.map(({ message, variant }) => ({ message, variant }))).toEqual([
      { message, variant: 'error' },
    ])
    expect(h.calls('PUT', removalPath)).toHaveLength(1)
  })

  it('keyboard confirmation reaches the real removal request without changing its body', async () => {
    const h = searchSettingsHarness('platform')
    const user = userEvent.setup()
    h.render()
    const row = await screen.findByTestId('search-row-tavily')
    const remove = within(row).getByRole('button', { name: 'Remove key' })
    remove.focus()
    await user.keyboard('{Enter}')
    const dialog = screen.getByRole('alertdialog', { name: 'Remove the Tavily key?' })
    within(dialog).getByRole('button', { name: 'Remove key' }).focus()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(h.calls('PUT', removalPath)).toHaveLength(1))
    expect(JSON.parse(String(h.calls('PUT', removalPath)[0][1]?.body))).toEqual({ kind: 'search', clear_api_key: true })
  })
})

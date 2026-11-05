/**
 * Issue #1620 T8 — founder's server-enforced replacement rule and team-lead's
 * Round 4 refusal scenario. A failed default-model lookup cannot turn a
 * refused DELETE into a successful removal. Only fetch is replaced: the
 * section, dialog, picker, API client, error parser, toast store and rendered
 * refusal notice remain real. No assertion pins the separate T6/T7 headings.
 */
import { afterEach, beforeAll, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, Provider } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api'
import { useUiStore, type Toast } from '@/store/ui'
import { ToastContainer } from '@/components/ui/toast-container'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { ProvidersSection } from './ProvidersSection'

// Browser-environment shims, not replacements for the real picker/popover.
beforeAll(() => {
  if (!window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

const SERVER_REFUSAL = 'provider backs the default model; supply new_default'
// T8 requires a refusal, not raw HTTP-body display. The human-facing HTTP 409
// notice is documented in docs/internal/uat/ADR-051-rev4-error-copy-review.html,
// section B, row B14. Exact notice wording is a characterization lock, not a
// new T8 copy requirement; the required behavior is a visible refusal with no
// success or removed-provider claim. Keep the server reason pinned separately.
const REFUSAL_NOTICE = 'This conflicts with the current state. Please refresh and try again.'
const APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}
const PROVIDERS: Provider[] = [
  {
    id: 'openrouter', name: 'openrouter', display_name: 'OpenRouter',
    status: 'connected', auth_method: 'api_key', backs_default: true,
    models: ['openai/gpt-5.4'],
    dependents: [{ id: 'scout', name: 'Scout', role: 'primary' }],
  },
  {
    id: 'anthropic', name: 'anthropic', display_name: 'Anthropic',
    status: 'connected', auth_method: 'api_key', backs_default: false,
    models: ['claude-sonnet-4-5'], dependents: [],
  },
]

let client: QueryClient
let stopNotices: (() => void) | undefined
function clearToasts() {
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
}

beforeEach(() => {
  clearToasts()
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  document.cookie = 'csrf=provider-removal-test; Path=/'
})

afterEach(() => {
  stopNotices?.()
  stopNotices = undefined
  cleanup()
  client.clear()
  clearToasts()
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
})

it('shows the server refusal and keeps the default-backed provider listed after a failed lookup and a chosen replacement', async () => {
  const deleteBodies: unknown[] = []
  const notices: Pick<Toast, 'message' | 'variant'>[] = []
  const observed = new Set<string>()
  // Observe real notices as they arrive, so a transient false success cannot
  // escape the assertion by disappearing before the final DOM inspection.
  stopNotices = useUiStore.subscribe((state) => {
    for (const toast of state.toasts) {
      if (observed.has(toast.id)) continue
      observed.add(toast.id)
      notices.push({ message: toast.message, variant: toast.variant })
    }
  })
  const fetchMock = vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    const headers = { 'Content-Type': 'application/json' }
    if (method === 'GET' && path === '/api/v1/state') return new Response(JSON.stringify(APP_STATE), { headers })
    if (method === 'GET' && path === '/api/v1/providers') return new Response(JSON.stringify(PROVIDERS), { headers })
    if (method === 'GET' && path === '/api/v1/providers/catalog') return new Response(JSON.stringify(PROVIDERS_CATALOG), { headers })
    if (method === 'GET' && path === '/api/v1/providers/default-model') {
      return new Response(JSON.stringify({ error: 'default-model lookup unavailable' }), { status: 503, headers })
    }
    if (method === 'DELETE' && path === '/api/v1/providers/openrouter') {
      deleteBodies.push(JSON.parse(String(init?.body)))
      // The server response is authoritative even after the UI chose a pair.
      // This injected refusal is not a claim that every valid pair gets 409.
      return new Response(JSON.stringify({ error: SERVER_REFUSAL }), { status: 409, headers })
    }
    throw new Error(`Unexpected network request: ${method} ${path}`)
  })
  vi.stubGlobal('fetch', fetchMock)
  render(<QueryClientProvider client={client}><ProvidersSection /><ToastContainer /></QueryClientProvider>)

  // Positive controls: a real cold-query error and a roster that still marks
  // OpenRouter as backing the default. There is no cached pair to rescue it.
  expect(await screen.findByTestId('default-model-error')).toBeVisible()
  expect(client.getQueryData(['default-model'])).toBeUndefined()
  expect(await screen.findByTestId('provider-row-openrouter')).toBeVisible()
  fireEvent.click(screen.getByTestId('configure-btn-openrouter'))
  fireEvent.click(await screen.findByTestId('remove-provider-btn-openrouter'))
  const dialog = await screen.findByRole('alertdialog', { name: /^Remove OpenRouter/ })
  const remove = within(dialog).getByRole('button', { name: /^Remove$/ })
  expect(remove).toBeDisabled()
  expect(within(dialog).getByTestId('new-default-provider-anthropic')).toHaveTextContent('Anthropic')
  fireEvent.click(within(dialog).getByTestId('new-default-model-select'))
  fireEvent.click(await screen.findByTestId('new-default-model-claude-sonnet-4-5'))
  await waitFor(() => expect(remove).toBeEnabled())
  expect(deleteBodies).toEqual([])
  fireEvent.click(remove)

  await waitFor(() => expect(deleteBodies).toEqual([{ new_default: { provider: 'anthropic', model: 'claude-sonnet-4-5' } }]))
  await waitFor(() => expect(client.isMutating()).toBe(0))
  const mutationErrors = client.getMutationCache().getAll().map((mutation) => mutation.state.error)
  expect(mutationErrors, 'the real API client must reject the response as a typed error').toEqual([expect.any(ApiError)])
  expect(mutationErrors, 'the refusal must be the DELETE 409, not a lookup or transport error').toEqual([
    expect.objectContaining({ status: 409, body: JSON.stringify({ error: SERVER_REFUSAL }) }),
  ])
  const refusal = await screen.findByText(REFUSAL_NOTICE, { exact: true })
  expect(refusal, 'the real UI must visibly expose the server refusal').toBeVisible()
  expect(dialog, 'a refused DELETE must not dismiss its removal dialog').toBeVisible()
  expect(notices, 'the error must reach the real notice store').toContainEqual({ message: REFUSAL_NOTICE, variant: 'error' })
  expect(notices.filter((notice) => notice.variant === 'success'), 'never report a refused deletion as successful').toEqual([])
  expect(notices.map((notice) => notice.message)).not.toContain('Provider removed')
  expect(screen.queryByText('Provider removed', { exact: true })).not.toBeInTheDocument()
  expect(screen.getByTestId('provider-row-openrouter')).toBeInTheDocument()
  expect(client.getQueryData(['providers'])).toEqual(PROVIDERS)

  // Inspect the actual roster again after dismissing both overlays; a hidden
  // retained fixture cannot stand in for a provider still listed to the user.
  fireEvent.click(within(dialog).getByRole('button', { name: /^Cancel$/ }))
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  fireEvent.click(screen.getByTestId('cancel-provider-openrouter'))
  await waitFor(() => expect(screen.getByTestId('provider-row-openrouter')).toBeVisible())
  expect(notices.filter((notice) => notice.variant === 'success')).toEqual([])
})

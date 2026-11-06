/**
 * Issue #1639: custom model edits obey ADR-067 FR-035's PUT identity contract;
 * a refused model save is visible (issue #1639 Expected).
 * Only fetch is replaced. The form, editor, step-up gate, API/error handling,
 * generated response validation, query state and rendered toasts remain real.
 */
import { afterEach, beforeAll, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AppState, Provider } from '@/lib/api/generated/openapi-types'
import { ApiError } from '@/lib/api'
import { resetProvidersCatalogCache } from '@/lib/api/providers'
import { useUiStore, type Toast } from '@/store/ui'
import { ToastContainer } from '@/components/ui/toast-container'
import { PROVIDERS_CATALOG } from '@/test/fixtures/providersCatalog'
import { ProvidersSection } from './ProvidersSection'

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

const CUSTOM_ID = 'localtest'
const CUSTOM_BASE = 'https://manual-endpoint.example/v1'
const MODEL_SLUG = 'test/model-a'
const TEST_KEY = 'test-only-custom-provider-key'
const SERVER_REFUSAL = 'unknown provider "localtest"'
const REFUSAL_BODY = { error: SERVER_REFUSAL, field: 'id' }
const APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}
// Provider.yaml does NOT expose api_base. Do not fabricate that extra property:
// T1 enters it through the real creation form, then reopens the returned row.
const CUSTOM_PROVIDER: Provider = {
  id: CUSTOM_ID,
  name: CUSTOM_ID,
  display_name: CUSTOM_ID,
  status: 'connected',
  auth_method: 'api_key',
  custom: true,
  protocol: 'openai-compatible',
  locality: 'cloud',
  has_api_key: true,
  has_models_endpoint: false,
  models: [],
  dependents: [],
  backs_default: false,
}

let client: QueryClient
let stopNotices: (() => void) | undefined

function clearToasts() {
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
}

beforeEach(() => {
  clearToasts()
  resetProvidersCatalogCache()
  client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  document.cookie = 'csrf=custom-models-test; Path=/'
})

afterEach(() => {
  stopNotices?.()
  stopNotices = undefined
  cleanup()
  client.clear()
  clearToasts()
  resetProvidersCatalogCache()
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
})

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function installServer(providers: () => Provider[], onPut: () => Response) {
  const putBodies: unknown[] = []
  vi.stubGlobal('fetch', vi.fn<typeof fetch>().mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    if (method === 'GET' && path === '/api/v1/state') return jsonResponse(APP_STATE)
    if (method === 'GET' && path === '/api/v1/providers') return jsonResponse(providers())
    if (method === 'GET' && path === '/api/v1/providers/catalog') return jsonResponse(PROVIDERS_CATALOG)
    if (method === 'GET' && path === '/api/v1/providers/default-model') {
      return jsonResponse({ error: 'default model not set' }, 404)
    }
    if (method === 'PUT' && path === `/api/v1/providers/${CUSTOM_ID}`) {
      putBodies.push(JSON.parse(String(init?.body)))
      return onPut()
    }
    throw new Error(`Unexpected network request: ${method} ${path}`)
  }))
  return putBodies
}

function renderProviders() {
  render(<QueryClientProvider client={client}><ProvidersSection /><ToastContainer /></QueryClientProvider>)
}

async function addModelAndSave() {
  fireEvent.click(await screen.findByTestId(`configure-btn-${CUSTOM_ID}`))
  expect(await screen.findByTestId('provider-config-sheet'), 'the real custom editor must open').toBeVisible()
  fireEvent.change(screen.getByTestId(`add-model-input-${CUSTOM_ID}`), { target: { value: MODEL_SLUG } })
  fireEvent.click(screen.getByTestId(`add-model-${CUSTOM_ID}`))
  expect(screen.getByTestId(`model-list-${CUSTOM_ID}`), 'the draft must contain the entered slug').toHaveTextContent(MODEL_SLUG)
  fireEvent.click(screen.getByTestId(`save-provider-${CUSTOM_ID}`))
  fireEvent.click(await screen.findByRole('button', { name: 'Save API key', exact: true }))
}

it('resends the custom base and protocol, but not the key, when saving models after normal creation', async () => {
  let configured: Provider[] = []
  let saves = 0
  const sent = installServer(() => configured, () => {
    saves += 1
    // Scripted server responses are not the oracle: assert the actual request
    // bodies independently below. No API-client or component mock can add fields.
    const row: Provider = { ...CUSTOM_PROVIDER, models: saves === 1 ? [] : [MODEL_SLUG] }
    configured = [row]
    return jsonResponse(row)
  })
  renderProviders()

  fireEvent.click(await screen.findByTestId('connect-provider-btn'))
  fireEvent.click(await screen.findByTestId('picker-custom-endpoint'))
  fireEvent.change(screen.getByTestId('custom-endpoint-id'), { target: { value: CUSTOM_ID } })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-base'), { target: { value: CUSTOM_BASE } })
  fireEvent.change(screen.getByTestId('custom-endpoint-protocol'), { target: { value: 'openai-compatible' } })
  fireEvent.change(screen.getByTestId('custom-endpoint-api-key'), { target: { value: TEST_KEY } })
  fireEvent.click(screen.getByTestId('custom-endpoint-submit'))
  fireEvent.click(await screen.findByRole('button', { name: 'Save API key', exact: true }))

  await waitFor(() => expect(client.isMutating()).toBe(0))
  expect(await screen.findByTestId(`provider-row-${CUSTOM_ID}`), 'normal creation must produce a configured row').toBeVisible()
  expect(sent, 'creation must have sent the real identity pair collected by the form').toEqual([
    { api_key: TEST_KEY, api_base: CUSTOM_BASE, protocol: 'openai-compatible' },
  ])
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

  await addModelAndSave()
  await waitFor(() => expect(sent, 'one create and one model-save PUT must reach the network boundary').toHaveLength(2))
  // Oracle: ProviderUpdateRequest.api_base/protocol are required for custom PUT;
  // omitting api_key on an existing row preserves the encrypted key.
  expect(sent[1], 'a custom model-save PUT needs both identity fields, not just models').toEqual({
    models: [MODEL_SLUG],
    api_base: CUSTOM_BASE,
    protocol: 'openai-compatible',
  })
  await waitFor(() => expect(client.getQueryData(['providers'])).toEqual([
    { ...CUSTOM_PROVIDER, models: [MODEL_SLUG] },
  ]))
})

it('visibly explains a 400 model-save refusal and keeps the custom model draft without a success claim', async () => {
  const sent = installServer(() => [CUSTOM_PROVIDER], () => jsonResponse(REFUSAL_BODY, 400))
  const notices: Pick<Toast, 'message' | 'variant'>[] = []
  const observed = new Set<string>()
  stopNotices = useUiStore.subscribe((state) => {
    for (const toast of state.toasts) {
      if (observed.has(toast.id)) continue
      observed.add(toast.id)
      notices.push({ message: toast.message, variant: toast.variant })
    }
  })
  renderProviders()
  await addModelAndSave()

  await waitFor(() => expect(sent, 'the refusal must follow an actual editor PUT').toHaveLength(1))
  await waitFor(() => expect(client.isMutating()).toBe(0))
  const errors = client.getMutationCache().getAll().map((mutation) => mutation.state.error)
  expect(errors, 'the real API client must parse the refusal as a typed error').toEqual([expect.any(ApiError)])
  expect(errors, 'a CSRF, schema or transport error must not stand in for the injected 400').toEqual([
    expect.objectContaining({ status: 400, body: JSON.stringify(REFUSAL_BODY) }),
  ])
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  // An inline alert OR the real toast is acceptable; never require inline-only
  // copy merely to manufacture RED. A store call alone is not visible evidence.
  const refusal = await screen.findByText(SERVER_REFUSAL, { exact: true })
  expect(refusal, 'the server reason must be visible, not just stored behind a silent open sheet').toBeVisible()
  expect(screen.getByTestId('provider-config-sheet'), 'a rejected save must keep the editor open').toBeVisible()
  expect(screen.getByTestId(`model-list-${CUSTOM_ID}`), 'a rejected save must preserve the unsaved slug').toHaveTextContent(MODEL_SLUG)
  expect(notices.filter((notice) => notice.variant === 'success'), 'a refused save must never announce success').toEqual([])
  expect(screen.queryByText('Provider saved', { exact: true })).not.toBeInTheDocument()
  expect(client.getQueryData(['providers']), 'the saved catalogue must not pretend the rejected draft was persisted').toEqual([CUSTOM_PROVIDER])
})

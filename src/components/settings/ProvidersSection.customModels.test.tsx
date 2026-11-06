/**
 * Issue #1639, accepted architect ruling B (2026-11-06), rules 1 and 6:
 * model edits send only models; the server keeps stored custom identity.
 * A refused save visibly explains the reason and retains the draft.
 * Only HTTP/WebSocket process edges and browser shims are replaced. AppShell,
 * its existing toast host, the form/editor, step-up gate and API/store stay real.
 * jsdom visibility cannot establish Sheet/overlay occlusion in a real browser.
 */
import { afterEach, beforeAll, beforeEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from '@tanstack/react-router'
import type { AppState, Provider, GodModeStatus, NotificationList, VersionResponse } from '@/lib/api/generated/openapi-types'
import { queryClient } from '@/lib/queryClient'
import { ApiError } from '@/lib/api'
import { resetProvidersCatalogCache } from '@/lib/api/providers'
import { useUiStore, type Toast } from '@/store/ui'
import { useSidebarStore } from '@/store/sidebar'
import { useSessionStore } from '@/store/session'
import { useConnectionStore } from '@/store/connection'
import { useWorkspacesStore } from '@/store/workspacesStore'
import { useChatStore } from '@/store/chat'
import { useAuthStore } from '@/store/auth'
import { AppShell } from '@/components/layout/AppShell'
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

const client = queryClient
const originalDefaults = client.getDefaultOptions()
let stopNotices: (() => void) | undefined
let unexpectedEdges: string[]

// AppShell's real WsLifecycle may start, but this raw edge never connects or
// fabricates a server frame. The provider assertions do not depend on a socket.
class OfflineWebSocket extends EventTarget {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  readyState = OfflineWebSocket.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onclose: ((event: CloseEvent) => void) | null = null
  constructor(readonly url: string) { super() }
  send(body: string) {
    unexpectedEdges.push(`WebSocket send: ${body}`)
    throw new Error('The provider fixture never opens its raw transport')
  }
  close() { this.readyState = OfflineWebSocket.CLOSED }
}

function clearToasts() {
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
}

beforeEach(() => {
  localStorage.clear()
  clearToasts()
  resetProvidersCatalogCache()
  client.clear()
  client.setDefaultOptions({ queries: { retry: false }, mutations: { retry: false } })
  unexpectedEdges = []
  vi.stubGlobal('WebSocket', OfflineWebSocket)
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('min-width'), media: query, onchange: null,
    addListener: () => {}, removeListener: () => {}, addEventListener: () => {},
    removeEventListener: () => {}, dispatchEvent: () => false,
  }))
  useUiStore.setState(useUiStore.getInitialState(), true)
  useSidebarStore.setState({ isOpen: false, isPinned: false })
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useWorkspacesStore.setState({ activeWorkspaceId: null })
  useAuthStore.setState({ username: 'custom-models-test' })
  document.cookie = 'csrf=custom-models-test; Path=/'
})

afterEach(() => {
  stopNotices?.()
  stopNotices = undefined
  cleanup()
  client.clear()
  client.setDefaultOptions(originalDefaults)
  clearToasts()
  resetProvidersCatalogCache()
  useConnectionStore.setState(useConnectionStore.getInitialState(), true)
  useSessionStore.setState(useSessionStore.getInitialState(), true)
  useChatStore.setState(useChatStore.getInitialState(), true)
  useUiStore.setState(useUiStore.getInitialState(), true)
  useAuthStore.setState(useAuthStore.getInitialState(), true)
  localStorage.clear()
  document.cookie = 'csrf=; Path=/; Max-Age=0'
  vi.unstubAllGlobals()
  expect(unexpectedEdges, 'all HTTP/socket process edges must be explicit; no real network').toEqual([])
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
    // Companion AppShell APIs stay real; only their HTTP responses are fixtures.
    if (method === 'GET' && ['/api/v1/agents', '/api/v1/tasks', '/api/v1/workspaces', '/api/v1/skills'].includes(path)) {
      return jsonResponse([])
    }
    if (method === 'GET' && path === '/api/v1/sessions') return jsonResponse({ sessions: [] })
    if (method === 'GET' && path === '/api/v1/activity') return jsonResponse({ events: [] })
    if (method === 'GET' && path === '/api/v1/performance') return jsonResponse({})
    if (method === 'GET' && path === '/api/v1/notifications') {
      return jsonResponse({ notifications: [], unread_count: 0 } satisfies NotificationList)
    }
    if (method === 'GET' && path === '/api/v1/version') {
      return jsonResponse({ version: '0.1.1', build_sha: 'dev' } satisfies VersionResponse)
    }
    if (method === 'GET' && path === '/api/v1/gateway/god-mode') {
      return jsonResponse({ enabled: false, persisted: false, supported: true, available: true } satisfies GodModeStatus)
    }
    if (method === 'GET' && path === '/api/v1/providers') return jsonResponse(providers())
    if (method === 'GET' && path === '/api/v1/providers/catalog') return jsonResponse(PROVIDERS_CATALOG)
    if (method === 'GET' && path === '/api/v1/providers/default-model') {
      return jsonResponse({ error: 'default model not set' }, 404)
    }
    if (method === 'PUT' && path === `/api/v1/providers/${CUSTOM_ID}`) {
      putBodies.push(JSON.parse(String(init?.body)))
      return onPut()
    }
    unexpectedEdges.push(`${method} ${path}`)
    throw new Error(`Unexpected network request: ${method} ${path}`)
  }))
  return putBodies
}

function renderProviders() {
  // The app's existing host owns ToastContainer; this test imports no private
  // design-system export and mounts the editor beneath the real routed shell.
  const root = createRootRoute({ component: AppShell })
  const page = createRoute({ getParentRoute: () => root, path: '/', component: ProvidersSection })
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider>)
}

async function addModelAndSave() {
  fireEvent.click(await screen.findByTestId(`configure-btn-${CUSTOM_ID}`))
  expect(await screen.findByTestId('provider-config-sheet'), 'the real custom editor must open').toBeVisible()
  fireEvent.change(screen.getByTestId(`add-model-input-${CUSTOM_ID}`), { target: { value: MODEL_SLUG } })
  fireEvent.click(screen.getByTestId(`add-model-${CUSTOM_ID}`))
  expect(screen.getByTestId(`model-list-${CUSTOM_ID}`), 'the draft must contain the entered slug').toHaveTextContent(MODEL_SLUG)
  fireEvent.click(screen.getByTestId(`save-provider-${CUSTOM_ID}`))
  fireEvent.click(await screen.findByRole('button', { name: /^Save API key$/ }))
}

it('sends only models, not custom identity or a key, when saving models after normal creation', async () => {
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
  fireEvent.click(await screen.findByRole('button', { name: /^Save API key$/ }))

  await waitFor(() => expect(client.isMutating()).toBe(0))
  expect(await screen.findByTestId(`provider-row-${CUSTOM_ID}`), 'normal creation must produce a configured row').toBeVisible()
  expect(sent, 'creation must have sent the real identity pair collected by the form').toEqual([
    { api_key: TEST_KEY, api_base: CUSTOM_BASE, protocol: 'openai-compatible' },
  ])
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

  await addModelAndSave()
  await waitFor(() => expect(sent, 'one create and one model-save PUT must reach the network boundary').toHaveLength(2))
  // Oracle: accepted architect ruling B, rule 1 and its frontend T1 ruling.
  // The server retains stored identity and key; none belongs in this model edit.
  expect(sent[1], 'ruling B: a model edit sends only models, never base, protocol or key').toEqual({
    models: [MODEL_SLUG],
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

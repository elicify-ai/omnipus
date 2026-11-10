import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type {
  AppState,
  CatalogProvider,
  Provider,
  ProvidersCatalog,
  SignInPollResponse,
  SignInStartResponse,
  SignInStatus,
} from '@/lib/api/generated/openapi-types'
import { ProvidersSection } from '@/components/settings/ProvidersSection'
import { SignInDialog } from './SignInDialog'

// Founder WU-24 follow-up, 2026-10-14: device code is ChatGPT's ONLY
// sign-in method; neither importing a local login nor terminal recovery is
// offered. All components, API wrappers and generated response parsers are
// real. Only fetch (the network boundary) is replaced.
const CHATGPT: CatalogProvider = {
  id: 'openai-chatgpt', name: 'ChatGPT', company: 'OpenAI',
  api: 'https://chatgpt.com/backend-api/codex', protocol: 'openai-compatible',
  tier: 'standard', auth_methods: ['sign_in'], aliases: [], locality: 'cloud', models: [],
}
const CATALOG: ProvidersCatalog = {
  schema_version: '2.0.0', version: 'v2026.10.14', updated_at: '2026-10-14T00:00:00Z',
  source: 'WU-24 independent UI fixture', served_from: 'embedded', stale: false,
  default_resize_limits: { long_edge_px: 7680, max_bytes: 10485760 }, providers: [CHATGPT],
}
const APP_STATE: AppState = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
}
const DEVICE_CODE: SignInStartResponse = {
  method: 'device_code', verification_url: 'https://auth.openai.com/codex/device',
  user_code: 'WU24-HTTP', device_auth_id: 'das_wu24_http',
  // A protocol-valid example: ten minutes is below the fifteen-minute ceiling.
  expires_at: new Date(Date.now() + 10 * 60_000).toISOString(), interval_seconds: 1,
}
const removedIds = ['codex' + '-cli', 'github' + '-copilot']
const SIGNED_IN: SignInStatus = { state: 'signed_in', account_label: 'wu24-account' }

// not-wire-format: observations of the network calls, never sent over the wire.
let calls: Array<{ method: string; path: string; body: BodyInit | null | undefined }>
let localLoginState: SignInStatus['state']
let configured: Provider[]
let pollComplete: boolean
const clients: QueryClient[] = []

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

beforeEach(() => {
  calls = []
  configured = []
  localLoginState = 'signed_in'
  pollComplete = false
  document.cookie = 'csrf=wu24-fixture-csrf; path=/'
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.href : input.url
    const path = new URL(url, 'http://localhost').pathname
    const method = init?.method ?? 'GET'
    calls.push({ method, path, body: init?.body })
    if (path === '/api/v1/providers/catalog') return jsonResponse(CATALOG)
    if (path === '/api/v1/providers') return jsonResponse(configured)
    if (path === '/api/v1/providers/default-model') return jsonResponse({ error: 'not set' }, 404)
    if (path === '/api/v1/state') return jsonResponse(APP_STATE)
    if (path === '/api/v1/providers/openai-chatgpt/sign-in' && method === 'POST') return jsonResponse(DEVICE_CODE)
    if (path === '/api/v1/providers/openai-chatgpt/sign-in/poll') {
      const response: SignInPollResponse = { state: pollComplete ? 'signed_in' : 'pending' }
      return jsonResponse(response)
    }
    if (path === '/api/v1/providers/openai-chatgpt/sign-in/status') return jsonResponse(SIGNED_IN)
    if (removedIds.some((id) => path === `/api/v1/providers/${id}/sign-in/status`)) {
      // If the old availability probe survives, a local login is available.
      // Hiding the link only because today's server refuses that probe is NOT
      // sufficient: the removed UI path itself must be gone.
      const status: SignInStatus = { state: localLoginState }
      return jsonResponse(status)
    }
    return jsonResponse({ error: `unexpected test request: ${method} ${path}` }, 404)
  }))
})

afterEach(() => {
  for (const client of clients.splice(0)) client.clear()
  vi.useRealTimers()
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; Max-Age=0; path=/'
})

function renderSignIn(onSignedIn = vi.fn()) {
  return render(
    <SignInDialog open providerId="openai-chatgpt" providerLabel="ChatGPT" onOpenChange={vi.fn()} onSignedIn={onSignedIn} />,
  )
}

async function settledDeviceCode() {
  const code = await screen.findByTestId('user-code')
  // Flush the dialog's concurrent, immediately fulfilled network effects;
  // no sleep or elapsed-time assertion decides whether the path exists.
  await act(async () => { await Promise.resolve() })
  expect(code).toHaveTextContent(DEVICE_CODE.user_code)
  const link = screen.getByTestId('verification-link')
  expect(link).toHaveAttribute('href', DEVICE_CODE.verification_url)
  expect(link).toHaveAttribute('target', '_blank')
  expect(link).toHaveAttribute('rel', 'noopener')
  return screen.getByTestId('sign-in-dialog')
}

function expectNoImportOrTerminalPath(dialog: HTMLElement) {
  expect(within(dialog).queryByRole('button', { name: /existing Codex login|import.*login|check sign-in/i })).not.toBeInTheDocument()
  expect(within(dialog).queryByText(/run in a terminal|codex login|copilot login/i)).not.toBeInTheDocument()
  expect(screen.queryByTestId('re-sign-in-dialog')).not.toBeInTheDocument()
}

describe('WU-24 — real sign-in dialog, never catalog-file-derived', () => {
  it.each(['signed_in', 'expired'] as const)('never offers Codex-login import even when an old status probe would report %s', async (state) => {
    localLoginState = state
    renderSignIn()
    const dialog = await settledDeviceCode()
    expect(calls.filter((call) => call.method === 'POST' && call.path === '/api/v1/providers/openai-chatgpt/sign-in')).toHaveLength(1)
    expectNoImportOrTerminalPath(dialog)
  })

  it('does not query removed-provider status or the deleted import route when opening HTTP sign-in', async () => {
    // Isolate opening from the later, legitimate polling timer. Slow machine
    // scheduling cannot add a valid poll to this exact opening-call oracle.
    vi.useFakeTimers()
    renderSignIn()
    await act(async () => { await vi.advanceTimersByTimeAsync(0) })
    expect(screen.getByTestId('user-code')).toHaveTextContent(DEVICE_CODE.user_code)
    expect(calls.map(({ method, path }) => ({ method, path }))).toEqual([
      { method: 'POST', path: '/api/v1/providers/openai-chatgpt/sign-in' },
    ])
  })

  it('keeps device-code copy, polling and successful HTTP sign-in functional', async () => {
    pollComplete = true
    const onSignedIn = vi.fn()
    renderSignIn(onSignedIn)
    await settledDeviceCode()
    fireEvent.click(screen.getByRole('button', { name: /^Copy code$/ }))
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledExactlyOnceWith(DEVICE_CODE.user_code))
    expect(await screen.findByTestId('sign-in-success')).toHaveTextContent('Signed in as wu24-account')
    expect(onSignedIn).toHaveBeenCalledExactlyOnceWith(SIGNED_IN)
    const polls = calls.filter((call) => call.path === '/api/v1/providers/openai-chatgpt/sign-in/poll')
    expect(polls).toHaveLength(1)
    expect(polls[0]?.method).toBe('POST')
    expect(JSON.parse(String(polls[0]?.body))).toEqual({ device_auth_id: DEVICE_CODE.device_auth_id })
  })
})

describe('WU-24 — actual Settings recovery flow, with real child dialogs', () => {
  it('an expired HTTP provider opens device code, never CLI re-sign-in or Codex import', async () => {
    configured = [{
      id: 'openai-chatgpt', name: 'ChatGPT', status: 'expired', auth_method: 'sign_in',
      models: [], dependents: [], backs_default: false,
    }]
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
    clients.push(client)
    render(<QueryClientProvider client={client}><ProvidersSection /></QueryClientProvider>)
    const manage = await screen.findByTestId('manage-btn-openai-chatgpt')
    expect(manage).toHaveTextContent('Manage')
    fireEvent.click(manage)
    const dialog = await settledDeviceCode()
    expectNoImportOrTerminalPath(dialog)
    expect(calls.filter((call) => removedIds.some((id) => call.path.includes(`/providers/${id}/`)))).toEqual([])
  })
})

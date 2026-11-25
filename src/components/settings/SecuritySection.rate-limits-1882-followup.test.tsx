/**
 * SecuritySection.rate-limits-1882-followup.test.tsx — #1882 follow-up, gate
 * findings F-PTA-1 and F-PTA-2 (test-author half; RED/GREEN/CHECK split).
 *
 * Oracles — the specification and the contracts, never the component under
 * test:
 *   - F-PTA-1: docs/settings.md (Security rate limits) promises "Leave either
 *     blank, or enter 0, for no limit", and the wire contracts spell that as
 *     0 (contracts/components/schemas/RateLimitsUpdateRequest.yaml and
 *     RateLimitConfig.yaml: "0 = unlimited"). So clearing a field must save
 *     0 for that field while the untouched field keeps the value the
 *     GET /api/v1/security/rate-limits route returned.
 *   - F-PTA-2: on a failing rate-limits GET the section must show its
 *     load-failure notice and must send no rate-limit save: the limits never
 *     hydrate, and under FR-010 there is no autosave to leak one, so the
 *     group is blocked from saving (FR-066: only the group needing the
 *     failed read is blocked) — the exact silent-failure class #1882 was
 *     filed to fix. The notice's exact wording is product copy, so the pin
 *     is by parts (names the failure, gives one thing to do); the
 *     behavioural assertions (notice shown on GET failure, nothing saved)
 *     are the verification.
 *
 * Harness: the sibling SecuritySection.rate-limits-1882.test.tsx mock set
 * (the two rate-limit client functions are not mocked — they go through the
 * generated client's request() transport and are stubbed at the global fetch
 * boundary, where every request is recorded).
 *
 * Every failure message names #1882 and states expected and seen.
 */

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchConfig: vi.fn(),
    updateConfig: vi.fn(),
    fetchGatewayStatus: vi.fn(),
    fetchCredentials: vi.fn(),
    fetchBuiltinTools: vi.fn(),
    fetchRegistryTools: vi.fn(),
    fetchDoctorResults: vi.fn(),
    fetchAppState: vi.fn(),
    fetchSandboxConfig: vi.fn(),
    updateSandboxConfig: vi.fn(),
    fetchSandboxStatus: vi.fn(),
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast: vi.fn() })),
}))

vi.mock('@/store/auth', () => ({
  useAuthStore: vi.fn((selector: (s: { role: string }) => unknown) => selector({ role: 'admin' })),
}))

import {
  fetchConfig,
  updateConfig,
  fetchGatewayStatus,
  fetchCredentials,
  fetchBuiltinTools,
  fetchDoctorResults,
  fetchAppState,
  fetchSandboxConfig,
  fetchSandboxStatus,
} from '@/lib/api'
import type { AppState } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SecuritySection } from './SecuritySection'

const RATE_LIMITS_PATH = '/api/v1/security/rate-limits'

// Distinct, recognisable values for what the route holds, so a body that
// keeps a field's value can only be explained by the GET hydration, and a
// 0 can only be explained by the cleared field (never a stray default).
const ROUTE_LLM = 7
const ROUTE_TOOL = 9

/**
 * characterization test (wording only): the load-failure banner's copy, read
 * from the product copy source because docs/settings.md gives no wording for
 * it. The behavioural oracle of F-PTA-2 — the banner shows on a failing GET
 * and no save is sent — is verified regardless of the wording.
 */
const CONFIG = {
  security: {
    exec_timeout_seconds: 0,
    max_background_seconds: 0,
    rate_limits: {
      max_agent_llm_calls_per_hour: 111,
      max_agent_tool_calls_per_minute: 222,
    },
  },
  gateway: { bind_address: '127.0.0.1', port: 5000, token: 'test-token', hot_reload: false, log_level: 'info' },
  agents: { defaults: { default_agent_id: '' } },
}

interface Recorded {
  method: string
  path: string
  body: string | null
  /** HTTP status the stub answered with. */
  status: number
}

let recorded: Recorded[] = []
let rateLimitsGetStatus = 200

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function stubFetch() {
  recorded = []
  rateLimitsGetStatus = 200
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      // Normalise every legal fetch call shape (T1-5 a): fetch(url, init),
      // fetch(URL, init) and fetch(new Request(...), init?), where init
      // overrides the Request. The generated client's fetch adapter sends a
      // Request object, so the method and body must be read from it too.
      const isRequest = typeof Request !== 'undefined' && input instanceof Request
      const url = typeof input === 'string' ? input : input instanceof URL ? input.pathname : input.url
      const path = url.replace(/^https?:\/\/[^/]+/, '').split('?')[0]
      const method = (init?.method ?? (isRequest ? input.method : 'GET')).toUpperCase()
      let body: string | null = null
      if (typeof init?.body === 'string') {
        body = init.body
      } else if (init?.body == null && isRequest) {
        const text = await input.clone().text()
        body = text === '' ? null : text
      }
      const entry: Recorded = { method, path, body, status: 404 }
      recorded.push(entry)
      const answer = (status: number, payload: unknown) => {
        entry.status = status
        return jsonResponse(status, payload)
      }
      if (path === RATE_LIMITS_PATH && method === 'GET') {
        if (rateLimitsGetStatus !== 200) return answer(rateLimitsGetStatus, { error: 'rate limits unavailable' })
        return answer(200, {
          enabled: true,
          max_agent_llm_calls_per_hour: ROUTE_LLM,
          max_agent_tool_calls_per_minute: ROUTE_TOOL,
        })
      }
      if (path === RATE_LIMITS_PATH && method === 'PUT') {
        return answer(200, { saved: true, requires_restart: false })
      }
      return answer(404, { error: 'not stubbed' })
    }),
  )
}

const rateLimitPuts = () => recorded.filter((r) => r.method === 'PUT' && r.path === RATE_LIMITS_PATH)
const rateLimitGets = () => recorded.filter((r) => r.method === 'GET' && r.path === RATE_LIMITS_PATH)

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SecuritySection view="protection" />
    </QueryClientProvider>,
  )
}

/**
 * Return the two rate-limit inputs once they exist. The Protection view shows
 * them in plain view; the legacy layout kept them behind a collapsed Advanced
 * disclosure, which is gone with that layout (skills-tools-redesign C-22 row
 * 6), so there is nothing to open.
 */
async function findRateLimitInputs() {
  await waitFor(() => expect(screen.getByTestId('security-health-header')).toBeInTheDocument())
  // Settings redesign #1519: the wireframe (the wording authority) renames
  // the rows — "Model calls per hour", "Tool calls per minute".
  const llm = (await screen.findByLabelText('Model calls per hour')) as HTMLInputElement
  const tool = (await screen.findByLabelText('Tool calls per minute')) as HTMLInputElement
  return { llm, tool }
}

/** Wait (bounded) for an input to show a value; a miss fails the test with a #1882 message (T1-5, F3). */
async function settleTo(input: HTMLInputElement, want: string) {
  await waitFor(
    () => expect(input.value, `#1882: the field must settle to the GET ${RATE_LIMITS_PATH} value "${want}", saw "${input.value}"`).toBe(want),
    { timeout: 1500 },
  )
}

const APP_STATE = { onboarding_complete: true, identity: { mode: 'platform', edition: 'hosted', signed_in: true } } as AppState

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(useUiStore).mockReturnValue({ addToast: vi.fn() } as never)
  vi.mocked(fetchConfig).mockResolvedValue(CONFIG as never)
  vi.mocked(updateConfig).mockResolvedValue({} as never)
  vi.mocked(fetchGatewayStatus).mockResolvedValue({ daily_cost: 0, uptime_seconds: 0 } as never)
  vi.mocked(fetchCredentials).mockResolvedValue([])
  vi.mocked(fetchBuiltinTools).mockResolvedValue([])
  vi.mocked(fetchDoctorResults).mockResolvedValue(null)
  vi.mocked(fetchAppState).mockResolvedValue(APP_STATE)
  vi.mocked(fetchSandboxConfig).mockResolvedValue({ auto_approve: false } as never)
  vi.mocked(fetchSandboxStatus).mockResolvedValue({ kernel_sandbox_active: true } as never)
  // The SPA refuses a state-changing call without the CSRF cookie.
  document.cookie = 'csrf=test-csrf-1882'
  stubFetch()
})

afterEach(() => {
  vi.unstubAllGlobals()
  document.cookie = 'csrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT'
})

describe('#1882 follow-up F-PTA-1 — a cleared field saves 0 (Unlimited) through the rate-limit route', () => {
  it('clearing "LLM calls / hour" PUTs max_agent_llm_calls_per_hour: 0 and keeps the tool value', async () => {
    renderSection()
    const { llm, tool } = await findRateLimitInputs()
    // Both fields show the route's stored values before the edit. F3 /
    // FR-015: keeping the tool limit means leaving it OUT of this partial
    // update, never echoing the tab's possibly stale cached 9.
    await settleTo(llm, String(ROUTE_LLM))
    await settleTo(tool, String(ROUTE_TOOL))
    fireEvent.change(llm, { target: { value: '' } })
    // FR-010 replaced the debounce autosave: clearing stores nothing — the
    // draft waits for Save changes.
    expect(rateLimitPuts().length, '#1882 F-PTA-1/FR-010: clearing must not save; it waits for Save changes').toBe(0)
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(rateLimitPuts().length).toBeGreaterThanOrEqual(1))

    const puts = rateLimitPuts()
    expect.soft(
      puts.length,
      `#1882 F-PTA-1: expected exactly 1 PUT ${RATE_LIMITS_PATH} after clearing the LLM field, saw ${puts.length}`,
    ).toBe(1)
    if (puts.length === 1) {
      expect(
        JSON.parse(puts[0].body ?? 'null'),
        `#1882 F-PTA-1 / F3 / FR-015: the cleared LLM field must save 0 (Unlimited); the untouched tool limit must be absent; saw body ${puts[0].body}`,
      ).toEqual({ max_agent_llm_calls_per_hour: 0 })
      expect(tool).toHaveValue(ROUTE_TOOL)
    }
  })

  it('clearing "Tool calls / minute" PUTs max_agent_tool_calls_per_minute: 0 and keeps the LLM value', async () => {
    renderSection()
    const { llm, tool } = await findRateLimitInputs()
    await settleTo(llm, String(ROUTE_LLM))
    await settleTo(tool, String(ROUTE_TOOL))
    fireEvent.change(tool, { target: { value: '' } })
    // FR-010: clearing stores nothing until Save changes.
    expect(rateLimitPuts().length, '#1882 F-PTA-1/FR-010: clearing must not save; it waits for Save changes').toBe(0)
    await screen.findByText(/1 unsaved change/)
    fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    await waitFor(() => expect(rateLimitPuts().length).toBeGreaterThanOrEqual(1))

    const puts = rateLimitPuts()
    expect.soft(
      puts.length,
      `#1882 F-PTA-1: expected exactly 1 PUT ${RATE_LIMITS_PATH} after clearing the tool field, saw ${puts.length}`,
    ).toBe(1)
    if (puts.length === 1) {
      expect(
        JSON.parse(puts[0].body ?? 'null'),
        `#1882 F-PTA-1 / F3 / FR-015: the cleared tool field must save 0 (Unlimited); the untouched model limit must be absent; saw body ${puts[0].body}`,
      ).toEqual({ max_agent_tool_calls_per_minute: 0 })
      expect(llm).toHaveValue(ROUTE_LLM)
    }
  })
})

describe('#1882 follow-up F-PTA-2 — a failing rate-limits GET shows the load-failure banner and saves nothing', () => {
  it('shows the load-failure banner and sends no rate-limit save after edits when the GET fails', async () => {
    rateLimitsGetStatus = 500
    renderSection()
    await findRateLimitInputs()

    // Instrument check: the failure must come from the real GET having been
    // made and answered 500 — not from a never-fired request.
    const gets = rateLimitGets()
    expect(
      gets.map((r) => r.status),
      `#1882 F-PTA-2: expected the screen to GET ${RATE_LIMITS_PATH} and be answered 500; requests seen: ${JSON.stringify(recorded.map((r) => `${r.method} ${r.path} -> ${r.status}`))}`,
    ).toEqual([500])

    // The notice must be up (the GET is stubbed to fail from the start).
    // FR-066: the notice names what failed and gives one thing to do; its
    // exact wording is product copy, so the pin is by parts.
    const notice = await screen.findByText(/could not be loaded/)
    expect(notice, '#1882 F-PTA-2/FR-066: the load-failure notice must be shown when the rate-limits GET fails').toBeInTheDocument()
    expect(
      screen.getByText(/reload the page/i),
      '#1882 F-PTA-2/FR-066: the notice must give one thing to do',
    ).toBeInTheDocument()

    // The person tries to edit both fields while the limits are unloaded.
    // The limits never hydrated, so the group is blocked from saving
    // (FR-066): no draft can be counted and no save may be sent — for the
    // edited fields and the untouched ones alike (a save would write 0 over
    // caps the screen never read).
    fireEvent.change(screen.getByLabelText('Model calls per hour'), { target: { value: '25' } })
    fireEvent.change(screen.getByLabelText('Tool calls per minute'), { target: { value: '60' } })

    expect(
      screen.queryByText(/unsaved change/),
      '#1882 F-PTA-2/FR-066: the unloaded group must be blocked from saving — no draft may be counted',
    ).not.toBeInTheDocument()
    expect(
      rateLimitPuts(),
      `#1882 F-PTA-2: with the limits unloaded, no save may be sent after edits; saw ${JSON.stringify(rateLimitPuts().map((r) => r.body))}`,
    ).toEqual([])
  })
})

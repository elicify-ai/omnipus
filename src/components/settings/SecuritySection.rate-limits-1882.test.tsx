/**
 * SecuritySection.rate-limits-1882.test.tsx — #1882 rider, RED tests.
 *
 * Oracles: the issue and the chief's conditions of 2026-11-18, not the
 * implementation:
 *   - the two per-agent rate limits are read from and saved through
 *     GET / PUT /api/v1/security/rate-limits (fields
 *     max_agent_llm_calls_per_hour and max_agent_tool_calls_per_minute;
 *     contracts/openapi.yaml `/security/rate-limits`);
 *   - PUT /config keeps `security` blocked by design, so the screen must send
 *     no `security` key to it (R2 in pkg/gateway guards the server side);
 *   - chief ruling Q1 A: the two fields with no server behind them, "Exec
 *     timeout (seconds)" (SecuritySection.tsx, Label htmlFor
 *     "exec-timeout-seconds", ~line 561) and "Background timeout (seconds)"
 *     (the max_background_seconds field; Label htmlFor "max-background-seconds",
 *     ~line 579), are not shown, and a save never sends them anywhere.
 *
 * Harness: the sibling SecuritySection.test.tsx mock set. There is no API
 * client function for the rate-limit route yet, so the route is stubbed at the
 * fetch boundary (whatever client function GREEN adds goes through
 * `request()` and `fetch`), and every request is recorded.
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
    fetchGlobalToolPolicies: vi.fn(),
    updateGlobalToolPolicies: vi.fn(),
    fetchDoctorResults: vi.fn(),
    fetchSkillTrust: vi.fn(),
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
  fetchGlobalToolPolicies,
  fetchDoctorResults,
  fetchSkillTrust,
  fetchAppState,
  fetchSandboxConfig,
  fetchSandboxStatus,
} from '@/lib/api'
import type { AppState } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import { SecuritySection } from './SecuritySection'

const RATE_LIMITS_PATH = '/api/v1/security/rate-limits'
const PUT_ERROR_TEXT = 'rate limits could not be saved'

// Distinct, recognisable values: what config.security.rate_limits holds, what
// the dedicated route holds, and what the person types.
const CONFIG_LLM = 111
const CONFIG_TOOL = 222
const ROUTE_LLM = 7
const ROUTE_TOOL = 9
const TYPED_LLM = 25
const TYPED_TOOL = 60

const CONFIG = {
  security: {
    exec_timeout_seconds: 0,
    max_background_seconds: 0,
    rate_limits: {
      max_agent_llm_calls_per_hour: CONFIG_LLM,
      max_agent_tool_calls_per_minute: CONFIG_TOOL,
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
let putStatus = 200

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function stubFetch() {
  recorded = []
  putStatus = 200
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
        return answer(200, {
          enabled: true,
          max_agent_llm_calls_per_hour: ROUTE_LLM,
          max_agent_tool_calls_per_minute: ROUTE_TOOL,
        })
      }
      if (path === RATE_LIMITS_PATH && method === 'PUT') {
        if (putStatus !== 200) return answer(putStatus, { error: PUT_ERROR_TEXT })
        return answer(200, { saved: true, requires_restart: false })
      }
      return answer(404, { error: 'not stubbed' })
    }),
  )
}

const rateLimitPuts = () => recorded.filter((r) => r.method === 'PUT' && r.path === RATE_LIMITS_PATH)

function renderSection() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <SecuritySection />
    </QueryClientProvider>,
  )
}

/** Open Advanced and return the two rate-limit inputs once they exist. */
async function openAdvanced() {
  await waitFor(() => expect(screen.getByTestId('security-health-header')).toBeInTheDocument())
  fireEvent.click(screen.getByTestId('advanced-disclosure-trigger'))
  const llm = (await screen.findByLabelText('LLM calls / hour')) as HTMLInputElement
  const tool = (await screen.findByLabelText('Tool calls / minute')) as HTMLInputElement
  return { llm, tool }
}

/** Wait (bounded) for an input to show a value; a miss fails the test with a #1882 message (T1-5, F3). */
async function settleTo(input: HTMLInputElement, want: string) {
  await waitFor(
    () => expect(input.value, `#1882: the field must settle to the GET ${RATE_LIMITS_PATH} value "${want}", saw "${input.value}"`).toBe(want),
    { timeout: 1500 },
  )
}

async function editAndSave(llm: HTMLInputElement, tool: HTMLInputElement) {
  fireEvent.change(llm, { target: { value: String(TYPED_LLM) } })
  fireEvent.change(tool, { target: { value: String(TYPED_TOOL) } })
  // The screen saves automatically after the debounce window (500 ms).
  await new Promise((resolve) => setTimeout(resolve, 1400))
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
  vi.mocked(fetchGlobalToolPolicies).mockResolvedValue({ policies: {} })
  vi.mocked(fetchDoctorResults).mockResolvedValue(null)
  vi.mocked(fetchSkillTrust).mockResolvedValue({ level: 'warn_unverified' as const })
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

describe('#1882 R1(a) — rate limits save through the rate-limit route', () => {
  it('sends ONE PUT to the rate-limit route with exactly the two values, and no security key in any PUT /config body', async () => {
    renderSection()
    const { llm, tool } = await openAdvanced()
    await editAndSave(llm, tool)

    const puts = rateLimitPuts()
    expect.soft(
      puts.length,
      `#1882: expected exactly 1 PUT ${RATE_LIMITS_PATH}, saw ${puts.length}`,
    ).toBe(1)
    if (puts.length === 1) {
      expect(
        JSON.parse(puts[0].body ?? 'null'),
        `#1882: the PUT body must carry exactly the two typed values`,
      ).toEqual({
        max_agent_llm_calls_per_hour: TYPED_LLM,
        max_agent_tool_calls_per_minute: TYPED_TOOL,
      })
    }

    const configBodies = [
      ...vi.mocked(updateConfig).mock.calls.map((args) => args[0] as Record<string, unknown>),
      ...recorded
        .filter((r) => r.method === 'PUT' && /\/api\/v1\/config$/.test(r.path))
        .map((r) => JSON.parse(r.body ?? '{}') as Record<string, unknown>),
    ]
    for (const body of configBodies) {
      expect(
        Object.keys(body),
        `#1882: PUT /config is blocked for security.*; expected no "security" key in any PUT /config body, saw keys ${JSON.stringify(Object.keys(body))}`,
      ).not.toContain('security')
    }
  })
})

describe('#1882 R1(b) — the screen shows the values the rate-limit route returns', () => {
  it('shows the GET /api/v1/security/rate-limits values, not config.security.rate_limits', async () => {
    renderSection()
    const { llm, tool } = await openAdvanced()
    await settleTo(llm, String(ROUTE_LLM))
    await settleTo(tool, String(ROUTE_TOOL))

    expect(
      recorded.some((r) => r.method === 'GET' && r.path === RATE_LIMITS_PATH),
      `#1882: expected the screen to GET ${RATE_LIMITS_PATH}; requests seen: ${JSON.stringify(recorded.map((r) => `${r.method} ${r.path}`))}`,
    ).toBe(true)
    expect(llm.value, `#1882: LLM calls / hour must show the route value ${ROUTE_LLM}, saw "${llm.value}" (config holds ${CONFIG_LLM})`).toBe(String(ROUTE_LLM))
    expect(tool.value, `#1882: Tool calls / minute must show the route value ${ROUTE_TOOL}, saw "${tool.value}" (config holds ${CONFIG_TOOL})`).toBe(String(ROUTE_TOOL))
  })
})

describe('#1882 R1(c) — a failed rate-limit save shows the existing error state', () => {
  it('shows the save-error alert and no saved state when the rate-limit PUT fails', async () => {
    renderSection()
    const { llm, tool } = await openAdvanced()
    await settleTo(llm, String(ROUTE_LLM))
    putStatus = 400
    // T2-1: before the save, no error alert is on the screen, so an alert
    // seen afterwards cannot be a leftover or an unrelated one.
    const alertsBefore = screen.queryAllByRole('alert').map((el) => (el.textContent ?? '').trim())
    expect(
      alertsBefore,
      '#1882: no error alert may be on the screen before the save is attempted',
    ).toEqual([])
    // T3: the rate-limit save status is the AutoSaveIndicator that the section
    // renders right after its title block. It is found by its place, not by
    // text, and kept as one DOM node so the same node is checked after the save.
    const titleBlock = screen.getByRole('heading', { name: 'Security & Policy' }).parentElement
    const indicatorBefore = titleBlock?.nextElementSibling ?? null
    expect(
      indicatorBefore,
      '#1882: the rate-limit save-status indicator (the region after the section title) must be mounted before the save',
    ).not.toBeNull()
    expect(
      indicatorBefore?.getAttribute('role'),
      '#1882: the save-status indicator must not already be an alert before the save',
    ).not.toBe('alert')
    await editAndSave(llm, tool)

    expect(
      rateLimitPuts().length,
      `#1882: the save must be attempted on ${RATE_LIMITS_PATH} (expected >= 1 PUT, saw ${rateLimitPuts().length})`,
    ).toBeGreaterThanOrEqual(1)

    // The 400 was the answer to the recorded rate-limit PUT, so the failure
    // being shown belongs to THIS save.
    expect(
      rateLimitPuts().map((r) => r.status),
      `#1882: every recorded rate-limit PUT must have been answered 400 by the stub, saw ${JSON.stringify(rateLimitPuts().map((r) => r.status))}`,
    ).toEqual(rateLimitPuts().map(() => 400))

    // AutoSaveIndicator error branch is the existing error state
    // (role="alert"). The alert must be the rate-limit save own indicator:
    // the same node that was mounted before the save, now in its error state
    // with a message. Its wording is not pinned (the real client shows its
    // own sentence, not the stub one); an alert elsewhere on the screen does not count.
    const indicatorAfter = screen.getByRole('heading', { name: 'Security & Policy' }).parentElement?.nextElementSibling ?? null
    expect(indicatorAfter, '#1882: the rate-limit save-status indicator must still be mounted after the save').toBe(indicatorBefore)
    expect(
      indicatorAfter?.getAttribute('role'),
      `#1882: a failed rate-limit save must put the rate-limit save-status indicator in its error state (role="alert"); it showed "${indicatorAfter?.textContent ?? ''}", other alerts: ${JSON.stringify(screen.queryAllByRole('alert').map((el) => (el.textContent ?? '').trim()))}`,
    ).toBe('alert')
    expect(
      (indicatorAfter?.textContent ?? '').trim(),
      '#1882: the rate-limit error indicator must carry a message',
    ).not.toBe('')

    // T1-5 b: the success state must be absent after the failed save. The
    // AutoSaveIndicator's saved text starts with "Saved"; no element on the
    // whole screen may show it, and no live region may announce it.
    expect(
      screen.queryAllByText(/^Saved\b/).map((el) => el.textContent),
      '#1882: a failed rate-limit save must not report success; expected no "Saved" text on the screen',
    ).toEqual([])
  })
})

describe('#1882 R1(d) — fields with no server behind them are not shown or sent (chief ruling Q1 A)', () => {
  it('does not show "Exec timeout" or "Background timeout" on the Security section', async () => {
    renderSection()
    await openAdvanced()

    expect(
      screen.queryByLabelText(/exec timeout/i),
      '#1882 Q1 A: "Exec timeout (seconds)" (exec_timeout_seconds) must not be shown; it is still rendered',
    ).toBeNull()
    expect(
      screen.queryByLabelText(/background timeout/i),
      '#1882 Q1 A: "Background timeout (seconds)" (max_background_seconds) must not be shown; it is still rendered',
    ).toBeNull()
    expect(document.getElementById('exec-timeout-seconds'), '#1882 Q1 A: input #exec-timeout-seconds must be gone').toBeNull()
    expect(document.getElementById('max-background-seconds'), '#1882 Q1 A: input #max-background-seconds must be gone').toBeNull()
  })

  it('a save never sends exec_timeout_seconds or max_background_seconds anywhere', async () => {
    renderSection()
    const { llm, tool } = await openAdvanced()
    await editAndSave(llm, tool)

    const everyBody = [
      ...vi.mocked(updateConfig).mock.calls.map((args) => JSON.stringify(args[0])),
      ...recorded.map((r) => r.body ?? ''),
    ].join('\n')
    expect(
      everyBody,
      '#1882 Q1 A: expected no request to carry exec_timeout_seconds, saw it in a save body',
    ).not.toContain('exec_timeout_seconds')
    expect(
      everyBody,
      '#1882 Q1 A: expected no request to carry max_background_seconds, saw it in a save body',
    ).not.toContain('max_background_seconds')
  })
})

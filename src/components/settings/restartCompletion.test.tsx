/**
 * restartCompletion.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 25 (completion half of row 24: FR-063,
 * C-2 "Restart completion"; BDD "An old process still answering does not
 * count as restarted", "A changed port shows where the gateway will answer",
 * "Behind a proxy the window gives an instruction instead of a new address",
 * "The new address confirms the restart on its own origin", "The gateway
 * does not come back").
 *
 * Oracles (spec, never code):
 *   - FR-063: completion is proved ONLY by the page's own GET /about showing
 *     uptime_seconds smaller than the seconds since Restart now was pressed,
 *     then GET /config/pending-restart no longer listing the restarted keys.
 *     The first answer from the old process never counts.
 *   - FR-063: every restart check is a SAME-ORIGIN request; the page never
 *     requests the new port.
 *   - FR-063: with gateway.port pending, the window names the new address
 *     (page's own host, saved port) and the proxy instruction ("If you reach
 *     Omnipus through a proxy, point it at port N instead") BEFORE
 *     restarting; when the page's port differs from the running gateway port
 *     (a proxy), it offers NO address and says the proxy must point at the
 *     new port; Open the new address (a plain link to
 *     <new-address>/settings?tab=gateway&restarted=1) appears only once the
 *     page's own /health stops answering.
 *   - FR-063: with no change within 60 s the window says the gateway did not
 *     come back, to check that it is running and read its log, and keeps
 *     Restart now.
 *
 * Harness: the REAL GatewayRestartModal (it exists today) with the fetch
 * stub at the network edge and the existing '@/hooks/restart' mock extended
 * with pending-restart entries. RED today: the modal reports success on the
 * FIRST 2xx from /health, never reads /about, and shows none of the new
 * wording — every new-contract assertion fails for the spec's reason.
 * The browser-policy proof itself is E2E G-10 on a production-served build
 * (spec, row 24 description) — this file asserts the request pattern only.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { PendingRestartEntry } from '@/lib/api'

/**
 * The fetch-like reply stubResponse builds: the shape request()
 * (http.ts::performRequest) actually reads — ok, status, headers, json —
 * structurally Response-compatible where it is consumed.
 */
type FetchLikeReply = { ok: boolean; status: number; headers: Headers; json: () => Promise<unknown> }

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, gatewayRestart: vi.fn(async () => undefined) }
})

// usePendingRestart: entries are the gateway's GET /config/pending-restart
// answer (PendingRestartEntry shape). Mutable per test. The mock mirrors the
// real hook's return shape (`{ entries }`, src/hooks/restart.ts::usePendingRestart)
// — the earlier `{ data }` shape was never the hook's contract.
const restartState: { entries: PendingRestartEntry[] } = { entries: [] }
const mockRefetchPending = vi.fn().mockResolvedValue(undefined)
vi.mock('@/hooks/restart', () => ({
  usePendingRestart: () => ({ entries: restartState.entries, refetch: mockRefetchPending }),
}))

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

import { GatewayRestartModal } from './GatewayRestartModal'

// fetch stub serving by URL; calls are recorded for the same-origin oracle.
const calls: Array<{ url: string }> = []
// Minimal fetch-like Response: the real request() (http.ts::performRequest)
// reads res.headers.get('Content-Length') after res.ok/res.status and then
// res.json(), and Zod-validates the body against the endpoint's schema — a
// bare { ok, status, json } object throws at res.headers and the body never
// parses (the harness gap the freshness test died of). The /about body
// carries the fields AboutInfoSchema requires.
function stubResponse(json: unknown, status = 200) {
  return { ok: status >= 200 && status < 300, status, headers: new Headers(), json: async () => json }
}
const fetchBehavior: { health: () => Promise<FetchLikeReply>; about: () => Promise<FetchLikeReply>; pending: () => Promise<FetchLikeReply> } = {
  health: async () => stubResponse({ status: 'ok' }),
  about: async () => stubResponse({ version: '1.2.3', go_version: 'go1.24', os: 'linux', arch: 'amd64', uptime_seconds: 999_999 }),
  pending: async () => stubResponse(restartState.entries),
}
vi.stubGlobal(
  'fetch',
  vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input)
    calls.push({ url })
    if (url.includes('/health')) return fetchBehavior.health()
    if (url.includes('/about')) return fetchBehavior.about()
    if (url.includes('/config/pending-restart')) return fetchBehavior.pending()
    return stubResponse({})
  }),
)

const PORT_ENTRY: PendingRestartEntry = { key: 'gateway.port', applied_value: 8080, persisted_value: 8081, reason: 'restart_required' }
// The direct case needs the page port to EQUAL the running port (FR-063's
// precondition: "page port equals the running port"). jsdom serves this page
// on localhost:3000, so the fixture's applied value is 3000 — a harness
// accommodation to the test browser's own address, not a spec value; the
// saved port 8081 is the spec's example new address.
const DIRECT_PORT_ENTRY: PendingRestartEntry = { key: 'gateway.port', applied_value: 3000, persisted_value: 8081, reason: 'restart_required' }

function renderModal() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={qc}>
      <GatewayRestartModal open onClose={vi.fn()} />
    </QueryClientProvider>,
  )
}

async function clickRestartNow() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: /restart now/i }))
  })
}

// Drive the poll loop deterministically: fake Date/setTimeout, advancing one
// poll interval at a time (bounded — a fixed number of advances, never a
// wall-clock wait).
async function drivePolls(rounds: number) {
  for (let i = 0; i < rounds; i++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1_000)
    })
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  calls.length = 0
  restartState.entries = []
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })
  vi.setSystemTime(new Date('2026-11-20T12:00:00Z'))
  fetchBehavior.health = async () => stubResponse({ status: 'ok' })
  fetchBehavior.about = async () => stubResponse({ version: '1.2.3', go_version: 'go1.24', os: 'linux', arch: 'amd64', uptime_seconds: 999_999 })
  fetchBehavior.pending = async () => stubResponse(restartState.entries)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('FR-063 — restart completion is proved by a fresh /about uptime, not by any /health 2xx', () => {
  it('an old process still answering /health and /about with an old uptime does NOT count as restarted', async () => {
    restartState.entries = [PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    await drivePolls(5)
    // /about answered with uptime_seconds 999999 — far larger than the seconds
    // since Restart now — so the old process is still in place: no success
    // (no success phase in the dialog, no success toast).
    expect(screen.queryByText(/The gateway restarted/i)).not.toBeInTheDocument()
    expect(addToast).not.toHaveBeenCalled()
    // The restart is still being attempted: during the wait the control reads
    // "Restarting…" (disabled), not "Restart now" — the spec's "keeps Restart
    // now" wording is the no-come-back scenario's, asserted after its
    // deadline below.
    expect(screen.getByRole('button', { name: /restart/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /restart/i })).toBeDisabled()
  })

  it('success only when /about shows a fresh uptime AND pending-restart no longer lists the keys', async () => {
    restartState.entries = [PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    // The new process answers: fresh uptime (1s old) and the key cleared.
    fetchBehavior.about = async () => stubResponse({ version: '1.2.3', go_version: 'go1.24', os: 'linux', arch: 'amd64', uptime_seconds: 1 })
    restartState.entries = []
    await drivePolls(3)
    // The success signal: the success toast fired and the pending list was
    // refreshed (the dialog auto-closes after its brief success pause, so
    // the toast is the durable oracle).
    expect(addToast).toHaveBeenCalledWith(expect.objectContaining({ variant: 'success' }))
    expect(mockRefetchPending).toHaveBeenCalled()
  })

  it('every restart check is a same-origin request — the page never polls the new port', async () => {
    restartState.entries = [PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    await drivePolls(5)
    for (const c of calls) {
      expect(c.url).not.toMatch(/:8081/)
      expect(c.url).not.toMatch(/^https?:\/\/(?!localhost)/) // no cross-origin absolute URL
    }
  })
})

describe('FR-063 — a changed port names the new address and the proxy instruction', () => {
  it('the window says the gateway will answer on the saved port and carries the proxy sentence', async () => {
    restartState.entries = [DIRECT_PORT_ENTRY]
    renderModal()
    // The Dialog renders synchronously; sync queries (fake timers make
    // findBy* wait on faked intervals that never advance). FR-063: BEFORE
    // restarting, the window names the new address (page's own host, saved
    // port), says the current address will stop working, and adds the proxy
    // sentence — the "restart required" wording this test asserted before is
    // not in the spec and named no address, so FR-063's wording replaces it.
    expect(screen.getByText(/answers at http:\/\/localhost:8081/)).toBeInTheDocument()
    expect(screen.getByText(/this address stops working/i)).toBeInTheDocument()
    expect(screen.getByText(/If you reach Omnipus through a proxy, point it at port 8081 instead\./)).toBeInTheDocument()
    // The new address is offered only once the page's own /health stops
    // answering — not in the idle window.
    expect(screen.queryByRole('link', { name: /open the new address/i })).not.toBeInTheDocument()
  })

  it('Open the new address appears only after the page own /health stops answering, linking to the restarted page', async () => {
    restartState.entries = [DIRECT_PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    // The old address stops answering (the gateway is restarting on 8081).
    fetchBehavior.health = async () => {
      throw new TypeError('ECONNREFUSED')
    }
    fetchBehavior.about = async () => {
      throw new TypeError('ECONNREFUSED')
    }
    await drivePolls(3)
    // FR-063: the derived address is the page's own host with the SAVED port,
    // landing on the gateway tab with restarted=1.
    const link = screen.getByRole('link', { name: /open the new address/i })
    // F9 / FR-063: include the app's hash route so this reaches Settings.
    expect(link).toHaveAttribute('href', 'http://localhost:8081/#/settings?tab=gateway&restarted=1')
  })

  it('behind a proxy (page port differs from the running port) no address is offered, only the instruction', async () => {
    restartState.entries = [PORT_ENTRY]
    // jsdom's page is localhost:3000 while the gateway runs on 8080 — that is
    // the proxy case per FR-063; no Open-the-new-address link may ever show.
    renderModal()
    await clickRestartNow()
    fetchBehavior.health = async () => {
      throw new TypeError('ECONNREFUSED')
    }
    await drivePolls(5)
    // FR-063: a proxy gets the instruction ("says the proxy must point at the
    // new port") and no address — no derived address sentence, no link.
    expect(screen.getByText(/Point your proxy at port 8081/)).toBeInTheDocument()
    expect(screen.queryByText(/answers at/)).not.toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /open the new address/i })).not.toBeInTheDocument()
  })
})

describe('FR-063 — the gateway does not come back', () => {
  it('after the deadline the window says it did not come back, to check it is running and read its log, keeping Restart now', async () => {
    restartState.entries = [PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    fetchBehavior.health = async () => {
      throw new TypeError('ECONNREFUSED')
    }
    fetchBehavior.about = async () => {
      throw new TypeError('ECONNREFUSED')
    }
    await act(async () => {
      await vi.advanceTimersByTimeAsync(61_000)
    })
    expect(screen.getByText(/did not come back|did not restart/i)).toBeInTheDocument()
    expect(screen.getByText(/read its log/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /restart now/i })).toBeInTheDocument()
  })
})

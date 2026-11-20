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

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, gatewayRestart: vi.fn(async () => undefined) }
})

// usePendingRestart: entries are the gateway's GET /config/pending-restart
// answer (PendingRestartEntry shape). Mutable per test.
const restartState: { entries: any[] } = { entries: [] }
const mockRefetchPending = vi.fn().mockResolvedValue(undefined)
vi.mock('@/hooks/restart', () => ({
  usePendingRestart: () => ({ refetch: mockRefetchPending, data: restartState.entries }),
}))

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast }),
}))

import { GatewayRestartModal } from './GatewayRestartModal'

// fetch stub serving by URL; calls are recorded for the same-origin oracle.
const calls: Array<{ url: string }> = []
const fetchBehavior: { health: () => Promise<any>; about: () => Promise<any>; pending: () => Promise<any> } = {
  health: async () => ({ ok: true, status: 200, json: async () => ({ status: 'ok' }) }),
  about: async () => ({ ok: true, status: 200, json: async () => ({ uptime_seconds: 999_999, pid: 1 }) }),
  pending: async () => ({ ok: true, status: 200, json: async () => restartState.entries }),
}
vi.stubGlobal(
  'fetch',
  vi.fn(async (input: any) => {
    const url = String(input)
    calls.push({ url })
    if (url.includes('/health')) return fetchBehavior.health()
    if (url.includes('/about')) return fetchBehavior.about()
    if (url.includes('/config/pending-restart')) return fetchBehavior.pending()
    return { ok: true, status: 200, json: async () => ({}) }
  }),
)

const PORT_ENTRY = { key: 'gateway.port', applied_value: 8080, persisted_value: 8081, reason: 'restart_required' }

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
  fetchBehavior.health = async () => ({ ok: true, status: 200, json: async () => ({ status: 'ok' }) })
  fetchBehavior.about = async () => ({ ok: true, status: 200, json: async () => ({ uptime_seconds: 999_999, pid: 1 }) })
  fetchBehavior.pending = async () => ({ ok: true, status: 200, json: async () => restartState.entries })
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
    // since Restart now — so the old process is still in place: no success.
    expect(screen.queryByText(/restarted successfully/i)).not.toBeInTheDocument()
    expect(addToast).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: /restart now/i })).toBeInTheDocument()
  })

  it('success only when /about shows a fresh uptime AND pending-restart no longer lists the keys', async () => {
    restartState.entries = [PORT_ENTRY]
    renderModal()
    await clickRestartNow()
    // The new process answers: fresh uptime (1s old) and the key cleared.
    fetchBehavior.about = async () => ({
      ok: true,
      status: 200,
      json: async () => ({ uptime_seconds: 1, pid: 2 }),
    })
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
    restartState.entries = [PORT_ENTRY]
    renderModal()
    // The Dialog renders synchronously; sync queries (fake timers make
    // findBy* wait on faked intervals that never advance).
    expect(screen.getByText(/restart required/i)).toBeInTheDocument()
    expect(screen.getByText(/8081/)).toBeInTheDocument()
    expect(screen.getByText(/If you reach Omnipus through a proxy, point it at port 8081/i)).toBeInTheDocument()
  })

  it('Open the new address appears only after the page own /health stops answering, linking to the restarted page', async () => {
    restartState.entries = [PORT_ENTRY]
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
    expect(link).toHaveAttribute('href', expect.stringContaining('localhost:8081/settings?tab=gateway&restarted=1'))
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
    expect(screen.getByText(/point it at port 8081|the proxy must point/i)).toBeInTheDocument()
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

/**
 * useStepUp.test.tsx — ADR-0010 WP3.
 *
 * useStepUp() is the one hook every settings screen goes through to ask the
 * operator to stand behind a sensitive change: `local` mode (core edition)
 * opens ReAuthDialog FIRST and calls `run` once with the minted consent token
 * (useReAuthGate's dialog, without its optimistic no-token probe); `platform`
 * mode (desktop, hosted) opens ConfirmDialog with no token. The mode is read
 * from AppState.identity.mode.
 *
 * One test per mode, driving the hook through a minimal harness component
 * rather than a real settings screen — the screens' own tests already cover
 * gate() wired into a real mutation; this file pins the hook's own contract.
 */

import { useState } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(),
    reAuth: vi.fn(),
  }
})

import { fetchAppState, reAuth } from '@/lib/api'
import type { AppState } from '@/lib/api'
import { useStepUp } from './useStepUp'

const LOCAL_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'local', edition: 'core', signed_in: true },
} as AppState

const PLATFORM_APP_STATE = {
  onboarding_complete: true,
  identity: { mode: 'platform', edition: 'hosted', signed_in: true },
} as AppState

// Harness renders the hook's mode and dialogs, and exposes a button that
// calls gate() with a spy `run` — the test controls `run`'s outcome via the
// mock passed in as a prop.
function Harness({ run }: { run: (token?: string) => Promise<string> }) {
  const stepUp = useStepUp()
  const [result, setResult] = useState<string | null>(null)
  const [errored, setErrored] = useState(false)

  return (
    <div>
      <div data-testid="mode">{stepUp.mode}</div>
      {result && <div data-testid="result">{result}</div>}
      {errored && <div data-testid="errored">errored</div>}
      <button
        type="button"
        data-testid="trigger"
        onClick={() => {
          stepUp
            .gate(run, { title: 'Change the thing?', body: 'This changes the thing.', confirmLabel: 'Change it' })
            .then((r) => setResult(r))
            .catch(() => setErrored(true))
        }}
      >
        Trigger
      </button>
      {stepUp.dialogs}
    </div>
  )
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderHarness(run: (token?: string) => Promise<string>) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <Harness run={run} />
    </QueryClientProvider>,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('useStepUp', () => {
  it("password mode (identity.mode: 'local'): gate() opens ReAuthDialog and replays the minted token", async () => {
    vi.mocked(fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
    vi.mocked(reAuth).mockResolvedValue({ verified: true, token: 'reauth_tok', expires_in: 300 } as never)
    const run = vi.fn(async (token?: string) => `ran-with-${String(token)}`)

    renderHarness(run)

    await waitFor(() => {
      expect(screen.getByTestId('mode')).toHaveTextContent('password')
    })

    fireEvent.click(screen.getByTestId('trigger'))

    // Dialog first: ReAuthDialog opens before `run` is ever called — the
    // screen's mutation must not fire a doomed no-token probe (whose onError
    // would toast a failure before the prompt is even visible).
    await waitFor(() => {
      expect(screen.getByTestId('reauth-password-input')).toBeInTheDocument()
    })
    expect(run).not.toHaveBeenCalled()

    fireEvent.change(screen.getByTestId('reauth-password-input'), { target: { value: 'mypassword' } })
    fireEvent.click(screen.getByTestId('reauth-confirm'))

    await waitFor(() => {
      expect(reAuth).toHaveBeenCalledWith('mypassword')
      expect(screen.getByTestId('result')).toHaveTextContent('ran-with-reauth_tok')
    })
    // Exactly one call, and it carried the minted token.
    expect(run).toHaveBeenCalledTimes(1)
    expect(run.mock.calls[0][0]).toBe('reauth_tok')
  })

  it("password mode: dismissing ReAuthDialog rejects with the cancellation sentinel and never calls run", async () => {
    vi.mocked(fetchAppState).mockResolvedValue(LOCAL_APP_STATE)
    const run = vi.fn(async (token?: string) => `ran-with-${String(token)}`)

    renderHarness(run)
    await waitFor(() => {
      expect(screen.getByTestId('mode')).toHaveTextContent('password')
    })
    fireEvent.click(screen.getByTestId('trigger'))

    await waitFor(() => screen.getByTestId('reauth-cancel'))
    fireEvent.click(screen.getByTestId('reauth-cancel'))

    await waitFor(() => {
      expect(screen.getByTestId('errored')).toBeInTheDocument()
    })
    expect(run).not.toHaveBeenCalled()
    expect(reAuth).not.toHaveBeenCalled()
  })

  it("confirm mode (identity.mode: 'platform'): gate() opens ConfirmDialog and calls run() with no token", async () => {
    vi.mocked(fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
    const run = vi.fn(async (token?: string) => `ran-with-${String(token)}`)

    renderHarness(run)

    await waitFor(() => {
      expect(screen.getByTestId('mode')).toHaveTextContent('confirm')
    })

    fireEvent.click(screen.getByTestId('trigger'))

    const dialog = await screen.findByTestId('confirm-dialog')
    expect(dialog).toHaveTextContent('Change the thing?')
    expect(run).not.toHaveBeenCalled()

    fireEvent.click(screen.getByTestId('confirm-accept'))

    await waitFor(() => {
      expect(run).toHaveBeenCalledTimes(1)
      expect(run.mock.calls[0][0]).toBeUndefined()
      expect(screen.getByTestId('result')).toHaveTextContent('ran-with-undefined')
    })
  })
})

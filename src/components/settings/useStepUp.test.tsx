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
import { render, screen, waitFor, fireEvent, within, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'

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
import { REAUTH_CANCELLED_MESSAGE } from './useReAuthGate'

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
      <Button
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
      </Button>
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

    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Change the thing?')
    expect(run).not.toHaveBeenCalled()

    fireEvent.click(within(dialog).getByRole('button', { name: 'Change it' }))

    await waitFor(() => {
      expect(run).toHaveBeenCalledTimes(1)
      expect(run.mock.calls[0][0]).toBeUndefined()
      expect(screen.getByTestId('result')).toHaveTextContent('ran-with-undefined')
    })
  })
})

// ── #1468 — Escape while a confirmed save is in flight ──────────────────────
//
// Contract §3 rider row: pressing Escape while a confirmed save is in flight
// must CLOSE the dialog WITHOUT cancelling. The gate promise settles with the
// run's real result (resolve or reject), so the page shows the true outcome —
// saved, or the real error — never "cancelled". Escape BEFORE Confirm still
// cancels with REAUTH_CANCELLED_MESSAGE. ConfirmDialog itself is unchanged
// (its tested "permits Escape while pending" contract is an existing kit
// decision); the fix lives in useStepUp.tsx alone.
//
// RED on 69aa07d9a: handleConfirmAccept keeps `pendingRef` set while `run()`
// is in flight, so Escape → handleConfirmOpenChange rejects the gate with the
// cancel sentinel; the run's later settle then lands on an already-rejected
// promise and the real outcome is lost. Cases 1 and 2 fail on today's code
// with the sentinel in place of the expected value; case 3 is the control
// (passes today and must keep passing).
describe('useStepUp — Escape while a confirmed save is in flight (#1468)', () => {
  // Harness that exposes the raw gate promise (the existing Harness above
  // only shows a boolean errored flag, which cannot tell the cancel sentinel
  // from a real error).
  function PromiseHarness({
    run,
    gate,
  }: {
    run: (token?: string) => Promise<string>
    gate: { promise?: Promise<string> }
  }) {
    const stepUp = useStepUp()
    return (
      <div>
        <div data-testid="mode">{stepUp.mode}</div>
        <Button
          type="button"
          data-testid="trigger"
          onClick={() => {
            gate.promise = stepUp.gate(run, {
              title: 'Change the thing?',
              body: 'This changes the thing.',
              confirmLabel: 'Change it',
            })
          }}
        >
          Trigger
        </Button>
        {stepUp.dialogs}
      </div>
    )
  }

  async function renderConfirmMode(run: (token?: string) => Promise<string>) {
    vi.mocked(fetchAppState).mockResolvedValue(PLATFORM_APP_STATE)
    const gate: { promise?: Promise<string> } = {}
    render(
      <QueryClientProvider client={makeClient()}>
        <PromiseHarness run={run} gate={gate} />
      </QueryClientProvider>,
    )
    await waitFor(() => {
      expect(screen.getByTestId('mode')).toHaveTextContent('confirm')
    })
    fireEvent.click(screen.getByTestId('trigger'))
    // Consume the rejection (if any) at the environment level so a today-red
    // run fails on the ASSERTION below — named expected-vs-actual — and not
    // on an unhandledRejection report. Later `.resolves`/`.rejects`
    // expectations still see the true outcome.
    gate.promise?.catch(() => {})
    const dialog = await screen.findByRole('alertdialog')
    return { gate, dialog }
  }

  it('Escape during an in-flight confirmed save closes the dialog without cancelling: the gate resolves with the run’s value', async () => {
    let resolveRun!: (value: string) => void
    const run = vi.fn(
      () =>
        new Promise<string>((resolve) => {
          resolveRun = resolve
        }),
    )
    const { gate, dialog } = await renderConfirmMode(run)

    // Accept: the run starts and stays in flight (pending).
    fireEvent.click(within(dialog).getByRole('button', { name: 'Change it' }))
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1))

    // Escape while the run is in flight — the dialog closes...
    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    // ...and the run still lands: the gate must resolve with its REAL value.
    await act(async () => {
      resolveRun('saved-value')
    })
    await expect(gate.promise).resolves.toBe('saved-value')
  })

  it('Escape during an in-flight confirmed save surfaces the run’s REAL error, not the cancel sentinel', async () => {
    let rejectRun!: (err: unknown) => void
    const run = vi.fn(
      () =>
        new Promise<string>((_resolve, reject) => {
          rejectRun = reject
        }),
    )
    const { gate, dialog } = await renderConfirmMode(run)

    fireEvent.click(within(dialog).getByRole('button', { name: 'Change it' }))
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1))

    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    await act(async () => {
      rejectRun(new Error('disk full'))
    })
    // The specific real error, not REAUTH_CANCELLED_MESSAGE.
    await expect(gate.promise).rejects.toThrow('disk full')
  })

  it('Escape BEFORE confirming still cancels with the sentinel and never calls run (control — green today)', async () => {
    const run = vi.fn(async (token?: string) => `ran-with-${String(token)}`)
    const { gate } = await renderConfirmMode(run)

    fireEvent.keyDown(screen.getByRole('alertdialog'), { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())

    await expect(gate.promise).rejects.toThrow(REAUTH_CANCELLED_MESSAGE)
    expect(run).not.toHaveBeenCalled()
  })
})

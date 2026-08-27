/**
 * SignInDialog.test.tsx — ADR-068 §8b sign-in dialog (T068-33).
 *
 * Covers (per adr-068-providers-ux-spec-tasks.md T068-33 "Tests first"):
 *  - cli_login: renders the command + instructions, "Check sign-in" round trip.
 *  - device_code: renders the verification link (new tab, rel="noopener") and
 *    a copyable user code.
 *  - Polls pollSignIn at interval_seconds via fake timers, never faster than
 *    the last-known interval, and backs off on a slow_down (raised
 *    interval_seconds in a poll response).
 *  - Stops polling once the dialog closes.
 *  - End states signed_in | expired | denied, each with "Try again" restarting
 *    the flow via a fresh startSignIn call.
 *  - aria-live="polite" status announcement.
 *  - FR-047 import link: offered ONLY when the server reports a Codex login
 *    file exists (GET /providers/codex-cli/sign-in/status), never otherwise.
 *  - Escape closes the dialog, and focus is trapped inside it.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    startSignIn: vi.fn(),
    pollSignIn: vi.fn(),
    fetchSignInStatus: vi.fn(),
    importCodexLogin: vi.fn(),
  }
})

import * as api from '@/lib/api'
import { SignInDialog, isTransientSignInPollError } from './SignInDialog'

const CLI_LOGIN_RESPONSE = {
  method: 'cli_login' as const,
  command: 'codex login',
  instructions: 'Run `codex login` in a terminal, then click Check sign-in.',
}

const DEVICE_CODE_RESPONSE = {
  method: 'device_code' as const,
  verification_url: 'https://auth.openai.com/codex/device',
  user_code: 'WDJB-MJHT',
  device_auth_id: 'das_9f3a2b1c',
  expires_at: '2026-08-23T12:15:00Z',
  interval_seconds: 5,
}

function renderDialog(props?: Partial<React.ComponentProps<typeof SignInDialog>>) {
  const onOpenChange = vi.fn()
  const onSignedIn = vi.fn()
  const utils = render(
    <SignInDialog
      open
      onOpenChange={onOpenChange}
      providerId="openai-chatgpt"
      providerLabel="ChatGPT"
      onSignedIn={onSignedIn}
      {...props}
    />,
  )
  return { ...utils, onOpenChange, onSignedIn }
}

beforeEach(() => {
  vi.clearAllMocks()
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } })
})

afterEach(() => {
  vi.useRealTimers()
})

describe('SignInDialog — cli_login', () => {
  it('renders the command, instructions, and a Check sign-in button', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(CLI_LOGIN_RESPONSE)
    renderDialog({ providerId: 'codex-cli', providerLabel: 'Codex CLI' })

    await waitFor(() => {
      expect(screen.getByTestId('cli-login-command')).toHaveTextContent('codex login')
    })
    expect(screen.getByText(/Run `codex login`/)).toBeInTheDocument()
    expect(screen.getByTestId('check-sign-in-btn')).toBeInTheDocument()
    expect(api.startSignIn).toHaveBeenCalledWith('codex-cli')
  })

  it('Check sign-in: not-yet-signed-in shows a retry hint, no terminal state', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(CLI_LOGIN_RESPONSE)
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'not_signed_in' })
    renderDialog({ providerId: 'codex-cli', providerLabel: 'Codex CLI' })

    await waitFor(() => screen.getByTestId('check-sign-in-btn'))
    fireEvent.click(screen.getByTestId('check-sign-in-btn'))

    await waitFor(() => {
      // The retry hint appears twice in the DOM (the visible warning line and
      // the sr-only aria-live announcement) — scope to the visible alert.
      expect(screen.getByRole('alert')).toHaveTextContent(/Not signed in yet/)
    })
    expect(screen.getByTestId('check-sign-in-btn')).toBeInTheDocument()
  })

  it('Check sign-in: signed_in reaches the success state and fires onSignedIn', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(CLI_LOGIN_RESPONSE)
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'octocat' })
    const { onSignedIn } = renderDialog({ providerId: 'codex-cli', providerLabel: 'Codex CLI' })

    await waitFor(() => screen.getByTestId('check-sign-in-btn'))
    fireEvent.click(screen.getByTestId('check-sign-in-btn'))

    await waitFor(() => {
      expect(screen.getByTestId('sign-in-success')).toHaveTextContent('Signed in as octocat')
    })
    expect(onSignedIn).toHaveBeenCalledWith({ state: 'signed_in', account_label: 'octocat' })
  })
})

describe('SignInDialog — device_code', () => {
  it('renders the verification link (new tab, rel=noopener) and the user code', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    renderDialog()

    await waitFor(() => screen.getByTestId('user-code'))
    expect(screen.getByTestId('user-code')).toHaveTextContent('WDJB-MJHT')

    const link = screen.getByTestId('verification-link')
    expect(link).toHaveAttribute('href', DEVICE_CODE_RESPONSE.verification_url)
    expect(link).toHaveAttribute('target', '_blank')
    expect(link).toHaveAttribute('rel', 'noopener')
  })

  it('copies the user code to the clipboard', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    renderDialog()

    await waitFor(() => screen.getByTestId('copy-code-btn'))
    await act(async () => {
      fireEvent.click(screen.getByTestId('copy-code-btn'))
    })
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('WDJB-MJHT')
    await waitFor(() => expect(screen.getByTestId('copy-code-btn')).toHaveTextContent('Copied'))
  })

  it('polls at interval_seconds, never faster', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(api.pollSignIn).not.toHaveBeenCalled()

    // Just under the 5s interval — must not have polled yet.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4999)
    })
    expect(api.pollSignIn).not.toHaveBeenCalled()

    // At the interval — first poll fires.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
    expect(api.pollSignIn).toHaveBeenCalledWith('openai-chatgpt', 'das_9f3a2b1c')

    // A second full interval — second poll.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(2)
  })

  it('backs off when a poll response raises interval_seconds (vendor slow_down)', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValueOnce({ state: 'pending', interval_seconds: 10 })
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    renderDialog()

    // Prime: let the startSignIn() promise resolve and the polling effect
    // schedule its first setTimeout before advancing the clock for real —
    // without this, a single big jump can race ahead of that scheduling.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)

    // The slow_down response raised the interval to 10s — 6s more must NOT
    // trigger a second poll (would have, at the original 5s cadence).
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)

    // The remaining 4s (10s total) does trigger it.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(4000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(2)
  })

  it('stops polling once the dialog closes', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    const onOpenChange = vi.fn()
    const { rerender } = render(
      <SignInDialog open onOpenChange={onOpenChange} providerId="openai-chatgpt" providerLabel="ChatGPT" />,
    )

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)

    rerender(
      <SignInDialog open={false} onOpenChange={onOpenChange} providerId="openai-chatgpt" providerLabel="ChatGPT" />,
    )

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    // No further polls after close, however long we wait.
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
  })

  it('reaches signed_in, fetches the account label, and fires onSignedIn — polling stops', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'signed_in' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'user@example.com' })
    const { onSignedIn } = renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    // Settle the trailing fetchSignInStatus() chain triggered by the poll's
    // signed_in result — RTL's waitFor also runs on the now-fake clock, so it
    // cannot be used to await this; advance-and-assert directly instead.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })

    expect(screen.getByTestId('sign-in-success')).toHaveTextContent('Signed in as user@example.com')
    expect(onSignedIn).toHaveBeenCalledWith({ state: 'signed_in', account_label: 'user@example.com' })

    // No more polling once terminal.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
  })

  it('expired end state offers Try again, which restarts the flow', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'expired' })
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })

    expect(screen.getByTestId('sign-in-failure')).toHaveTextContent(/expired/i)
    expect(screen.getByTestId('try-again-btn')).toBeInTheDocument()

    vi.mocked(api.startSignIn).mockClear()
    await act(async () => {
      fireEvent.click(screen.getByTestId('try-again-btn'))
    })
    expect(api.startSignIn).toHaveBeenCalledTimes(1)
  })

  it('denied end state is shown distinctly from expired', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'denied' })
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })

    expect(screen.getByTestId('sign-in-failure')).toHaveTextContent(/denied/i)
  })

  it('announces status changes via an aria-live="polite" region', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    renderDialog()

    await waitFor(() => screen.getByTestId('sign-in-status'))
    const live = screen.getByTestId('sign-in-status')
    expect(live).toHaveAttribute('aria-live', 'polite')
    expect(live).toHaveTextContent(/approve/i)
  })

  it('the openai-chatgpt "Use my existing Codex login" import path succeeds', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    // FR-047: the link is offered only because the server reports a Codex
    // login file (codex-cli status) exists.
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'codex-user' })
    vi.mocked(api.importCodexLogin).mockResolvedValue({ state: 'signed_in', account_label: 'codex-user' })
    const { onSignedIn } = renderDialog({ providerId: 'openai-chatgpt' })

    await waitFor(() => screen.getByTestId('import-codex-login-btn'))
    expect(api.fetchSignInStatus).toHaveBeenCalledWith('codex-cli')
    await act(async () => {
      fireEvent.click(screen.getByTestId('import-codex-login-btn'))
    })

    await waitFor(() => {
      expect(screen.getByTestId('sign-in-success')).toHaveTextContent('Signed in as codex-user')
    })
    expect(onSignedIn).toHaveBeenCalledWith({ state: 'signed_in', account_label: 'codex-user' })
  })

  it('the import link is not offered for a non-openai-chatgpt device_code provider', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in' })
    renderDialog({ providerId: 'xai', providerLabel: 'xAI' })

    await waitFor(() => screen.getByTestId('user-code'))
    expect(screen.queryByTestId('import-codex-login-btn')).not.toBeInTheDocument()
    // A provider that is not openai-chatgpt never even asks about the file.
    expect(api.fetchSignInStatus).not.toHaveBeenCalledWith('codex-cli')
  })

  it('FR-047: the import link stays hidden when the server reports no Codex login file', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'not_signed_in' })
    renderDialog({ providerId: 'openai-chatgpt' })

    await waitFor(() => screen.getByTestId('user-code'))
    await waitFor(() => expect(api.fetchSignInStatus).toHaveBeenCalledWith('codex-cli'))
    expect(screen.queryByTestId('import-codex-login-btn')).not.toBeInTheDocument()
  })
})

// ── O9: a single transient poll failure must not kill device-code sign-in
// and invalidate a code the operator already approved (code-review finding,
// major). Before the fix, `tick()`'s catch treated ANY thrown error as
// terminal — a dropped connection, a 502 from a proxy, or (the concrete,
// filed backend defect) a `SignInPollResponse` whose `interval_seconds`
// exceeds the contract's `maximum: 30` and fails the generated Zod schema —
// permanently stopped polling and dropped straight to the `error` phase,
// even though the operator's approval on the vendor's page was still good.
describe('isTransientSignInPollError — classification (O9)', () => {
  it('classifies network failures, 5xx, 429, and schema-validation failures as transient', () => {
    expect(isTransientSignInPollError(new api.ApiError(0, 'Network unavailable.'))).toBe(true)
    expect(
      isTransientSignInPollError(
        new api.ApiError(502, 'could not reach the provider to check sign-in status'),
      ),
    ).toBe(true)
    expect(isTransientSignInPollError(new api.ApiError(503, 'credential store unavailable'))).toBe(true)
    expect(isTransientSignInPollError(new api.ApiError(429, 'Too many requests.'))).toBe(true)
    expect(
      isTransientSignInPollError(
        new api.ApiSchemaError(
          'POST /api/v1/providers/openai-chatgpt/sign-in/poll',
          [{ path: ['interval_seconds'], message: 'Number must be less than or equal to 30' }],
          { state: 'pending', interval_seconds: 45 },
        ),
      ),
    ).toBe(true)
  })

  it('classifies a dead/unknown device-code session and unrecognised errors as terminal', () => {
    expect(isTransientSignInPollError(new api.ApiError(404, 'unknown or expired device_auth_id'))).toBe(
      false,
    )
    expect(isTransientSignInPollError(new api.ApiError(400, 'device_auth_id is required'))).toBe(false)
    expect(isTransientSignInPollError(new Error('boom'))).toBe(false)
  })
})

describe('SignInDialog — device_code polling resilience (O9)', () => {
  // The outer file's `beforeEach(() => vi.clearAllMocks())` clears call/result
  // history but — per Vitest's own contract — does NOT drain a mock's queued
  // `mockResolvedValueOnce`/`mockRejectedValueOnce` entries. These tests
  // deliberately queue MULTIPLE once-values per test (to script a failure
  // then a recovery), and several exist specifically to prove the loop stops
  // retrying at some point — so a queued value can legitimately go unconsumed
  // within a test. Left unreset, that leftover would leak into and pollute
  // the NEXT test's assertions. `mockReset()` (which does clear the once
  // queue) keeps every test in this block isolated regardless of how many of
  // a previous test's queued responses were actually consumed.
  beforeEach(() => {
    vi.mocked(api.pollSignIn).mockReset()
    vi.mocked(api.startSignIn).mockReset()
    vi.mocked(api.fetchSignInStatus).mockReset()
  })

  it('rides out a single transient poll failure (502) and still reaches signed_in — the approved code is not lost', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn)
      .mockRejectedValueOnce(
        new api.ApiError(502, 'could not reach the provider to check sign-in status'),
      )
      .mockResolvedValueOnce({ state: 'pending' })
      .mockResolvedValueOnce({ state: 'signed_in' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'user@example.com' })
    const { onSignedIn } = renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })

    // First poll: transient 502. Must NOT drop to the error state — the
    // dialog stays exactly where the operator left it, code still visible.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('user-code')).toHaveTextContent('WDJB-MJHT')
    expect(screen.queryByTestId('sign-in-failure')).not.toBeInTheDocument()

    // Second poll: pending — the retry loop kept going on its own.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(2)

    // Third poll: signed_in — the operator's earlier approval was never lost.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByTestId('sign-in-success')).toHaveTextContent('Signed in as user@example.com')
    expect(onSignedIn).toHaveBeenCalledWith({ state: 'signed_in', account_label: 'user@example.com' })
  })

  it('rides out an ApiSchemaError from a malformed poll response (interval_seconds over the contract max)', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn)
      .mockRejectedValueOnce(
        new api.ApiSchemaError(
          'POST /api/v1/providers/openai-chatgpt/sign-in/poll',
          [{ path: ['interval_seconds'], message: 'Number must be less than or equal to 30' }],
          { state: 'pending', interval_seconds: 45 },
        ),
      )
      .mockResolvedValueOnce({ state: 'signed_in' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'signed_in', account_label: 'user@example.com' })
    const { onSignedIn } = renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('sign-in-failure')).not.toBeInTheDocument()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(screen.getByTestId('sign-in-success')).toHaveTextContent('Signed in as user@example.com')
    expect(onSignedIn).toHaveBeenCalled()
  })

  it('gives up after enough CONSECUTIVE transient failures — bounded, not infinite retry', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockRejectedValue(
      new api.ApiError(0, 'Network unavailable. Check your connection.'),
    )
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    // 6 attempts: the 1st plus 5 tolerated consecutive-failure retries.
    for (let i = 0; i < 6; i++) {
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5000)
      })
    }
    expect(api.pollSignIn).toHaveBeenCalledTimes(6)
    expect(screen.getByTestId('sign-in-failure')).toBeInTheDocument()

    // Having given up, it must not schedule yet another attempt.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(6)
  })

  it('a terminal poll failure (404 unknown/expired device_auth_id) still stops immediately', async () => {
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockRejectedValue(
      new api.ApiError(404, 'unknown or expired device_auth_id'),
    )
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })

    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
    expect(screen.getByTestId('sign-in-failure')).toHaveTextContent('unknown or expired device_auth_id')

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)
  })

  it('a transient failure followed by a genuine denial still ends in denied, not error', async () => {
    // Confirms the fix does not blur "denied" (a real 200 response) with a
    // retried transient failure — the two must never be conflated.
    vi.useFakeTimers()
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn)
      .mockRejectedValueOnce(new api.ApiError(0, 'Network unavailable.'))
      .mockResolvedValueOnce({ state: 'denied' })
    renderDialog()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(api.pollSignIn).toHaveBeenCalledTimes(1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000)
    })
    expect(screen.getByTestId('sign-in-failure')).toHaveTextContent(/denied/i)
  })
})

describe('SignInDialog — accessibility (FR-045 WCAG constraints)', () => {
  it('Escape asks the caller to close the dialog', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'not_signed_in' })
    const { onOpenChange } = renderDialog()

    await waitFor(() => screen.getByTestId('user-code'))
    fireEvent.keyDown(document.activeElement ?? document.body, { key: 'Escape', code: 'Escape' })

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
  })

  it('traps focus: the dialog is modal and focus starts inside it', async () => {
    vi.mocked(api.startSignIn).mockResolvedValue(DEVICE_CODE_RESPONSE)
    vi.mocked(api.pollSignIn).mockResolvedValue({ state: 'pending' })
    vi.mocked(api.fetchSignInStatus).mockResolvedValue({ state: 'not_signed_in' })
    // An outside control that focus must NOT be able to reach while the
    // dialog is open — Radix marks everything outside `aria-hidden`.
    render(<button data-testid="outside-btn">outside</button>)
    renderDialog()

    await waitFor(() => screen.getByTestId('user-code'))
    const dialog = screen.getByTestId('sign-in-dialog')
    expect(dialog).toHaveAttribute('role', 'dialog')

    await waitFor(() => {
      expect(dialog.contains(document.activeElement)).toBe(true)
    })

    // Focus cannot escape to the outside control: it is inert to the
    // accessibility tree while the modal is up.
    const outside = screen.getByTestId('outside-btn')
    expect(outside.closest('[aria-hidden="true"]')).not.toBeNull()
  })
})

describe('SignInDialog — start failure', () => {
  it('surfaces a startSignIn error with Try again', async () => {
    vi.mocked(api.startSignIn).mockRejectedValue(new Error('provider does not support sign-in'))
    renderDialog()

    await waitFor(() => {
      expect(screen.getByTestId('sign-in-failure')).toHaveTextContent('provider does not support sign-in')
    })
    expect(screen.getByTestId('try-again-btn')).toBeInTheDocument()
  })
})

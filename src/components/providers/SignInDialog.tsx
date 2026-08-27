// SignInDialog — the shared vendor sign-in dialog for a `sign_in`-capable
// provider (ADR-068 §8b, FR-005/FR-044..FR-049). One component, two call
// sites: Settings → Providers (ProvidersSection.tsx) and onboarding step 3
// (routes/onboarding.tsx) — same dialog, same api.ts wrappers, so a future
// AuthMethodControl (T068-21) can lift it unchanged.
//
// Two vendor-determined shapes, discriminated by SignInStartResponse.method:
//   - `cli_login` (codex-cli, github-copilot): Omnipus never performs or
//     stores the vendor login itself — shows the exact command to run in a
//     terminal plus a manual "Check sign-in" button (fetchSignInStatus, no
//     polling — there is nothing to poll, the operator drives it).
//   - `device_code` (openai-chatgpt, and xai once its catalog row carries
//     `sign_in`): Omnipus requests a device code itself; the dialog shows
//     the verification link + user code and polls pollSignIn at
//     interval_seconds (never faster; backs off on vendor slow_down) until
//     signed_in | expired | denied (FR-044/FR-045).
//
// Closing the dialog (Escape, overlay click, Cancel, or the Done button)
// stops any in-flight polling — see the polling effect's cleanup below.
// Focus trap + Escape-to-close come from Radix Dialog (DialogContent) for
// free; the live status line is aria-live="polite" per FR-045.

import { useEffect, useRef, useState } from 'react'
import {
  ArrowSquareOut,
  Check,
  CheckCircle,
  Copy,
  SpinnerGap,
  Warning,
  XCircle,
} from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import {
  startSignIn,
  pollSignIn,
  fetchSignInStatus,
  importCodexLogin,
  getErrorMessage,
} from '@/lib/api'
import type { SignInStatus } from '@/lib/api/generated/openapi-types'

// ---------------------------------------------------------------------------
// Local phase state machine — not-wire-format: purely a client-side render
// state; nothing here is serialized or crosses the gateway/SPA boundary.
// ---------------------------------------------------------------------------

type Phase =
  | { kind: 'starting' }
  | { kind: 'cli_login'; command: string; instructions: string; checking: boolean; checkResult?: 'not_yet' | 'signed_in' | 'expired' }
  | { kind: 'device_code'; verificationUrl: string; userCode: string; deviceAuthId: string; intervalSeconds: number }
  | { kind: 'signed_in'; accountLabel?: string }
  | { kind: 'expired' }
  | { kind: 'denied' }
  | { kind: 'error'; message: string }

export function SignInDialog({
  open,
  onOpenChange,
  providerId,
  providerLabel,
  onSignedIn,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  providerId: string
  providerLabel: string
  /** Fires once the flow reaches signed_in — the caller invalidates/refetches its own provider data. Does not close the dialog; the operator closes it via Done. */
  onSignedIn?: (status: SignInStatus) => void
}) {
  const [phase, setPhase] = useState<Phase>({ kind: 'starting' })
  const [copied, setCopied] = useState(false)
  const [importing, setImporting] = useState(false)
  const [importError, setImportError] = useState('')
  // FR-047: the *Use my existing Codex login* link is offered "only when the
  // server reports the file exists". There is no dedicated wire field for
  // that, and inventing one would put a second source of truth beside the
  // status route — so we ASK the status route about the CLI provider that
  // owns the file: GET /providers/codex-cli/sign-in/status reads
  // ~/.codex/auth.json read-only and answers not_signed_in when it is absent
  // or unreadable. Anything else means the file is there and yielded a token.
  const [codexLoginPresent, setCodexLoginPresent] = useState(false)

  // Monotonic start id. Close-then-reopen, or *Try again* clicked twice, can
  // leave an older startSignIn() in flight; only the newest one may set state,
  // or a stale device_auth_id would be the one the dialog starts polling.
  const startSeq = useRef(0)

  // begin() starts (or restarts, for "Try again") the sign-in flow.
  const begin = async () => {
    const seq = ++startSeq.current
    setPhase({ kind: 'starting' })
    setImportError('')
    try {
      const resp = await startSignIn(providerId)
      if (seq !== startSeq.current) return
      if (resp.method === 'cli_login') {
        setPhase({ kind: 'cli_login', command: resp.command, instructions: resp.instructions, checking: false })
      } else {
        setPhase({
          kind: 'device_code',
          verificationUrl: resp.verification_url,
          userCode: resp.user_code,
          deviceAuthId: resp.device_auth_id,
          intervalSeconds: resp.interval_seconds,
        })
      }
    } catch (err) {
      if (seq !== startSeq.current) return
      setPhase({ kind: 'error', message: getErrorMessage(err, 'Could not start sign-in') })
    }
  }

  // Reset and (re-)start every time the dialog opens.
  useEffect(() => {
    if (open) {
      void begin()
    } else {
      // Invalidate any in-flight start so a late response cannot repopulate a
      // closed dialog (and, on reopen, cannot race the fresh begin()).
      startSeq.current += 1
      setPhase({ kind: 'starting' })
      setCopied(false)
      setImporting(false)
      setImportError('')
      setCodexLoginPresent(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, providerId])

  // FR-047 availability probe, openai-chatgpt only. Kept out of `begin()` so a
  // *Try again* does not re-ask, and deliberately fire-and-forget: a failure
  // here only means the secondary link stays hidden, never that sign-in is
  // blocked.
  useEffect(() => {
    if (!open || providerId !== 'openai-chatgpt') return
    let cancelled = false
    void (async () => {
      try {
        const status = await fetchSignInStatus('codex-cli')
        if (!cancelled) setCodexLoginPresent(status?.state === 'signed_in' || status?.state === 'expired')
      } catch {
        if (!cancelled) setCodexLoginPresent(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [open, providerId])

  // Device-code polling — a self-scheduling chain of setTimeout calls (not
  // setInterval) so a vendor `slow_down` response can raise the interval for
  // the NEXT wait without racing an already-scheduled shorter tick. Keyed
  // only on providerId/deviceAuthId/open so a poll's own state transition
  // doesn't restart the effect — the chain stops itself by simply not
  // scheduling another tick once a terminal state is reached, and the
  // cleanup below stops it early on close/unmount (FR-045: "closing the
  // dialog MUST stop polling").
  const deviceAuthId = phase.kind === 'device_code' ? phase.deviceAuthId : undefined
  const startIntervalSeconds = phase.kind === 'device_code' ? phase.intervalSeconds : undefined
  useEffect(() => {
    if (!open || deviceAuthId === undefined || startIntervalSeconds === undefined) return
    let cancelled = false
    let timeoutId: ReturnType<typeof setTimeout> | undefined
    let intervalMs = Math.max(1, startIntervalSeconds) * 1000

    const tick = async () => {
      if (cancelled) return
      try {
        const result = await pollSignIn(providerId, deviceAuthId)
        if (cancelled) return
        if (result.interval_seconds) {
          // Never speed up — a slow_down response only ever raises the floor.
          intervalMs = Math.max(intervalMs, result.interval_seconds * 1000)
        }
        if (result.state === 'signed_in') {
          const status = await fetchSignInStatus(providerId)
          if (cancelled) return
          setPhase({ kind: 'signed_in', accountLabel: status.account_label })
          onSignedIn?.(status)
          return
        }
        if (result.state === 'expired') {
          setPhase({ kind: 'expired' })
          return
        }
        if (result.state === 'denied') {
          setPhase({ kind: 'denied' })
          return
        }
        // pending — schedule the next poll, never faster than intervalMs.
        timeoutId = setTimeout(() => { void tick() }, intervalMs)
      } catch (err) {
        if (cancelled) return
        setPhase({ kind: 'error', message: getErrorMessage(err, 'Sign-in check failed') })
      }
    }

    timeoutId = setTimeout(() => { void tick() }, intervalMs)
    return () => {
      cancelled = true
      if (timeoutId) clearTimeout(timeoutId)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, providerId, deviceAuthId, startIntervalSeconds])

  const handleCheckCliSignIn = async () => {
    if (phase.kind !== 'cli_login') return
    setPhase({ ...phase, checking: true })
    try {
      const status = await fetchSignInStatus(providerId)
      if (status.state === 'signed_in') {
        setPhase({ kind: 'signed_in', accountLabel: status.account_label })
        onSignedIn?.(status)
      } else if (status.state === 'expired') {
        setPhase({ kind: 'expired' })
      } else {
        setPhase({ kind: 'cli_login', command: phase.command, instructions: phase.instructions, checking: false, checkResult: 'not_yet' })
      }
    } catch (err) {
      setPhase({ kind: 'error', message: getErrorMessage(err, 'Sign-in check failed') })
    }
  }

  const handleImportCodexLogin = async () => {
    setImporting(true)
    setImportError('')
    try {
      const status = await importCodexLogin()
      if (status.state === 'signed_in') {
        setPhase({ kind: 'signed_in', accountLabel: status.account_label })
        onSignedIn?.(status)
      } else {
        setImportError('No existing Codex login found.')
      }
    } catch (err) {
      setImportError(getErrorMessage(err, 'No existing Codex login found.'))
    } finally {
      setImporting(false)
    }
  }

  const handleCopyCode = async () => {
    if (phase.kind !== 'device_code') return
    try {
      await navigator.clipboard.writeText(phase.userCode)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard access denied/unavailable — the code is still visible and
      // selectable; no toast infrastructure is threaded into this dialog, so
      // this is a silent no-op degrade rather than a broken interaction.
    }
  }

  // The single aria-live="polite" status line (FR-045) — one source of truth
  // for what a screen reader announces on every phase transition.
  const statusText = (() => {
    switch (phase.kind) {
      case 'starting': return 'Starting sign-in…'
      case 'cli_login': return phase.checkResult === 'not_yet' ? 'Not signed in yet — run the command above, then check again.' : 'Waiting for you to sign in.'
      case 'device_code': return 'Waiting for you to approve this sign-in…'
      case 'signed_in': return phase.accountLabel ? `Signed in as ${phase.accountLabel}.` : 'Signed in.'
      case 'expired': return 'Sign-in expired before it was approved.'
      case 'denied': return 'Sign-in was denied.'
      case 'error': return phase.message
    }
  })()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" data-testid="sign-in-dialog">
        <DialogHeader>
          <DialogTitle>Sign in to {providerLabel}</DialogTitle>
          <DialogDescription>
            {phase.kind === 'device_code'
              ? 'Approve this sign-in on the vendor’s page, using any device.'
              : phase.kind === 'cli_login'
              ? 'Sign in using the vendor’s own command-line tool.'
              : ' '}
          </DialogDescription>
        </DialogHeader>

        <div
          role="status"
          aria-live="polite"
          data-testid="sign-in-status"
          className="sr-only"
        >
          {statusText}
        </div>

        <div className="space-y-4">
          {phase.kind === 'starting' && (
            <div className="flex items-center gap-2 text-sm text-[var(--color-muted)]" data-testid="sign-in-starting">
              <SpinnerGap size={14} className="animate-spin" />
              Starting sign-in…
            </div>
          )}

          {phase.kind === 'cli_login' && (
            <div className="space-y-3">
              <div>
                <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">Run in a terminal</p>
                <code
                  className="block rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm font-mono text-[var(--color-secondary)]"
                  data-testid="cli-login-command"
                >
                  {phase.command}
                </code>
                <p className="text-xs text-[var(--color-muted)] mt-1.5">{phase.instructions}</p>
              </div>
              {phase.checkResult === 'not_yet' && (
                <p className="text-sm text-[var(--color-warning)] flex items-center gap-1.5" role="alert" aria-live="assertive">
                  <Warning size={13} weight="fill" /> Not signed in yet — run the command above, then check again.
                </p>
              )}
              <Button
                onClick={handleCheckCliSignIn}
                disabled={phase.checking}
                className="w-full gap-2"
                data-testid="check-sign-in-btn"
              >
                {phase.checking ? <SpinnerGap size={14} className="animate-spin" /> : null}
                Check sign-in
              </Button>
            </div>
          )}

          {phase.kind === 'device_code' && (
            <div className="space-y-3">
              <div>
                <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">1. Open the sign-in page</p>
                <a tabIndex={0}
                  href={phase.verificationUrl}
                  target="_blank"
                  rel="noopener"
                  className="inline-flex items-center gap-1.5 text-sm font-medium text-[var(--color-accent)] hover:underline"
                  data-testid="verification-link"
                >
                  {phase.verificationUrl}
                  <ArrowSquareOut size={13} aria-hidden />
                </a>
              </div>
              <div>
                <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">2. Enter this code</p>
                <div className="flex items-center gap-2">
                  <output
                    className="flex-1 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2.5 text-center text-lg font-mono font-bold tracking-[0.2em] text-[var(--color-secondary)]"
                    data-testid="user-code"
                  >
                    {phase.userCode}
                  </output>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleCopyCode}
                    className="h-10 px-3 gap-1.5 shrink-0"
                    aria-label="Copy code"
                    data-testid="copy-code-btn"
                  >
                    {copied ? <Check size={14} className="text-[var(--color-success)]" /> : <Copy size={14} />}
                    {copied ? 'Copied' : 'Copy'}
                  </Button>
                </div>
              </div>
              {/* Same words as the sr-only aria-live="polite" status line above
                  (FR-045's single source of truth) — hidden from the
                  accessibility tree so a screen reader announces it once, not
                  twice, while sighted operators still see the spinner + copy. */}
              <div
                className="flex items-center gap-2 text-sm text-[var(--color-muted)]"
                data-testid="device-code-waiting"
                aria-hidden="true"
              >
                <SpinnerGap size={13} className="animate-spin" />
                Waiting for you to approve this sign-in…
              </div>
              {providerId === 'openai-chatgpt' && codexLoginPresent && (
                <div className="pt-1 border-t border-[var(--color-border)]">
                  <button tabIndex={0}
                    type="button"
                    onClick={handleImportCodexLogin}
                    disabled={importing}
                    className="text-xs font-medium text-[var(--color-accent)] hover:underline disabled:opacity-50 mt-2"
                    data-testid="import-codex-login-btn"
                  >
                    {importing ? 'Checking…' : 'Use my existing Codex login'}
                  </button>
                  {importError && (
                    <p className="text-xs text-[var(--color-error)] mt-1" role="alert" aria-live="assertive">
                      {importError}
                    </p>
                  )}
                </div>
              )}
            </div>
          )}

          {phase.kind === 'signed_in' && (
            <div
              className="flex items-center gap-2 text-sm text-[var(--color-success)]"
              role="status"
              data-testid="sign-in-success"
            >
              <CheckCircle size={16} weight="fill" />
              {phase.accountLabel ? `Signed in as ${phase.accountLabel}` : 'Signed in'}
            </div>
          )}

          {(phase.kind === 'expired' || phase.kind === 'denied' || phase.kind === 'error') && (
            <div className="space-y-3">
              <div
                className="flex items-start gap-2 text-sm text-[var(--color-error)]"
                role="alert"
                aria-live="assertive"
                data-testid="sign-in-failure"
              >
                <XCircle size={15} weight="fill" className="shrink-0 mt-0.5" />
                <span>
                  {phase.kind === 'expired' && 'Sign-in expired before it was approved.'}
                  {phase.kind === 'denied' && 'Sign-in was denied.'}
                  {phase.kind === 'error' && phase.message}
                </span>
              </div>
              <Button onClick={() => void begin()} variant="outline" className="w-full" data-testid="try-again-btn">
                Try again
              </Button>
            </div>
          )}
        </div>

        <DialogFooter>
          {phase.kind === 'signed_in' ? (
            <Button onClick={() => onOpenChange(false)} className="w-full" data-testid="sign-in-done-btn">
              Done
            </Button>
          ) : (
            <Button variant="outline" onClick={() => onOpenChange(false)} className="w-full" data-testid="sign-in-cancel-btn">
              Cancel
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

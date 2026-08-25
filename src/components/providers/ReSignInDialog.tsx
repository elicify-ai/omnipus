// ReSignInDialog — the `expired`-row re-sign-in surface for `cli_login`
// providers (codex-cli, github-copilot), ADR-068 FR-034/FR-009 amended
// 2026-08-23, T068-26.
//
// This is deliberately NOT the same component as `SignInDialog`
// (src/components/providers/SignInDialog.tsx). SignInDialog always starts by
// calling `POST /providers/{id}/sign-in` (FR-008) — for a `cli_login`
// provider that fetches the vendor login command from the server every time,
// which is the right shape for a FIRST sign-in but wrong for re-checking an
// EXPIRED one: `cli_login` providers hold no Omnipus-side session to refresh
// (FR-007 — Omnipus never writes, refreshes or proxies the vendor's own
// credential file), so there is nothing to re-fetch and no reason to touch
// the sign-in-start endpoint again. The re-sign-in copy is therefore a
// static, client-side string per `cli_kind` (NOT the server's `instructions`
// field, which reads "…then click Check sign-in" for a first-time sign-in —
// this dialog's copy is deliberately the shorter "…again, then check" the
// BDD scenario "Expired session routes to re-sign-in" specifies), and the
// only network call this dialog makes, ever, is the status GET
// (`fetchSignInStatus` → `GET /providers/{id}/sign-in/status`) fired by
// *Check* — never a POST, never a refresh (MAJ-006).
//
// Device-code providers (openai-chatgpt, and xai once configured) do NOT use
// this dialog when expired: their expired state means Omnipus's own token
// refresh already failed (FR-046), so recovering requires a brand new
// device-code approval — that is SignInDialog's job, unchanged. The caller
// (ProvidersSection.tsx's `handleManage`) picks this dialog only when the row
// carries a `cli_kind`.

import { useEffect, useState } from 'react'
import { CheckCircle, SpinnerGap, Warning, XCircle } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { fetchSignInStatus, getErrorMessage } from '@/lib/api'
import type { SignInStatus } from '@/lib/api/generated/openapi-types'

type CliKind = 'codex' | 'copilot'

// Static, client-side re-sign-in copy per cli_kind (FR-034 / BDD "Expired
// session routes to re-sign-in"). Deliberately not fetched from the server —
// see the file header.
const RE_SIGN_IN_COPY: Record<CliKind, { command: string; instruction: string }> = {
  codex: { command: 'codex login', instruction: 'Run `codex login` again, then check' },
  copilot: { command: 'copilot login', instruction: 'Run `copilot login` again, then check' },
}

type Phase =
  | { kind: 'idle' }
  | { kind: 'checking' }
  | { kind: 'still_expired' }
  | { kind: 'not_signed_in' }
  | { kind: 'signed_in'; accountLabel?: string }
  | { kind: 'error'; message: string }

export function ReSignInDialog({
  open,
  onOpenChange,
  providerId,
  providerLabel,
  cliKind,
  onSignedIn,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  providerId: string
  providerLabel: string
  cliKind: CliKind
  /** Fires once the status GET reports signed_in — the caller invalidates/refetches its own provider data. */
  onSignedIn?: (status: SignInStatus) => void
}) {
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const copy = RE_SIGN_IN_COPY[cliKind]

  useEffect(() => {
    if (open) setPhase({ kind: 'idle' })
  }, [open])

  const handleCheck = async () => {
    setPhase({ kind: 'checking' })
    try {
      const status = await fetchSignInStatus(providerId)
      if (status.state === 'signed_in') {
        setPhase({ kind: 'signed_in', accountLabel: status.account_label })
        onSignedIn?.(status)
      } else if (status.state === 'expired') {
        setPhase({ kind: 'still_expired' })
      } else {
        // not_signed_in or (unexpectedly, for a cli_login row) pending — both
        // read as "not there yet, run the command above".
        setPhase({ kind: 'not_signed_in' })
      }
    } catch (err) {
      setPhase({ kind: 'error', message: getErrorMessage(err, 'Sign-in check failed') })
    }
  }

  const statusText = (() => {
    switch (phase.kind) {
      case 'idle': return `${copy.instruction}.`
      case 'checking': return 'Checking…'
      case 'still_expired': return 'Still expired — run the command above, then check again.'
      case 'not_signed_in': return 'Not signed in yet — run the command above, then check again.'
      case 'signed_in': return phase.accountLabel ? `Signed in as ${phase.accountLabel}.` : 'Signed in.'
      case 'error': return phase.message
    }
  })()

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md" data-testid="re-sign-in-dialog">
        <DialogHeader>
          <DialogTitle>Sign in to {providerLabel} again</DialogTitle>
          <DialogDescription>Your session has expired.</DialogDescription>
        </DialogHeader>

        <div role="status" aria-live="polite" data-testid="re-sign-in-status" className="sr-only">
          {statusText}
        </div>

        <div className="space-y-3">
          <div>
            <p className="text-xs font-medium text-[var(--color-muted)] mb-1.5">Run in a terminal</p>
            <code
              className="block rounded-md border border-[var(--color-border)] bg-[var(--color-surface-2)] px-3 py-2 text-sm font-mono text-[var(--color-secondary)]"
              data-testid="re-sign-in-command"
            >
              {copy.command}
            </code>
            <p className="text-xs text-[var(--color-muted)] mt-1.5" data-testid="re-sign-in-instruction">
              {copy.instruction}
            </p>
          </div>

          {phase.kind === 'still_expired' && (
            <p className="text-sm text-[var(--color-warning)] flex items-center gap-1.5" role="alert" aria-live="assertive">
              <Warning size={13} weight="fill" /> Still expired — run the command above, then check again.
            </p>
          )}
          {phase.kind === 'not_signed_in' && (
            <p className="text-sm text-[var(--color-warning)] flex items-center gap-1.5" role="alert" aria-live="assertive">
              <Warning size={13} weight="fill" /> Not signed in yet — run the command above, then check again.
            </p>
          )}
          {phase.kind === 'error' && (
            <p className="text-sm text-[var(--color-error)] flex items-start gap-1.5" role="alert" aria-live="assertive">
              <XCircle size={13} weight="fill" className="shrink-0 mt-0.5" /> {phase.message}
            </p>
          )}
          {phase.kind === 'signed_in' && (
            <div className="flex items-center gap-2 text-sm text-[var(--color-success)]" role="status" data-testid="re-sign-in-success">
              <CheckCircle size={16} weight="fill" />
              {phase.accountLabel ? `Signed in as ${phase.accountLabel}` : 'Signed in'}
            </div>
          )}

          {phase.kind !== 'signed_in' && (
            <Button
              onClick={() => void handleCheck()}
              disabled={phase.kind === 'checking'}
              className="w-full gap-2"
              data-testid="re-sign-in-check-btn"
            >
              {phase.kind === 'checking' ? <SpinnerGap size={14} className="animate-spin" /> : null}
              Check
            </Button>
          )}
        </div>

        <DialogFooter>
          {phase.kind === 'signed_in' ? (
            <Button onClick={() => onOpenChange(false)} className="w-full" data-testid="re-sign-in-done-btn">
              Done
            </Button>
          ) : (
            <Button variant="outline" onClick={() => onOpenChange(false)} className="w-full" data-testid="re-sign-in-cancel-btn">
              Cancel
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

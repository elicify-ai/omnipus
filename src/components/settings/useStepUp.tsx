import { useCallback, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { fetchAppState } from '@/lib/api'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { ReAuthDialog } from './ReAuthDialog'
import { REAUTH_CANCELLED_MESSAGE } from './useReAuthGate'

// useStepUp (ADR-0010 WP3) is the ONE place a settings screen decides how to
// ask the operator to stand behind a sensitive change, before it is sent.
// Upstream re-types the password (ReAuthDialog, restored verbatim from the
// engine merge base); our editions replaced that with a plain confirmation
// (ADR-0008 ruling 6, ConfirmDialog). Both still exist — this hook is the
// seam that picks between them, so no screen composes a dialog for itself:
//
//   - `local` mode (core edition): gate() opens ReAuthDialog SYNCHRONOUSLY,
//     the SAME tick as the click that requested the change — byte-for-byte
//     upstream's own ProvidersSection/IntegrationsSection/GodModeControl/
//     PerformanceSection `requestChange` (merge base 184d7247): the dialog
//     always shows before anything is sent, never an optimistic no-token
//     attempt first. ReAuthDialog mints the consent token itself (it calls
//     POST /auth/reauth directly) and closes itself the instant that
//     succeeds; `run(token)` — the screen's actual mutation — only fires
//     once the dialog has already closed, exactly as upstream's
//     `onReAuthConfirmed` fires `applyChange` after `ReAuthDialog` calls
//     `onOpenChange(false)`. (`useReAuthGate`'s separate `runGated` — an
//     optimistic no-token attempt, then prompt-and-retry on the server's
//     403 — stays unused by this hook: that is upstream's own pattern for
//     auto-save flows with no explicit save click, e.g. SandboxSection and
//     SecuritySection, which call it directly and are untouched by WP3.)
//
//     An earlier version of this hook routed the password path through
//     `useReAuthGate.runGated` here too, wrapping the screen's mutation in a
//     client-side-simulated "optimistic attempt" so a doomed no-token probe
//     would never fire the mutation's onError toast before the dialog
//     opened. That added a promise round trip (the query read, the
//     simulated rejection, the catch) between the click and `setOpen(true)`
//     — a real delay in test-clock terms, even though the dialog still
//     opened "first" in wall-clock terms before anything else could ever
//     show a result. It broke upstream's own `provider-save.test.tsx`
//     (unmodified, oracle-checked against this seam): that test resolves
//     `screen.findByRole('dialog')` against WHATEVER dialog-role element
//     exists at the very first check, and the provider-config Sheet already
//     has `role="dialog"` — the delay let that first check land before
//     ReAuthDialog replaced it as the topmost (and only non-`aria-hidden`)
//     dialog. Gating the mutation, not the raw request, was the wrong
//     layer: this version never calls `run` speculatively, so there is
//     nothing for a doomed attempt to fail — the fix upstream already uses.
//   - `platform` mode (desktop, hosted): requireReAuth is a server-side
//     no-op (there is no local password to re-type; the omnipus.ai session
//     is the guard) — gate() opens ConfirmDialog instead, synchronously,
//     and calls `run()` with no token once the operator confirms.
//
// The mode is read from AppState.identity.mode (WP1, generated) — never
// inferred locally and never a prop a screen has to thread through.
export type StepUpMode = 'password' | 'confirm'

export interface StepUpDescribeChange {
  // The change, phrased as a question: "Enable god mode?", "Rotate the master key?".
  title: string
  // One or two plain sentences saying what happens. Only shown in confirm mode
  // (ReAuthDialog's copy is fixed — see its own title/description defaults).
  body: string
  // Repeats the action verb: "Enable god mode", "Rotate master key". Only
  // shown in confirm mode.
  confirmLabel: string
}

export interface StepUpResult {
  mode: StepUpMode
  // gate runs `run`, first asking the operator to stand behind the change per
  // `mode`. In password mode `run` is called with the freshly minted re-auth
  // consent token; in confirm mode it is called with no token at all (the
  // confirmation carries no credential). Resolves with run's value; rejects
  // with run's error, or a cancellation Error if the operator dismisses the
  // dialog (REAUTH_CANCELLED_MESSAGE in both modes — see useReAuthGate's
  // isReAuthCancelled(), which useAutoSave.ts already treats as a no-op).
  gate: <T>(run: (token?: string) => Promise<T>, describeChange: StepUpDescribeChange) => Promise<T>
  // dialogs are the <ReAuthDialog>/<ConfirmDialog> elements to render once in
  // the component tree — only the one matching `mode` is ever mounted open.
  dialogs: React.ReactElement
  // busy is true while a confirm-mode `run` is in flight (password mode has
  // no equivalent state of its own to report: ReAuthDialog owns its
  // submitting state internally, and — matching upstream — this hook's
  // `run` only ever starts AFTER the dialog has already closed, so `busy`
  // stands in for "the dialog is up" there, same as before this file's
  // restructuring).
  busy: boolean
  // open is true while either dialog is visible — handy for a screen's manual
  // "Save now" escape hatch, which should hide itself while the step-up
  // prompt this same edit already triggered is on screen.
  open: boolean
}

// PendingStepUp holds the one in-flight gate() call: the function to run
// once the operator stands behind the change, and the promise executor pair
// waiting on the outcome. Held in a ref (not state) so the dialog callbacks
// can read the latest `run` without a stale closure.
interface PendingStepUp {
  run: (token?: string) => Promise<unknown>
  resolve: (value: unknown) => void
  reject: (err: unknown) => void
}

export function useStepUp(): StepUpResult {
  // Shared with every other consumer of AppState (AppShell, DataSection,
  // _app.tsx's beforeLoad) — same ['app-state'] cache entry, so this rarely
  // triggers its own network round trip.
  const { data: appState } = useQuery({
    queryKey: ['app-state'],
    queryFn: fetchAppState,
  })
  // Fail toward the stronger control while the app state hasn't loaded yet
  // (or an older backend's response has no `identity` field at all): a
  // password prompt that turns out to be unnecessary (platform mode) is a
  // moment of friction, but a confirmation that turns out to have skipped a
  // real credential check (local mode) is a missed control. identity is a
  // required AppState field on the current contract (ADR-0010 WP1), so this
  // fallback is defense-in-depth, not the normal path.
  const mode: StepUpMode = appState?.identity?.mode === 'platform' ? 'confirm' : 'password'

  const [reAuthOpen, setReAuthOpen] = useState(false)
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [confirmDescribe, setConfirmDescribe] = useState<StepUpDescribeChange | null>(null)
  const [busy, setBusy] = useState(false)
  const pendingRef = useRef<PendingStepUp | null>(null)

  // gate() opens the mode-appropriate dialog in the SAME tick it is called —
  // no query, no optimistic attempt, no await between the click and the
  // dialog appearing. `run` is stored, not called, until the operator stands
  // behind the change.
  const gate = useCallback(
    <T,>(run: (token?: string) => Promise<T>, describeChange: StepUpDescribeChange): Promise<T> => {
      return new Promise<T>((resolve, reject) => {
        pendingRef.current = {
          run: run as (token?: string) => Promise<unknown>,
          resolve: resolve as (value: unknown) => void,
          reject,
        }
        if (mode === 'password') {
          setReAuthOpen(true)
        } else {
          setConfirmDescribe(describeChange)
          setConfirmOpen(true)
        }
      })
    },
    [mode],
  )

  // handleReAuthConfirmed fires once ReAuthDialog has already minted the
  // consent token AND closed itself (it calls onOpenChange(false) right
  // after this, mirroring upstream's ReAuthDialog exactly) — so `run(token)`
  // starts with no dialog on screen, same as upstream's `onReAuthConfirmed`.
  const handleReAuthConfirmed = useCallback((token: string) => {
    const pending = pendingRef.current
    if (!pending) return
    pendingRef.current = null
    pending.run(token).then(pending.resolve, pending.reject)
  }, [])

  const handleReAuthOpenChange = useCallback((next: boolean) => {
    setReAuthOpen(next)
    if (!next) {
      const pending = pendingRef.current
      if (pending) {
        pendingRef.current = null
        pending.reject(new Error(REAUTH_CANCELLED_MESSAGE))
      }
    }
  }, [])

  const handleConfirmAccept = useCallback(() => {
    const pending = pendingRef.current
    if (!pending) return
    setBusy(true)
    pending
      .run(undefined)
      .then((result) => {
        pendingRef.current = null
        setBusy(false)
        setConfirmOpen(false)
        pending.resolve(result)
      })
      .catch((err: unknown) => {
        pendingRef.current = null
        setBusy(false)
        setConfirmOpen(false)
        pending.reject(err)
      })
  }, [])

  const handleConfirmOpenChange = useCallback((next: boolean) => {
    setConfirmOpen(next)
    if (!next) {
      const pending = pendingRef.current
      if (pending) {
        pendingRef.current = null
        // Same sentinel as the password-mode dismissal, so a caller (or
        // useAutoSave's isReAuthCancelled) never has to branch on `mode` to
        // tell "the operator said no" from a real failure.
        pending.reject(new Error(REAUTH_CANCELLED_MESSAGE))
      }
    }
  }, [])

  const dialogs = (
    <>
      <ReAuthDialog open={reAuthOpen} onOpenChange={handleReAuthOpenChange} onConfirmed={handleReAuthConfirmed} />
      <ConfirmDialog
        open={confirmOpen}
        onOpenChange={handleConfirmOpenChange}
        title={confirmDescribe?.title ?? ''}
        description={confirmDescribe?.body ?? ''}
        confirmLabel={confirmDescribe?.confirmLabel ?? 'Confirm'}
        onConfirm={handleConfirmAccept}
        pending={busy}
      />
    </>
  )

  const open = mode === 'password' ? reAuthOpen : confirmOpen
  return { mode, gate, dialogs, busy: mode === 'password' ? reAuthOpen : busy, open }
}

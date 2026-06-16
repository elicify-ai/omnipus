import { useCallback, useRef, useState } from 'react'
import { isApiError } from '@/lib/api'
import { ReAuthDialog } from './ReAuthDialog'

// useReAuthGate wires the Spec-6 FR-12.2 re-auth consent flow into mutations
// that hit a re-auth-gated PUT route (e.g. /security/sandbox-config,
// /security/tool-policies). It mirrors IntegrationsSection's gated-save UX —
// the same ReAuthDialog, the same "re-type your password" copy, the same
// token-replay-in-header semantics — but in a reactive, promise-based form so
// it slots cleanly into debounced auto-save flows that fire the PUT directly.
//
// Flow: runGated(fn) calls fn(token). The first attempt passes token = '' (no
// consent token). If the server answers with the re-auth 403, the dialog opens
// and runGated's promise stays pending. On a successful re-auth the minted
// consent token is replayed into a single fn(token) retry. Any other error (or
// a second re-auth 403) rejects so the caller's normal error handling runs.

// isReAuthRequiredError distinguishes the re-auth gate's 403 from other 403s
// (CSRF, plain forbidden). The backend's requireReAuth writes
//   {"error":"this change requires re-typing your password — ..."}
// with no machine code, whereas the client-side CSRF guard sets
// code === 'csrf_missing'. ApiError preserves the raw body verbatim, so a body
// match is reliable and the CSRF code is excluded explicitly.
export function isReAuthRequiredError(err: unknown): boolean {
  if (!isApiError(err)) return false
  if (err.status !== 403) return false
  if (err.code === 'csrf_missing') return false
  const haystack = `${err.body ?? ''} ${err.userMessage}`.toLowerCase()
  return (
    haystack.includes('re-typing your password') ||
    haystack.includes('re-type your password') ||
    haystack.includes('auth/reauth')
  )
}

interface ReAuthGateOptions {
  title?: string
  description?: string
}

interface ReAuthGateResult {
  // runGated runs fn(token), replaying a freshly-minted consent token if the
  // server demands re-auth. Resolves with fn's value; rejects with fn's error
  // (or a cancellation Error if the user dismisses the dialog).
  runGated: <T>(fn: (token: string) => Promise<T>) => Promise<T>
  // dialog is the <ReAuthDialog> element to render once in the component tree.
  dialog: React.ReactElement
  // open is true while the consent dialog is showing — handy for disabling
  // Save buttons during the prompt.
  open: boolean
}

export function useReAuthGate(options?: ReAuthGateOptions): ReAuthGateResult {
  const { title, description } = options ?? {}
  const [open, setOpen] = useState(false)

  // The pending re-auth promise's resolver/rejecter. Held in a ref so the
  // dialog's onConfirmed / onOpenChange callbacks can settle it without
  // re-rendering churn.
  const resolverRef = useRef<{
    resolve: (token: string) => void
    reject: (err: unknown) => void
  } | null>(null)

  // awaitToken opens the dialog and resolves once the user re-authenticates.
  const awaitToken = useCallback(() => {
    return new Promise<string>((resolve, reject) => {
      resolverRef.current = { resolve, reject }
      setOpen(true)
    })
  }, [])

  const runGated = useCallback(
    async <T,>(fn: (token: string) => Promise<T>): Promise<T> => {
      try {
        // Optimistic first attempt with no consent token. Onboarding-style or
        // already-consented paths succeed here without a prompt.
        return await fn('')
      } catch (err) {
        if (!isReAuthRequiredError(err)) throw err
        // Server demands re-auth: prompt, then replay the token exactly once.
        const token = await awaitToken()
        return await fn(token)
      }
    },
    [awaitToken],
  )

  const onConfirmed = useCallback((token: string) => {
    const r = resolverRef.current
    resolverRef.current = null
    setOpen(false)
    r?.resolve(token)
  }, [])

  const onOpenChange = useCallback((next: boolean) => {
    setOpen(next)
    if (!next) {
      // Dialog dismissed without confirming — reject the pending retry so the
      // caller's error handling runs (and the change is not silently dropped).
      const r = resolverRef.current
      resolverRef.current = null
      r?.reject(new Error('Re-authentication cancelled'))
    }
  }, [])

  const dialog = (
    <ReAuthDialog
      open={open}
      onOpenChange={onOpenChange}
      title={title ?? 'Confirm your password'}
      description={description ?? 'For your security, re-type your password to make this change.'}
      onConfirmed={onConfirmed}
    />
  )

  return { runGated, dialog, open }
}

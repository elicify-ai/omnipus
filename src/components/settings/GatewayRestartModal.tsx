/**
 * GatewayRestartModal — O4 restart UX.
 *
 * Shown when the user saves a restart-gated setting. Offers two actions:
 *   [Restart now]  — calls POST /api/v1/gateway/restart (authed, admin-only),
 *                    receives 202 Accepted, then polls /health until the gateway
 *                    responds, clears itself, and shows a success toast.
 *   [Later]        — defers; the pending-restart state persists in the banner.
 *
 * Down→up reattach: on restart, the WS connection drops. The existing
 * OmnipusRuntimeProvider reconnect loop re-establishes the connection. We
 * poll /health here to detect the gateway coming back up, then show a
 * success toast and clear the modal. /health is the canonical liveness probe
 * (no auth required) per the CLAUDE.md gateway docs.
 */

import { useState, useEffect, useRef, useCallback } from 'react'
import { ArrowsClockwise, CheckCircle } from '@phosphor-icons/react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { usePendingRestart } from '@/store/restart'
import { getErrorMessage, gatewayRestart } from '@/lib/api'

// Poll /health until the gateway responds, then resolve. Times out after
// timeoutMs (default 60 s). Polls every intervalMs (default 1 s).
// /health is the public liveness endpoint — no auth required. Using
// /api/v1/status would return 401 in authed deployments, so it cannot serve
// as a liveness probe from the SPA.
async function pollUntilOnline(
  timeoutMs = 60_000,
  intervalMs = 1_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch('/health')
      if (res.ok) return
    } catch {
      // Gateway not yet up — continue polling.
    }
    await new Promise<void>((resolve) => setTimeout(resolve, intervalMs))
  }
  throw new Error('Gateway did not come back online within 60 s')
}

// ─── Component ────────────────────────────────────────────────────────────────

export interface GatewayRestartModalProps {
  open: boolean
  /** Called when the modal should close (either after success or Later). */
  onClose: () => void
}

type RestartPhase = 'idle' | 'restarting' | 'waiting' | 'success' | 'error'

export function GatewayRestartModal({ open, onClose }: GatewayRestartModalProps) {
  const { addToast } = useUiStore()
  const { refetch: refetchPending } = usePendingRestart()
  const [phase, setPhase] = useState<RestartPhase>('idle')
  const [errorMsg, setErrorMsg] = useState<string | undefined>()
  const abortRef = useRef(false)

  // Reset state when modal opens.
  useEffect(() => {
    if (open) {
      setPhase('idle')
      setErrorMsg(undefined)
      abortRef.current = false
    } else {
      // Signal any in-flight polling to stop when modal closes.
      abortRef.current = true
    }
  }, [open])

  const handleRestartNow = useCallback(async () => {
    setPhase('restarting')
    setErrorMsg(undefined)
    try {
      // gatewayRestart() uses the authed request() helper (injects the bearer
      // token + CSRF header automatically). The backend responds with 202
      // Accepted; any 2xx is treated as success.
      await gatewayRestart()
    } catch (err) {
      if (abortRef.current) return
      setPhase('error')
      setErrorMsg(getErrorMessage(err, 'Restart request failed'))
      return
    }

    // Gateway is restarting — poll /health until it comes back up.
    setPhase('waiting')
    try {
      await pollUntilOnline()
    } catch (err) {
      if (abortRef.current) return
      setPhase('error')
      setErrorMsg(
        err instanceof Error
          ? err.message
          : 'Gateway did not respond after restart',
      )
      return
    }

    if (abortRef.current) return
    setPhase('success')
    // Refresh the pending-restart list so the banner clears.
    void refetchPending()
    addToast({ message: 'Gateway restarted successfully', variant: 'success' })
    // Close the modal after a brief success pause.
    setTimeout(() => {
      if (!abortRef.current) onClose()
    }, 1_200)
  }, [addToast, refetchPending, onClose])

  const handleLater = useCallback(() => {
    abortRef.current = true
    onClose()
  }, [onClose])

  const isRestarting = phase === 'restarting' || phase === 'waiting'

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v && !isRestarting) handleLater() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Gateway restart required</DialogTitle>
          <DialogDescription>
            Your changes have been saved. The gateway must restart to apply them. You can
            restart now or come back to it later from Settings → Gateway.
          </DialogDescription>
        </DialogHeader>

        {phase === 'waiting' && (
          <div className="flex items-center gap-2 text-sm text-[var(--color-muted)] py-2">
            <ArrowsClockwise size={15} className="animate-spin shrink-0 text-[var(--color-accent)]" />
            Restarting — waiting for gateway to come back online…
          </div>
        )}
        {phase === 'restarting' && (
          <div className="flex items-center gap-2 text-sm text-[var(--color-muted)] py-2">
            <ArrowsClockwise size={15} className="animate-spin shrink-0 text-[var(--color-accent)]" />
            Sending restart signal…
          </div>
        )}
        {phase === 'success' && (
          <div className="flex items-center gap-2 text-sm text-[var(--color-success)] py-2">
            <CheckCircle size={15} weight="fill" className="shrink-0" />
            Gateway restarted successfully.
          </div>
        )}
        {phase === 'error' && errorMsg && (
          <p className="text-sm text-[var(--color-error)] py-2">{errorMsg}</p>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={handleLater}
            disabled={isRestarting}
          >
            Later
          </Button>
          <Button
            onClick={() => void handleRestartNow()}
            disabled={isRestarting || phase === 'success'}
          >
            {isRestarting ? (
              <>
                <ArrowsClockwise size={13} className="animate-spin mr-1.5" />
                Restarting…
              </>
            ) : (
              'Restart now'
            )}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

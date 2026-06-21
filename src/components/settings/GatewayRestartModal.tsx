/**
 * GatewayRestartModal — O4 restart UX.
 *
 * Shown when the user saves a restart-gated setting. Offers two actions:
 *   [Restart now]  — calls the self-restart endpoint (Track A wires the
 *                    generated client here at integration; see RESTART_NOW
 *                    below), then polls the gateway back up and clears itself.
 *   [Later]        — defers; the pending-restart state persists in the banner.
 *
 * Down→up reattach: on restart, the WS connection drops. The existing
 * OmnipusRuntimeProvider reconnect loop re-establishes the connection. We
 * poll /api/v1/status here to detect the gateway coming back up, then show a
 * success toast and clear the modal.
 *
 * The actual API call is isolated to `triggerGatewayRestart` below so the
 * integration point is a single clearly-marked function.
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
import { isApiError } from '@/lib/api'

// ─── Integration seam: self-restart endpoint ──────────────────────────────────
//
// Track A adds POST /api/v1/gateway/restart. At integration the lead wires the
// generated client call here and removes the stub.
//
// RESTART_NOW is the single call site — a clearly-marked async function that
// fires the HTTP request and resolves when the server has acknowledged the
// restart intent (it will close the connection shortly after).

async function triggerGatewayRestart(): Promise<void> {
  // INTEGRATION_SEAM: replace this stub with the generated client call, e.g.:
  //   await gatewayRestartApi()
  // The generated function lives in src/lib/api/generated/ once Track A
  // regenerates the contracts. The backend responds with 200 + a minimal JSON
  // body before beginning its graceful drain.
  const res = await fetch('/api/v1/gateway/restart', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      // The generated client injects the auth header automatically; a raw
      // fetch here would need the bearer token. At integration, swap the fetch
      // for the generated client so auth is handled consistently.
    },
  })
  if (!res.ok) {
    throw new Error(`Restart failed: ${res.status}`)
  }
}

// Poll /api/v1/status until the gateway responds, then resolve. Times out
// after timeoutMs (default 60 s). Polls every intervalMs (default 1 s).
async function pollUntilOnline(
  timeoutMs = 60_000,
  intervalMs = 1_000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const res = await fetch('/api/v1/status')
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
      await triggerGatewayRestart()
    } catch (err) {
      if (abortRef.current) return
      setPhase('error')
      setErrorMsg(
        isApiError(err)
          ? err.userMessage
          : err instanceof Error
          ? err.message
          : 'Restart request failed',
      )
      return
    }

    // Gateway is restarting — poll until it comes back up.
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

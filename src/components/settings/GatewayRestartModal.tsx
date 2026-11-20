/**
 * GatewayRestartModal — the "Restart now?" window (Settings redesign #1519,
 * FR-033, FR-063; also used by God Mode).
 *
 * Shown right after a Save that stored a restart-gated value (listing those
 * settings by label) and from the restart banner. Offers Later and Restart now.
 *
 * Completion (FR-063). Every check is a same-origin request: the page's
 * connection policy allows only its own address, and it is not widened.
 *   - The page's own address keeps serving the gateway: done means the page's
 *     own GET /about answers with `uptime_seconds` smaller than the seconds
 *     since Restart now was pressed (an old process still answering never
 *     counts), and then GET /config/pending-restart no longer lists the
 *     restarted keys.
 *   - A saved port (or host) change while the page talks to the gateway
 *     directly (page port equals the running port): before restarting, the
 *     window names the new address and the proxy instruction; it polls only
 *     its own /health, and once that stops answering it offers "Open the new
 *     address", a plain link the person follows. Settings at that address
 *     confirms the restart (`?restarted=1`).
 *   - The page port differs from the running port (a proxy): the window names
 *     the new port and says the proxy must point there, offers no address,
 *     and checks completion on its own address.
 *   - Nothing changes within 60 s: the window says so, names what to check,
 *     and keeps Restart now.
 */

import { useState, useEffect, useRef, useCallback, useMemo } from 'react'
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
import { usePendingRestart } from '@/hooks/restart'
import { fetchAboutInfo, fetchPendingRestart, getErrorMessage, gatewayRestart } from '@/lib/api'
import type { PendingRestartEntry } from '@/lib/api'

const RESTART_TIMEOUT_MS = 60_000
const POLL_INTERVAL_MS = 1_000

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms))

function pagePort(): number {
  const { port, protocol } = window.location
  if (port) return Number(port)
  return protocol === 'https:' ? 443 : 80
}

function asPort(v: unknown): number | null {
  const n = typeof v === 'number' ? v : typeof v === 'string' ? Number(v) : NaN
  return Number.isInteger(n) && n > 0 ? n : null
}

export interface AddressChange { // not-wire-format: screen-side restart plan
  kind: 'direct' | 'proxy' | 'unknown'
  newPort: number | null
  newAddress: string | null
}

/**
 * Derives where the gateway will answer after a port or host change (FR-063).
 * A new address is derived only when it needs no guess.
 */
export function deriveAddressChange(entries: PendingRestartEntry[]): AddressChange | null {
  const port = entries.find((e) => e.key === 'gateway.port')
  const host = entries.find((e) => e.key === 'gateway.host')
  if (!port && !host) return null
  const runningPort = asPort(port?.applied_value) ?? pagePort()
  const newPort = asPort(port?.persisted_value) ?? runningPort
  if (pagePort() !== runningPort) return { kind: 'proxy', newPort, newAddress: null }
  let hostname = window.location.hostname
  if (host) {
    const h = typeof host.persisted_value === 'string' ? host.persisted_value : ''
    const allInterfaces = h === '0.0.0.0' || h === '::' || h === ''
    if (!allInterfaces && h !== window.location.hostname) return { kind: 'unknown', newPort, newAddress: null }
    hostname = window.location.hostname
  }
  return { kind: 'direct', newPort, newAddress: `${window.location.protocol}//${hostname}:${newPort}` }
}

/** Polls the page's own /about until a process younger than the press answers. */
async function waitForFreshProcess(pressedAt: number, deadline: number, aborted: () => boolean): Promise<boolean> {
  while (Date.now() < deadline && !aborted()) {
    try {
      const about = await fetchAboutInfo()
      const sincePress = (Date.now() - pressedAt) / 1000
      if (typeof about.uptime_seconds === 'number' && about.uptime_seconds < sincePress) return true
    } catch {
      // Not answering yet — keep polling.
    }
    await sleep(POLL_INTERVAL_MS)
  }
  return false
}

/** Polls the page's own /health until it stops answering. */
async function waitForOldAddressToStop(deadline: number, aborted: () => boolean): Promise<boolean> {
  while (Date.now() < deadline && !aborted()) {
    try {
      const res = await fetch('/health', { cache: 'no-store' })
      if (!res.ok) return true
    } catch {
      return true
    }
    await sleep(POLL_INTERVAL_MS)
  }
  return false
}

export interface GatewayRestartModalProps {
  open: boolean
  /** Called when the window closes (after success, Later or Escape). */
  onClose: () => void
  /** Labels of the settings just saved that need the restart (FR-033). */
  labels?: string[]
  /** Called once a restart is proved complete (FR-035: focus to the heading). */
  onRestarted?: () => void
}

type RestartPhase = 'idle' | 'restarting' | 'waiting' | 'moved' | 'success' | 'error'

export function GatewayRestartModal({ open, onClose, labels, onRestarted }: GatewayRestartModalProps) {
  const { addToast } = useUiStore()
  // `entries` may be absent from a partial hook stand-in; treat that as no waiting changes.
  const { entries = [], refetch: refetchPending } = usePendingRestart()
  const [phase, setPhase] = useState<RestartPhase>('idle')
  const [errorMsg, setErrorMsg] = useState<string | undefined>()
  const abortRef = useRef(false)

  const change = useMemo(() => deriveAddressChange(entries), [entries])

  useEffect(() => {
    if (open) {
      setPhase('idle')
      setErrorMsg(undefined)
      abortRef.current = false
    } else {
      abortRef.current = true
    }
  }, [open])

  const handleRestartNow = useCallback(async () => {
    const restartedKeys = entries.map((e) => e.key)
    const plan = change
    setPhase('restarting')
    setErrorMsg(undefined)
    const pressedAt = Date.now()
    try {
      await gatewayRestart()
    } catch (err) {
      if (abortRef.current) return
      setPhase('error')
      setErrorMsg(getErrorMessage(err, 'The restart request failed'))
      return
    }
    setPhase('waiting')
    const deadline = pressedAt + RESTART_TIMEOUT_MS
    const aborted = () => abortRef.current

    if (plan?.kind === 'direct') {
      const stopped = await waitForOldAddressToStop(deadline, aborted)
      if (abortRef.current) return
      if (stopped) {
        setPhase('moved')
        return
      }
      setPhase('error')
      setErrorMsg('The gateway did not restart within 60 seconds: the old address still answers. Check that the gateway is running and read its log, then try again.')
      return
    }

    const fresh = await waitForFreshProcess(pressedAt, deadline, aborted)
    if (abortRef.current) return
    if (!fresh) {
      setPhase('error')
      setErrorMsg('The gateway did not come back within 60 seconds. Check that it is running and read its log, then try again.')
      return
    }
    // The new process answers; done once the restarted keys are no longer listed.
    while (Date.now() < deadline && !abortRef.current) {
      try {
        const now = await fetchPendingRestart()
        if (!now.some((e) => restartedKeys.includes(e.key) && e.reason === 'restart_required')) break
      } catch {
        // keep polling
      }
      await sleep(POLL_INTERVAL_MS)
    }
    if (abortRef.current) return
    setPhase('success')
    void refetchPending()
    addToast({ message: 'The gateway restarted.', variant: 'success' })
    onRestarted?.()
    setTimeout(() => {
      if (!abortRef.current) onClose()
    }, 1_200)
  }, [entries, change, addToast, refetchPending, onClose, onRestarted])

  const handleLater = useCallback(() => {
    abortRef.current = true
    onClose()
  }, [onClose])

  const isRestarting = phase === 'restarting' || phase === 'waiting'
  const shown = labels && labels.length ? labels : null

  return (
    <Dialog open={open} onOpenChange={(v) => { if (!v && !isRestarting) handleLater() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restart now?</DialogTitle>
          <DialogDescription>
            {shown
              ? `${shown.length === 1 ? 'The change you just saved takes' : `The ${shown.length} changes you just saved take`} effect after a gateway restart. Running agents pause for a few seconds. Later keeps the banner at the top, so you can restart when it suits you.`
              : 'Saved changes that need a restart take effect after it. Running agents pause for a few seconds.'}
          </DialogDescription>
        </DialogHeader>

        {shown && (
          <ul className="list-disc pl-[var(--space-4)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]" data-testid="restart-labels">
            {shown.map((l) => (
              <li key={l}>{l}</li>
            ))}
          </ul>
        )}

        {change?.kind === 'direct' && change.newAddress && (
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            After the restart the gateway answers at {change.newAddress}, and this address stops working. If you reach Omnipus through a proxy, point it at port {change.newPort} instead.
          </p>
        )}
        {change?.kind === 'proxy' && (
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            After the restart the gateway listens on port {change.newPort}. Point your proxy at port {change.newPort}, or this page cannot reach it.
          </p>
        )}
        {change?.kind === 'unknown' && (
          <p className="text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)]">
            After the restart the gateway listens on a different address, on port {change.newPort}. Open Omnipus there once it has restarted.
          </p>
        )}

        {phase === 'waiting' && (
          <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] py-[var(--space-2)]">
            <ArrowsClockwise size={15} className="animate-spin motion-reduce:animate-none shrink-0 text-[var(--color-accent)]" />
            Restarting. Waiting for the gateway to come back…
          </div>
        )}
        {phase === 'restarting' && (
          <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-muted)] py-[var(--space-2)]">
            <ArrowsClockwise size={15} className="animate-spin motion-reduce:animate-none shrink-0 text-[var(--color-accent)]" />
            Sending the restart request…
          </div>
        )}
        {phase === 'moved' && change?.newAddress && (
          <div className="space-y-[var(--space-2)] text-[length:var(--type-body-compact-size)]">
            <p className="text-[var(--color-secondary)]">This address stopped answering. Open the new address to finish; Settings there confirms the restart.</p>
            <a tabIndex={0}
              href={`${change.newAddress}/settings?tab=gateway&restarted=1`}
              className="text-[var(--color-accent)] underline-offset-4 hover:underline"
            >
              Open the new address
            </a>
          </div>
        )}
        {phase === 'success' && (
          <div role="status" className="flex items-center gap-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-success)] py-[var(--space-2)]">
            <CheckCircle size={15} weight="fill" className="shrink-0" />
            The gateway restarted.
          </div>
        )}
        {phase === 'error' && errorMsg && (
          <p role="alert" className="text-[length:var(--type-body-compact-size)] text-[var(--color-error)] py-[var(--space-2)]">{errorMsg}</p>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={handleLater} disabled={isRestarting}>
            Later
          </Button>
          {phase !== 'moved' && (
            <Button onClick={() => void handleRestartNow()} disabled={isRestarting || phase === 'success'}>
              {isRestarting ? (
                <>
                  <ArrowsClockwise size={13} className="animate-spin motion-reduce:animate-none mr-[var(--space-1)]" />
                  Restarting…
                </>
              ) : (
                'Restart now'
              )}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

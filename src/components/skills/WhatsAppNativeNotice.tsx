import { useState, useEffect, useRef } from 'react'
import { Warning } from '@phosphor-icons/react'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import type { WhatsAppPairingState } from '@/store/whatsappPairing'
import { useConnectionStore } from '@/store/connection'
import { disableChannel, enableChannel } from '@/lib/api'
import { WhatsAppPairingBody } from './WhatsAppPairingBody'

// RETRY_TIMEOUT_MS is the bounded window after a user presses Retry during which
// we wait for a fresh `code` frame from the backend. Retry restarts the channel
// (disable → enable), which re-arms whatsmeow's QR loop; the first code can take
// up to ~60 s after connect, plus channel stop/start time, so the window must
// comfortably exceed that. If no `code` frame arrives within this window, we
// revert to the original fallback state with the Retry affordance so the user
// is never stranded in an endless spinner.
const RETRY_TIMEOUT_MS = 90_000

// QR_INITIAL_TIMEOUT_MS: if no QR frame arrives within this window from mount,
// surface a "timed out — click Retry" message. Prevents infinite spinner (#368).
const QR_INITIAL_TIMEOUT_MS = 15_000

// TIMEOUT_STATE: synthetic pairing state surfaced when the 15s initial-load timer
// fires with no QR frame. Avoids an inline object literal at the effectivePairing site.
const TIMEOUT_STATE: WhatsAppPairingState = {
  status: 'timeout',
  qr: '',
  message: 'QR code timed out — click Retry',
}

// WhatsAppNativeNotice renders the live linked-device pairing QR + status in the
// browser (#283 / US-C3), fed by the whatsapp_pairing WS frame.
//
// channelId is the INSTANCE id the config panel is open for ("whatsapp" for the
// bare/legacy instance, "whatsapp.<slug>" for ADR-029 namespaced instances).
// Since the multi-instance change the channel manager keys channels — and
// therefore stamps pairing frames — by instance id (manager.go: m.channels
// keyed by instanceID), NOT by the old registry name "whatsapp_native". Each
// instance has its own whatsmeow store and its own QR, so pairing state,
// subscribe interest and cleanup are all scoped per instance id.
export function WhatsAppNativeNotice({ channelId }: { channelId: string }) {
  const pairing = useWhatsAppPairingStore((s) => s.byChannel[channelId])
  const clear = useWhatsAppPairingStore((s) => s.clear)
  const isConnected = useConnectionStore((s) => s.isConnected)

  // retryFallbackState holds the status to revert to if the bounded retry timer
  // fires without a fresh `code` frame arriving. null = not in retry mode.
  const [retryFallbackState, setRetryFallbackState] = useState<'timeout' | 'error' | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // timedOut: true when the 15s initial window expires with no QR frame arriving.
  const [timedOut, setTimedOut] = useState(false)
  const initialTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // Initial 15s timeout: start when pairing is undefined, clear when QR arrives.
  useEffect(() => {
    if (pairing !== undefined) {
      // QR (or any pairing state) arrived — clear the initial timer, reset flag.
      if (initialTimerRef.current !== null) {
        clearTimeout(initialTimerRef.current)
        initialTimerRef.current = null
      }
      setTimedOut(false)
      return
    }
    // pairing still undefined: arm the timer. The effect only runs when pairing
    // changes identity (dependency array), and the cleanup below cancels any
    // prior timer, so no double-arm guard is needed here.
    initialTimerRef.current = setTimeout(() => {
      initialTimerRef.current = null
      setTimedOut(true)
    }, QR_INITIAL_TIMEOUT_MS)
    return () => {
      if (initialTimerRef.current !== null) {
        clearTimeout(initialTimerRef.current)
        initialTimerRef.current = null
      }
    }
  }, [pairing])

  // When a `code` frame arrives while we're in retry mode, cancel the fallback
  // timer and exit retry mode. This is the success path.
  useEffect(() => {
    if (pairing?.status === 'code' && retryFallbackState !== null) {
      if (retryTimerRef.current !== null) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
      setRetryFallbackState(null)
    }
  }, [pairing?.status, retryFallbackState])

  // #283 (Option B): tell the gateway this connection is viewing WhatsApp
  // pairing so the QR is delivered only here, not broadcast to every admin tab.
  // Re-subscribe whenever the socket (re)connects while the panel is open —
  // per-connection interest is lost across a reconnect.
  useEffect(() => {
    if (!isConnected) return
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: channelId,
      active: true,
    })
  }, [isConnected, channelId])

  // On unmount (panel closed): cancel any pending timers, unsubscribe and
  // drop the QR/pairing secret from the store so it doesn't linger in memory
  // past the pairing flow (#283).
  useEffect(
    () => () => {
      if (retryTimerRef.current !== null) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
      if (initialTimerRef.current !== null) {
        clearTimeout(initialTimerRef.current)
        initialTimerRef.current = null
      }
      useConnectionStore.getState().connection?.send({
        type: 'whatsapp_pairing_subscribe',
        channel_id: channelId,
        active: false,
      })
      clear(channelId)
    },
    [clear, channelId],
  )

  async function handleRetry() {
    const fallback = pairing?.status === 'error' ? 'error' : 'timeout'

    // Reset initial-timeout state and re-arm the window for the new attempt.
    setTimedOut(false)
    if (initialTimerRef.current !== null) {
      clearTimeout(initialTimerRef.current)
      initialTimerRef.current = null
    }

    // Clear the stale pairing state so we show the spinner immediately, and
    // enter retry mode BEFORE the restart round-trip (retryFallbackState != null
    // → the body renders the spinner while the channel bounces).
    clear(channelId)
    setRetryFallbackState(fallback)

    // whatsmeow's QR loop is NOT request-driven and is terminal once its codes
    // expire (or on error) — no amount of re-subscribing produces a new code.
    // The only way to mint a fresh QR is to restart the channel: disable →
    // enable walks the ChannelManager.Reload diff (stop, then start), and a
    // starting unpaired whatsapp channel re-arms the QR loop (#358).
    try {
      await disableChannel(channelId)
      await enableChannel(channelId)
    } catch {
      // Restart failed (gateway unreachable, reload error) — surface the error
      // state immediately instead of spinning for the full retry window.
      useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing',
        channel_id: channelId,
        status: 'error',
        message: 'the channel could not be restarted — check the gateway logs',
      })
      setRetryFallbackState(null)
      return
    }

    // Re-register pairing interest for this connection (toggle false → true);
    // the freshly-started channel emits its first QR over whatsapp_pairing and
    // the store picks it up under this instance id.
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: channelId,
      active: false,
    })
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: channelId,
      active: true,
    })

    // Bounded timeout: if no `code` frame arrives within RETRY_TIMEOUT_MS, revert
    // to the original state with the Retry affordance (no endless spinner).
    if (retryTimerRef.current !== null) {
      clearTimeout(retryTimerRef.current)
    }
    retryTimerRef.current = setTimeout(() => {
      retryTimerRef.current = null
      // Re-inject the fallback into the store BEFORE clearing retryFallbackState so
      // the store holds the correct state when the re-render fires (effectivePairing
      // switches from undefined to the store value in the same synchronous batch).
      useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing',
        channel_id: channelId,
        status: fallback,
        message: fallback === 'timeout'
          ? 'the QR code expired before it was scanned'
          : 'enable multi-device in WhatsApp, then scan the code again',
      })
      // Reset timedOut so it does not re-assert the timeout banner on top of the
      // freshly-injected fallback state from the store.
      setTimedOut(false)
      setRetryFallbackState(null)
    }, RETRY_TIMEOUT_MS)
  }

  // While in retry mode (retryFallbackState is set), the store has been cleared,
  // so `pairing` is undefined. We show the spinner by passing undefined as pairing
  // (the WhatsAppPairingBody default), not the stale pre-clear value.
  // When timedOut and still no pairing: show the timeout state with Retry.
  const effectivePairing =
    retryFallbackState !== null
      ? undefined
      : timedOut && pairing === undefined
        ? TIMEOUT_STATE
        : pairing

  return (
    <div className="space-y-2 mt-1">
      <div className="flex flex-col items-center gap-3 p-4 rounded-lg bg-[var(--color-surface-1)] border border-[var(--color-border)]">
        <WhatsAppPairingBody pairing={effectivePairing} onRetry={handleRetry} />
      </div>
      <div className="flex gap-2 p-3 rounded-md bg-[var(--color-surface-2)] border border-[var(--color-error)]/30">
        <Warning size={14} className="text-[var(--color-error)] shrink-0 mt-0.5" weight="fill" />
        <p className="text-xs text-[var(--color-muted)]">
          WhatsApp native mode stores sessions locally. The gateway must keep running for the session
          to stay active.
        </p>
      </div>
    </div>
  )
}

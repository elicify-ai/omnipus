import { useState, useEffect, useRef } from 'react'
import { Warning } from '@phosphor-icons/react'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import type { WhatsAppPairingState } from '@/store/whatsappPairing'
import { useConnectionStore } from '@/store/connection'
import { WhatsAppPairingBody } from './WhatsAppPairingBody'
import { WHATSAPP_NATIVE_CHANNEL_ID } from './whatsappChannelId'

// RETRY_TIMEOUT_MS is the bounded window after a user presses Retry during which
// we wait for a fresh `code` frame from the backend. The subscribe toggle only
// controls forwarding interest — it does NOT make whatsmeow mint a new QR.
// For a `timeout` state whatsmeow may emit a new code automatically; for `error`
// the QR loop is terminal and no new code will arrive. If no `code` frame arrives
// within this window, we revert to the original fallback state with the Retry
// affordance so the user is never stranded in an endless spinner.
const RETRY_TIMEOUT_MS = 30_000

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
// browser (#283 / US-C3), fed by the whatsapp_pairing WS frame. The native channel
// emits under channel_id "whatsapp_native". Replaces the old "check the gateway
// terminal" text — no terminal access required.
export function WhatsAppNativeNotice() {
  const pairing = useWhatsAppPairingStore((s) => s.byChannel[WHATSAPP_NATIVE_CHANNEL_ID])
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
    // pairing still undefined: arm the timer once (guard against double-arm).
    if (initialTimerRef.current !== null) return
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
      channel_id: WHATSAPP_NATIVE_CHANNEL_ID,
      active: true,
    })
  }, [isConnected])

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
        channel_id: WHATSAPP_NATIVE_CHANNEL_ID,
        active: false,
      })
      clear(WHATSAPP_NATIVE_CHANNEL_ID)
    },
    [clear],
  )

  function handleRetry() {
    const fallback = pairing?.status === 'error' ? 'error' : 'timeout'

    // Reset initial-timeout state and re-arm the 15s window for the new attempt.
    setTimedOut(false)
    if (initialTimerRef.current !== null) {
      clearTimeout(initialTimerRef.current)
      initialTimerRef.current = null
    }

    // Clear the stale pairing state so we show the spinner immediately.
    clear(WHATSAPP_NATIVE_CHANNEL_ID)

    // Toggle subscribe interest: false then true restores forwarding to this
    // connection. Note: this does NOT make whatsmeow mint a new QR — it only
    // re-enables delivery of any QR frames the backend emits on its own schedule.
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: WHATSAPP_NATIVE_CHANNEL_ID,
      active: false,
    })
    useConnectionStore.getState().connection?.send({
      type: 'whatsapp_pairing_subscribe',
      channel_id: WHATSAPP_NATIVE_CHANNEL_ID,
      active: true,
    })

    // Enter retry mode: record the fallback state so WhatsAppPairingBody still
    // renders the spinner (retryFallbackState != null → pairing is undefined in
    // the store after clear()).
    setRetryFallbackState(fallback)

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
        channel_id: WHATSAPP_NATIVE_CHANNEL_ID,
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

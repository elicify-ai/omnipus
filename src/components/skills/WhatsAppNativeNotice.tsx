import { useState, useEffect, useRef } from 'react'
import { Warning } from '@phosphor-icons/react'
import { useWhatsAppPairingStore } from '@/store/whatsappPairing'
import type { WhatsAppPairingState } from '@/store/whatsappPairing'
import { disableChannel, enableChannel } from '@/lib/api'
import type { ChannelEnableRequest } from '@/lib/api/generated/openapi-types'
import { isApiError, parseServerErrorField } from '@/lib/api-error'
import { getWhatsAppEnableErrorMessage, WHATSAPP_ENABLE_CANCELLED_MESSAGE, WHATSAPP_PAIRING_DELETED_MESSAGE } from '@/lib/whatsappEnableError'
import { logError } from '@/lib/telemetry'
import { WhatsAppPairingBody } from './WhatsAppPairingBody'
import { useWhatsAppPairingInterest } from './useWhatsAppPairingInterest'
import { Card } from '@/components/ui/card'
import { ErrorState } from '@/components/ui/error-state'

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

const PAIRING_RETRY_CONFLICT_MESSAGE = 'This chat can no longer retry this pairing. Open the WhatsApp connector to start a new pairing.'

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
//
// The same component renders the QR in the requesting chat (K10, FR-081):
// `requestId`/`sessionId` are the original connect_channel call's ids. Retry
// claims the next attempt through REST enable; subscription ids only correlate
// observations. A stored pairing belonging to a DIFFERENT chat request is not
// shown in this card. An uncorrelated Connectors-started pairing is shown.
export function WhatsAppNativeNotice({ channelId, requestId, sessionId }: { channelId: string; requestId?: string; sessionId?: string }) {
  const stored = useWhatsAppPairingStore((s) => s.byChannel[channelId])
  const pairing = requestId && stored?.requestId && stored.requestId !== requestId ? undefined : stored
  const clear = useWhatsAppPairingStore((s) => s.clear)

  // retryFallbackState holds the status to revert to if the bounded retry timer
  // fires without a fresh `code` frame arriving. null = not in retry mode.
  const [retryFallbackState, setRetryFallbackState] = useState<'timeout' | 'error' | null>(null)
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)

  // retryInFlightRef guards against a rapid double-click firing two
  // overlapping restart round-trips (disable→enable twice). A ref updates
  // synchronously the instant handleRetry starts running — unlike
  // retryFallbackState, which is only visible to a SECOND invocation once
  // React has actually re-rendered — so it also closes the window where both
  // clicks land in the same synchronous batch before any render happens.
  const retryInFlightRef = useRef(false)
  const [retryPending, setRetryPending] = useState(false)
  const [retryError, setRetryError] = useState<{
    message: string
    canRetry: boolean
    recovery?: { requestId?: string; sessionId?: string; frameAtFailure?: WhatsAppPairingState }
  } | null>(null)

  // A later frame can resolve an uncertain enable 500 only for the NEW Retry
  // intent: its explicit chat request/session, or no owner for Connectors.
  // Refusals (404/409) and frames predating the HTTP failure never recover this way.
  useEffect(() => {
    const recovery = retryError?.recovery
    if (recovery && pairing && pairing !== recovery.frameAtFailure
      && pairing.requestId === recovery.requestId && pairing.sessionId === recovery.sessionId) {
      setRetryError(null)
    }
  }, [pairing, retryError])

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

  // A QR or terminal status is usable only AFTER REST confirms admission and
  // enable acceptance. Errors and an already-linked device must end QR wait too.
  useEffect(() => {
    if (pairing && pairing.status !== 'waiting' && retryFallbackState !== null && !retryPending) {
      if (retryTimerRef.current !== null) {
        clearTimeout(retryTimerRef.current)
        retryTimerRef.current = null
      }
      setRetryFallbackState(null)
    }
  }, [pairing, retryFallbackState, retryPending])

  // #283 (Option B): tell the gateway this connection is viewing WhatsApp
  // pairing so the QR is delivered only here, not broadcast to every admin tab.
  // Shared with the requesting chat and ref-counted per channel: every viewer
  // re-subscribes on (re)connect, and the LAST viewer to close unsubscribes
  // and drops the QR secret from the store (#283).
  useWhatsAppPairingInterest(channelId, requestId, sessionId)

  // On unmount: cancel any pending timers.
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
    },
    [],
  )

  async function handleRetry() {
    // Re-entrancy guard: ignore a click while a retry is already in flight.
    // retryInFlightRef is set BEFORE any await below (synchronously, at the
    // very top), so it also blocks a second click that lands in the same
    // synchronous batch as the first — before React has re-rendered and
    // retryFallbackState (checked as a belated defense-in-depth) would
    // reflect the first click yet.
    if (retryInFlightRef.current || retryFallbackState !== null) return

    // A chat with incomplete identity must not silently become an ordinary,
    // unowned enable. Connectors (neither id supplied) keeps ordinary Retry.
    const isChatRetry = requestId !== undefined || sessionId !== undefined
    if (isChatRetry && (!requestId?.trim() || !sessionId?.trim())) {
      setRetryError({ message: PAIRING_RETRY_CONFLICT_MESSAGE, canRetry: false })
      return
    }
    const retryRequest: ChannelEnableRequest | undefined = requestId && sessionId
      ? { pairing_retry: { request_id: requestId, session_id: sessionId } }
      : undefined
    retryInFlightRef.current = true
    setRetryPending(true)
    setRetryError(null)

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
    let disableSucceeded = false
    try {
      await disableChannel(channelId)
      disableSucceeded = true

      // The viewer is already subscribed. REST admits the exact original chat
      // claim before starting; no WebSocket toggle or bodyless fallback.
      if (retryRequest) {
        await enableChannel(channelId, retryRequest)
      } else {
        await enableChannel(channelId)
      }
    } catch (err) {
      // Surface the failed step immediately instead of spinning for the full
      // retry window. Preserve the confirmed disable separately from an enable
      // refusal or a response/config failure with an unknown current state.
      logError({
        event: 'whatsappRetryRestartFailed',
        channelId,
        step: disableSucceeded ? 'enable' : 'disable',
        message: err instanceof Error ? err.message : String(err),
      })
      const deleted = isApiError(err) && err.status === 404
      const conflict = isApiError(err) && err.status === 409 && err.code === 'pairing_retry_conflict'
      const inProgress = isApiError(err) && err.status === 409 && err.code === 'pairing_in_progress'
      const enableRefused = isApiError(err) && ((err.status >= 400 && err.status < 500) || err.status === 503)
      const reason = conflict
        ? parseServerErrorField(err.body) ?? PAIRING_RETRY_CONFLICT_MESSAGE
        : getWhatsAppEnableErrorMessage(isApiError(err) ? err : undefined, 'Could not restart pairing. Open the WhatsApp connector and try again.', {
          statusOverrides: {
            0: 'Network unavailable. Check your connection and try again.',
            503: 'Pairing ownership service unavailable. Try again.',
          },
        })
      // Cancellation explicitly confirms enabled config; an ordinary competing
      // pairing leaves config unchanged. Neither may inherit a disabled claim.
      const enableCancelled = disableSucceeded && isApiError(err) && err.status === 500
        && reason === WHATSAPP_ENABLE_CANCELLED_MESSAGE
      const message = deleted
        ? WHATSAPP_PAIRING_DELETED_MESSAGE
        : enableCancelled || inProgress
          ? reason
          : disableSucceeded
            ? enableRefused
              ? `The channel is now disabled. ${reason}`
              : `The channel was disabled before the enable request, but its current state could not be confirmed. ${reason}`
            : reason
      // Recovery follows the NEW enable intent. A bodyless Connectors Retry
      // starts unowned; never inherit the ended pairing's chat ownership.
      const originalRequest = retryRequest?.pairing_retry?.request_id
      const originalSession = retryRequest?.pairing_retry?.session_id
      useWhatsAppPairingStore.getState().apply({
        type: 'whatsapp_pairing',
        channel_id: channelId,
        status: 'error',
        message,
        request_id: originalRequest,
        session_id: originalSession,
      })
      setRetryError({
        message,
        canRetry: !conflict && !deleted,
        ...(disableSucceeded && isApiError(err) && err.status === 500 ? {
          recovery: {
            requestId: originalRequest,
            sessionId: originalSession,
            frameAtFailure: useWhatsAppPairingStore.getState().byChannel[channelId],
          },
        } : {}),
      })
      setRetryFallbackState(null)
      return
    } finally {
      retryInFlightRef.current = false
      setRetryPending(false)
    }

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
    <div className="space-y-[var(--space-2)] mt-[var(--space-1)]">
      <Card
        className="flex flex-col items-center gap-[var(--space-2-5)] p-[var(--space-3)]"
        aria-live="polite"
      >
        {retryError ? (
          <ErrorState message={retryError.message} onRetry={retryError.canRetry ? handleRetry : undefined} />
        ) : (
          <WhatsAppPairingBody pairing={effectivePairing} onRetry={handleRetry} />
        )}
      </Card>
      <div className="flex gap-[var(--space-2)] p-[var(--space-2-5)] rounded-md bg-[var(--color-surface-2)] border border-[var(--color-error)]/30">
        <Warning size={14} className="text-[var(--color-error)] shrink-0 mt-[var(--space-0-5)]" weight="fill" />
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)]">
          WhatsApp native mode stores sessions locally. The gateway must keep running for the session
          to stay active.
        </p>
      </div>
    </div>
  )
}

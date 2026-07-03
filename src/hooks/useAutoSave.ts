import { useCallback, useEffect, useRef, useState } from 'react'
import { isApiError } from '@/lib/api'
import { isReAuthCancelled } from '@/components/settings/useReAuthGate'

export type AutoSaveStatus = 'idle' | 'saving' | 'saved' | 'error'

interface UseAutoSaveOptions {
  /** Debounce delay in ms. Default: 500 */
  debounceMs?: number
  /** If true, auto-save is disabled (e.g., for locked agents) */
  disabled?: boolean
  /**
   * Optional endpoint to receive a best-effort flush of the pending data
   * when the page is hidden or unloaded. Uses `fetch(..., { keepalive: true })`
   * (preferred over `navigator.sendBeacon` because keepalive fetch can carry
   * an `Authorization` header, which sendBeacon cannot). If not provided, no
   * flush is attempted.
   */
  flushUrl?: string
  /**
   * Optional bearer token sent as `Authorization: Bearer <token>` on the
   * keepalive flush request. Required when the flush endpoint requires auth
   * (the Omnipus gateway validates every state-changing call against the
   * account's bearer token); without it the flush will 401 and silently drop.
   */
  flushAuthToken?: string
  /**
   * Optional component-supplied override for the page-hide/unload flush.
   * When provided, `flushBeacon` calls THIS instead of the built-in
   * single-URL `fetch(flushUrl, { keepalive: true, ... })`. `flushUrl` /
   * `flushAuthToken` are ignored when this is supplied.
   *
   * Needed when a component's `saveFn` writes to more than one endpoint in a
   * load-bearing order (e.g. WorkspaceTeamTab's core_team-before-edges
   * ordering — the delegation PUT validates edge endpoints against the
   * STORED core_team, so a member added and connected in the same edit
   * session must land in core_team first or the edges PUT 400s). The
   * built-in single-`flushUrl` flush only knows how to PUT one endpoint, so
   * it can't preserve that ordering during an emergency flush — it would
   * PUT the edges endpoint alone, reintroducing the exact bug the ordering
   * fix in `saveFn` was meant to close. The caller is responsible for its
   * own keepalive fetch(es) and any error logging; this hook only decides
   * WHEN to invoke it (hidden / beforeunload / pagehide, and only when
   * `hasPendingChanges()` is true).
   */
  beaconFlush?: () => void
}

interface UseAutoSaveResult {
  status: AutoSaveStatus
  error: string | undefined
  /** Timestamp of the last successful save, or undefined if none yet. */
  lastSavedAt: Date | undefined
  /** Call this to trigger an immediate save (no debounce) */
  saveNow: () => void
}

/**
 * useAutoSave — debounced auto-save hook.
 *
 * Watches `data` for changes (deep compare via JSON.stringify).
 * After the debounce period, calls `saveFn` with the current data.
 * Skips the initial render (loading data is not a change).
 *
 * Usage:
 *   const { status } = useAutoSave(formData, (data) => updateAgent(id, data))
 */
export function useAutoSave<T>(
  data: T,
  saveFn: (data: T) => Promise<unknown>,
  options?: UseAutoSaveOptions,
): UseAutoSaveResult {
  const { debounceMs = 500, disabled = false, flushUrl, flushAuthToken, beaconFlush } = options ?? {}
  const [status, setStatus] = useState<AutoSaveStatus>('idle')
  const [error, setError] = useState<string>()
  const [lastSavedAt, setLastSavedAt] = useState<Date | undefined>(undefined)

  // Track whether initial hydration has happened.
  const initializedRef = useRef(false)
  // The JSON last SEEN by the change effect — used only to debounce-dedupe
  // (don't re-fire a save for identical data). Advanced eagerly when data
  // changes; NOT a record of what's been persisted.
  const previousJsonRef = useRef<string>('')
  // The JSON of the last data that SUCCESSFULLY persisted. `hasPendingChanges()`
  // compares against THIS (not `previousJsonRef`) so a failed PUT keeps the data
  // "pending" — the unmount flush + page-hide beacon still fire and retry it.
  // Without this, a thrown save would still mark the edit as saved and the
  // user's delegation-edge changes would silently vanish on reload.
  //
  // FIX 2 — stale-resolve guard: only advance this ref when the just-resolved
  // save is the most recently FIRED one. Without this guard, a concurrent save
  // A that fires before B but resolves after B writes json_A (stale) into
  // lastSavedJsonRef while latestDataRef already holds json_B. That leaves
  // hasPendingChanges()=true and an extra (correct-valued) PUT fires on unmount.
  // Tracking the fired-sequence counter and comparing at resolution time prevents
  // the stale write. Latest-wins correctness is preserved: save B always resolves
  // its own json_B, and A's late resolution is simply ignored.
  const lastSavedJsonRef = useRef<string>('')
  // Monotonically increasing counter: incremented each time a save is FIRED.
  // Each save closure captures its own sequence number and only advances
  // lastSavedJsonRef when its sequence equals the latest fired sequence.
  const firedSeqRef = useRef<number>(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latestDataRef = useRef<T>(data)
  const saveFnRef = useRef(saveFn)
  saveFnRef.current = saveFn
  latestDataRef.current = data

  const hasPendingChanges = useCallback(() => {
    return JSON.stringify(latestDataRef.current) !== lastSavedJsonRef.current
  }, [])

  const doSave = useCallback(async () => {
    if (disabled) return
    setStatus('saving')
    setError(undefined)
    // Snapshot what we're about to persist so success marks exactly that JSON
    // saved (data may change again while the request is in flight).
    const inFlightJson = JSON.stringify(latestDataRef.current)
    // FIX 2: capture the sequence number for this particular fire so we can
    // guard against a stale-resolving concurrent save clobbering lastSavedJsonRef.
    firedSeqRef.current += 1
    const thisSeq = firedSeqRef.current
    try {
      await saveFnRef.current(latestDataRef.current)
      // Only NOW is the data durable — advance the saved marker so
      // hasPendingChanges() flips false for this exact payload.
      // FIX 2: only advance if no newer save has been fired since this one;
      // a later-fired save that already resolved would have set a higher seq,
      // and we must not regress lastSavedJsonRef to our (older) payload.
      if (thisSeq === firedSeqRef.current) {
        lastSavedJsonRef.current = inFlightJson
      }
      setStatus('saved')
      setLastSavedAt(new Date())
      // Fade back to idle after 2s. Cancel any previous fade timer first to
      // avoid leaking setTimeouts when saves happen in quick succession.
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      fadeTimerRef.current = setTimeout(() => {
        setStatus((s) => (s === 'saved' ? 'idle' : s))
        fadeTimerRef.current = null
      }, 2000)
    } catch (err) {
      // FIX 1: a user-initiated re-auth dialog dismissal is not an error —
      // treat it as a no-op and restore idle status so the indicator does not
      // stay red until the next edit. Genuine save failures still land in error.
      if (isReAuthCancelled(err)) {
        setStatus('idle')
        setError(undefined)
        return
      }
      setStatus('error')
      setError(isApiError(err) ? err.userMessage : err instanceof Error ? err.message : String(err))
    }
  }, [disabled])

  useEffect(() => {
    if (disabled) return

    const json = JSON.stringify(data)

    // Skip first render (initial load) — the loaded data is the persisted
    // baseline, so it is both "last seen" and "last saved".
    if (!initializedRef.current) {
      initializedRef.current = true
      previousJsonRef.current = json
      lastSavedJsonRef.current = json
      return
    }

    // Skip if data hasn't changed since the last change we observed.
    if (json === previousJsonRef.current) return
    previousJsonRef.current = json

    // Clear previous debounce timer.
    if (timerRef.current) clearTimeout(timerRef.current)

    // Set new debounce timer.
    timerRef.current = setTimeout(doSave, debounceMs)

    return () => {
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [data, debounceMs, disabled, doSave])

  // Best-effort flush of pending changes when the page is hidden or unloaded.
  // Uses `fetch(..., { keepalive: true })` rather than `navigator.sendBeacon`
  // because keepalive fetch can carry an `Authorization: Bearer` header —
  // the Omnipus gateway validates every state-changing call against the
  // account's bearer token, and sendBeacon cannot set request headers, so a
  // sendBeacon flush would 401 and silently drop the pending edit. This
  // prevents silently losing edits on tab close, browser reload, or
  // background throttling.
  //
  // When `beaconFlush` is supplied it takes over entirely (see the option's
  // doc comment) — the built-in single-URL fetch below is the default path
  // used by the ~6 other callers that only ever write one endpoint.
  const flushBeacon = useCallback(() => {
    if ((!flushUrl && !beaconFlush) || !initializedRef.current || !hasPendingChanges()) return
    if (beaconFlush) {
      try {
        beaconFlush()
      } catch {
        // Best-effort — a synchronously-throwing caller-supplied flush must
        // not block the other unload listeners (beforeunload/pagehide) from
        // running.
      }
      return
    }
    const payload = JSON.stringify(latestDataRef.current)
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (flushAuthToken) headers['Authorization'] = `Bearer ${flushAuthToken}`
    try {
      void fetch(flushUrl!, {
        method: 'PUT',
        keepalive: true,
        headers,
        body: payload,
      })
    } catch {
      // Best-effort — browser may block outbound requests during unload.
    }
  }, [flushUrl, flushAuthToken, hasPendingChanges, beaconFlush])

  useEffect(() => {
    if ((!flushUrl && !beaconFlush) || disabled) return

    const onVisibilityChange = () => {
      if (document.hidden) flushBeacon()
    }
    const onBeforeUnload = () => flushBeacon()
    const onPageHide = () => flushBeacon()

    document.addEventListener('visibilitychange', onVisibilityChange)
    window.addEventListener('beforeunload', onBeforeUnload)
    window.addEventListener('pagehide', onPageHide)

    return () => {
      document.removeEventListener('visibilitychange', onVisibilityChange)
      window.removeEventListener('beforeunload', onBeforeUnload)
      window.removeEventListener('pagehide', onPageHide)
    }
  }, [flushUrl, flushAuthToken, disabled, flushBeacon, beaconFlush])

  // Cleanup on unmount: cancel timers and flush any pending save so changes
  // made just before navigation/unmount are not silently dropped.
  useEffect(() => {
    return () => {
      // Clear debounce timer
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      // Clear "fade to idle" timer
      if (fadeTimerRef.current) {
        clearTimeout(fadeTimerRef.current)
        fadeTimerRef.current = null
      }
      // Flush pending save (fire-and-forget — component is unmounting)
      if (initializedRef.current) {
        if (hasPendingChanges()) {
          saveFnRef.current(latestDataRef.current).catch((err) => {
            console.error('[useAutoSave] unmount flush save failed:', err)
          })
        }
      }
    }
  }, [hasPendingChanges])

  return { status, error, lastSavedAt, saveNow: doSave }
}

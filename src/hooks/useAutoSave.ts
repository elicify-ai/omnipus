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
  const lastSavedJsonRef = useRef<string>('')
  // FIX 3 — serialize saves, never let two be in flight at once. Without
  // this, two debounce cycles firing more than `debounceMs` apart — but with
  // the FIRST save's async work (network latency, or a pre-save step like an
  // executor runner-test) still outstanding — dispatch TWO overlapping PUT
  // requests for the same full-resource replacement. Whichever response the
  // SERVER processes/returns last wins the write, even when it carries an
  // OLDER form-state snapshot than the other in-flight request: a classic
  // last-write-wins clobber (root cause of the agent fallback_models UAT
  // data-loss bug — a debounced save fired from stale state arrived after a
  // fresher save and silently wiped its fallback_models).
  // An earlier stale-resolve guard (a fired-sequence counter compared at
  // resolution time) only protected this hook's OWN bookkeeping from a
  // stale-resolving concurrent save; it never stopped the second network
  // request from going out in the first place, and is redundant now that
  // serialization guarantees at most one save is ever in flight — it has
  // been removed rather than kept as unreachable defensive code. Serializing
  // — never fire while one is outstanding, always re-run against the LATEST
  // data once the outstanding one settles — makes the final persisted state
  // deterministic regardless of network ordering. This is strictly safer
  // than aborting the in-flight fetch: once a PUT has been transmitted,
  // cancelling the client's wait for the response does not guarantee the
  // server discards the write.
  const isSavingRef = useRef(false)
  const rerunPendingRef = useRef(false)
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
    // FIX 3: a save is already in flight — do not fire a second, concurrent
    // request. Queue a re-run for when the in-flight one settles; that
    // re-run reads `latestDataRef.current` at THAT time, so it always picks
    // up the freshest edits rather than whatever snapshot existed when this
    // call was attempted.
    if (isSavingRef.current) {
      rerunPendingRef.current = true
      return
    }
    isSavingRef.current = true
    setStatus('saving')
    setError(undefined)
    // Snapshot what we're about to persist so success marks exactly that JSON
    // saved (data may change again while the request is in flight).
    const inFlightJson = JSON.stringify(latestDataRef.current)
    try {
      await saveFnRef.current(latestDataRef.current)
      // Only NOW is the data durable — advance the saved marker so
      // hasPendingChanges() flips false for this exact payload.
      lastSavedJsonRef.current = inFlightJson
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
    } finally {
      // FIX 3: release the guard, then honor any re-run queued while this
      // save was outstanding — but only if the data actually still differs
      // from what just (successfully or not) became the persisted marker,
      // so a queued re-run that this save's own outcome already covers
      // doesn't fire a redundant no-op PUT.
      isSavingRef.current = false
      if (rerunPendingRef.current) {
        rerunPendingRef.current = false
        if (JSON.stringify(latestDataRef.current) !== lastSavedJsonRef.current) {
          void doSave()
        }
      }
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
      // Flush pending save (fire-and-forget — component is unmounting).
      // Routed through `doSave()` itself — the same function the debounce
      // timer and `saveNow` fire through — rather than calling
      // `saveFnRef.current` directly, so all three save-firing sites
      // visibly share one serialization guard instead of this call site
      // quietly depending on an invariant that lives elsewhere. `doSave()`
      // re-checks `isSavingRef` on entry (a cheap, synchronous check before
      // its own first `await`), so calling it here is safe.
      if (initializedRef.current) {
        // FIX 3: if a save is already in flight, do NOT fire a second,
        // concurrent flush PUT straight past the serialize guard in
        // `doSave` — that would reintroduce the exact overlapping-request
        // race this hook exists to prevent. Queue a re-run instead; the
        // in-flight save's own `finally` block re-checks the latest data
        // and re-fires if anything changed. Refs/closures outlive the
        // unmount, so this still runs to completion.
        if (isSavingRef.current) {
          rerunPendingRef.current = true
        } else if (hasPendingChanges()) {
          void doSave()
        }
      }
    }
  }, [hasPendingChanges, doSave])

  return { status, error, lastSavedAt, saveNow: doSave }
}

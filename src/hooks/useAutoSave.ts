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

  // Clear the "saved → idle" fade timer on unmount. The data-change effect
  // above only clears the debounce timer (timerRef); a fade timer armed by a
  // save within 2s of unmount would otherwise fire setStatus AFTER the
  // component is gone. In the app that is a harmless no-op; in jsdom the
  // callback runs after environment teardown, so setStatus dereferences a
  // torn-down `window` and throws an UNHANDLED "ReferenceError: window is not
  // defined" that fails the whole vitest run (a flaky false-positive that CI
  // caught). Mount-once so it runs only on final unmount.
  useEffect(() => {
    return () => {
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

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
    // FIX 5 (7-reviewer-gate finding, supersedes FIX 4 below): FIX 4 gated
    // this beacon fetch behind isSavingRef to close an overlapping-PUT race
    // — correct in isolation, but it traded that race for a worse
    // regression. flushBeacon only ever runs from
    // visibilitychange/beforeunload/pagehide — exactly the moment a real
    // tab-close might happen — and gating the keepalive fetch behind
    // isSavingRef meant a save-in-flight-at-unload got NO keepalive fetch at
    // all: the newest edit was queued through doSave()'s own `finally`
    // block instead, which fires a non-keepalive fetch once the in-flight
    // save settles — but a non-keepalive fetch has no guarantee of
    // completing after the page has genuinely unloaded. Net effect: closing
    // the tab within ~500ms of an edit (while a debounced save happens to be
    // in flight) had a plausible path to silently drop the newest edit
    // entirely — the exact class of data-loss bug this whole track exists
    // to fix, just moved to a narrower trigger condition.
    //
    // Fix: fire the keepalive fetch (or the caller-supplied `beaconFlush`,
    // which carries the same keepalive-on-unload contract — see its option
    // doc comment) with the LATEST data regardless of whether a save is
    // already in flight. Accepting a possible duplicate/overlapping
    // keepalive write is strictly safer for data durability on genuine
    // unload than guaranteeing none goes out (the backend's optimistic-
    // concurrency handling, plus the `updated_at`-precision fix landing
    // alongside this one, makes an overlapping pair of writes recoverable;
    // a guaranteed-lost edit on tab-close is not). Still queue a re-run
    // through doSave() too — belt-and-braces for the visibilitychange case,
    // where the tab may just be backgrounded rather than genuinely closing,
    // so a normal serialized save (with response handling / status update /
    // `lastSavedJsonRef` advancement, none of which this raw fetch does)
    // still happens once the in-flight one settles. This is a DELIBERATE
    // divergence from the unmount-cleanup site's guard below (which still
    // skips firing when a save is in flight) — see that comment for the
    // full 4-site invariant and keep the two in sync on any future change.
    if (isSavingRef.current) {
      rerunPendingRef.current = true
    }
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
      // timer and `saveNow` fire through — so THIS call site shares
      // `doSave()`'s serialization guard rather than quietly depending on an
      // invariant that lives elsewhere. `doSave()` re-checks `isSavingRef`
      // on entry (a cheap, synchronous check before its own first `await`),
      // so calling it here is safe.
      //
      // Note this hook has a 4th save-firing site — `flushBeacon` (above) —
      // that does NOT route through `doSave()`: it needs `keepalive: true`
      // (or a caller-supplied `beaconFlush`), which `doSave()`'s fetch does
      // not use, so it duplicate-checks `isSavingRef`/`rerunPendingRef` by
      // hand instead. Per FIX 5 (see `flushBeacon`'s own comment),
      // `flushBeacon` deliberately does NOT skip firing when a save is
      // already in flight — unlike this unmount site and the debounce-timer/
      // `saveNow` sites, which do skip (queueing a re-run) to avoid an
      // overlapping PUT. A future change to the guard invariant at ANY of
      // these four sites must be checked against the other three —
      // `flushBeacon`'s comment cross-references this one; keep them in
      // sync.
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

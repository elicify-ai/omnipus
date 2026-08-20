import { useCallback, useEffect, useRef, useState } from 'react'
import { isApiError, getCsrfCookie, CSRF_HEADER_NAME } from '@/lib/api'
import { isReAuthCancelled } from '@/components/settings/useReAuthGate'

// Draft-ownership rule (canon): a dirty field is never hydrated from the
// server; a caller's dirty flag clears ONLY when the save snapshot still
// equals the live draft. See `UseAutoSaveOptions.onSaved`'s doc comment
// below for the full rule and the callback contract that enforces it — every
// consumer's own onSaved comment should point back here rather than
// re-narrating it.

export type AutoSaveStatus = 'idle' | 'saving' | 'saved' | 'error'

interface UseAutoSaveOptions<T> {
  /** Debounce delay in ms. Default: 500 */
  debounceMs?: number
  /** If true, auto-save is disabled (e.g., for locked agents) */
  disabled?: boolean
  /**
   * Optional endpoint to receive a best-effort flush of the pending data
   * when the page is hidden or unloaded. Uses `fetch(..., { keepalive: true })`
   * (preferred over `navigator.sendBeacon` because keepalive fetch can carry
   * the CSRF double-submit header the PUT requires, which sendBeacon cannot
   * set). Auth itself rides the `omnipus-session` HttpOnly cookie via
   * `credentials: 'include'` — there is no bearer token to attach. If not
   * provided, no flush is attempted.
   */
  flushUrl?: string
  /**
   * Optional component-supplied override for the page-hide/unload flush.
   * When provided, `flushBeacon` calls THIS instead of the built-in
   * single-URL `fetch(flushUrl, { keepalive: true, ... })`. `flushUrl` is
   * ignored when this is supplied.
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
  /**
   * Called once per successful save, after this hook's own bookkeeping
   * (`lastSavedJsonRef`, `status`, `lastSavedAt`) has already been updated.
   * `saved` is the exact payload that was just persisted (the `data` this
   * save actually sent, not necessarily today's live `data`); `isCurrent`
   * is true only when the live `data` — read now, AFTER the save's network
   * round-trip completed, not when it started — is still deep-equal to
   * `saved`.
   *
   * DRAFT-OWNERSHIP RULE this callback exists to let callers enforce: a
   * dirty field is never hydrated from the server, and dirty clears ONLY
   * when the save snapshot equals the live draft. A caller that hand-rolls
   * an `isDirtyRef` (cleared elsewhere to unblock a hydration-guarded
   * effect) must clear it here — `if (isCurrent) isDirtyRef.current =
   * false` — and NOT unconditionally on every successful save. If the
   * operator edited `data` again while this save's request was still in
   * flight, `isCurrent` is false: the dirty flag must stay armed, because
   * this hook's own serialization (FIX 3 below) has already queued a
   * re-run that will persist the newer draft the moment this save's
   * `finally` releases the guard. Clearing dirty unconditionally is exactly
   * the bug this callback exists to prevent — it lets a same-tick (or
   * refetch-driven) hydration effect revert keystrokes the operator typed
   * during the round-trip.
   *
   * Not called on a failed save, and not called when the save was a no-op
   * because the user cancelled a re-auth gate (see FIX 1 below) — there is
   * nothing "saved" to report in either case.
   */
  onSaved?: (saved: T, isCurrent: boolean) => void
  /**
   * Finding A (7-reviewer-gate, fix/uat-v0.1.1-defects): called ONCE, at
   * the exact moment `disabled` transitions false→true, if `data` at that
   * instant genuinely differs from the last successfully-persisted
   * snapshot AND no save is currently in flight for it. This is the one
   * safe window to detect a soon-to-be-discarded edit: the debounce timer
   * for it is about to be cleared (this same effect's cleanup, triggered by
   * the `disabled` dep changing), and re-enabling later re-seeds the
   * baseline from whatever `data` is at THAT point (see `wasDisabledRef`
   * below) — so unless something acts on this callback, the edit vanishes
   * with no error, no toast, no log.
   *
   * Deliberately NOT an auto-flush trigger. Calling `saveNow()` from here
   * would use `data` that may already be a transitional, cross-entity
   * mismatch by the time a caller's OWN reset effects have run (a plain
   * `disabled: !someReadinessFlag` consumer can legitimately have `data`
   * partially reflect a newly-swapped identity before its own hydration
   * effect corrects it) — flushing there is "exactly the cross-entity
   * clobber this wave fixed" per this file's own `wasDisabledRef` comment.
   * Callers that want the edit preserved must capture what they need
   * (typically just an id/name for a user-facing message) and present it —
   * e.g. a toast telling the operator to redo the change — never a
   * best-effort re-PUT of a payload this hook can no longer vouch for.
   *
   * Guarded internally against firing on a hook's initial mount-disabled
   * state (`initializedRef` never having seeded a real baseline yet — see
   * the D3 test "a hook that STARTS disabled... captures its first enable
   * as the baseline, not a save") and against a save that is already
   * legitimately in flight for this exact disable transition (the normal
   * "flush then close" pattern, e.g. `AgentProfile`'s
   * `handleCloseAgentSheet` — its `saveNow()` call sets `isSavingRef`
   * synchronously, before the store write that flips `disabled` true lands
   * in a later render, so this callback correctly stays silent for it).
   */
  onDisabledWithPendingChanges?: (data: T) => void
}

interface UseAutoSaveResult {
  status: AutoSaveStatus
  error: string | undefined
  /** Timestamp of the last successful save, or undefined if none yet. */
  lastSavedAt: Date | undefined
  /** Call this to trigger an immediate save (no debounce) */
  saveNow: () => void
  /**
   * True when the live `data` has not yet been durably persisted (deep
   * JSON-compared against the last successfully-saved snapshot). Exposed so
   * a caller can flush deliberately at a moment the hook itself has no
   * hook into — e.g. a sheet/modal `onClose` that does NOT unmount the
   * component (so the hook's own unmount-flush never fires): check this,
   * and if true call `saveNow()` BEFORE tearing down whatever state the
   * save's closure depends on (see AgentProfile's sheet-close handler for
   * the canonical example and the ordering hazard it documents).
   */
  hasPendingChanges: () => boolean
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
  options?: UseAutoSaveOptions<T>,
): UseAutoSaveResult {
  // Merge note (hotfix/v0.1.1): flushAuthToken was retired hook-wide — the
  // keepalive flush now carries the CSRF double-submit header instead of a
  // bearer token. onSaved is this lineage's draft-ownership callback.
  const { debounceMs = 500, disabled = false, flushUrl, beaconFlush, onSaved, onDisabledWithPendingChanges } = options ?? {}
  const [status, setStatus] = useState<AutoSaveStatus>('idle')
  const [error, setError] = useState<string>()
  const [lastSavedAt, setLastSavedAt] = useState<Date | undefined>(undefined)

  // Track whether initial hydration has happened.
  const initializedRef = useRef(false)
  // D3 (2nd-entity spurious-PUT) fix: tracks whether `disabled` was true the
  // last time the change-detection effect below actually ran. `initializedRef`
  // alone only guards the VERY FIRST commit a hook instance ever sees — it is
  // set true permanently and never reset, so a long-lived hook instance that
  // gets disabled, re-hydrated with a DIFFERENT entity's data, then
  // re-enabled (e.g. AgentProfile switching from agent A to agent B without
  // unmounting) skips the seed branch on the second hydration and instead
  // diffs B's freshly-hydrated data against A's stale previousJsonRef/
  // lastSavedJsonRef baseline — indistinguishable from a genuine user edit,
  // arming the debounce and firing a spurious PUT that just echoes B's own
  // data back. Seeded from the CURRENT `disabled` value (not a hardcoded
  // `false`) so a hook instance that starts out disabled (the common
  // `disabled: !hydrated` pattern) correctly treats its first enable as a
  // re-arm event too, not just a plain "first ever" seed — both are the same
  // case below (`!initializedRef.current || wasDisabledRef.current`).
  const wasDisabledRef = useRef(disabled)
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
  // Mounted-flag for late-firing fade timers: the useEffect cleanup that
  // clears fadeTimerRef runs synchronously on unmount, but a real (not fake)
  // timer that already started dispatching its callback at unmount time is
  // not cancellable — clearTimeout only stops a still-pending timer. In
  // production the setStatus call is harmless; in jsdom it dereferences a
  // torn-down `window` and throws an unhandled ReferenceError that fails
  // the whole vitest run. The guard makes the late callback a no-op.
  // Initial value intentionally false — the effect below sets it true on
  // mount. Initializing during render (`useRef(true)`) broke under React
  // StrictMode, where effects run setup→cleanup→setup on initial mount in
  // dev, leaving `mountedRef.current` permanently false in development.
  const mountedRef = useRef(false)
  const latestDataRef = useRef<T>(data)
  const saveFnRef = useRef(saveFn)
  saveFnRef.current = saveFn
  latestDataRef.current = data
  // Always-fresh ref for the optional `onSaved` callback — same pattern as
  // `saveFnRef` above, so a caller's inline arrow function doesn't need to
  // be a stable identity across renders.
  const onSavedRef = useRef(onSaved)
  onSavedRef.current = onSaved
  // Finding A: same always-fresh-ref pattern for the new disable-transition
  // callback (see its own doc comment on `UseAutoSaveOptions` above).
  const onDisabledWithPendingChangesRef = useRef(onDisabledWithPendingChanges)
  onDisabledWithPendingChangesRef.current = onDisabledWithPendingChanges

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
    const inFlightData = latestDataRef.current
    const inFlightJson = JSON.stringify(inFlightData)
    // Hook polish (7-reviewer-gate finding): tracks whether THIS save's own
    // `saveFnRef` call actually succeeded, so the `onSaved` callback below —
    // now invoked OUTSIDE this try/catch — knows whether it's allowed to
    // fire without re-deriving that from `status` (which a concurrent
    // queued re-run could have already moved past 'saved' by the time we
    // get there).
    let savedSuccessfully = false
    try {
      await saveFnRef.current(inFlightData)
      // Only NOW is the data durable — advance the saved marker so
      // hasPendingChanges() flips false for this exact payload.
      lastSavedJsonRef.current = inFlightJson
      setStatus('saved')
      setLastSavedAt(new Date())
      savedSuccessfully = true
      // Fade back to idle after 2s. Cancel any previous fade timer first to
      // avoid leaking setTimeouts when saves happen in quick succession.
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      fadeTimerRef.current = setTimeout(() => {
        if (!mountedRef.current) return
        setStatus((s) => (s === 'saved' ? 'idle' : s))
        fadeTimerRef.current = null
        // The host environment can disappear while this 2s timer is still
        // armed. The unmount cleanup below only fires if an unmount actually
        // happens — a jsdom test file that ends with the component still
        // mounted tears the environment down without one, and then setStatus
        // dereferences a destroyed `window` inside React's scheduler and
        // throws an UNHANDLED "ReferenceError: window is not defined". That
        // fails the entire vitest run even when every test passed, which is
        // exactly what CI hit. Bail if the host is gone.
        if (typeof window === 'undefined') return
        setStatus((s) => (s === 'saved' ? 'idle' : s))
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
    // Hook polish (7-reviewer-gate finding): `onSaved` is invoked OUTSIDE
    // the try/catch above, in its own try/catch that only logs. A THROWING
    // caller callback must never flip a save that already genuinely
    // succeeded (the PUT landed, `lastSavedJsonRef` already advanced) into
    // 'error' status — that would be a lie to the user (the AutoSaveIndicator
    // would show a failure for a save that, in fact, persisted). `return`
    // inside the `catch` block above (the re-auth-cancel path) still skips
    // this entirely, since code after a try/catch/finally is unreachable
    // once a `return` inside try/catch has fired; the plain error path falls
    // through here with `savedSuccessfully` still false, so onSaved is
    // correctly skipped for both failure modes.
    if (savedSuccessfully) {
      try {
        // Draft-ownership rule (see `onSaved`'s doc comment above): report
        // whether the LIVE draft — read now, after the round-trip — is
        // still the exact payload we just persisted. Comparing
        // `latestDataRef.current` (not `inFlightData`) here is the whole
        // point: if the operator typed more while this save was in flight,
        // they differ and `isCurrent` is false, so a caller-owned dirty
        // flag must stay armed.
        onSavedRef.current?.(inFlightData, JSON.stringify(latestDataRef.current) === inFlightJson)
      } catch (err) {
        console.error('useAutoSave: onSaved callback threw', err)
      }
    }
  }, [disabled])

  useEffect(() => {
    if (disabled) {
      // Finding A (7-reviewer-gate, fix/uat-v0.1.1-defects): on the
      // TRANSITION into disabled (not on every commit while already
      // disabled — `!wasDisabledRef.current` below is only true the first
      // time), check whether `data` right now genuinely differs from the
      // last successfully-persisted snapshot. If it does, the debounce
      // timer that would have saved it (armed by the PREVIOUS instance of
      // this same effect, while enabled) is about to be cancelled by that
      // instance's own cleanup — and the re-arm branch further down will
      // later reseed the baseline from whatever `data` is AT RE-ENABLE
      // TIME, making `hasPendingChanges()` read `false` with nothing ever
      // having reached the server. See `UseAutoSaveOptions.onDisabledWithPendingChanges`'s
      // doc comment for why this only ever REPORTS the loss (never
      // attempts an automatic flush).
      //
      // Two guards against false positives:
      //   - `initializedRef.current` — a hook that STARTS disabled (the
      //     common `disabled: !someReadinessFlag` shape) has never seeded a
      //     real baseline yet (`lastSavedJsonRef` is still its initial
      //     `''`), so comparing the hardcoded-default `data` against `''`
      //     would trivially "differ" with nothing real to lose.
      //   - `!isSavingRef.current` — a save already in flight for this
      //     exact transition (the normal "flush then tear down" pattern,
      //     e.g. `AgentProfile.handleCloseAgentSheet` calling `saveNow()`
      //     synchronously before the store write that will flip `disabled`
      //     true on the next render) is being handled, not lost — reporting
      //     it here would be a false alarm on the CORRECT path.
      if (!wasDisabledRef.current && initializedRef.current && !isSavingRef.current) {
        const jsonAtDisable = JSON.stringify(data)
        if (jsonAtDisable !== lastSavedJsonRef.current) {
          onDisabledWithPendingChangesRef.current?.(data)
        }
      }
      // Remember that we were disabled so the NEXT commit where `disabled`
      // is false — whenever that happens — knows a re-arm is owed, however
      // many renders (or however much `data` drifted underneath) happened
      // while disabled. See `wasDisabledRef`'s own doc comment above.
      wasDisabledRef.current = true
      return
    }

    const json = JSON.stringify(data)

    // Re-arm the baseline on: (a) the very first commit this hook instance
    // ever sees (`initializedRef` still false — the original "skip first
    // render" case), or (b) any LATER commit where `disabled` just
    // transitioned true→false (`wasDisabledRef` still true from the branch
    // above). Both mean the same thing: `data` right now is a freshly
    // hydrated, authoritative snapshot — not a user edit relative to
    // whatever baseline an EARLIER (possibly different) entity left behind —
    // so it becomes the new "last seen" / "last saved" baseline rather than
    // being diffed against a stale one.
    if (!initializedRef.current || wasDisabledRef.current) {
      initializedRef.current = true
      wasDisabledRef.current = false
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
    // Own both sides of the lifecycle so StrictMode's setup→cleanup→setup
    // dance doesn't leave `mountedRef.current` permanently false after the
    // initial-mount cleanup. Setting true here (not at render) means the
    // post-cleanup re-setup restores it correctly in development.
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      if (fadeTimerRef.current) clearTimeout(fadeTimerRef.current)
      if (timerRef.current) clearTimeout(timerRef.current)
    }
  }, [])

  // Best-effort flush of pending changes when the page is hidden or unloaded.
  // Uses `fetch(..., { keepalive: true })` rather than `navigator.sendBeacon`
  // because keepalive fetch can carry the CSRF double-submit header the PUT
  // requires — sendBeacon cannot set request headers, so a sendBeacon flush
  // would fail CSRF validation and silently drop the pending edit. Auth
  // itself is the `omnipus-session` HttpOnly cookie, sent automatically via
  // `credentials: 'include'`. This prevents silently losing edits on tab
  // close, browser reload, or background throttling.
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
    // PUT is state-changing — auth is the omnipus-session cookie (US-5 /
    // FR-010), which requires the CSRF double-submit header alongside it.
    // Read fresh (never cache — see getCsrfCookie's own doc comment).
    const csrf = getCsrfCookie()
    if (csrf) headers[CSRF_HEADER_NAME] = csrf
    try {
      void fetch(flushUrl!, {
        method: 'PUT',
        keepalive: true,
        credentials: 'include',
        headers,
        body: payload,
      })
    } catch {
      // Best-effort — browser may block outbound requests during unload.
    }
  }, [flushUrl, hasPendingChanges, beaconFlush])

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
  }, [flushUrl, disabled, flushBeacon, beaconFlush])

  // D3 residual defect (found while verifying the `wasDisabledRef` re-arm
  // fix against a REAL 2nd-entity consumer, WorkspaceSettingsTab): this
  // effect's cleanup is the unmount-flush logic below. Before this fix, the
  // effect depended on `[hasPendingChanges, doSave]` directly — `doSave` is
  // a `useCallback` keyed on `[disabled]`, so its identity changes every
  // time `disabled` toggles. React runs an effect's CLEANUP whenever ANY dep
  // changes, not only on a genuine unmount (a changed dep means "tear down
  // the old effect instance, set up a new one" — cleanup-then-effect, even
  // though nothing is actually unmounting). So the unmount-flush cleanup
  // below fired on EVERY `disabled` toggle — including the exact moment a
  // consumer's readiness flag flips true→false→true across a 2nd entity —
  // and it fired the STALE (pre-toggle) `doSave` closure, which itself
  // checks a closed-over `disabled` from BEFORE the toggle, so it didn't
  // even bail on its own `if (disabled) return` guard. For a consumer whose
  // tracked `data` embeds an identity field that updates synchronously with
  // props (e.g. WorkspaceSettingsTab's `instructionsFormData.workspaceId`)
  // while the REST of `data` (e.g. `content`) still lags behind an async
  // re-hydration, `hasPendingChanges()` reads as true for that mismatched,
  // transitional snapshot — so this stale flush fired a real PUT carrying
  // the NEW entity's id with the OLD entity's stale content: not just an
  // echo, an actual cross-entity data clobber.
  //
  // Fixed the same way `saveFnRef`/`onSavedRef` already solve this
  // elsewhere in this hook: read the LATEST `doSave` through a ref updated
  // every render, and drop `doSave` from the dependency array so this
  // effect mounts once and its cleanup runs ONLY on a genuine unmount. By
  // the time a genuine unmount happens, `doSaveRef.current` is whatever the
  // most recent render's `doSave` was — correctly reflecting the CURRENT
  // `disabled` value, not a stale one from an earlier toggle.
  const doSaveRef = useRef(doSave)
  doSaveRef.current = doSave

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
          void doSaveRef.current()
        }
      }
    }
    // Mount-once (see `doSaveRef`'s doc comment above) — `hasPendingChanges`
    // is already a stable, `[]`-deps `useCallback` so listing it costs
    // nothing; `doSave` is deliberately NOT listed here — it's read fresh
    // via `doSaveRef` specifically so this effect's cleanup does not re-fire
    // on every `disabled` toggle.
     
  }, [hasPendingChanges])

  return { status, error, lastSavedAt, saveNow: doSave, hasPendingChanges }
}

import { useCallback, useEffect, useRef, useState } from 'react'
import { isApiError } from '@/lib/api'

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
   * per-user RBAC token); without it the flush will 401 and silently drop.
   */
  flushAuthToken?: string
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
  const { debounceMs = 500, disabled = false, flushUrl, flushAuthToken } = options ?? {}
  const [status, setStatus] = useState<AutoSaveStatus>('idle')
  const [error, setError] = useState<string>()
  const [lastSavedAt, setLastSavedAt] = useState<Date | undefined>(undefined)

  // Track whether initial hydration has happened.
  const initializedRef = useRef(false)
  const previousJsonRef = useRef<string>('')
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const fadeTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const latestDataRef = useRef<T>(data)
  const saveFnRef = useRef(saveFn)
  saveFnRef.current = saveFn
  latestDataRef.current = data

  const hasPendingChanges = useCallback(() => {
    return JSON.stringify(latestDataRef.current) !== previousJsonRef.current
  }, [])

  const doSave = useCallback(async () => {
    if (disabled) return
    setStatus('saving')
    setError(undefined)
    try {
      await saveFnRef.current(latestDataRef.current)
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
      setStatus('error')
      setError(isApiError(err) ? err.userMessage : err instanceof Error ? err.message : String(err))
    }
  }, [disabled])

  useEffect(() => {
    if (disabled) return

    const json = JSON.stringify(data)

    // Skip first render (initial load).
    if (!initializedRef.current) {
      initializedRef.current = true
      previousJsonRef.current = json
      return
    }

    // Skip if data hasn't changed.
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
  // per-user RBAC token, and sendBeacon cannot set request headers, so a
  // sendBeacon flush would 401 and silently drop the pending edit. This
  // prevents silently losing edits on tab close, browser reload, or
  // background throttling.
  const flushBeacon = useCallback(() => {
    if (!flushUrl || !initializedRef.current || !hasPendingChanges()) return
    const payload = JSON.stringify(latestDataRef.current)
    const headers: Record<string, string> = { 'Content-Type': 'application/json' }
    if (flushAuthToken) headers['Authorization'] = `Bearer ${flushAuthToken}`
    try {
      void fetch(flushUrl, {
        method: 'PUT',
        keepalive: true,
        headers,
        body: payload,
      })
    } catch {
      // Best-effort — browser may block outbound requests during unload.
    }
  }, [flushUrl, flushAuthToken, hasPendingChanges])

  useEffect(() => {
    if (!flushUrl || disabled) return

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
  }, [flushUrl, flushAuthToken, disabled, flushBeacon])

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

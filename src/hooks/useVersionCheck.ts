import { useEffect, useRef } from 'react'
import { useUiStore } from '@/store/ui'
import { fetchVersion } from '@/lib/api'

/**
 * useVersionCheck — polls /api/v1/version on mount, window focus, and every 60s.
 * If build_sha changes from the initial value, shows a "New version available" toast (#110).
 *
 * The version fetch goes through the shared `request<T>` helper in
 * `src/lib/api.ts` so the response is validated by the generated
 * `VersionResponse` Zod schema — no raw `fetch()` and no hand-written
 * `VersionResponse` interface (the contract is the source of truth).
 */
export function useVersionCheck() {
  const addToast = useUiStore((s) => s.addToast)
  const initialSha = useRef<string | null>(null)
  const toastShown = useRef(false)

  function checkVersion() {
    fetchVersion()
      .then((v) => {
        if (initialSha.current === null) {
          initialSha.current = v.build_sha
          return
        }
        if (!toastShown.current && v.build_sha !== initialSha.current) {
          toastShown.current = true
          addToast({
            message: 'New version available — refresh to update',
            variant: 'default',
            duration: 30_000, // linger for 30s
            testId: 'version-toast',
          })
        }
      })
      .catch(() => {
        // version endpoint unavailable — ignore
      })
  }

  useEffect(() => {
    // Initial fetch
    checkVersion()

    // Poll every 60 seconds
    const interval = setInterval(checkVersion, 60_000)

    // Re-check on window focus
    const onFocus = () => checkVersion()
    window.addEventListener('focus', onFocus)

    return () => {
      clearInterval(interval)
      window.removeEventListener('focus', onFocus)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
}

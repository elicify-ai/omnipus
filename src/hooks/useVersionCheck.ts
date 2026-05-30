import { useEffect, useRef } from 'react'
import { useUiStore } from '@/store/ui'

interface VersionResponse {
  version: string
  build_sha: string
}

async function fetchVersion(): Promise<VersionResponse> {
  const res = await fetch('/api/v1/version')
  if (!res.ok) throw new Error(`version fetch failed: ${res.status}`)
  return res.json() as Promise<VersionResponse>
}

/**
 * useVersionCheck — polls /api/v1/version on mount, window focus, and every 60s.
 * If build_sha changes from the initial value, shows a "New version available" toast (#110).
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

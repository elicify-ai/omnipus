import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { checkSearchProviderConnection } from '@/lib/api/providers'
import { isApiError, parseServerErrorField } from '@/lib/api-error'
import type {
  IntegrationProvidersResponse,
  SearchProviderCheckResponse,
} from '@/lib/api/generated/openapi-types'

const CHECK_COOLDOWN_MS = 30_000

const CHECK_MESSAGES: Record<SearchProviderCheckResponse['status'], string> = {
  success: 'Connection works.',
  auth_error: 'The service rejected your key. Edit it and try again.',
  rate_limited: 'The service is limiting requests. Wait and try again.',
  timeout: 'The service did not respond in time. Try again.',
  network_error: 'Could not reach the service. Try again.',
  provider_error: 'The service could not complete the check. Try again.',
  invalid_response: 'The service returned an unexpected response. Try again.',
}

interface ConnectionCheckState { // not-wire-format: temporary UI state, never sent to the gateway
  pending: boolean
  message?: string
  error?: string
  cooldownUntil: number
  localCooldown?: boolean
  catalogueState?: string
}

/** Manual diagnostics only: a cooldown never schedules another request. */
export function useSearchConnectionChecks() {
  const queryClient = useQueryClient()
  const [checks, setChecks] = useState<Record<string, ConnectionCheckState>>({})
  const [now, setNow] = useState(() => Date.now())
  const inFlight = useRef(new Map<string, AbortController>())

  // Readiness refreshes are not key changes. Compare each service's public
  // row; local key writes clear that service explicitly, and the server rejects
  // a key changed during a check with HTTP 409 (keys have no wire identity).
  const getCatalogue = useCallback(() =>
    queryClient.getQueryData<IntegrationProvidersResponse>(['integrations']),
  [queryClient])
  const subscribe = useCallback((notify: () => void) =>
    queryClient.getQueryCache().subscribe((event) => {
      if (event.query.queryKey[0] === 'integrations') notify()
    }),
  [queryClient])
  const catalogue = useSyncExternalStore(subscribe, getCatalogue)

  useEffect(() => {
    setChecks((previous) => {
      let next = previous
      for (const [id, check] of Object.entries(previous)) {
        const current = catalogue?.search.find((row) => row.id === id)
        if (!check.pending && check.catalogueState !== JSON.stringify(current) &&
            (check.message || check.error || check.localCooldown)) {
          next = {
            ...next,
            [id]: { ...check, message: undefined, error: undefined, localCooldown: false },
          }
        }
      }
      return next
    })
  }, [catalogue])

  const hasCooldown = Object.values(checks).some((check) => check.cooldownUntil > now)
  useEffect(() => {
    if (!hasCooldown) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [hasCooldown])

  useEffect(() => {
    const requests = inFlight.current
    return () => {
      for (const controller of requests.values()) controller.abort()
      requests.clear()
    }
  }, [])

  const clear = (id: string) => {
    setChecks((previous) => {
      const check = previous[id]
      if (!check) return previous
      return {
        ...previous,
        [id]: { ...check, message: undefined, error: undefined, localCooldown: false },
      }
    })
  }

  const run = async (id: string) => {
    const provider = getCatalogue()?.search.find((row) => row.id === id)
    const startedAt = Date.now()
    if (!provider?.requires_key || !provider.configured || provider.usable !== true) return
    if (inFlight.current.has(id) || (checks[id]?.cooldownUntil ?? 0) > startedAt) return

    const controller = new AbortController()
    inFlight.current.set(id, controller)
    const startedState = JSON.stringify(provider)
    let requestError: string | undefined
    let state: ConnectionCheckState = {
      pending: true,
      cooldownUntil: startedAt + CHECK_COOLDOWN_MS,
      catalogueState: startedState,
    }
    setNow(startedAt)
    setChecks((previous) => ({ ...previous, [id]: state }))

    try {
      const result = await checkSearchProviderConnection(id, { signal: controller.signal })
      if (result.provider_id !== id) {
        state = { ...state, error: 'Could not verify the connection check. Try again.' }
      } else {
        const message = CHECK_MESSAGES[result.status]
        state = {
          ...state,
          ...(result.status === 'success' ? { message } : { error: message }),
          cooldownUntil: Math.max(
            state.cooldownUntil,
            result.retry_after_seconds ? Date.now() + result.retry_after_seconds * 1000 : 0,
          ),
        }
      }
    } catch (err) {
      if (isApiError(err) && err.status === 429) {
        state = {
          ...state,
          localCooldown: true,
          cooldownUntil: Date.now() + (err.retryAfterMs ?? CHECK_COOLDOWN_MS),
        }
      } else {
        // Non-2xx and malformed gateway responses are not upstream diagnostic
        // outcomes. Preserve the gateway's recovery guidance, not its raw body
        // or an exception. A 409 is a rejected check, never an old-key result.
        requestError = isApiError(err)
          ? parseServerErrorField(err.body) ?? err.userMessage
          : 'Could not verify the connection check. Try again.'
        state = { ...state, error: requestError }
      }
    } finally {
      const superseded = startedState !== JSON.stringify(getCatalogue()?.search.find((row) => row.id === id))
      // Even failed attempts may discover a changed readiness state. Only the
      // catalogue, never the diagnostic result, owns the Ready badge. Bind the
      // outcome to this service after its own readiness refresh; identical-looking
      // key changes after the check response remain the accepted wire-identity gap.
      await queryClient.invalidateQueries({ queryKey: ['integrations'] })
      inFlight.current.delete(id)
      if (!controller.signal.aborted) {
        const current = getCatalogue()?.search.find((row) => row.id === id)
        setNow(Date.now())
        setChecks((previous) => ({
          ...previous,
          [id]: {
            ...state,
            pending: false,
            catalogueState: JSON.stringify(current),
            ...(!current?.configured || superseded
              ? { message: undefined, error: requestError, localCooldown: false }
              : {}),
          },
        }))
      }
    }
  }

  const view = (id: string) => {
    const check = checks[id]
    const seconds = Math.max(0, Math.ceil(((check?.cooldownUntil ?? 0) - now) / 1000))
    const current = check?.catalogueState === JSON.stringify(catalogue?.search.find((row) => row.id === id))
    return {
      pending: check?.pending === true,
      cooldown: seconds > 0,
      message: current ? check?.message : undefined,
      error: current ? check?.error : undefined,
      cooldownMessage: current && check?.localCooldown && seconds > 0
        ? `You can check again in ${seconds} seconds.`
        : undefined,
    }
  }

  return { run, clear, view, anyPending: Object.values(checks).some((check) => check.pending) }
}

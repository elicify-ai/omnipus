import { isApiError } from '@/lib/api'

// #359: validateToken() used to run as a raw, uncached fetch on EVERY route
// transition, and the route guard cleared auth on ANY error. With hot_reload now
// default-on (#352) the gateway config swaps more often, so a transient 401 during
// a brief reload window logged the user out within minutes — the reported
// "~2-3 min session expiry". This module caches a successful validation and
// classifies failures so the guard only evicts the session on a CONFIRMED 401.
export const VALIDATE_TTL_MS = 30_000

let lastValidatedAt = 0

// resetTokenValidationCache forces a fresh validation on the next guard call.
// Called by the login flow so a new session (possibly a different user) is
// validated immediately rather than riding a previous session's cache window.
export function resetTokenValidationCache() {
  lastValidatedAt = 0
}

export type TokenVerdict = 'ok' | 'unauthorized' | 'transient'

// checkTokenValidity runs `validate` at most once per VALIDATE_TTL_MS and maps the
// outcome to a verdict: 'ok' (valid, or a cache hit), 'unauthorized' (a confirmed
// 401 → caller must log out), or 'transient' (network/5xx → caller keeps the
// session). `now` is injectable for deterministic tests.
export async function checkTokenValidity(
  validate: () => Promise<unknown>,
  now: number = Date.now(),
): Promise<TokenVerdict> {
  if (now - lastValidatedAt <= VALIDATE_TTL_MS) {
    return 'ok'
  }
  try {
    await validate()
    lastValidatedAt = now
    return 'ok'
  } catch (err) {
    // Only a confirmed 401 means the token is actually invalid. A transient
    // failure (network status 0, 5xx) must NOT log the user out — keep the
    // session and let the next check decide. A 401 also clears the cache so the
    // next attempt re-validates rather than riding a stale "ok" window.
    if (isApiError(err) && err.status === 401) {
      lastValidatedAt = 0
      return 'unauthorized'
    }
    return 'transient'
  }
}

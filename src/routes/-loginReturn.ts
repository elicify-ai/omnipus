const LOGIN_RETURN_KEY = 'omnipus_login_return'

function validatedAppPath(value: unknown): string | null {
  if (
    typeof value !== 'string' ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    value.includes('\\')
  ) {
    return null
  }
  if (value === '/login' || value.startsWith('/login?')) return null
  return value
}

/** Return a same-origin app path, or the app root for an unsafe target. */
export function safeAppPath(value: unknown): string {
  return validatedAppPath(value) ?? '/'
}

function currentHashPath(): string | null {
  if (typeof window === 'undefined' || !window.location.hash.startsWith('#/')) return null
  return validatedAppPath(window.location.hash.slice(1))
}

/** Preserve the router's internal path before the auth gate redirects. */
export function captureLoginReturn(path: unknown): void {
  const target = validatedAppPath(path)
  if (target === null || typeof window === 'undefined') return
  try {
    window.sessionStorage.setItem(LOGIN_RETURN_KEY, target)
  } catch {
    // Storage denial degrades to the current-hash fallback below.
  }
}

/** Consume once so a later, unrelated sign-in cannot inherit a stale link. */
export function consumeLoginReturn(): string | null {
  if (typeof window === 'undefined') return null
  let stored: string | null = null
  try {
    stored = window.sessionStorage.getItem(LOGIN_RETURN_KEY)
    window.sessionStorage.removeItem(LOGIN_RETURN_KEY)
  } catch {
    // The hash fallback still covers a login form mounted over the target.
  }
  return validatedAppPath(stored) ?? currentHashPath()
}

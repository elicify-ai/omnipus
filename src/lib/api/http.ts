// http.ts: Transport core — base URL, CSRF, the fetch wrapper, schema validation

import { ApiError, isApiError as isApiErrorFn } from '../api-error'
import { maybeDevToast } from '../dev-toast'
import { logError } from '../telemetry'
import type { ZodType } from 'zod'

// ── Schema validation error ────────────────────────────────────────────────────
//
// Thrown when an API response does not conform to the expected Zod schema.
// Only thrown when a schema was explicitly passed to request() — unvalidated
// calls fall back to the old untyped behaviour.
//
// In dev mode, a toast is emitted as well (see request() below).

export class ApiSchemaError extends Error {
  readonly endpoint: string
  readonly zodIssues: Array<{ path: (string | number)[]; message: string }>
  readonly rawBody: unknown

  constructor(endpoint: string, zodIssues: Array<{ path: (string | number)[]; message: string }>, rawBody: unknown) {
    super(`API response schema mismatch for ${endpoint}: ${zodIssues[0]?.message ?? 'unknown'}`)
    this.name = 'ApiSchemaError'
    this.endpoint = endpoint
    this.zodIssues = zodIssues
    this.rawBody = rawBody
  }
}

let _apiSchemaErrorCount = 0

export function getApiSchemaErrorCount(): number {
  return _apiSchemaErrorCount
}

export function resetApiSchemaErrorCount(): void {
  _apiSchemaErrorCount = 0
}

// Increment the API-schema-error counter AND emit a production telemetry
// event. Rate-limited inside logError so a contract-drift flood doesn't
// spam the log collector. Dev builds skip telemetry (they already get the
// dev toast).
export function _recordApiSchemaError(endpoint: string, issueCount: number): void {
  _apiSchemaErrorCount++
  if (!import.meta.env.DEV && import.meta.env.MODE !== 'test') {
    logError({
      event: 'apiSchemaError',
      endpoint,
      issueCount,
      totalErrors: _apiSchemaErrorCount,
    })
  }
}

export const BASE_URL = import.meta.env.VITE_API_URL ?? ''

// The server issues one of two cookie names depending on the request's TLS
// state. TLS: __Host-csrf (browser enforces Secure + Path=/ + no Domain).
// Plain HTTP: csrf (no __Host- prefix, Secure=false) — __Host- cookies are
// silently dropped by browsers on non-localhost plain-HTTP origins.
// Keep both constants in sync with pkg/gateway/middleware/csrf.go.
const CSRF_COOKIE_NAME = '__Host-csrf'

const CSRF_COOKIE_NAME_HTTP = 'csrf'

export const CSRF_HEADER_NAME = 'X-CSRF-Token'

const STATE_CHANGING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

// CSRF_EXEMPT_PATHS lists state-changing endpoints whose handler's job is to
// ISSUE the __Host-csrf cookie. They can't require the cookie to be present
// — that's the chicken-and-egg bootstrap problem. Keep this list in sync
// with pkg/gateway/middleware/csrf.go `exemptPaths` for /api/v1/* entries.
// Paths here are compared against the /api/v1/-prefixed URL.
//
// Why each entry is here:
//   - /api/v1/onboarding/complete — called on fresh install (no cookie exists).
//   - /api/v1/auth/login — called on first load of an existing install
//     (refresh, new tab); cookie may be absent until the login succeeds.
const CSRF_EXEMPT_PATHS = new Set<string>([
  '/api/v1/onboarding/complete',
  '/api/v1/onboarding/probe-provider',
  '/api/v1/auth/login',
])

// readCSRFCookie parses document.cookie and returns the __Host-csrf (or
// plain-HTTP `csrf`) value, or null if neither cookie is present. We
// intentionally do not cache — cookies can change after login/logout/
// onboarding/re-mint, and caching would cause stale tokens on the next
// state-changing call. Called fresh on every request (FR-010, r4-MAJ-004).
export function readCSRFCookie(): string | null {
  if (typeof document === 'undefined') return null
  // Try __Host-csrf (TLS) first, then the plain-HTTP fallback.
  for (const name of [CSRF_COOKIE_NAME, CSRF_COOKIE_NAME_HTTP]) {
    const prefix = `${name}=`
    for (const part of document.cookie.split(';')) {
      const trimmed = part.trim()
      if (trimmed.startsWith(prefix)) {
        const raw = trimmed.slice(prefix.length)
        // Apply decodeURIComponent defensively: if the browser percent-encoded
        // the cookie value (e.g. standard base64 "=", "+", "/"), we decode it
        // so the header value matches what the server originally set. If
        // decoding fails (malformed sequence such as a lone "%"), fall back to
        // the raw string and let the server compare verbatim.
        try {
          return decodeURIComponent(raw)
        } catch {
          return raw
        }
      }
    }
  }
  return null
}

/**
 * getCsrfCookie is the public form of readCSRFCookie, for the rare caller
 * that must build its own fetch() outside request() (e.g. the useAutoSave
 * pagehide/visibilitychange keepalive beacon, which fires a raw
 * `fetch(..., {keepalive:true})` that cannot go through the async request()
 * pipeline). Kept as a thin wrapper so there is exactly one cookie-parsing
 * implementation — see the "byte-for-byte identical" caution in
 * api.test.ts's own reimplementation of this logic.
 */
export function getCsrfCookie(): string | null {
  return readCSRFCookie()
}

// buildHeaders composes the standard request headers, layering (in order):
// content-type → CSRF header (read fresh) → caller overrides. There is no
// Authorization header here: the SPA authenticates via the omnipus-session
// cookie (sent automatically by the browser via credentials:'include' in
// performRequest below), never a JS-visible bearer token (US-5 / FR-010).
export function buildHeaders(extra?: HeadersInit): HeadersInit {
  const csrf = readCSRFCookie()
  return {
    'Content-Type': 'application/json',
    ...(csrf ? { [CSRF_HEADER_NAME]: csrf } : {}),
    ...extra,
  }
}

// isPathCSRFExempt checks whether a state-changing call can skip the
// client-side cookie-presence check. Only onboarding-complete is exempt —
// see CSRF_EXEMPT_PATHS.
function isPathCSRFExempt(apiPath: string): boolean {
  return CSRF_EXEMPT_PATHS.has(`/api/v1${apiPath}`)
}

// isCsrfFailureBody distinguishes a CSRF-caused 403 from a generic
// authorization/permission 403 (e.g. RBAC denial, agent-locked). The
// gateway's CSRF middleware (pkg/gateway/middleware/csrf.go writeCSRFError)
// always responds with {"error":"csrf cookie missing"|"csrf header
// missing"|"csrf token mismatch"} on a CSRF rejection — every other 403
// uses unrelated wording. We match on this fixed vocabulary (via
// ApiError.body, the raw response text) rather than a machine code because
// the endpoint does not emit one.
function isCsrfFailureBody(body: string | undefined): boolean {
  if (!body) return false
  return /csrf/i.test(body)
}

/**
 * withCsrfRetry wraps a single state-changing attempt and recovers from a
 * CSRF-specific 403 exactly once (FR-010 / FR-019 / r4-MAJ-004): a returning
 * user whose CSRF cookie expired (browser reopened same-day) — or whose very
 * first action is a state-changing call before any GET has hit the server —
 * gets rejected by the server's double-submit check even though a client-
 * side presence check (where one exists) saw a cookie. Recovery: issue a
 * safe GET (the gateway re-mints the CSRF cookie on any authenticated safe
 * request that lacks one), then re-run `attempt` with a freshly read cookie.
 * If the retry also fails, that failure is surfaced — this can never loop
 * more than once because `attempt` is invoked directly, not through this
 * wrapper again.
 *
 * Shared by request() (JSON calls) and the two multipart raw-fetch call
 * sites (uploadFiles, transcribeAudio) so the recovery logic isn't
 * duplicated three times.
 */
export async function withCsrfRetry<T>(attempt: () => Promise<T>): Promise<T> {
  try {
    return await attempt()
  } catch (err) {
    if (isApiErrorFn(err) && err.status === 403 && isCsrfFailureBody(err.body)) {
      try {
        await performRequest('/state', undefined, undefined)
      } catch {
        // Best-effort re-mint trigger — proceed to retry regardless of
        // whether the GET itself succeeded.
      }
      return await attempt()
    }
    throw err
  }
}

export async function request<T>(path: string, init?: RequestInit, schema?: ZodType<T>): Promise<T> {
  // Client-side CSRF gate: reject state-changing calls that would be
  // guaranteed to 403 at the server. This gives a clear error immediately
  // instead of a cryptic "403 csrf cookie missing" from the network tab
  // and also prevents a cascade of dependent requests firing during a
  // broken auth state.
  //
  // We synthesize an ApiError with status 403 + a CSRF-specific code so
  // callers can branch on `err.code === 'csrf_missing'` without having to
  // string-match the message. The message text is preserved verbatim from
  // the previous implementation for any caller that hasn't migrated yet.
  const method = (init?.method ?? 'GET').toUpperCase()
  const stateChanging = STATE_CHANGING_METHODS.has(method)
  if (
    stateChanging &&
    !isPathCSRFExempt(path) &&
    readCSRFCookie() === null
  ) {
    throw new ApiError(
      403,
      `CSRF cookie missing — cannot ${method} ${path}. ` +
        `Log in or complete onboarding first so the server can issue the CSRF cookie.`,
      { code: 'csrf_missing' },
    )
  }

  const attempt = () => performRequest<T>(path, init, schema)
  return stateChanging ? withCsrfRetry(attempt) : attempt()
}

async function performRequest<T>(path: string, init?: RequestInit, schema?: ZodType<T>): Promise<T> {
  const method = (init?.method ?? 'GET').toUpperCase()
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1${path}`, {
      ...init,
      // Auth is the omnipus-session HttpOnly cookie — always send it (and
      // accept the server's Set-Cookie, e.g. a CSRF re-mint) even when
      // BASE_URL points at a different origin in dev (US-5 / FR-010).
      credentials: 'include',
      headers: buildHeaders(init?.headers),
    })
  } catch (cause) {
    // Transport-level failure — DNS, TCP, TLS, AbortController, or fetch threw
    // for any other reason. Surface as a status-0 ApiError so callers can
    // distinguish "browser couldn't reach the server" from "server said no".
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }

  // Empty-body responses (HTTP 204 No Content, 205 Reset Content, or any 2xx
  // that explicitly advertises a zero-length body) carry no JSON to parse.
  // DELETE/PUT/POST handlers that return 204 would otherwise throw on
  // res.json() ("Unexpected end of JSON input"), making a successful mutation
  // appear to fail. Detect the DEFINITIVE empty-body signals and resolve with
  // `undefined` — the correct value for the Promise<void> callers (deleteTask,
  // deleteSkill, deleteMcpServer, deleteCredential, deleteSchedule, …).
  // clearAllSessions is NOT one of these — it returns HTTP 200 with a JSON
  // body, handled by the parse path below. We deliberately do NOT key off
  // Content-Type here: a non-JSON body with actual content (e.g. an HTML
  // error page served with a misconfigured 200) is a different failure
  // that must still flow to the schema/JSON-parse path below so it
  // surfaces as an ApiSchemaError, not a
  // silent success.
  const contentLength = res.headers.get('Content-Length')
  if (res.status === 204 || res.status === 205 || contentLength === '0') {
    return undefined as T
  }

  // Parse the response body, handling non-JSON (e.g. unexpected HTML 200)
  // gracefully — surface as ApiSchemaError with the raw text as rawBody so
  // callers can see what the server actually sent.
  let body: unknown
  try {
    body = await res.json() as unknown
  } catch (cause) {
    // A JSON-parse failure on a body with no Content-Length header is the
    // common shape of an empty 2xx (some gateways omit Content-Length on a
    // bodyless 200/202 instead of using 204). For a caller that passed NO
    // schema — i.e. a Promise<void> mutation — that is a legitimate success,
    // so resolve with undefined rather than throwing. When a schema WAS
    // provided the body genuinely should have been JSON, so surface the
    // ApiSchemaError as before.
    if (schema === undefined && (contentLength === null || contentLength === '')) {
      return undefined as T
    }
    const rawText = String(cause instanceof Error ? cause.message : cause)
    if (schema !== undefined) {
      _recordApiSchemaError(`${method} /api/v1${path}`, 1)
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        [{ path: [], message: 'Response is not valid JSON' }],
        rawText,
      )
      void maybeDevToast(`[api] Non-JSON response: ${path}`, `${method}:${path}:non-json`)
      throw schemaErr
    }
    // No schema — throw a generic ApiError for non-JSON bodies on non-2xx
    // (we already checked res.ok above, so a JSON parse error here on a 200
    // is itself unexpected; surface as a 0-status transport error).
    throw new ApiError(0, `Response from ${path} is not valid JSON`, { cause })
  }

  if (import.meta.env.DEV && schema === undefined) {
    console.warn(`[api] ${method} /api/v1${path}: no Zod schema — response validation skipped. Add schema from src/lib/api/generated/schemas.ts.`)
  }

  // When a Zod schema is provided, validate the response body against it.
  // On failure: throw ApiSchemaError (+ dev toast). Never silently return
  // schema-invalid data — callers that need the old unchecked behaviour
  // should not pass a schema.
  if (schema !== undefined) {
    const result = schema.safeParse(body)
    if (!result.success) {
      _recordApiSchemaError(`${method} /api/v1${path}`, result.error.issues.length)
      const schemaErr = new ApiSchemaError(
        `${method} /api/v1${path}`,
        result.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message })),
        body,
      )
      const first = schemaErr.zodIssues[0]
      void maybeDevToast(`[api] Schema mismatch: ${path} — ${first?.message ?? 'unknown'}`, `${method}:${path}:schema`)
      throw schemaErr
    }
    return result.data
  }

  return body as T
}

/**
 * Rewrite a `409 Conflict` into an action-specific, human-actionable message
 * — the generic `ApiError` default for 409 ("This conflicts with the
 * current state. Please refresh and try again.") doesn't say WHY a
 * restart/run isn't possible right now. Only 409 is rewritten; every other
 * status (400/401/404/network) and every non-`ApiError` passes through
 * completely unchanged. Shared by `restartPlan`/`restartTask`/`runTask`
 * (ADR-052 FR-016/FR-019/FR-026 — restart/run reject with 409 for "not
 * restartable"/"not runnable" states).
 */
export function friendlyConflictError(err: unknown, message: string): unknown {
  if (isApiErrorFn(err) && err.status === 409) {
    return new ApiError(409, message, { code: err.code, body: err.body, cause: err.cause })
  }
  return err
}

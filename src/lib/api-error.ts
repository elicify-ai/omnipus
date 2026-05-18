// ApiError is the typed error thrown by request() in @/lib/api for any non-2xx
// HTTP response or transport-level failure. Callers branch on `err.status` (or
// the `is*Error()` helpers) to render context-specific UX without re-parsing
// stringified error messages.
//
// Why this exists: before this class, `request()` threw raw `Error` instances
// whose `.message` was `"${status}: ${body}"`. Callers across the SPA had to
// regex the message to recover the status code, which was brittle, fragile to
// translation, and collapsed transport failures (network down) into the same
// branch as legitimate HTTP errors. ApiError separates the wire response
// (status / code / body) from the human-facing message (userMessage).
//
// Backward-compat note: the inherited `Error.message` is set to the legacy
// `"${status}: ${userMessage}"` string for `status > 0`, and to `userMessage`
// alone for transport failures (`status === 0`). Code paths that haven't been
// migrated to type-narrow on ApiError still produce the same string they
// always did, so the migration is mechanical rather than a UX change.

export interface ApiErrorOptions { // not-wire-format: constructor options for the ApiError class; never sent over any HTTP/WS boundary
  /**
   * Optional machine-readable code from the backend. Most server endpoints
   * return `{"error": "..."}` without a code field today, so this is best-
   * effort and frequently absent. When present (e.g. a future migration to
   * `{"code":"INVALID_CSRF","message":"..."}`), callers can branch on it
   * instead of/in addition to `status`.
   */
  code?: string
  /**
   * Raw response body for debug/log surfaces. Never display this directly to
   * end users — use `userMessage` for UI.
   */
  body?: string
  /**
   * Underlying error (e.g. the TypeError from a failed `fetch()`) for stack
   * preservation. Surfaced via the standard `Error.cause` plumbing.
   */
  cause?: unknown
}

/**
 * defaultUserMessage returns a generic, safe-to-display string for a given
 * HTTP status class. Used as the fallback when the server response body is
 * empty, unparseable, or visibly leaks server internals.
 */
function defaultUserMessage(status: number): string {
  if (status === 0) return 'Network unavailable. Check your connection.'
  if (status === 401) return 'Your session has expired. Please log in again.'
  if (status === 403) return "You don't have permission to perform this action."
  if (status === 404) return 'The requested resource was not found.'
  if (status === 409) return 'This conflicts with the current state. Please refresh and try again.'
  if (status === 410) return 'This item is no longer available.'
  if (status === 413) return 'The request is too large.'
  if (status === 429) return 'Too many requests. Please slow down and try again shortly.'
  if (status >= 500 && status < 600) return 'The server is unavailable. Please try again in a moment.'
  if (status >= 400 && status < 500) return 'The request was rejected by the server.'
  return 'An unexpected error occurred.'
}

export class ApiError extends Error {
  /**
   * HTTP status code from the response. `0` indicates a transport-level
   * failure (DNS, TCP, TLS, fetch threw) — i.e. the request never reached
   * a stage where the server could answer.
   */
  readonly status: number
  /**
   * Optional machine-readable code from the response body. See `ApiErrorOptions.code`.
   */
  readonly code?: string
  /**
   * Human-displayable error message. Safe to render in UI as-is. Defaults
   * apply when the server doesn't return a useful body.
   */
  readonly userMessage: string
  /**
   * Raw response body, if any. Use for debugging/logging only.
   */
  readonly body?: string

  constructor(status: number, userMessage?: string, options?: ApiErrorOptions) {
    const message =
      userMessage && userMessage.trim().length > 0 ? userMessage : defaultUserMessage(status)
    // Legacy compat: the historical Error.message contract was "${status}: ${body}"
    // for HTTP errors and a plain string for transport failures. Preserve that
    // exactly so any un-migrated caller that still does err.message.includes('409')
    // / err.message.startsWith('401') keeps working.
    const errMessage = status > 0 ? `${status}: ${message}` : message
    super(errMessage, options?.cause !== undefined ? { cause: options.cause } : undefined)
    this.name = 'ApiError'
    this.status = status
    this.code = options?.code
    this.userMessage = message
    this.body = options?.body
    // Stabilise prototype for `instanceof` to work across realms / when minified.
    Object.setPrototypeOf(this, ApiError.prototype)
  }

  /** True for 401 (no session) / 403 (forbidden). */
  isAuthError(): boolean {
    return this.status === 401 || this.status === 403
  }

  /** True for 404. */
  isNotFound(): boolean {
    return this.status === 404
  }

  /** True for 429. */
  isRateLimited(): boolean {
    return this.status === 429
  }

  /** True for any 5xx response. */
  isServerError(): boolean {
    return this.status >= 500 && this.status < 600
  }

  /** True for transport-level failures (no HTTP response received). */
  isNetworkError(): boolean {
    return this.status === 0
  }

  /**
   * fromResponse builds an ApiError from a non-2xx Response, parsing the body
   * as JSON `{code?, error?, message?}` when possible and falling back to
   * plain text. This is a static convenience so request() in api.ts and the
   * standalone uploadFiles() path both stay in sync.
   *
   * H3-FE: Guards against large or binary bodies that could freeze the UI when
   * rendered as a toast:
   *   - Bodies larger than 4 KiB are rejected; defaultUserMessage() is used instead.
   *   - Bodies whose Content-Type is not text/ or application/json, or that
   *     contain >5% non-printable bytes in the first 256 bytes, are rejected.
   */
  static async fromResponse(res: Response): Promise<ApiError> {
    // H3-FE: Reject oversized bodies before reading.
    const MAX_BODY_BYTES = 4 * 1024 // 4 KiB
    const contentLengthHeader = res.headers.get('content-length')
    const declaredLength = contentLengthHeader !== null ? parseInt(contentLengthHeader, 10) : null
    if (declaredLength !== null && !isNaN(declaredLength) && declaredLength > MAX_BODY_BYTES) {
      console.warn('[api-error] response body too large, using status default', {
        status: res.status,
        contentLength: declaredLength,
      })
      return new ApiError(res.status, defaultUserMessage(res.status))
    }

    // H3-FE: Reject non-text, non-JSON content types before reading.
    const contentType = res.headers.get('content-type') ?? ''
    const isTextLike =
      contentType.includes('application/json') ||
      contentType.includes('text/')
    // When Content-Type is absent we allow reading and rely on the binary-sniff below.
    if (contentType !== '' && !isTextLike) {
      console.warn('[api-error] non-text content-type, using status default', {
        status: res.status,
        contentType,
      })
      return new ApiError(res.status, defaultUserMessage(res.status))
    }

    let bodyText = ''
    try {
      bodyText = await res.text()
    } catch (err) {
      // Reading body failed (e.g. response stream errored). Use statusText.
      console.warn('[api-error] Could not read response body:', err)
      bodyText = res.statusText
    }

    // H3-FE: Reject bodies that exceed MAX_BODY_BYTES after the read (handles
    // servers that omit Content-Length).
    if (bodyText.length > MAX_BODY_BYTES) {
      console.warn('[api-error] response body exceeds cap after read, using status default', {
        status: res.status,
        bodyLength: bodyText.length,
      })
      return new ApiError(res.status, defaultUserMessage(res.status))
    }

    // H3-FE: Binary content sniff — check the first 256 chars for non-printable
    // characters. If >5% are non-printable (i.e. code point < 0x20 excluding
    // common whitespace \t \n \r), treat as binary and use the default.
    if (bodyText.length > 0) {
      const sample = bodyText.slice(0, 256)
      let nonPrintable = 0
      for (let i = 0; i < sample.length; i++) {
        const cp = sample.charCodeAt(i)
        // Allow tab (0x09), newline (0x0A), carriage return (0x0D); reject other
        // control characters below 0x20 and also DEL (0x7F).
        if ((cp < 0x20 && cp !== 0x09 && cp !== 0x0a && cp !== 0x0d) || cp === 0x7f) {
          nonPrintable++
        }
      }
      if (nonPrintable / sample.length > 0.05) {
        console.warn('[api-error] binary content detected, using status default', {
          status: res.status,
          nonPrintableRatio: (nonPrintable / sample.length).toFixed(2),
        })
        return new ApiError(res.status, defaultUserMessage(res.status))
      }
    }

    let parsedMessage: string | undefined
    let parsedCode: string | undefined
    if (bodyText) {
      try {
        const parsed = JSON.parse(bodyText) as { code?: unknown; error?: unknown; message?: unknown }
        if (typeof parsed.code === 'string') parsedCode = parsed.code
        if (typeof parsed.error === 'string') parsedMessage = parsed.error
        else if (typeof parsed.message === 'string') parsedMessage = parsed.message
      } catch {
        // Not JSON — fall back to the raw text below.
      }
    }

    // Display priority: status-class default (for known codes) > parsed JSON
    // message > non-empty body text > generic default.
    //
    // The earlier behaviour preferred the server-provided `error` field, but
    // that leaks server-internal phrasing into the user-facing toast (e.g.
    // "agent name already exists" vs the safer "This conflicts with the
    // current state. Please refresh and try again.") and surprises tests that
    // assume `userMessage` matches `defaultUserMessage(status)`. The raw
    // server text is still preserved on `body` and `message`.
    const knownStatuses = new Set([0, 401, 403, 404, 409, 410, 413, 429])
    const isKnown = knownStatuses.has(res.status) || (res.status >= 500 && res.status < 600)
    const userMessage = isKnown
      ? defaultUserMessage(res.status)
      : (parsedMessage ?? (bodyText.trim().length > 0 ? bodyText : defaultUserMessage(res.status)))
    return new ApiError(res.status, userMessage, { code: parsedCode, body: bodyText })
  }
}

/**
 * Type-narrowing predicate for catch blocks. Use this rather than
 * `err instanceof ApiError` directly — it stays robust if the class is
 * imported through different bundling realms (rare in this app, but the
 * predicate also documents intent at the call site).
 */
export function isApiError(err: unknown): err is ApiError {
  return err instanceof ApiError
}

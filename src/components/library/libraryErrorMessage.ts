// libraryErrorMessage — Library-scoped error-message resolution.
//
// UAT fix (Dana, re-verified v8): a Move onto a missing destination parent
// gets back EXACTLY this from the server —
//   {"error":"destination directory \"dana-missing-folder\" does not exist —
//   create it first with POST /library/{workspace_id}/mkdir"}
// — but the SPA showed the generic "The requested resource was not found."
// in two places at once, naming neither the folder nor the remedy. Root
// cause: `ApiError.fromResponse` (src/lib/api-error.ts) deliberately
// overrides `userMessage` with a generic, safe-to-display string for
// "known" HTTP statuses (401/403/404/409/410/413/429/5xx) — the right
// default for MOST of the app, since it avoids leaking server-internal
// phrasing into a toast — but it throws away Library's own, purpose-written
// guidance messages in the process, which name the SPECIFIC missing
// directory and how to fix it. Losing that is a functional regression, not
// a safety improvement: "functionally the same silent-404 experience as
// before" (tester's words).
//
// getLibraryErrorMessage recovers the server's `error` field from the raw
// response body ApiError still retains on `.body` even when `userMessage`
// was overridden, then rewords the one piece of server guidance that leaks
// a raw API path into user-facing text (the mkdir pointer) into UI terms —
// WITHOUT dropping the directory name, which is the load-bearing part of
// the message. Falls back to the app-wide getErrorMessage()/generic message
// whenever there's no recoverable server text (network errors, non-JSON
// bodies, etc).
//
// Scoped to Library rather than changing api-error.ts's app-wide behaviour:
// that file is shared by every feature in the SPA, and its "known status ->
// generic message" policy is a deliberate, tested choice for the rest of
// the app (see api-error.ts's own comment) — this is a narrow, Library-only
// override, not a reversal of that policy.

import { isApiError, getErrorMessage } from '@/lib/api'

// Matches the exact backend guidance appended to a 404 "destination parent
// doesn't exist" message (pkg/gateway/rest_library.go's mapLibraryErr):
// `create it first with POST /library/{workspace_id}/mkdir`. Kept
// case-insensitive and tolerant of the workspace id segment so it still
// matches if the backend ever substitutes a real id instead of the literal
// `{workspace_id}` placeholder.
const MKDIR_GUIDANCE_RE = /create it first with\s+POST\s+\/library\/[^\s.]+\/mkdir\.?/i

/**
 * Rewords server guidance that names a raw API path into UI terms, without
 * dropping the specific detail (e.g. the missing directory name) that
 * precedes it — that detail is the load-bearing part of the message.
 */
function rewordServerGuidance(message: string): string {
  return message.replace(MKDIR_GUIDANCE_RE, 'create it first using New Folder')
}

/** Recovers the server's `error` (preferred) or `message` field from a raw JSON response body, if parseable. */
function parseServerErrorField(body: string | undefined): string | undefined {
  if (!body) return undefined
  try {
    const parsed = JSON.parse(body) as { error?: unknown; message?: unknown }
    if (typeof parsed.error === 'string' && parsed.error.trim().length > 0) return parsed.error
    if (typeof parsed.message === 'string' && parsed.message.trim().length > 0) return parsed.message
  } catch {
    // Not JSON (or unparsable) — no server-authored message to recover; fall
    // through to the generic getErrorMessage() behaviour.
  }
  return undefined
}

/**
 * getLibraryErrorMessage extracts a human-displayable string from a caught
 * Library mutation error, PREFERRING the server's own `error` field (when
 * present and recoverable) over the generic status-class default that
 * `getErrorMessage` would otherwise return for a "known" HTTP status.
 */
export function getLibraryErrorMessage(err: unknown, fallback: string): string {
  if (isApiError(err)) {
    const serverMessage = parseServerErrorField(err.body)
    if (serverMessage) return rewordServerGuidance(serverMessage)
  }
  return getErrorMessage(err, fallback)
}

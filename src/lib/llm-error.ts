/**
 * llm-error.ts — ADR-051 SPA-side error translation.
 *
 * The backend emits a typed `ErrorFrame` (live) and `ReplayErrorFrame`
 * (history replay) carrying an `llm_error` payload with a stable,
 * enumerated `code` and human-friendly `message`. This module is the
 * single SPA-side source of truth for translating those codes into
 * user-facing copy and for safely reading the typed payload off a
 * frame that may pre-date the contract (legacy frames lack it).
 *
 * Wire types live in `src/lib/api/generated/asyncapi-types.ts`
 * (`LLMError`, `LLMErrorReplay`) — this module intentionally re-declares
 * only the SHAPE of those payloads as plain TS types so the chat store
 * can stay decoupled from the generated barrel. The union of codes is
 * mirrored from the generated `LLMError['code']` enum; if the spec adds
 * a new code, regenerate the contract and add it here in the same PR.
 *
 * NOT a wire type (no `json:` tags, never crosses the gateway boundary) —
 * display-only. Per CLAUDE.md hard-constraint #8 this is explicitly not a
 * hand-written wire type; the canonical wire types are the generated ones.
 */

/**
 * The 7 stable backend error codes (see ADR-051 / `LLMError.code` in the
 * AsyncAPI contract). Kept as a literal union (not an enum) so it stays
 * plain JSON-serializable data and matches the generated `LLMError['code']`
 * type without an import cycle.
 */
export type LLMErrorCode =
  | 'media_unsupported'
  | 'provider_rejected'
  | 'rate_limited'
  | 'network'
  | 'content_policy'
  | 'context_too_long'
  | 'unknown'

/**
 * Display-side mirror of the generated `LLMError` wire payload.
 * `{ code, message, retryable, detail? }` — `detail` is optional and only
 * surfaced when `verboseChatEnabled` is on (see `getLLMErrorDisplay`).
 */
export interface LLMError {
  code: LLMErrorCode
  message: string
  retryable: boolean
  detail?: string
}

/**
 * Display-side mirror of the generated `LLMErrorReplay` wire payload.
 * Same as `LLMError` minus the verbose-only `detail` field — the replay
 * path strips provider internals before persisting, so history never
 * carries them.
 */
export interface LLMErrorReplay {
  code: LLMErrorCode
  message: string
  retryable: boolean
}

/**
 * Generic, user-facing copy per error code. Always shown regardless of the
 * verbose-chat preference — this is the minimum a user needs to understand
 * what went wrong. The raw `message` from the wire is NOT shown in the
 * bubble (it can leak provider internals / PII); verbose mode surfaces it
 * via the "Technical details" disclosure as `detail`.
 *
 * Copy is intentionally actionable — tells the user what happened and what
 * (if anything) they can do.
 */
export const codeToDisplay: Record<LLMErrorCode, string> = {
  media_unsupported:
    "That file type isn't supported by this model yet. Try a different format or model.",
  provider_rejected:
    'The model provider rejected the request. Try again, or switch models.',
  rate_limited:
    'The model provider is rate-limiting requests. Please wait a moment and retry.',
  network:
    "Couldn't reach the model provider. Check your connection and retry.",
  content_policy:
    'The model provider refused the request under its content policy.',
  context_too_long:
    "That conversation is too long for this model's context window. Start a fresh session or trim older turns.",
  unknown:
    'Something went wrong talking to the model. Please try again.',
}

/**
 * Resolve the user-facing copy for a code, with a safe fallback for an
 * unrecognized code (forward-compat: a newer backend may emit a code this
 * SPA build doesn't know yet — never throw, never render blank).
 */
export function codeToMessage(code: string | undefined): string {
  if (code && code in codeToDisplay) {
    return codeToDisplay[code as LLMErrorCode]
  }
  return codeToDisplay.unknown
}

/**
 * Compute the display shape for an LLM error.
 *
 * `message` is ALWAYS the generic code→display copy (stable, user-friendly,
 * never leaks provider internals). `detail` is included ONLY when BOTH
 * `verboseChatEnabled` is true AND `le.detail` is a non-empty string —
 * otherwise it is `undefined`, and the renderer must omit the "Technical
 * details" disclosure entirely (not just hide it).
 *
 * The typed `LLMErrorReplay` (replay path) carries no `detail` field, so
 * for replay frames `detail` is always `undefined` regardless of the
 * verbose preference — this is by design (history never persists provider
 * internals).
 */
export function getLLMErrorDisplay(
  le: { code: string; message: string; retryable: boolean; detail?: string },
  verboseChatEnabled: boolean,
): { message: string; detail?: string } {
  const message = codeToMessage(le.code)
  // Trim before the emptiness check — a whitespace-only detail carries no
  // information and would render as a blank "Technical details" disclosure.
  // Mirrors the renderer's own trim guard on `message.errorDetail`.
  const rawDetail = typeof le.detail === 'string' ? le.detail.trim() : ''
  const detail =
    verboseChatEnabled && rawDetail.length > 0 ? le.detail : undefined
  return detail ? { message, detail } : { message }
}

// ─── Safe optional accessors ─────────────────────────────────────────────────
//
// Legacy frames (pre-ADR-051 backend, or frames that never carried the
// typed payload — e.g. a cancel-ack synthesized on the gateway side) don't
// have `payload.llm_error`. These accessors cast `unknown` → typed and
// return `undefined` for any shape mismatch, so the reducer's fallback to
// `frame.message` stays a one-liner.
//
// The shape guards are deliberately permissive (object + a string `code`):
// they only exist to confirm a typed payload is present, not to validate
// its full contract — Zod already did that at the WS boundary.

function isTypedLLMError(value: unknown): value is { code: string; message: string; retryable: boolean; detail?: string } {
  if (typeof value !== 'object' || value === null) return false
  const v = value as Record<string, unknown>
  return typeof v.code === 'string' && typeof v.message === 'string' && typeof v.retryable === 'boolean'
}

/**
 * Read the typed `payload.llm_error` off a live `ErrorFrame`-shaped value.
 * Returns the (narrowed) `LLMError` shape when present and well-formed, else
 * `undefined`. The input is `unknown` so callers can pass a raw `frame`
 * without first narrowing the frame union.
 */
export function readLLMErrorFromFrame(frame: unknown): LLMError | undefined {
  if (typeof frame !== 'object' || frame === null) return undefined
  const f = frame as { payload?: unknown }
  const payload = f.payload
  if (typeof payload !== 'object' || payload === null) return undefined
  const p = payload as { llm_error?: unknown }
  if (!isTypedLLMError(p.llm_error)) return undefined
  // `detail` is optional on the wire; only copy it through if it's a string.
  const le = p.llm_error
  return {
    code: le.code as LLMErrorCode,
    message: le.message,
    retryable: le.retryable,
    ...(typeof le.detail === 'string' ? { detail: le.detail } : {}),
  }
}

/**
 * Read the typed `payload.llm_error` off a `ReplayErrorFrame`-shaped value.
 * Replay payloads have no `detail`, so the returned shape is the narrower
 * `LLMErrorReplay`. Returns `undefined` for legacy frames / shape mismatch.
 */
export function readLLMErrorFromReplayFrame(frame: unknown): LLMErrorReplay | undefined {
  if (typeof frame !== 'object' || frame === null) return undefined
  const f = frame as { payload?: unknown }
  const payload = f.payload
  if (typeof payload !== 'object' || payload === null) return undefined
  const p = payload as { llm_error?: unknown }
  if (!isTypedLLMError(p.llm_error)) return undefined
  const le = p.llm_error
  return {
    code: le.code as LLMErrorCode,
    message: le.message,
    retryable: le.retryable,
  }
}

/**
 * Read the optional `entry_id` off a replay/error frame. Used for live↔replay
 * dedup so a live error bubble that already rendered isn't duplicated when
 * history is reloaded. Returns `undefined` for frames without one.
 */
export function readEntryIdFromFrame(frame: unknown): string | undefined {
  if (typeof frame !== 'object' || frame === null) return undefined
  const f = frame as { entry_id?: unknown }
  return typeof f.entry_id === 'string' ? f.entry_id : undefined
}

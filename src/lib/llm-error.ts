import type {
  LLMError as GeneratedLLMError,
  LLMErrorReplay as GeneratedLLMErrorReplay,
} from '@/lib/api/generated/asyncapi-types'
import {
  llmErrorUserAttributions,
  llmErrorUserMessages,
  type LLMErrorAttribution,
} from '@/lib/api/generated/llm-error-messages'

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
 * (`LLMError`, `LLMErrorReplay`) — this module aliases them so the chat
 * store can stay decoupled from the generated barrel's exact name while
 * remaining pinned to the regenerated enum union (compile-time gate via
 * `Record<LLMErrorCode, string>` below — adding a new code without
 * regenerating and updating the copy map fails `tsc -b --noEmit`).
 *
 * NOT a wire type (no `json:` tags, never crosses the gateway boundary) —
 * display-only. Per CLAUDE.md hard-constraint #8 the canonical wire types
 * are the generated ones; this file aliases them rather than mirroring
 * them so the manually-maintained surface can never drift from the spec.
 */
export type LLMError = GeneratedLLMError
export type LLMErrorReplay = GeneratedLLMErrorReplay
export type LLMErrorCode = GeneratedLLMError['code']

/**
 * Generic, user-facing copy per error code. Always shown regardless of the
 * verbose-chat preference — this is the minimum a user needs to understand
 * what went wrong. The raw `message` from the wire is NOT shown in the
 * bubble (it can leak provider internals / PII); verbose mode surfaces it
 * via the "Technical details" disclosure as `detail`.
 *
 * GENERATED CONTENT — re-exported from
 * `@/lib/api/generated/llm-error-messages`, which is emitted from the
 * `x-user-messages` block on `components.schemas.LLMError` in
 * `contracts/asyncapi.yaml`. To change a message, edit the contract and re-run
 * `make gen-contracts`. The Go catalogue that backs the persisted transcript
 * (`pkg/api/generated/llm_error_messages.gen.go`) is emitted from the same
 * block, so the bubble and the transcript cannot drift — previously these were
 * two hand-written maps that had to be edited in lockstep, and weren't.
 *
 * Attribution is written into each sentence rather than carried by a blanket
 * "From the model:" prefix. That prefix was deleted: it blamed the model for
 * every failure, including the ones Omnipus causes (an oversized request we
 * built) and the ones an operator fixes in Settings (a bad API key). The
 * machine-readable tag now lives in `codeToAttribution`.
 *
 * The `Record<LLMErrorCode, string>` annotation is retained as a compile-time
 * gate: a code in the generated AsyncAPI types with no catalogue entry (or an
 * entry for a code that no longer exists) fails `tsc -b --noEmit`.
 */
export const codeToDisplay: Record<LLMErrorCode, string> = llmErrorUserMessages

/**
 * Who owns the fault behind each code — `model`, `provider`, `product` (ours),
 * `config` (operator-fixable), `ambiguous`, or `unknown`. Generated from the
 * same contract block as `codeToDisplay`.
 *
 * Consumed by the copy-rule tests (a `product`/`config` message must never tell
 * the user to switch models, rephrase their content, or — for `config` — retry)
 * and available to any renderer that wants to style the bubble by fault owner
 * rather than by code.
 */
export const codeToAttribution: Record<LLMErrorCode, LLMErrorAttribution> =
  llmErrorUserAttributions

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

/**
 * D5 fix (UAT "Site 3") — a legacy/synthesized `ErrorFrame` (no typed
 * `llm_error` payload — an auth-handshake rejection, a workspace-setup
 * kickoff reject, a gateway-synthesized cancel-ack, etc.) falls back to the
 * raw wire `message` for display in `store/chat.ts`'s `case 'error'`. Most
 * of those ARE deliberately-authored, human-readable strings (see the
 * `Message:` literals in pkg/gateway/websocket.go) — but a raw Go-internal/
 * protocol string can still reach this fallback: an unwrapped backend error,
 * or any future ErrorFrame the backend hasn't wrapped in the typed payload
 * yet. This is the safety net for that path — it collapses anything that
 * LOOKS like internal protocol/Go-error shape into a generic, human message
 * instead of showing it verbatim:
 *   - a `{"type":"..."}` JSON-ish wire frame accidentally strung into the
 *     message field instead of being parsed, or
 *   - the Go `<component>: <verb...>` error-wrapping convention (a single
 *     lowercase/snake_case/dotted identifier immediately followed by
 *     `": "`, e.g. "browser_control: attach before requesting control",
 *     "browser_attach: agent_id and session_id are required") — a
 *     deliberately-authored user-facing sentence never starts this way (see
 *     the `Message:` literals above — "cancel failed: ...", "workspace
 *     setup has already run", etc. all have a SPACE, not a bare identifier,
 *     before their first colon or none at all), so this is a conservative,
 *     low-false-positive signal.
 *
 * The concrete leaking strings this UAT finding traced (the WS auth-
 * handshake rejection messages) were fixed at their SOURCE — see
 * `wsAuthErrBadFirstFrame`/`wsAuthErrInvalidToken`/`wsAuthErrNoUsers` in
 * pkg/gateway/websocket.go — this function is defense-in-depth for anything
 * else that reaches the same raw-fallback path.
 */
export function sanitizeLegacyErrorMessage(raw: string): string {
  const trimmed = raw.trim()
  if (trimmed === '') return raw
  if (/^\{"type"\s*:/.test(trimmed)) return 'Something went wrong — please try again.'
  if (/^[a-z][a-z0-9_]*(\.[a-z0-9_]+)*:\s/.test(trimmed)) return 'Something went wrong — please try again.'
  return raw
}
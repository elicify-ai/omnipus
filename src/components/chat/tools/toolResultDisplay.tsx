/**
 * toolResultDisplay — the ONE place that resolves a persisted tool-call
 * `result` (string | undefined/null | structured sentinel | any other JSON
 * value) into a renderable description, and renders the structured sentinel
 * displays.
 *
 * Gate fix round (2026-09-26, findings 1+2 of the ctui-gate): the five
 * dedicated tool blocks (BashOutputBlock / FileReadBlock / FileTreeBlock /
 * WebSearchBlock / WebFetchBlock) each did `String(result)` on the replay
 * path. On a persisted non-string result — a `{ text }` envelope
 * (pkg/agent/loop_run_turn_tools.go::finishCall), a server truncation /
 * offload sentinel (pkg/gateway/replay.go::truncateResult), a marshal-error
 * sentinel, or one of the three structured-failure sentinels — that prints
 * the literal text "[object Object]", and a large read_file displayed
 * "1 lines" (the string was one line of that garbage). GenericToolCall
 * already handled every one of these shapes; the blocks did not.
 *
 * The four display components below were MOVED here verbatim from
 * GenericToolCall.tsx (same markup, same data-testids) rather than copied:
 * the sentinels module's own F1 history (toolResultSentinels.ts header) is
 * the evidence that a second copy of this job silently drifts. GenericToolCall
 * imports them from here now.
 */

import { useState } from 'react'
import {
  ArrowsClockwise,
  XCircle,
  Prohibit,
  Lock,
  Warning,
  DownloadSimple,
} from '@phosphor-icons/react'
import { useQuery } from '@tanstack/react-query'
import { fetchToolResult } from '@/lib/api'
import { Button } from '@/components/ui/button'
import type { TruncatedResult, MarshalErrorResult } from '@/lib/ws'
import type {
  ToolResultRef,
  DelegationFailure,
  FileExistsRefusal,
  PermissionDenied,
} from '@/lib/api/generated/asyncapi-types'
import { isClientTruncatedResult, isToolResultRef } from '@/store/chat'
import type { ClientTruncatedResult } from '@/store/chat'
import {
  detectToolResultSentinels,
  policyAxisLabel,
} from './toolResultSentinels'

// ── Detection helpers (moved verbatim from GenericToolCall.tsx) ──────────────

/** Returns true when the result is the truncation sentinel from replay.go:truncateResult. */
export function isTruncatedResult(value: unknown): value is TruncatedResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['_truncated'] === true
  )
}

/** Returns true when the result is the marshal-error sentinel from replay.go. */
export function isMarshalErrorResult(value: unknown): value is MarshalErrorResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>)['_marshal_error'] === 'string'
  )
}

/**
 * GenericToolCall.tsx::safeJson, moved here so the dedicated blocks' JSON
 * fallback behaves byte-identically to the generic badge's.
 */
export function safeJson(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

// ── Resolution ───────────────────────────────────────────────────────────────

/**
 * The resolution of one persisted tool-call result. `text` is the ordinary
 * string path every block renders with its own body; every other kind is a
 * structured payload that must NEVER reach a `String()` call.
 */
export type ResolvedToolResult =
  | { kind: 'ref'; sentinel: ToolResultRef }
  | { kind: 'truncated'; sentinel: TruncatedResult }
  | { kind: 'clientTruncated'; sentinel: ClientTruncatedResult }
  | { kind: 'marshalError'; sentinel: MarshalErrorResult }
  | { kind: 'delegationDenied'; failure: DelegationFailure }
  | { kind: 'fileExists'; refusal: FileExistsRefusal }
  | { kind: 'permissionDenied'; failure: PermissionDenied }
  | { kind: 'text'; text: string }
  | { kind: 'json'; text: string }
  | { kind: 'empty' }

/**
 * True when a resolution carries a structured payload that needs the shared
 * sentinel rendering (ToolResultSentinelBody below) instead of the block's
 * own plain-text body.
 */
export function isSentinelResolution(
  resolved: ResolvedToolResult,
): boolean {
  return (
    resolved.kind === 'ref' ||
    resolved.kind === 'truncated' ||
    resolved.kind === 'clientTruncated' ||
    resolved.kind === 'marshalError' ||
    resolved.kind === 'delegationDenied' ||
    resolved.kind === 'fileExists' ||
    resolved.kind === 'permissionDenied'
  )
}

/**
 * Resolves a persisted tool-call result into its renderable description.
 * Detection order mirrors GenericToolCall's own render chain: the three
 * structured-failure sentinels first, then the transport sentinels (ref >
 * server-truncated > client-truncated > marshal-error), then the plain
 * string, then the JSON fallback for every other value. `JSON.stringify` —
 * never `String()` — is the fallback for a non-string object, so a
 * `{ text }` envelope renders as formatted JSON instead of
 * "[object Object]".
 */
export function resolveToolResult(result: unknown): ResolvedToolResult {
  if (result === undefined || result === null || result === '') {
    return { kind: 'empty' }
  }
  const sentinels = detectToolResultSentinels(result)
  if (sentinels.delegationFailure) {
    return { kind: 'delegationDenied', failure: sentinels.delegationFailure }
  }
  if (sentinels.fileExistsRefusal) {
    return { kind: 'fileExists', refusal: sentinels.fileExistsRefusal }
  }
  if (sentinels.permissionDenied) {
    return { kind: 'permissionDenied', failure: sentinels.permissionDenied }
  }
  if (isToolResultRef(result)) return { kind: 'ref', sentinel: result }
  if (isTruncatedResult(result)) return { kind: 'truncated', sentinel: result }
  if (isClientTruncatedResult(result)) {
    return { kind: 'clientTruncated', sentinel: result }
  }
  if (isMarshalErrorResult(result)) {
    return { kind: 'marshalError', sentinel: result }
  }
  if (typeof result === 'string') return { kind: 'text', text: result }
  try {
    return { kind: 'json', text: JSON.stringify(result, null, 2) }
  } catch {
    return { kind: 'json', text: String(result) }
  }
}

// ── Shared sentinel displays (moved verbatim from GenericToolCall.tsx) ───────

/** Format bytes into a human-readable size string (e.g. "2.3 MiB"). */
function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

/** Renders a server-side ToolResultRef sentinel; full body fetched lazily on click. */
export function ToolResultRefDisplay({
  sentinel,
  sessionId,
}: {
  sentinel: ToolResultRef
  sessionId: string
}) {
  const [fetchEnabled, setFetchEnabled] = useState(false)

  const { data, isFetching, isError, error } = useQuery<unknown, Error>({
    queryKey: ['tool-result-ref', sessionId, sentinel.ref],
    queryFn: () => fetchToolResult(sessionId, sentinel.ref),
    enabled: fetchEnabled && sessionId !== '',
    // Cache the result for the lifetime of this component tree — do not re-fetch on focus.
    staleTime: Infinity,
    gcTime: 5 * 60 * 1000,
    retry: 1,
  })

  return (
    <div data-testid="result-tool-ref">
      {/* Banner — flat: a warning-tinted left accent stands in for the old
          amber box (ticket "Tool components in chat"); text stays warning-colored. */}
      <div className="flex items-start gap-[var(--space-2)] border-l-2 border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] pl-[var(--space-2)] py-[var(--space-1)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <Warning size={12} weight="fill" className="shrink-0 mt-[var(--space-0-5)]" />
        <span>
          Result stored server-side ({humanSize(sentinel.original_size_bytes)}) — preview only
        </span>
      </div>

      {/* Preview */}
      {!data && (
        <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto mb-[var(--space-1)]">
          {sentinel.preview}
        </pre>
      )}

      {/* Full result once fetched */}
      {data !== undefined && (
        <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-96 overflow-auto mb-[var(--space-1)]">
          {safeJson(data)}
        </pre>
      )}

      {/* Fetch error */}
      {isError && (
        <div className="text-[var(--color-error)] text-[length:var(--type-caption-size)] font-body mb-[var(--space-1)]">
          Failed to load: {error?.message ?? 'unknown error'}
        </div>
      )}

      {/* Fetch button — hidden once data is loaded */}
      {!data && !isError && (
        <Button
          type="button"
          variant="link"
          onClick={() => setFetchEnabled(true)}
          disabled={isFetching}
          className="rounded-none flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] font-body disabled:cursor-wait"
        >
          {isFetching ? (
            <ArrowsClockwise size={11} className="animate-spin" />
          ) : (
            <DownloadSimple size={11} />
          )}
          {isFetching ? 'Loading...' : 'Show full output'}
        </Button>
      )}
    </div>
  )
}

/**
 * G4: Renders a client-side truncation sentinel (ClientTruncatedResult).
 * The full body never reached the SPA (clamped before storage), so there is
 * no fetch button — only the preview and an explanatory hint.
 */
export function ClientTruncatedDisplay({ sentinel }: { sentinel: ClientTruncatedResult }) {
  return (
    <div data-testid="result-client-truncated">
      {/* Flat: warning-tinted left accent instead of the old amber box; text stays warning-colored. */}
      <div className="flex items-start gap-[var(--space-2)] border-l-2 border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] pl-[var(--space-2)] py-[var(--space-1)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)] text-[var(--color-warning)]">
        <Warning size={12} weight="fill" className="shrink-0 mt-[var(--space-0-5)]" />
        <span>
          Truncated client-side — showing first 4 KiB of {humanSize(sentinel.original_size_bytes)}.
          The full result is preserved in the server transcript.
        </span>
      </div>
      <pre className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
        {sentinel.preview}
      </pre>
    </div>
  )
}

/**
 * BLOCKER 2: Renders the structured delegation-denied sentinel the backend emits
 * when a delegation tool call is refused by policy. Without this path a denied
 * delegation falls through to plainResult and renders as a raw JSON blob inside a
 * collapsed "Failed" tool call. We surface the human `reason`, the `policy` axis
 * that blocked it (trust set / mode / depth), and the target agent when present.
 */
export function DelegationFailureDisplay({ failure }: { failure: DelegationFailure }) {
  return (
    // Flat: a warning-tinted left accent replaces the old bordered/tinted
    // box (ticket "Tool components in chat") — icon/label text stay warning-colored.
    <div
      data-testid="result-delegation-denied"
      className="border-l-2 pl-[var(--space-2)] py-[var(--space-2)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)]"
      style={{
        borderColor: 'color-mix(in srgb, var(--color-warning) 60%, transparent)',
      }}
    >
      <div className="flex items-center gap-[var(--space-2)] mb-[var(--space-1)]">
        <Prohibit
          size={13}
          weight="fill"
          className="shrink-0"
          style={{ color: 'var(--color-warning)' }}
        />
        <span className="font-medium" style={{ color: 'var(--color-warning)' }}>
          Delegation denied
        </span>
      </div>

      {/* Human-readable reason */}
      <p className="text-[var(--color-secondary)] leading-relaxed mb-[var(--space-1)] break-words">
        {failure.reason}
      </p>

      {/* Policy axis + target agent metadata */}
      <dl className="grid grid-cols-[auto_1fr] gap-x-[var(--space-2)] gap-y-[var(--space-0-5)] text-[var(--color-muted)]">
        <dt>Blocked by</dt>
        <dd className="text-[var(--color-secondary)]">{policyAxisLabel(failure.policy)}</dd>
        {failure.target_agent_id && (
          <>
            <dt>Target agent</dt>
            <dd className="text-[var(--color-secondary)] break-all">{failure.target_agent_id}</dd>
          </>
        )}
      </dl>
    </div>
  )
}

/**
 * Renders the file-exists precondition refusal (ADR-059 W5) — extracted from
 * GenericToolCall's inline JSX so the dedicated blocks render the identical
 * block instead of a second copy.
 */
export function FileExistsRefusalDisplay({ refusal }: { refusal: FileExistsRefusal }) {
  return (
    <div
      data-testid="result-file-exists"
      className="border-l-2 pl-[var(--space-2)] py-[var(--space-2)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)] text-[var(--color-secondary)]"
      style={{ borderColor: 'color-mix(in srgb, var(--color-warning) 60%, transparent)' }}
    >
      {refusal.reason}
    </div>
  )
}

/**
 * Renders the structured permission-denied sentinel (issue #618). Mirrors
 * DelegationFailureDisplay's layout: a warning-tinted left accent, the
 * model-facing message, and the tool/reason/permanent metadata — so a
 * permission denial reads as a distinct, human-readable block instead of a
 * raw JSON blob, matching the treatment the other two structured-failure
 * members already get.
 */
export function PermissionDeniedDisplay({ failure }: { failure: PermissionDenied }) {
  return (
    <div
      data-testid="result-permission-denied"
      className="border-l-2 pl-[var(--space-2)] py-[var(--space-2)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)]"
      style={{
        borderColor: 'color-mix(in srgb, var(--color-warning) 60%, transparent)',
      }}
    >
      <div className="flex items-center gap-[var(--space-2)] mb-[var(--space-1)]">
        <Lock
          size={13}
          weight="fill"
          className="shrink-0"
          style={{ color: 'var(--color-warning)' }}
        />
        <span className="font-medium" style={{ color: 'var(--color-warning)' }}>
          Permission denied
        </span>
      </div>

      <p className="text-[var(--color-secondary)] leading-relaxed mb-[var(--space-1)] break-words">
        {failure.message}
      </p>

      <dl className="grid grid-cols-[auto_1fr] gap-x-[var(--space-2)] gap-y-[var(--space-0-5)] text-[var(--color-muted)]">
        <dt>Tool</dt>
        <dd className="text-[var(--color-secondary)] break-all">{failure.tool}</dd>
        <dt>Reason</dt>
        <dd className="text-[var(--color-secondary)] break-words">{failure.reason}</dd>
        <dt>Retry</dt>
        <dd className="text-[var(--color-secondary)]">
          {failure.permanent === false ? 'May succeed later this turn' : 'Not this turn'}
        </dd>
      </dl>
    </div>
  )
}

// ── Shared sentinel body ─────────────────────────────────────────────────────

/**
 * Renders the structured half of a resolved tool result — every sentinel
 * kind (ref / server-truncated / client-truncated / marshal-error /
 * delegation-denied / file-exists / permission-denied). Returns null for the
 * text/json/empty kinds so a block can render it unconditionally next to its
 * own plain-text body.
 *
 * `sessionId` is required for the ref kind's lazy full-body fetch (the same
 * session-scoped fetchToolResult call GenericToolCall's ToolResultRefDisplay
 * makes); a ref arriving with no sessionId still renders its preview and
 * banner, with the fetch disabled — the GenericToolCall default-sessionId
 * behavior.
 */
export function ToolResultSentinelBody({
  resolved,
  sessionId,
}: {
  resolved: ResolvedToolResult
  sessionId?: string
}) {
  if (resolved.kind === 'ref') {
    return <ToolResultRefDisplay sentinel={resolved.sentinel} sessionId={sessionId ?? ''} />
  }
  if (resolved.kind === 'truncated') {
    return (
      <>
        <div
          data-testid="result-truncated-banner"
          className="flex items-start gap-[var(--space-2)] border-l-2 border-[color-mix(in_srgb,var(--color-warning)_40%,transparent)] pl-[var(--space-2)] py-[var(--space-1)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)] text-[var(--color-warning)]"
        >
          <Warning size={12} weight="fill" className="shrink-0 mt-[var(--space-0-5)]" />
          <span>
            Truncated — showing first 10 KiB of {humanSize(resolved.sentinel.original_size_bytes)}
          </span>
        </div>
        <pre
          tabIndex={0}
          role="region"
          aria-label="Tool output"
          className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
        >
          {resolved.sentinel.preview}
        </pre>
      </>
    )
  }
  if (resolved.kind === 'clientTruncated') {
    return <ClientTruncatedDisplay sentinel={resolved.sentinel} />
  }
  if (resolved.kind === 'marshalError') {
    return (
      <div
        data-testid="result-marshal-error"
        className="flex items-start gap-[var(--space-2)] border-l-2 border-[var(--color-error)]/40 pl-[var(--space-2)] py-[var(--space-1)] mb-[var(--space-1)] font-body text-[length:var(--type-caption-size)] text-[var(--color-error)]"
      >
        <XCircle size={12} weight="fill" className="shrink-0 mt-[var(--space-0-5)]" />
        <span>Result serialization failed: {resolved.sentinel._marshal_error}</span>
      </div>
    )
  }
  if (resolved.kind === 'delegationDenied') {
    return <DelegationFailureDisplay failure={resolved.failure} />
  }
  if (resolved.kind === 'fileExists') {
    return <FileExistsRefusalDisplay refusal={resolved.refusal} />
  }
  if (resolved.kind === 'permissionDenied') {
    return <PermissionDeniedDisplay failure={resolved.failure} />
  }
  return null
}

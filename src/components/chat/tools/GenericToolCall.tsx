import { useState } from 'react'
import {
  Wrench,
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  Prohibit,
  CaretDown,
  CaretUp,
  Warning,
  DownloadSimple,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { TruncatedResult, MarshalErrorResult } from '@/lib/ws'
import type { ToolResultRef } from '@/lib/api/generated/asyncapi-types'
import { isClientTruncatedResult, isToolResultRef } from '@/store/chat'
import type { ClientTruncatedResult } from '@/store/chat'
import { useQuery } from '@tanstack/react-query'
import { fetchToolResult } from '@/lib/api'

interface GenericToolCallProps {
  toolName: string
  args?: unknown
  result?: unknown
  status: MessagePartStatus
  /** Optional error text from the store */
  error?: string
  /** Optional duration in milliseconds */
  durationMs?: number
  /** Lite-mode: tool calls start collapsed so the virtualizer skips measuring large expanded content. */
  defaultCollapsed?: boolean
  /** Session this tool call belongs to. Required to fetch ToolResultRef bodies session-scoped. */
  sessionId?: string
}

function formatDuration(ms?: number): string {
  if (!ms) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

function safeJson(value: unknown): string {
  if (value === undefined || value === null) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

/** Returns true when the result is the truncation sentinel from replay.go:truncateResult. */
function isTruncatedResult(value: unknown): value is TruncatedResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['_truncated'] === true
  )
}

/** Returns true when the result is the marshal-error sentinel from replay.go. */
function isMarshalErrorResult(value: unknown): value is MarshalErrorResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>)['_marshal_error'] === 'string'
  )
}

/** Format bytes into a human-readable size string (e.g. "2.3 MiB"). */
function humanSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`
}

// Renders a server-side ToolResultRef sentinel; full body fetched lazily on click.
function ToolResultRefDisplay({
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
      {/* Banner */}
      <div className="flex items-start gap-2 rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1.5 mb-1 font-sans text-[10px] text-amber-400">
        <Warning size={12} weight="fill" className="shrink-0 mt-0.5" />
        <span>
          Result stored server-side ({humanSize(sentinel.original_size_bytes)}) — preview only
        </span>
      </div>

      {/* Preview */}
      {!data && (
        <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto mb-1">
          {sentinel.preview}
        </pre>
      )}

      {/* Full result once fetched */}
      {data !== undefined && (
        <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-96 overflow-auto mb-1">
          {safeJson(data)}
        </pre>
      )}

      {/* Fetch error */}
      {isError && (
        <div className="text-[var(--color-error)] text-[10px] font-sans mb-1">
          Failed to load: {error?.message ?? 'unknown error'}
        </div>
      )}

      {/* Fetch button — hidden once data is loaded */}
      {!data && !isError && (
        <button
          type="button"
          onClick={() => setFetchEnabled(true)}
          disabled={isFetching}
          className="flex items-center gap-1.5 text-[10px] font-sans text-[var(--color-accent)] hover:underline disabled:opacity-50 disabled:cursor-wait"
        >
          {isFetching ? (
            <ArrowsClockwise size={11} className="animate-spin" />
          ) : (
            <DownloadSimple size={11} />
          )}
          {isFetching ? 'Loading...' : 'Show full output'}
        </button>
      )}
    </div>
  )
}

/**
 * G4: Renders a client-side truncation sentinel (ClientTruncatedResult).
 * The full body never reached the SPA (clamped before storage), so there is
 * no fetch button — only the preview and an explanatory hint.
 */
function ClientTruncatedDisplay({ sentinel }: { sentinel: ClientTruncatedResult }) {
  return (
    <div data-testid="result-client-truncated">
      <div className="flex items-start gap-2 rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1.5 mb-1 font-sans text-[10px] text-amber-400">
        <Warning size={12} weight="fill" className="shrink-0 mt-0.5" />
        <span>
          Truncated client-side — showing first 4 KiB of {humanSize(sentinel.original_size_bytes)}.
          The full result is preserved in the server transcript.
        </span>
      </div>
      <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
        {sentinel.preview}
      </pre>
    </div>
  )
}

export function GenericToolCall({
  toolName,
  args,
  result,
  status,
  error,
  durationMs,
  // defaultCollapsed: accepted but not used — tool calls always start collapsed.
  defaultCollapsed: _defaultCollapsed,
  sessionId = '',
}: GenericToolCallProps) {
  const [expanded, setExpanded] = useState(false)

  const isRunning = status.type === 'running'
  const isError = status.type === 'incomplete' || !!error
  // AssistantUI's MessagePartStatus does not expose `reason` on the `incomplete` variant in its
  // public types, so we narrow with `'reason' in status` before casting to access it safely.
  const isCancelled = status.type === 'incomplete' && 'reason' in status && (status as { reason?: string }).reason === 'cancelled'

  const statusConfig = isRunning
    ? { icon: <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />, label: 'Running...', border: 'border-[var(--color-border)]' }
    : isCancelled
    ? { icon: <Prohibit size={12} weight="fill" className="text-[var(--color-muted)]" />, label: 'Cancelled', border: 'border-[var(--color-border)]' }
    : isError
    ? { icon: <XCircle size={12} weight="fill" className="text-[var(--color-error)]" />, label: 'Failed', border: 'border-[var(--color-error)]/20' }
    : { icon: <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />, label: formatDuration(durationMs) || 'Done', border: 'border-[var(--color-success)]/20' }

  const hasDetail = !isRunning && (args !== undefined || result !== undefined || error)

  // Resolve result rendering: determine which sentinel type (if any) applies.
  const truncated = isTruncatedResult(result) ? result : null
  const marshalErr = isMarshalErrorResult(result) ? result : null
  const clientTruncated = isClientTruncatedResult(result) ? result : null
  const toolRef = isToolResultRef(result) ? result : null
  const plainResult = !truncated && !marshalErr && !clientTruncated && !toolRef ? result : undefined

  return (
    <div
      data-testid="tool-call-badge"
      data-tool={toolName}
      className={cn('mt-2 rounded-md border bg-[var(--color-surface-1)] text-xs font-mono overflow-hidden', statusConfig.border)}
    >
      {/* Header row */}
      <button
        type="button"
        onClick={() => hasDetail && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 text-left transition-colors',
          hasDetail && 'hover:bg-[var(--color-surface-2)] cursor-pointer',
          !hasDetail && 'cursor-default'
        )}
        aria-expanded={expanded}
        disabled={!hasDetail}
      >
        <Wrench size={13} className="text-[var(--color-muted)] shrink-0" />
        <span className="text-[var(--color-secondary)] font-medium">{toolName}</span>
        <span className="flex items-center gap-1 ml-1">
          {statusConfig.icon}
          <span className="text-[var(--color-muted)]">{statusConfig.label}</span>
        </span>
        {hasDetail && (
          <span className="ml-auto text-[var(--color-muted)]">
            {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
          </span>
        )}
      </button>

      {/* Expanded detail */}
      {expanded && hasDetail && (
        <div className="border-t border-[var(--color-border)] px-3 py-2 space-y-2">
          {args !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1 font-sans">Parameters</div>
              <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
                {safeJson(args)}
              </pre>
            </div>
          )}

          {/* Result section — five rendering paths */}
          {result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1 font-sans">Result</div>

              {/* Marshal-error sentinel: result could not be serialized */}
              {marshalErr && (
                <div
                  data-testid="result-marshal-error"
                  className="flex items-start gap-2 rounded border border-[var(--color-error)]/40 bg-[var(--color-error)]/10 px-2 py-1.5 mb-1 font-sans text-[10px] text-[var(--color-error)]"
                >
                  <XCircle size={12} weight="fill" className="shrink-0 mt-0.5" />
                  <span>Result serialization failed: {marshalErr._marshal_error}</span>
                </div>
              )}

              {/* Server-truncated sentinel: result exceeded 10 KiB server-side */}
              {truncated && (
                <>
                  <div
                    data-testid="result-truncated-banner"
                    className="flex items-start gap-2 rounded border border-amber-500/40 bg-amber-500/10 px-2 py-1.5 mb-1 font-sans text-[10px] text-amber-400"
                  >
                    <Warning size={12} weight="fill" className="shrink-0 mt-0.5" />
                    <span>
                      Truncated — showing first 10 KiB of {humanSize(truncated.original_size_bytes)}
                    </span>
                  </div>
                  <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
                    {truncated.preview}
                  </pre>
                </>
              )}

              {/* G4: Client-truncated sentinel — SPA clamped before storage, no server copy */}
              {clientTruncated && <ClientTruncatedDisplay sentinel={clientTruncated} />}

              {/* G4: ToolResultRef sentinel — server stored full body, fetch on demand */}
              {toolRef && <ToolResultRefDisplay sentinel={toolRef} sessionId={sessionId} />}

              {/* Plain result: normal rendering */}
              {plainResult !== undefined && (
                <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
                  {safeJson(plainResult)}
                </pre>
              )}
            </div>
          )}

          {error && (
            <div className="text-[var(--color-error)] text-[10px] font-sans">{error}</div>
          )}
        </div>
      )}
    </div>
  )
}

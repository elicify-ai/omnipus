import { useState } from 'react'
import {
  Wrench,
  ArrowsClockwise,
  XCircle,
  Prohibit,
  CaretDown,
  CaretUp,
  Warning,
  DownloadSimple,
  Broadcast,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import type { MessagePartStatus } from '@assistant-ui/react'
import type { TruncatedResult, MarshalErrorResult } from '@/lib/ws'
import type { ToolResultRef, DelegationFailure } from '@/lib/api/generated/asyncapi-types'
import { isClientTruncatedResult, isToolResultRef } from '@/store/chat'
import type { ClientTruncatedResult } from '@/store/chat'
import { useQuery } from '@tanstack/react-query'
import { fetchToolResult } from '@/lib/api'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, type ToolBadgeStatusConfig } from '@/lib/toolStatusConfig'

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

/**
 * Returns true when the result is the structured delegation-denied sentinel the
 * backend emits when a delegation tool call is refused by policy. Mirrors the
 * sentinel-type detector pattern above; matched against the generated
 * DelegationFailure contract (error: "delegation_denied").
 */
function isDelegationFailure(value: unknown): value is DelegationFailure {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['error'] === 'delegation_denied'
  )
}

/** Human label for the policy axis that blocked the delegation. */
function policyAxisLabel(policy: DelegationFailure['policy']): string {
  switch (policy) {
    case 'trust_set':
      return 'Trust set'
    case 'mode':
      return 'Delegation mode'
    case 'depth':
      return 'Delegation depth'
    default:
      return policy
  }
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

/**
 * BLOCKER 2: Renders the structured delegation-denied sentinel the backend emits
 * when a delegation tool call is refused by policy. Without this path a denied
 * delegation falls through to plainResult and renders as a raw JSON blob inside a
 * collapsed "Failed" tool call. We surface the human `reason`, the `policy` axis
 * that blocked it (trust set / mode / depth), and the target agent when present.
 */
function DelegationFailureDisplay({ failure }: { failure: DelegationFailure }) {
  return (
    <div
      data-testid="result-delegation-denied"
      className="rounded border px-2.5 py-2 mb-1 font-sans text-[10px]"
      style={{
        borderColor: 'color-mix(in srgb, var(--color-warning) 40%, transparent)',
        backgroundColor: 'color-mix(in srgb, var(--color-warning) 10%, transparent)',
      }}
    >
      <div className="flex items-center gap-2 mb-1.5">
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
      <p className="text-[var(--color-secondary)] leading-relaxed mb-1.5 break-words">
        {failure.reason}
      </p>

      {/* Policy axis + target agent metadata */}
      <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-0.5 text-[var(--color-muted)]">
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
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)

  const isRunning = status.type === 'running'
  const isError = status.type === 'incomplete' || !!error
  // AssistantUI's MessagePartStatus does not expose `reason` on the `incomplete` variant in its
  // public types, so we narrow with `'reason' in status` before casting to access it safely.
  const isCancelled = status.type === 'incomplete' && 'reason' in status && (status as { reason?: string }).reason === 'cancelled'

  // G17: a delegation denial is an error-status result; surface it in the
  // COLLAPSED header ("Delegation denied · <axis>") instead of a generic
  // "Failed" so the user sees the policy block without expanding. The full
  // reason stays in the expanded DelegationFailureDisplay. Computed here
  // (rather than after the gate below) because the gate needs it too — a
  // delegation denial is an error outcome even when `status` alone doesn't
  // say so.
  const delegationFailure = isDelegationFailure(result) ? result : null

  // Marshal-error sentinel: the backend emits `{_marshal_error: "..."}` when
  // JSON-marshaling a tool result fails during replay-frame construction —
  // this can happen even when the tool call itself succeeded, so neither
  // `status` nor `error` reflects it. Computed here (before the gate below,
  // alongside delegationFailure) because the gate needs it too: a
  // load_tool/background bash/delegate call whose args match the "hide by
  // default" shape must still surface if its result silently failed to
  // marshal, exactly like a policy-denied delegation must.
  const marshalErr = isMarshalErrorResult(result) ? result : null

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (load_tool, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. An
  // error/denial/marshal-failure outcome always overrides the hide decision —
  // a policy-denied delegation or a marshal-error result must never disappear
  // just because its default args look like ordinary background dispatch.
  // Must sit after every hook above and before the JSX return (Rules of Hooks).
  if (
    !shouldRenderToolCall(
      toolName,
      args as Record<string, unknown> | undefined,
      verboseChatEnabled,
      isError || !!delegationFailure || !!marshalErr,
    )
  ) {
    return null
  }

  // ADR-038: browser tool calls expose a "Watch live" launcher on their
  // finalized/replayed row too. Browser tools complete sub-second, so the live
  // AssistantUI block (which carries its own "Watch live") is essentially never
  // seen — this GenericToolCall row is what the user actually looks at once the
  // turn finalizes. Mirrors the web_serve replay-parity precedent (ChatScreen).
  const isBrowserTool = /^browser[._]/.test(toolName)
  function handleWatchLive() {
    const { activeSessionId, activeAgentId } = useSessionStore.getState()
    const sid = sessionId || activeSessionId
    if (!sid || !activeAgentId) {
      useUiStore.getState().addToast({ message: 'No active session to watch.', variant: 'error' })
      return
    }
    useUiStore.getState().openBrowserPanel(sid, activeAgentId)
  }

  let statusConfig: ToolBadgeStatusConfig
  if (isRunning) {
    statusConfig = getToolBadgeStatusConfig('running', { size: 12 })
  } else if (isCancelled) {
    statusConfig = getToolBadgeStatusConfig('cancelled', { size: 12, cancelledVariant: 'muted' })
  } else if (delegationFailure) {
    statusConfig = {
      icon: <Prohibit size={12} weight="fill" className="text-[var(--color-warning)]" />,
      label: `Delegation denied · ${policyAxisLabel(delegationFailure.policy)}`,
      border: 'border-[var(--color-warning)]/20',
    }
  } else if (isError) {
    statusConfig = getToolBadgeStatusConfig('error', { size: 12 })
  } else {
    statusConfig = getToolBadgeStatusConfig('success', { size: 12, durationMs })
  }

  const hasDetail = !isRunning && (args !== undefined || result !== undefined || error)

  // Resolve result rendering: determine which sentinel type (if any) applies.
  // (marshalErr is already computed above, before the gate.)
  const truncated = isTruncatedResult(result) ? result : null
  const clientTruncated = isClientTruncatedResult(result) ? result : null
  const toolRef = isToolResultRef(result) ? result : null
  const plainResult =
    !truncated && !marshalErr && !clientTruncated && !toolRef && !delegationFailure
      ? result
      : undefined

  return (
    <div
      data-testid="tool-call-badge"
      data-tool={toolName}
      className={cn('mt-2 rounded-md border bg-[var(--color-surface-1)] text-xs font-mono overflow-hidden', statusConfig.border)}
    >
      {/* Header row — a composed row so browser tools can carry a separate,
          independently clickable "Watch live" launcher next to the toggle. */}
      <div className="flex w-full items-center">
        <button
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            'flex flex-1 min-w-0 items-center gap-2 px-3 py-2 text-left transition-colors',
            hasDetail && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={expanded}
          disabled={!hasDetail}
        >
          <Wrench size={13} className="text-[var(--color-muted)] shrink-0" />
          <span className="text-[var(--color-secondary)] font-medium">
            {humanizeToolName(toolName)}
          </span>
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
        {isBrowserTool && (
          <button
            type="button"
            onClick={handleWatchLive}
            aria-label="Watch live"
            title="Watch this agent's browser live"
            className="shrink-0 flex items-center gap-1 px-2 py-2 text-[10px] text-[var(--color-muted)] hover:text-[var(--color-accent)] hover:bg-[var(--color-surface-3)] transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--color-accent)]"
          >
            <Broadcast size={13} />
            <span>Watch live</span>
          </button>
        )}
      </div>

      {/* Expanded detail */}
      {expanded && hasDetail && (
        <div className="border-t border-[var(--color-border)] px-3 py-2 space-y-2">
          <div>
            <div className="text-[var(--color-muted)] mb-1 font-sans">Tool</div>
            <code className="text-[10px] text-[var(--color-secondary)] break-all">{toolName}</code>
          </div>
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

              {/* Structured delegation-denied sentinel — render a distinct,
                  human-readable block instead of a raw JSON blob. */}
              {delegationFailure && <DelegationFailureDisplay failure={delegationFailure} />}

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

import { useState } from 'react'
import {
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
import type { ToolResultRef, DelegationFailure, FileExistsRefusal } from '@/lib/api/generated/asyncapi-types'
import { isClientTruncatedResult, isToolResultRef } from '@/store/chat'
import type { ClientTruncatedResult } from '@/store/chat'
import { useQuery } from '@tanstack/react-query'
import { fetchToolResult } from '@/lib/api'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import {
  getToolBadgeStatusConfig,
  statusDot,
  isCancelledStatus,
  type ToolBadgeStatusConfig,
} from '@/lib/toolStatusConfig'

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
// Exported so ToolCallBadge.tsx (the historical-list / SubagentBlock-step
// renderer for the same delegation-denied outcome) can reuse the exact same
// detection + label logic instead of re-deriving it — both callers must
// render pixel-identical "Delegation denied · <axis>" chips for the same
// sentinel shape.
export function isDelegationFailure(value: unknown): value is DelegationFailure {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['error'] === 'delegation_denied'
  )
}

/**
 * Detects the structured write_file precondition refusal (ADR-059 W5,
 * error: "file_exists" — the generated FileExistsRefusal contract).
 *
 * Needed for the same reason isDelegationFailure is: without a detector the
 * payload falls through to `plainResult` and renders as a raw JSON blob. That
 * is the house rule this file already states one function up — "the house
 * response to a structured payload is to write a renderer, not to let it
 * through as text" — and it applies on REPLAY specifically, because live chat
 * routes write_file to its own registered UI (FileWriteConfirm) while replay
 * routes it here.
 */
export function isFileExistsRefusal(value: unknown): value is FileExistsRefusal {
  return (
    typeof value === 'object' &&
    value !== null &&
    (value as Record<string, unknown>)['error'] === 'file_exists' &&
    typeof (value as Record<string, unknown>)['reason'] === 'string'
  )
}

/** Human label for the policy axis that blocked the delegation. */
export function policyAxisLabel(policy: DelegationFailure['policy']): string {
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
      {/* Banner — flat: a warning-tinted left accent stands in for the old
          amber box (ticket "Tool components in chat"); text stays amber. */}
      <div className="flex items-start gap-2 border-l-2 border-amber-500/40 pl-2 py-1 mb-1 font-sans text-[10px] text-amber-400">
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
        <button tabIndex={0}
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
      {/* Flat: warning-tinted left accent instead of the old amber box; text stays amber. */}
      <div className="flex items-start gap-2 border-l-2 border-amber-500/40 pl-2 py-1 mb-1 font-sans text-[10px] text-amber-400">
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
    // Flat: a warning-tinted left accent replaces the old bordered/tinted
    // box (ticket "Tool components in chat") — icon/label text stay warning-colored.
    <div
      data-testid="result-delegation-denied"
      className="border-l-2 pl-2.5 py-2 mb-1 font-sans text-[10px]"
      style={{
        borderColor: 'color-mix(in srgb, var(--color-warning) 60%, transparent)',
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
  const isCancelled = isCancelledStatus(status)

  // G17: a delegation denial is an error-status result; surface it in the
  // COLLAPSED header ("Delegation denied · <axis>") instead of a generic
  // "Failed" so the user sees the policy block without expanding. The full
  // reason stays in the expanded DelegationFailureDisplay. Computed here
  // (rather than after the gate below) because the gate below also consults
  // it — though (revised 2026-07-16) it is only actually HONORED there for
  // load_tool; see the gate comment below and toolVisibility.ts's doc
  // comment for the per-tool-class rule.
  const delegationFailure = isDelegationFailure(result) ? result : null
  const fileExistsRefusal = isFileExistsRefusal(result) ? result : null

  // Marshal-error sentinel: the backend emits `{_marshal_error: "..."}` when
  // JSON-marshaling a tool result fails during replay-frame construction —
  // this can happen even when the tool call itself succeeded, so neither
  // `status` nor `error` reflects it. Computed here (before the gate below,
  // alongside delegationFailure) because the gate needs it too — but only
  // for load_tool: shouldRenderToolCall's outcome override is per-tool-class
  // (see that function's doc comment, toolVisibility.ts), so a load_tool
  // call whose args match the "hide by default" shape still surfaces on a
  // marshal failure, while a delegate/background-bash call does NOT get
  // that exception (the failure is left to the calling agent's own response
  // text; the raw result stays inspectable in the ActivityPanel).
  const marshalErr = isMarshalErrorResult(result) ? result : null

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (load_tool, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. The
  // isError/delegationFailure/marshalErr outcome signal passed below is only
  // honored by shouldRenderToolCall's load_tool case (toolVisibility.ts doc
  // comment) — a load_tool call still forces visible on error/denial/
  // marshal-failure, but delegate and background-bash do NOT get that
  // exception: that failure is left to the calling agent's own response
  // text, with the raw result staying inspectable in the ActivityPanel
  // slide-out. Must sit after every hook above and before the JSX return
  // (Rules of Hooks).
  if (
    !shouldRenderToolCall(
      toolName,
      args as Record<string, unknown> | undefined,
      verboseChatEnabled,
      isError || !!delegationFailure || !!fileExistsRefusal || !!marshalErr,
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
    // No equivalent status in getToolBadgeStatusConfig's 4-value domain (see
    // src/lib/toolStatusConfig.tsx's file header), so this stays a local
    // literal — but reuses the shared `statusDot` helper so its dot matches
    // the other four statuses exactly.
    statusConfig = {
      indicator: statusDot('bg-[var(--color-warning)]'),
      label: `Delegation denied · ${policyAxisLabel(delegationFailure.policy)}`,
    }
  } else if (fileExistsRefusal) {
    // A precondition refusal is NOT an I/O failure — the file is simply
    // already there, which is frequently the correct outcome when a sibling
    // agent got there first. Labelling it "Failed" is the same conflation the
    // backend half of W5 removed for the calling agent.
    statusConfig = {
      indicator: statusDot('bg-[var(--color-warning)]'),
      label: 'File already exists',
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
    !truncated && !marshalErr && !clientTruncated && !toolRef && !delegationFailure && !fileExistsRefusal
      ? result
      : undefined

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no
    // border, no surface fill, no rounded frame, no overflow-hidden — the
    // row is transparent on the thread. Separation comes from `mt-2`
    // spacing and the status dot, not a card frame.
    <div data-testid="tool-call-badge" data-tool={toolName} className="mt-2 text-xs font-mono">
      {/* Header row — the toggle button spans the row (flex-1) and owns the
          caret as its last child (ml-auto pushes it to the toggle's own
          right edge), so the caret is inside the clickable toggle and
          actually expands on click — mirrors ToolCallBadge.tsx. "Watch
          live" stays an independent sibling *after* the toggle: a button
          can't nest inside another button, so it can't live inside the
          toggle too. For non-browser rows the toggle fills the whole row
          and the caret still lands at the row's far right, unchanged. For
          browser rows the toggle only fills the space left of "Watch
          live", so the caret now sits immediately before that launcher
          instead of after it — an acceptable, intentional shift. */}
      <div className="flex w-full items-center gap-2">
        <button tabIndex={0}
          type="button"
          onClick={() => hasDetail && setExpanded((e) => !e)}
          className={cn(
            // flex-1: the toggle spans the whole row (minus the Watch-live
            // launcher, which stays an independent target) — matching
            // ToolCallBadge's full-row click target. Without it the toggle
            // shrinks to its text and the row's middle is dead space.
            'flex flex-1 min-w-0 items-center gap-2 py-1 text-left transition-colors',
            hasDetail && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
            !hasDetail && 'cursor-default'
          )}
          aria-expanded={hasDetail ? expanded : undefined}
          disabled={!hasDetail}
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-secondary)] font-medium">
            {humanizeToolName(toolName)}
          </span>
          <span className={cn('text-[var(--color-muted)]', statusConfig.textClass)}>
            {statusConfig.label}
          </span>
          {hasDetail && (
            <span className="ml-auto shrink-0 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
            </span>
          )}
        </button>
        {isBrowserTool && (
          <button tabIndex={0}
            type="button"
            onClick={handleWatchLive}
            aria-label="Watch live"
            title="Watch this agent's browser live"
            className="shrink-0 flex items-center gap-1 text-[10px] text-[var(--color-accent)] hover:underline transition-colors"
          >
            <Broadcast size={13} />
            <span>Watch live</span>
          </button>
        )}
      </div>

      {/* Expanded detail — indented quote-block: a thin left accent line
          stands in for the old bordered panel, aligned under the
          status-dot column instead of boxing the whole row. */}
      {expanded && hasDetail && (
        <div className="ml-[3px] space-y-2 border-l-2 border-[var(--color-border)] py-1 pl-3">
          <div>
            <div className="text-[var(--color-muted)] mb-1 font-sans">Tool</div>
            <code className="text-[10px] text-[var(--color-secondary)] break-all">{toolName}</code>
          </div>
          {args !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1 font-sans">Parameters</div>
              {/* Keyboard-scrollable: WebKit doesn't put a plain scrollable
                  <pre> in the Tab order by default, so a keyboard-only user
                  can't reach/scroll it at all. tabIndex + role="region" +
                  label make it a reachable, scrollable landmark. */}
              <pre
                tabIndex={0}
                role="region"
                aria-label="Tool output"
                className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
              >
                {safeJson(args)}
              </pre>
            </div>
          )}

          {/* Result section — five rendering paths */}
          {result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1 font-sans">Result</div>

              {/* Marshal-error sentinel: result could not be serialized. Flat:
                  error-tinted left accent instead of the old bordered box. */}
              {marshalErr && (
                <div
                  data-testid="result-marshal-error"
                  className="flex items-start gap-2 border-l-2 border-[var(--color-error)]/40 pl-2 py-1 mb-1 font-sans text-[10px] text-[var(--color-error)]"
                >
                  <XCircle size={12} weight="fill" className="shrink-0 mt-0.5" />
                  <span>Result serialization failed: {marshalErr._marshal_error}</span>
                </div>
              )}

              {/* Server-truncated sentinel: result exceeded 10 KiB server-side.
                  Flat: warning-tinted left accent instead of the old amber box. */}
              {truncated && (
                <>
                  <div
                    data-testid="result-truncated-banner"
                    className="flex items-start gap-2 border-l-2 border-amber-500/40 pl-2 py-1 mb-1 font-sans text-[10px] text-amber-400"
                  >
                    <Warning size={12} weight="fill" className="shrink-0 mt-0.5" />
                    <span>
                      Truncated — showing first 10 KiB of {humanSize(truncated.original_size_bytes)}
                    </span>
                  </div>
                  <pre
                    tabIndex={0}
                    role="region"
                    aria-label="Tool output"
                    className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
                  >
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
              {fileExistsRefusal && (
                <div
                  data-testid="result-file-exists"
                  className="border-l-2 pl-2.5 py-2 mb-1 font-sans text-[10px] text-[var(--color-secondary)]"
                  style={{ borderColor: 'color-mix(in srgb, var(--color-warning) 60%, transparent)' }}
                >
                  {fileExistsRefusal.reason}
                </div>
              )}

              {/* Plain result: normal rendering */}
              {plainResult !== undefined && (
                <pre
                  tabIndex={0}
                  role="region"
                  aria-label="Tool output"
                  className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
                >
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

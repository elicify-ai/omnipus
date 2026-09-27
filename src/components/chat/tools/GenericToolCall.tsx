import { useState } from 'react'
import { Broadcast } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useSessionStore } from '@/store/session'
import { useUiStore } from '@/store/ui'
import type { MessagePartStatus } from '@assistant-ui/react'
import { isClientTruncatedResult, isToolResultRef } from '@/store/chat'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import {
  getToolBadgeStatusConfig,
  isCancelledStatus,
  type ToolBadgeStatusConfig,
} from '@/lib/toolStatusConfig'
import { detectToolResultSentinels } from './toolResultSentinels'
import {
  ClientTruncatedDisplay,
  DelegationFailureDisplay,
  FileExistsRefusalDisplay,
  PermissionDeniedDisplay,
  ToolResultRefDisplay,
  ToolResultSentinelBody,
  isMarshalErrorResult,
  isTruncatedResult,
  safeJson,
} from './toolResultDisplay'

interface GenericToolCallProps {
  toolName: string
  args?: unknown
  result?: unknown
  status: MessagePartStatus
  /** Optional error text from the store */
  error?: string
  /**
   * Issue #617: the tool call's real error outcome, sourced from the store's
   * resolved ToolCall.status. When provided this is authoritative and wins
   * over the `status`/`error`-derived fallback below — `status` is hardcoded
   * to `{type:'complete'}` on replay (ChatScreen.tsx), so deriving isError
   * from it alone can never see a failure that offloaded its `result` (>50
   * KiB) or replaced it with a parsed object, both of which leave `error`
   * empty per pkg/gateway/websocket.go's own documented behavior. Optional
   * (not every caller has a resolved ToolCall to read a status off of) —
   * omitted falls back to the pre-existing status/error derivation.
   */
  isError?: boolean
  /** Optional duration in milliseconds */
  durationMs?: number
  /** Lite-mode: tool calls start collapsed so the virtualizer skips measuring large expanded content. */
  defaultCollapsed?: boolean
  /** Session this tool call belongs to. Required to fetch ToolResultRef bodies session-scoped. */
  sessionId?: string
}

// F1 (second review wave on branch fix/615-617-618-hardening): the
// isDelegationFailure / isFileExistsRefusal / isPermissionDenied detectors
// and policyAxisLabel used to be defined here, byte-identical to a second
// copy in a dead, never-imported toolResultSentinels.ts — six edits across
// two files for any fourth sentinel, and the exact gap that let #618 ship
// with permission_denied having no SPA detector at all. They now live in
// ./toolResultSentinels (imported above as detectToolResultSentinels /
// policyAxisLabel) — the ONE place that detects the three structured-failure
// sentinels and derives their shared amber status config. This component
// still needs the individual delegationFailure/fileExistsRefusal/
// permissionDenied objects beyond just the status config — for
// PermissionDeniedDisplay, fileExistsRefusal.reason, and excluding a matched
// sentinel from the plain-JSON-result fallback below — see
// detectToolResultSentinels's return type doc comment.

export function GenericToolCall({
  toolName,
  args,
  result,
  status,
  error,
  isError: isErrorProp,
  durationMs,
  // defaultCollapsed is accepted in GenericToolCallProps for API compatibility with
  // callers (e.g. ChatScreen), but intentionally not destructured here — tool calls
  // always start collapsed regardless of the caller's requested initial state.
  sessionId = '',
}: GenericToolCallProps) {
  const [expanded, setExpanded] = useState(false)
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)

  const isRunning = status.type === 'running'
  // Issue #617: the caller's resolved outcome wins when supplied — see
  // isErrorProp's doc comment on GenericToolCallProps for why the
  // status/error fallback below can silently miss a real failure.
  const isError = isErrorProp ?? (status.type === 'incomplete' || !!error)
  const isCancelled = isCancelledStatus(status)

  // G17/F1: a delegation denial, file-exists refusal, or permission denial
  // is an error-status result; surface it in the COLLAPSED header
  // ("Delegation denied · <axis>" / "File already exists" / "Permission
  // denied") instead of a generic "Failed" so the user sees the outcome
  // without expanding. The full detail stays in each sentinel's own expanded
  // Display component below. Computed here (rather than after the gate
  // below) because the gate below also consults `sentinels.any` — though
  // (revised 2026-07-16) it is only actually HONORED there for ToolSearch;
  // see the gate comment below and toolVisibility.ts's doc comment for the
  // per-tool-class rule. detectToolResultSentinels (./toolResultSentinels)
  // is the ONE place that detects all three; it does NOT decide precedence
  // between them and isRunning/isCancelled — that stays here, on purpose:
  // a still-running or cancelled call must never show a sentinel label even
  // if `result` happens to already carry one (e.g. a stale/replayed partial
  // result) — see the statusConfig chain below, where isRunning/isCancelled
  // are checked BEFORE sentinels.statusConfig.
  const sentinels = detectToolResultSentinels(result)
  const { delegationFailure, fileExistsRefusal, permissionDenied } = sentinels

  // Marshal-error sentinel: the backend emits `{_marshal_error: "..."}` when
  // JSON-marshaling a tool result fails during replay-frame construction —
  // this can happen even when the tool call itself succeeded, so neither
  // `status` nor `error` reflects it. Computed here (before the gate below,
  // alongside delegationFailure) because the gate needs it too — but only
  // for ToolSearch: shouldRenderToolCall's outcome override is per-tool-class
  // (see that function's doc comment, toolVisibility.ts), so a ToolSearch
  // call whose args match the "hide by default" shape still surfaces on a
  // marshal failure, while a delegate/background-bash call does NOT get
  // that exception (the failure is left to the calling agent's own response
  // text; the raw result stays inspectable in the ActivityPanel).
  const marshalErr = isMarshalErrorResult(result) ? result : null

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (ToolSearch, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. The
  // isError/sentinels.any/marshalErr outcome signal passed below is only
  // honored by shouldRenderToolCall's ToolSearch case (toolVisibility.ts doc
  // comment) — a ToolSearch call still forces visible on error/denial/
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
      isError || sentinels.any || !!marshalErr,
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

  // F1: isRunning/isCancelled are checked BEFORE sentinels.statusConfig —
  // a still-running or cancelled call never shows a sentinel label even if
  // `result` happens to already carry one (e.g. a stale/replayed partial
  // result). This ordering is GenericToolCall's own precedence choice, not
  // the shared module's — see detectToolResultSentinels's doc comment
  // (./toolResultSentinels) for why ToolCallBadge orders it the other way
  // (sentinel detection ABOVE all four ordinary statuses).
  let statusConfig: ToolBadgeStatusConfig
  if (isRunning) {
    statusConfig = getToolBadgeStatusConfig('running', { size: 12 })
  } else if (isCancelled) {
    statusConfig = getToolBadgeStatusConfig('cancelled', { size: 12, cancelledVariant: 'muted' })
  } else if (sentinels.statusConfig) {
    statusConfig = sentinels.statusConfig
  } else if (isError) {
    statusConfig = getToolBadgeStatusConfig('error', { size: 12 })
  } else {
    statusConfig = getToolBadgeStatusConfig('success', { size: 12, durationMs })
  }

  const hasDetail = !!(!isRunning && (args !== undefined || result !== undefined || error))

  // Resolve result rendering: determine which sentinel type (if any) applies.
  // (marshalErr is already computed above, before the gate.)
  const truncated = isTruncatedResult(result) ? result : null
  const clientTruncated = isClientTruncatedResult(result) ? result : null
  const toolRef = isToolResultRef(result) ? result : null
  const plainResult =
    !truncated &&
    !marshalErr &&
    !clientTruncated &&
    !toolRef &&
    !delegationFailure &&
    !fileExistsRefusal &&
    !permissionDenied
      ? result
      : undefined

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no
    // border, no surface fill, no rounded frame, no overflow-hidden — the
    // row is transparent on the thread. Separation comes from `mt-2`
    // spacing and the status dot, not a card frame.
    <div data-testid="tool-call-badge" data-tool={toolName} className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
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
      <div className="flex w-full items-center gap-[var(--space-2)]">
        <DisclosureRow
          expanded={expanded}
          onExpandedChange={setExpanded}
          expandable={hasDetail}
          data-testid="tool-call-toggle"
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-secondary)] font-medium">
            {humanizeToolName(toolName)}
          </span>
          <span className={cn('text-[var(--color-muted)]')}>
            {statusConfig.label}
          </span>
        </DisclosureRow>
        {isBrowserTool && (
          <Button
            type="button"
            variant="link"
            onClick={handleWatchLive}
            aria-label="Watch live"
            title="Watch this agent's browser live"
            className="rounded-none shrink-0 flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)]"
          >
            <Broadcast size={13} />
            <span>Watch live</span>
          </Button>
        )}
      </div>

      {/* Expanded detail — indented quote-block: a thin left accent line
          stands in for the old bordered panel, aligned under the
          status-dot column instead of boxing the whole row. */}
      {expanded && hasDetail && (
        <div className="ml-[var(--space-1)] space-y-[var(--space-2)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          <div>
            <div className="text-[var(--color-muted)] mb-[var(--space-1)] font-body">Tool</div>
            <code className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] break-all">{toolName}</code>
          </div>
          {args !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-[var(--space-1)] font-body">Parameters</div>
              {/* Keyboard-scrollable: WebKit doesn't put a plain scrollable
                  <pre> in the Tab order by default, so a keyboard-only user
                  can't reach/scroll it at all. tabIndex + role="region" +
                  label make it a reachable, scrollable landmark. */}
              <pre
                tabIndex={0}
                role="region"
                aria-label="Tool output"
                className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
              >
                {safeJson(args)}
              </pre>
            </div>
          )}

          {/* Result section — five rendering paths */}
          {result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-[var(--space-1)] font-body">Result</div>

              {/* Marshal-error sentinel: result could not be serialized. Flat:
                  error-tinted left accent instead of the old bordered box. */}
              {marshalErr && (
                <ToolResultSentinelBody resolved={{ kind: 'marshalError', sentinel: marshalErr }} sessionId={sessionId} />
              )}

              {truncated && (
                <ToolResultSentinelBody resolved={{ kind: 'truncated', sentinel: truncated }} sessionId={sessionId} />
              )}

              {clientTruncated && <ClientTruncatedDisplay sentinel={clientTruncated} />}

              {/* G4: ToolResultRef sentinel — server stored full body, fetch on demand */}
              {toolRef && <ToolResultRefDisplay sentinel={toolRef} sessionId={sessionId} />}

              {/* Structured delegation-denied sentinel — render a distinct,
                  human-readable block instead of a raw JSON blob. */}
              {delegationFailure && <DelegationFailureDisplay failure={delegationFailure} />}
              {fileExistsRefusal && (
                <FileExistsRefusalDisplay refusal={fileExistsRefusal} />
              )}

              {permissionDenied && <PermissionDeniedDisplay failure={permissionDenied} />}

              {/* Plain result: normal rendering */}
              {plainResult !== undefined && (
                <pre
                  tabIndex={0}
                  role="region"
                  aria-label="Tool output"
                  className="text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto"
                >
                  {safeJson(plainResult)}
                </pre>
              )}
            </div>
          )}

          {error && (
            <div className="text-[var(--color-error)] text-[length:var(--type-caption-size)] font-body">{error}</div>
          )}
        </div>
      )}
    </div>
  )
}

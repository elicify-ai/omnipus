/**
 * BashOutput — AssistantUI tool-call rendering for the unified `bash` tool
 * (ADR-036 §3.1, §6). Replaces the three separate rendering components that
 * existed for the three shell tools this consolidates:
 *   - TerminalOutput.tsx  (`exec`)
 *   - WorkspaceShellUI.tsx (`workspace_shell` / `workspace_shell_bg`, plus
 *     their legacy dotted-namespace aliases `workspace.shell` / `workspace.shell_bg`)
 *
 * All five names now render through the same BashOutputBlock — `bash` is the
 * new canonical registration; the other five are legacy aliases kept ONLY so
 * old, already-persisted session transcripts (which still literally contain
 * tool_name: "exec" / "workspace_shell" / etc. — historical JSONL data is
 * never migrated) keep their rich terminal-styled rendering instead of
 * falling back to the generic tool-call chip. This mirrors the project's
 * existing convention for prior tool renames (see the browser_* /
 * browser.* pairs registered in OmnipusRuntimeProvider.tsx).
 */

import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowsClockwise, CaretDown, CaretUp } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'

// ── Args shape ────────────────────────────────────────────────────────────────
//
// Superset of the new canonical `bash` schema (ADR-036 §3.1: command,
// description, cwd, timeout_seconds, run_in_background, persistent, action)
// plus the legacy field names the retired `exec` tool used (timeout,
// background, pty, session_id, and the read/write/kill/send-keys action
// values) so historical transcripts render exactly as they used to.

interface BashArgs {
  // Canonical (bash) fields
  command?: string
  description?: string
  cwd?: string
  timeout_seconds?: number
  run_in_background?: boolean
  persistent?: boolean
  action?: string // bash: run|poll|read|kill — legacy exec: run|read|write|kill|send-keys
  // Legacy (exec / workspace_shell*) fields — kept so old transcripts render.
  timeout?: number
  background?: boolean
  pty?: boolean
  session_id?: string
}

// ── Shared block ──────────────────────────────────────────────────────────────

function actionLabel(action: string, isBackground: boolean): string {
  const base =
    action === 'run' ? 'bash' :
    action === 'poll' ? 'bash poll' :
    action === 'read' ? 'bash read' :
    action === 'write' ? 'bash write' :
    action === 'kill' ? 'bash kill' :
    action === 'send-keys' ? 'bash send-keys' :
    action
  return isBackground && (action === 'run' || action === 'poll') ? `${base} (bg)` : base
}

function BashOutputBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
}: {
  toolName: string
  args: BashArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const [expanded, setExpanded] = useState(true)

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background `bash` dispatches (run_in_background) and poll/read calls
  // unless the user has opted into verbose chat. Scoped to the literal
  // canonical `bash` tool name only — the five legacy aliases below
  // (`exec`, `workspace_shell`, `workspace_shell_bg`, and their dotted
  // forms) render OLD, already-persisted transcripts exactly as they were
  // stored and must NEVER be hidden by this new gate. shouldRenderToolCall's
  // outcome override is per-tool-class (toolVisibility.ts doc comment) and
  // does NOT apply to bash's poll/read/background-dispatch cases — an error
  // outcome does not force this row visible; that failure is left to the
  // calling agent's own response text, with the raw output staying
  // inspectable in the ActivityPanel slide-out. The `isError` argument
  // below (threaded from the tool-call part's real `isError` field by
  // makeBashUI below — see issue #617) is therefore currently a no-op for
  // bash — kept only so this call site
  // matches shouldRenderToolCall's uniform 4-argument gate signature, the
  // same one GenericToolCall/ToolCallBadge call with their own outcome
  // signal, so a future revision that DOES add a bash-specific exception
  // (e.g. for `kill`) doesn't need to change this call site's shape, only
  // toolVisibility.ts's switch. Must sit after every hook above and before
  // the JSX return (Rules of Hooks); the hook itself is called
  // unconditionally — only the resulting early return is gated on toolName.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (
    toolName === 'bash' &&
    !shouldRenderToolCall(toolName, args as unknown as Record<string, unknown>, verboseChatEnabled, !!isError)
  ) {
    return null
  }

  const command = args.command || args.description || args.session_id || '(unknown command)'
  const action = args.action ?? 'run'
  const isBackground =
    args.run_in_background === true ||
    args.background === true ||
    toolName === 'workspace_shell_bg' ||
    toolName === 'workspace.shell_bg'
  const label = actionLabel(action, isBackground)
  const output = result != null ? String(result) : ''

  // Flat text-line redesign (ticket "Tool components in chat", P2): no
  // rounded-md/border/overflow-hidden card, no status-tinted border, no
  // header background — the row is transparent on the thread and the only
  // status color lives in the leading dot/spinner (shared with
  // GenericToolCall/ToolCallBadge via getToolBadgeStatusConfig). The
  // per-tool Terminal glyph is dropped as redundant with the `label` text
  // ("bash" / "bash poll" / …) it used to sit beside.
  const statusConfig = getToolBadgeStatusConfig(
    isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
    { size: 12, cancelledVariant: 'muted' }
  )

  return (
    <div className="mt-2 text-xs font-mono">
      {/* Header row — a single toggle button; there is no sibling action on
          this row (unlike BrowserTool/BrowserNavigate's "Watch live"), so
          the caret lives INSIDE the button and the whole row is one click
          target. */}
      <div className="flex w-full items-center gap-2">
        <button tabIndex={0}
          type="button"
          onClick={() => setExpanded((e) => !e)}
          className="flex flex-1 min-w-0 items-center gap-2 py-1 text-left transition-colors hover:bg-[var(--color-surface-2)]/60 cursor-pointer"
          aria-expanded={expanded}
        >
          {statusConfig.indicator}
          <span className="text-[var(--color-muted)] shrink-0">{label}</span>
          <span className="text-[var(--color-secondary)] truncate flex-1 min-w-0">{command}</span>
          <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}>
            {statusConfig.label}
          </span>
          <span className="ml-auto shrink-0 text-[var(--color-muted)]">
            {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
          </span>
        </button>
      </div>

      {/* Output panel — indented left-accent block; the dark terminal panel
          keeps its own identity (bg-[#0d1117]) but is no longer wrapped in
          an outer bordered frame. */}
      {expanded && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] pl-3 py-1">
          <div className="bg-[#0d1117] rounded-sm">
            {isRunning && !output ? (
              <div className="px-3 py-2 text-[var(--color-muted)] italic flex items-center gap-2">
                <ArrowsClockwise size={11} className="animate-spin" />
                {isBackground ? 'Running in background...' : 'Executing...'}
              </div>
            ) : (
              <pre className="px-3 py-2 text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
                {output || <span className="text-[var(--color-muted)] italic">(no output)</span>}
              </pre>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

function makeBashUI(toolName: string) {
  return makeAssistantToolUI<BashArgs, unknown>({
    toolName,
    // Issue #617: isError comes straight from the tool-call part's own
    // `isError` field (set in omnipus-runtime.ts from the store's resolved
    // ToolCall.status), not from `status.type === 'incomplete'` — that
    // AssistantUI status can never be true for a finished tool call with a
    // result (toMessagePartStatus always returns COMPLETE_STATUS once
    // `result` is truthy), so the old check could never actually fire on a
    // real tool failure.
    render: ({ args, result, status, isError }) => (
      <BashOutputBlock
        toolName={toolName}
        args={args ?? {}}
        result={result}
        isRunning={status.type === 'running'}
        isError={isError}
        isCancelled={isCancelledStatus(status)}
      />
    ),
  })
}

// ── Registrations ─────────────────────────────────────────────────────────────

// New canonical name (ADR-036 §3.1 — replaces exec / workspace_shell / workspace_shell_bg).
export const BashOutputUI = makeBashUI('bash')

// Legacy aliases kept for backward compat with old session transcripts only.
// Do NOT use these names for new tool calls — the canonical name is `bash`.
export const ExecLegacyUI = makeBashUI('exec')
export const WorkspaceShellLegacyUI = makeBashUI('workspace_shell')
export const WorkspaceShellDotLegacyUI = makeBashUI('workspace.shell')
export const WorkspaceShellBgLegacyUI = makeBashUI('workspace_shell_bg')
export const WorkspaceShellBgDotLegacyUI = makeBashUI('workspace.shell_bg')

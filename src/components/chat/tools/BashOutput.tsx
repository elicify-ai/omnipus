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
import {
  Terminal,
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

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
}: {
  toolName: string
  args: BashArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
}) {
  const [expanded, setExpanded] = useState(true)

  const command = args.command || args.description || args.session_id || '(unknown command)'
  const action = args.action ?? 'run'
  const isBackground =
    args.run_in_background === true ||
    args.background === true ||
    toolName === 'workspace_shell_bg' ||
    toolName === 'workspace.shell_bg'
  const label = actionLabel(action, isBackground)
  const output = result != null ? String(result) : ''

  return (
    <div
      className={cn(
        'mt-2 rounded-md border overflow-hidden font-mono text-xs',
        isRunning
          ? 'border-[var(--color-border)]'
          : isError
          ? 'border-[var(--color-error)]/30 bg-[var(--color-error)]/5'
          : 'border-[var(--color-success)]/20',
      )}
    >
      {/* Header */}
      <button
        type="button"
        onClick={() => setExpanded((e) => !e)}
        className="flex w-full items-center gap-2 px-3 py-2 bg-[var(--color-surface-1)] hover:bg-[var(--color-surface-2)] transition-colors text-left cursor-pointer"
        aria-expanded={expanded}
      >
        <Terminal
          size={13}
          weight="bold"
          className={cn(
            isRunning ? 'text-[var(--color-accent)]' :
            isError ? 'text-[var(--color-error)]' :
            'text-[var(--color-success)]',
          )}
        />
        <span className="text-[var(--color-muted)] shrink-0">{label}</span>
        <span className="text-[var(--color-secondary)] truncate flex-1 min-w-0">{command}</span>
        <span className="flex items-center gap-1 shrink-0">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : isError ? (
            <XCircle size={12} weight="fill" className="text-[var(--color-error)]" />
          ) : (
            <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
          )}
          <span className="text-[var(--color-muted)] ml-0.5">
            {expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
          </span>
        </span>
      </button>

      {/* Output panel */}
      {expanded && (
        <div className="bg-[#0d1117] border-t border-[var(--color-border)]">
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
      )}
    </div>
  )
}

function makeBashUI(toolName: string) {
  return makeAssistantToolUI<BashArgs, unknown>({
    toolName,
    render: ({ args, result, status }) => (
      <BashOutputBlock
        toolName={toolName}
        args={args ?? {}}
        result={result}
        isRunning={status.type === 'running'}
        isError={status.type === 'incomplete'}
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

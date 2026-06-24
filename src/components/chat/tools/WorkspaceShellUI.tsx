/**
 * WorkspaceShellUI — AssistantUI tool components for workspace.shell (foreground)
 * and workspace.shell_bg (background) tool calls.
 *
 * Both tools produce captured stdout/stderr as a string result; the JSON shape
 * is identical to the `exec` tool. This component reuses the same
 * TerminalOutputBlock rendering style.
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

interface WorkspaceShellArgs {
  command?: string
  cwd?: string
  timeout?: number
  // shell_bg additional fields
  description?: string
}

// ── Shared block ──────────────────────────────────────────────────────────────

function WorkspaceShellBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
}: {
  toolName: string
  args: WorkspaceShellArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
}) {
  const [expanded, setExpanded] = useState(true)

  const command = args.command ?? args.description ?? '(unknown command)'
  const output = result != null ? String(result) : ''

  // Distinguish foreground vs background in the label
  const actionLabel = toolName === 'workspace.shell_bg' ? 'shell (bg)' : 'shell'

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
        <span className="text-[var(--color-muted)] shrink-0">{actionLabel}</span>
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
              Running...
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

// ── Registrations ─────────────────────────────────────────────────────────────

// New canonical name (post §7 rename: workspace.shell → workspace_shell)
export const WorkspaceShellUI = makeAssistantToolUI<WorkspaceShellArgs, unknown>({
  toolName: 'workspace_shell',
  render: ({ args, result, status }) => (
    <WorkspaceShellBlock
      toolName="workspace_shell"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

// Legacy alias kept for backward compat with old session transcripts.
export const WorkspaceShellLegacyUI = makeAssistantToolUI<WorkspaceShellArgs, unknown>({
  toolName: 'workspace.shell',
  render: ({ args, result, status }) => (
    <WorkspaceShellBlock
      toolName="workspace.shell"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

// New canonical name (post §7 rename: workspace.shell_bg → workspace_shell_bg)
export const WorkspaceShellBgUI = makeAssistantToolUI<WorkspaceShellArgs, unknown>({
  toolName: 'workspace_shell_bg',
  render: ({ args, result, status }) => (
    <WorkspaceShellBlock
      toolName="workspace_shell_bg"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

// Legacy alias kept for backward compat with old session transcripts.
export const WorkspaceShellBgLegacyUI = makeAssistantToolUI<WorkspaceShellArgs, unknown>({
  toolName: 'workspace.shell_bg',
  render: ({ args, result, status }) => (
    <WorkspaceShellBlock
      toolName="workspace.shell_bg"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

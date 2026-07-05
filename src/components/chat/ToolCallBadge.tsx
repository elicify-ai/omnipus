import { useState } from 'react'
import {
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  CaretDown,
  CaretUp,
  Terminal,
  Globe,
  FileText,
  Wrench,
  Prohibit,
} from '@phosphor-icons/react'
import type { ToolCall } from '@/lib/api'
import { cn } from '@/lib/utils'
import { humanizeToolName } from '@/lib/humanizeToolName'

interface ToolCallBadgeProps {
  toolCall: ToolCall & { call_id: string }
}

function getToolIcon(tool: string) {
  // ADR-036: `bash` is the canonical unified shell tool; `exec` (and its
  // `exec.` sub-actions) is kept only for historical session transcripts.
  if (tool === 'bash' || tool === 'exec' || tool.startsWith('exec.')) return Terminal
  if (tool === 'web_search' || tool === 'browser.search') return Globe
  if (tool.startsWith('file.') || tool.startsWith('fs.')) return FileText
  return Wrench
}

function formatDuration(ms?: number): string {
  if (!ms) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

export function ToolCallBadge({ toolCall }: ToolCallBadgeProps) {
  const [expanded, setExpanded] = useState(false)
  const Icon = getToolIcon(toolCall.tool)

  const statusConfig = {
    running: {
      icon: <ArrowsClockwise size={13} className="animate-spin text-[var(--color-accent)]" />,
      label: 'Running...',
      border: 'border-[var(--color-border)]',
    },
    success: {
      icon: <CheckCircle size={13} className="text-[var(--color-success)]" weight="fill" />,
      label: toolCall.duration_ms ? formatDuration(toolCall.duration_ms) : 'Done',
      border: 'border-[var(--color-success)]/20',
    },
    error: {
      icon: <XCircle size={13} className="text-[var(--color-error)]" weight="fill" />,
      label: 'Failed',
      border: 'border-[var(--color-error)]/20',
    },
    cancelled: {
      icon: <Prohibit size={13} className="text-[var(--color-cancelled)]" weight="fill" />,
      label: 'Cancelled',
      border: 'border-[var(--color-cancelled)]/20',
    },
  }

  const config = statusConfig[toolCall.status]

  return (
    <div
      data-testid="tool-call-badge"
      data-tool={toolCall.tool}
      className={cn(
        'mt-2 rounded-md border bg-[var(--color-surface-1)] text-xs font-mono overflow-hidden',
        config.border
      )}
    >
      {/* Header row */}
      <button
        type="button"
        onClick={() => toolCall.status !== 'running' && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 text-left transition-colors',
          toolCall.status !== 'running' && 'hover:bg-[var(--color-surface-2)] cursor-pointer',
          toolCall.status === 'running' && 'cursor-default'
        )}
        aria-expanded={expanded}
      >
        <Icon size={13} className="text-[var(--color-muted)] shrink-0" />
        <span className="text-[var(--color-secondary)] font-medium">
          {humanizeToolName(toolCall.tool)}
        </span>
        <span className="flex items-center gap-1 ml-1">
          {config.icon}
          <span className="text-[var(--color-muted)]">{config.label}</span>
        </span>
        {toolCall.status !== 'running' && (
          <span className="ml-auto text-[var(--color-muted)]">
            {expanded ? <CaretUp size={12} /> : <CaretDown size={12} />}
          </span>
        )}
      </button>

      {/* Expanded detail */}
      {expanded && toolCall.status !== 'running' && (
        <div className="border-t border-[var(--color-border)] px-3 py-2 space-y-2">
          <div>
            <div className="text-[var(--color-muted)] mb-1">Tool</div>
            <code className="text-[10px] text-[var(--color-secondary)] break-all">
              {toolCall.tool}
            </code>
          </div>
          <div>
            <div className="text-[var(--color-muted)] mb-1">Parameters</div>
            <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all">
              {JSON.stringify(toolCall.params, null, 2)}
            </pre>
          </div>
          {toolCall.result !== undefined && (
            <div>
              <div className="text-[var(--color-muted)] mb-1">Result</div>
              <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-48 overflow-auto">
                {typeof toolCall.result === 'string'
                  ? toolCall.result
                  : JSON.stringify(toolCall.result, null, 2)}
              </pre>
            </div>
          )}
          {toolCall.error && (
            <div className="text-[var(--color-error)] text-[10px]">{toolCall.error}</div>
          )}
        </div>
      )}
    </div>
  )
}

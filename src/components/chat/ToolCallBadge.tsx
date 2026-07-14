import { useState } from 'react'
import { CaretDown, CaretUp, Terminal, Globe, FileText, Wrench } from '@phosphor-icons/react'
import type { ToolCall } from '@/lib/api'
import type { MarshalErrorResult } from '@/lib/ws'
import { cn } from '@/lib/utils'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'

interface ToolCallBadgeProps {
  toolCall: ToolCall & { call_id: string }
}

/**
 * Returns true when the result is the marshal-error sentinel from replay.go.
 * Mirrors GenericToolCall.tsx's detector of the same name — the backend emits
 * `{_marshal_error: "..."}` when JSON-marshaling a tool result fails during
 * replay-frame construction, which can happen even when the call itself
 * succeeded (i.e. `toolCall.status` doesn't reflect it).
 */
function isMarshalErrorResult(value: unknown): value is MarshalErrorResult {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as Record<string, unknown>)['_marshal_error'] === 'string'
  )
}

function getToolIcon(tool: string) {
  // ADR-036: `bash` is the canonical unified shell tool; `exec` (and its
  // `exec.` sub-actions) is kept only for historical session transcripts.
  if (tool === 'bash' || tool === 'exec' || tool.startsWith('exec.')) return Terminal
  if (tool === 'web_search' || tool === 'browser.search') return Globe
  if (tool.startsWith('file.') || tool.startsWith('fs.')) return FileText
  return Wrench
}

export function ToolCallBadge({ toolCall }: ToolCallBadgeProps) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (verbose-chat off by default): hides noisy
  // background infra calls (load_tool, background delegate/bash dispatch,
  // status polls) unless the user has opted into verbose chat. Covers all
  // call sites of this shared badge (MessageItem's historical list,
  // SubagentBlock's nested steps, and ActivityPanel's expanded native-agent
  // step rows). An error/marshal-failure outcome always overrides the hide
  // decision — a failed/denied call, or one whose result silently failed to
  // marshal, must never disappear just because its params look like ordinary
  // background dispatch. Must sit after every hook above and before the JSX
  // return (Rules of Hooks).
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  const marshalErr = isMarshalErrorResult(toolCall.result)
  if (
    !shouldRenderToolCall(
      toolCall.tool,
      toolCall.params,
      verboseChatEnabled,
      toolCall.status === 'error' || marshalErr,
    )
  ) {
    return null
  }

  const Icon = getToolIcon(toolCall.tool)

  const config = getToolBadgeStatusConfig(toolCall.status, { durationMs: toolCall.duration_ms })

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
          toolCall.status !== 'running' && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
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

import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { shouldRenderToolCall } from '@/lib/toolVisibility'
import { getToolBadgeStatusConfig, isCancelledStatus } from '@/lib/toolStatusConfig'

interface ReadFileArgs {
  path?: string
  offset?: number
  length?: number
}

function basename(p: string): string {
  return p.split(/[/\\]/).pop() ?? p
}

function FileReadBlock({
  toolName,
  args,
  result,
  isRunning,
  isError,
  isCancelled,
}: {
  toolName: string
  args: ReadFileArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
  isCancelled?: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  // Client-side render gate (issue #494): mirrors BashOutput.tsx's gate —
  // hides this row when shouldRenderToolCall says so, unless verbose chat is
  // on. Must sit after every hook above and before the JSX return.
  const verboseChatEnabled = useChatPreferencesStore((s) => s.verboseChatEnabled)
  if (
    !shouldRenderToolCall(toolName, args as unknown as Record<string, unknown>, verboseChatEnabled, !!isError)
  ) {
    return null
  }

  const path = args.path ?? '(unknown file)'
  const name = basename(path)
  const content = result != null ? String(result) : ''
  const lines = content.split('\n')
  // Zero (not 1) for a genuinely empty read — `''.split('\n')` yields `['']`,
  // which would otherwise misreport an empty file as "1 lines".
  const lineCount = content ? lines.length : 0
  const preview = lines.slice(0, 20).join('\n')
  const isTruncated = lineCount > 20

  // Always resolves to a real config — a completed read with empty content
  // (no error, no cancellation) is still a real "0 lines" success, not a
  // silently blank indicator (the old `content ? successDot : null` dropped
  // the indicator entirely for that case).
  const statusConfig = getToolBadgeStatusConfig(
    isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
    { size: 12, cancelledVariant: 'muted' }
  )
  // On a real success, always show the line count — including "0 lines" for
  // a genuinely empty file (previously silent: no text shown at all when
  // content was falsy). Running/error/cancelled show the shared status label
  // instead, matching BashOutput/WebFetchPreview.
  const countOrStatusLabel =
    isRunning || isError || isCancelled ? statusConfig.label : `${lineCount} lines`

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The decorative FileText tool-type icon is gone; the leading
    // slot is the status dot/spinner only.
    <div className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      {/* Header */}
      <DisclosureRow
        expanded={expanded}
        onExpandedChange={setExpanded}
        expandable={!isRunning}
        data-testid="file-read-toggle"
      >
        {statusConfig.indicator}
        <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">{name}</span>
        <span className="text-[var(--color-muted)] shrink-0">{countOrStatusLabel}</span>
      </DisclosureRow>

      {/* File content panel — left-accent block, no bordered card. The
          content pane keeps its dark code-block styling
          (bg-[var(--color-code-surface)]). */}
      {expanded && !isRunning && content && (
        <div className="ml-[var(--space-1)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          <div className="text-[length:var(--type-caption-size)] text-[var(--color-muted)] font-mono break-all mb-[var(--space-1)]">{path}</div>
          <pre className="p-[var(--space-2)] text-[length:var(--type-caption-size)] leading-5 font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-72 overflow-auto bg-[var(--color-code-surface)]">
            {preview}
            {isTruncated && (
              <span className="text-[var(--color-muted)] italic">
                {'\n'}... ({lineCount - 20} more lines)
              </span>
            )}
          </pre>
        </div>
      )}
    </div>
  )
}

// Issue #617: isError comes from the tool-call part's own `isError` field
// (set in omnipus-runtime.ts from the store's resolved ToolCall.status), not
// from `status.type === 'incomplete'` — that can never be true for a
// finished call carrying a result.
export const FileReadPreviewUI = makeAssistantToolUI<ReadFileArgs, unknown>({
  toolName: 'read_file',
  render: ({ args, result, status, isError }) => (
    <FileReadBlock
      toolName="read_file"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (read_file); both registered.
export const FileReadAliasDotUI = makeAssistantToolUI<ReadFileArgs, unknown>({
  toolName: 'file.read',
  render: ({ args, result, status, isError }) => (
    <FileReadBlock
      toolName="file.read"
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={isError}
      isCancelled={isCancelledStatus(status)}
    />
  ),
})

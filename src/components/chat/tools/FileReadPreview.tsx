import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowsClockwise, CaretDown, CaretUp } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { statusDot } from '@/lib/toolStatusConfig'

interface ReadFileArgs {
  path?: string
  offset?: number
  length?: number
}

function basename(p: string): string {
  return p.split(/[/\\]/).pop() ?? p
}

function FileReadBlock({
  args,
  result,
  isRunning,
}: {
  args: ReadFileArgs
  result: unknown
  isRunning: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const path = args.path ?? '(unknown file)'
  const name = basename(path)
  const content = result != null ? String(result) : ''
  const lines = content.split('\n')
  const lineCount = lines.length
  const preview = lines.slice(0, 20).join('\n')
  const isTruncated = lineCount > 20

  return (
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The decorative FileText tool-type icon is gone; the leading
    // slot is the status dot/spinner only.
    <div className="mt-2 text-xs font-mono">
      {/* Header */}
      <button tabIndex={0}
        type="button"
        onClick={() => !isRunning && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 py-1 transition-colors text-left',
          !isRunning && 'hover:bg-[var(--color-surface-2)]/60 cursor-pointer',
          isRunning && 'cursor-default'
        )}
        aria-expanded={expanded}
        disabled={isRunning}
      >
        {isRunning ? (
          <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)] shrink-0" />
        ) : content ? (
          statusDot('bg-[var(--color-success)]')
        ) : null}
        <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">{name}</span>
        <span className="flex items-center gap-1.5 text-[var(--color-muted)] shrink-0">
          {content && <span>{lineCount} lines</span>}
          {!isRunning && (
            <span className="ml-1">{expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}</span>
          )}
        </span>
      </button>

      {/* File content panel — left-accent block, no bordered card. The
          content pane keeps its dark code-block styling (bg-[#0d1117]). */}
      {expanded && !isRunning && content && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] py-1 pl-3">
          <div className="text-[10px] text-[var(--color-muted)] font-mono break-all mb-1">{path}</div>
          <pre className="p-2 text-[10px] leading-5 font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-72 overflow-auto bg-[#0d1117]">
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

export const FileReadPreviewUI = makeAssistantToolUI<ReadFileArgs, unknown>({
  toolName: 'read_file',
  render: ({ args, result, status }) => (
    <FileReadBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
    />
  ),
})

// BRD C.6.1.4 tool name (dot-notation). Backend uses Omnipus convention (read_file); both registered.
export const FileReadAliasDotUI = makeAssistantToolUI<ReadFileArgs, unknown>({
  toolName: 'file.read',
  render: ({ args, result, status }) => (
    <FileReadBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
    />
  ),
})

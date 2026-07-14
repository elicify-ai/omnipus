import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import {
  FileText,
  ArrowsClockwise,
  CheckCircle,
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

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
    <div className="mt-2 rounded-md border border-[var(--color-border)] overflow-hidden text-xs">
      {/* Header */}
      <button
        type="button"
        onClick={() => !isRunning && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 bg-[var(--color-surface-1)] transition-colors text-left',
          !isRunning && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
          isRunning && 'cursor-default'
        )}
        aria-expanded={expanded}
        disabled={isRunning}
      >
        <FileText size={13} className="text-[var(--color-accent)] shrink-0" weight="duotone" />
        <span className="font-mono text-[var(--color-secondary)] truncate flex-1 min-w-0">{name}</span>
        <span className="flex items-center gap-1.5 text-[var(--color-muted)] shrink-0">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : content ? (
            <>
              <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
              <span>{lineCount} lines</span>
            </>
          ) : null}
          {!isRunning && (
            <span className="ml-1">{expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}</span>
          )}
        </span>
      </button>

      {/* File content panel */}
      {expanded && !isRunning && content && (
        <div className="border-t border-[var(--color-border)]">
          <div className="px-1 py-0.5 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
            <span className="text-[10px] text-[var(--color-muted)] font-mono px-2">{path}</span>
          </div>
          <pre className="px-3 py-2 text-[10px] leading-5 font-mono text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-72 overflow-auto bg-[#0d1117]">
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

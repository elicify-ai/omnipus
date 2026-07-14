import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import {
  Globe,
  ArrowsClockwise,
  CheckCircle,
  XCircle,
  CaretDown,
  CaretUp,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

interface WebFetchArgs {
  url?: string
  max_chars?: number
  start_index?: number
}

function truncateContent(text: string, maxLines = 30): { preview: string; truncated: boolean } {
  const lines = text.split('\n')
  if (lines.length <= maxLines) return { preview: text, truncated: false }
  return {
    preview: lines.slice(0, maxLines).join('\n'),
    truncated: true,
  }
}

function displayUrl(url: string): string {
  try {
    const u = new URL(url)
    return u.hostname + (u.pathname !== '/' ? u.pathname : '')
  } catch {
    return url
  }
}

function WebFetchBlock({
  args,
  result,
  isRunning,
  isError,
}: {
  args: WebFetchArgs
  result: unknown
  isRunning: boolean
  isError?: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const url = args.url ?? '(unknown URL)'
  const content = result != null ? String(result) : ''
  const { preview, truncated } = content ? truncateContent(content) : { preview: '', truncated: false }

  return (
    <div
      className={cn(
        'mt-2 rounded-md border overflow-hidden text-xs',
        isRunning
          ? 'border-[var(--color-border)]'
          : isError
          ? 'border-[var(--color-error)]/30'
          : 'border-[var(--color-border)]'
      )}
    >
      {/* Header */}
      <button
        type="button"
        onClick={() => !isRunning && content && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 bg-[var(--color-surface-1)] transition-colors text-left',
          !isRunning && content && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
          (isRunning || !content) && 'cursor-default'
        )}
        aria-expanded={expanded}
      >
        <Globe
          size={13}
          weight="duotone"
          className={cn(
            isRunning ? 'text-[var(--color-accent)]' :
            isError ? 'text-[var(--color-error)]' :
            'text-[var(--color-secondary)]'
          )}
        />
        <span className="text-[var(--color-muted)] shrink-0">web_fetch</span>
        <span className="font-mono text-[var(--color-accent)] truncate flex-1 min-w-0 text-[10px]">
          {displayUrl(url)}
        </span>
        <span className="flex items-center gap-1 shrink-0">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : isError ? (
            <XCircle size={12} weight="fill" className="text-[var(--color-error)]" />
          ) : content ? (
            <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
          ) : null}
          {!isRunning && content && (
            <span className="ml-1 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
            </span>
          )}
        </span>
      </button>

      {/* Content panel */}
      {expanded && !isRunning && content && (
        <div className="border-t border-[var(--color-border)]">
          <div className="px-2 py-0.5 bg-[var(--color-surface-1)] border-b border-[var(--color-border)]">
            <span className="text-[10px] text-[var(--color-muted)] font-mono break-all">{url}</span>
          </div>
          <pre className="px-3 py-2 text-[10px] leading-5 text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto bg-[var(--color-surface-1)]">
            {preview}
            {truncated && (
              <span className="text-[var(--color-muted)] italic">{'\n'}... (content truncated)</span>
            )}
          </pre>
        </div>
      )}
    </div>
  )
}

// New canonical name (post §7 rename: web_fetch → fetch_url)
export const WebFetchPreviewUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'fetch_url',
  render: ({ args, result, status }) => (
    <WebFetchBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

// Legacy alias kept for backward compat with old session transcripts.
export const WebFetchLegacyUI = makeAssistantToolUI<WebFetchArgs, unknown>({
  toolName: 'web_fetch',
  render: ({ args, result, status }) => (
    <WebFetchBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
      isError={status.type === 'incomplete'}
    />
  ),
})

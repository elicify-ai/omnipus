import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import {
  MagnifyingGlass,
  ArrowsClockwise,
  CheckCircle,
  CaretDown,
  CaretUp,
  ArrowSquareOut,
} from '@phosphor-icons/react'
import { cn } from '@/lib/utils'

interface WebSearchArgs {
  query?: string
  count?: number
  provider?: string
}

interface ParsedResult {
  index: number
  title: string
  url: string
  snippet: string
}

/** Best-effort parser for the text-based search results returned by web_search */
function parseSearchResults(text: string): ParsedResult[] {
  const results: ParsedResult[] = []
  // Pattern: lines starting with "1. Title\n   URL" or similar
  const blocks = text.split(/\n(?=\d+\. )/)

  for (const block of blocks) {
    const firstLine = block.split('\n')[0]
    const match = firstLine.match(/^(\d+)\.\s+(.+)/)
    if (!match) continue

    const index = parseInt(match[1], 10)
    const restLines = block.split('\n').slice(1)

    // Find URL line (starts with http or 3+ spaces)
    const urlLine = restLines.find((l) => l.trim().startsWith('http'))
    const url = urlLine?.trim() ?? ''

    // Title from match or URL
    const title = match[2].trim()

    // Snippet is everything else
    const snippet = restLines
      .filter((l) => l.trim() !== url)
      .join(' ')
      .trim()

    results.push({ index, title, url, snippet })
  }

  return results
}

function WebSearchBlock({
  args,
  result,
  isRunning,
}: {
  args: WebSearchArgs
  result: unknown
  isRunning: boolean
}) {
  const [expanded, setExpanded] = useState(false)

  const query = args.query ?? '(search query)'
  const content = result != null ? String(result) : ''
  const parsed = content ? parseSearchResults(content) : []
  const hasStructured = parsed.length > 0

  return (
    <div className="mt-2 rounded-md border border-[var(--color-border)] overflow-hidden text-xs">
      {/* Header */}
      <button tabIndex={0}
        type="button"
        onClick={() => !isRunning && setExpanded((e) => !e)}
        className={cn(
          'flex w-full items-center gap-2 px-3 py-2 bg-[var(--color-surface-1)] transition-colors text-left',
          !isRunning && 'hover:bg-[var(--color-surface-3)] cursor-pointer',
          isRunning && 'cursor-default'
        )}
        aria-expanded={expanded}
      >
        <MagnifyingGlass
          size={13}
          weight="bold"
          className={cn(
            isRunning ? 'text-[var(--color-accent)]' : 'text-[var(--color-secondary)]'
          )}
        />
        <span className="text-[var(--color-muted)] shrink-0">web_search</span>
        <span className="text-[var(--color-secondary)] truncate flex-1 min-w-0 italic">{query}</span>
        <span className="flex items-center gap-1.5 shrink-0">
          {isRunning ? (
            <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)]" />
          ) : content ? (
            <>
              <CheckCircle size={12} weight="fill" className="text-[var(--color-success)]" />
              {hasStructured && (
                <span className="text-[var(--color-muted)]">{parsed.length} results</span>
              )}
            </>
          ) : null}
          {!isRunning && content && (
            <span className="ml-1 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
            </span>
          )}
        </span>
      </button>

      {/* Results panel */}
      {expanded && !isRunning && content && (
        <div className="border-t border-[var(--color-border)] bg-[var(--color-surface-1)]">
          {hasStructured ? (
            <div className="divide-y divide-[var(--color-border)]">
              {parsed.map((item) => (
                <div key={item.index} className="px-3 py-2 space-y-0.5">
                  <div className="flex items-start gap-1.5">
                    <span className="text-[var(--color-muted)] shrink-0 mt-0.5">{item.index}.</span>
                    <div className="min-w-0">
                      <p className="text-[var(--color-secondary)] font-medium leading-snug break-words">
                        {item.title}
                      </p>
                      {item.url && (
                        <p className="text-[var(--color-accent)] font-mono text-[10px] truncate flex items-center gap-1">
                          {item.url}
                          <ArrowSquareOut size={9} className="shrink-0" />
                        </p>
                      )}
                      {item.snippet && (
                        <p className="text-[var(--color-muted)] text-[10px] leading-relaxed mt-0.5 line-clamp-2">
                          {item.snippet}
                        </p>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          ) : (
            <pre className="px-3 py-2 text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
              {content}
            </pre>
          )}
        </div>
      )}
    </div>
  )
}

export const WebSearchResultUI = makeAssistantToolUI<WebSearchArgs, unknown>({
  toolName: 'web_search',
  render: ({ args, result, status }) => (
    <WebSearchBlock
      args={args ?? {}}
      result={result}
      isRunning={status.type === 'running'}
    />
  ),
})

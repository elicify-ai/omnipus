import { useState } from 'react'
import { makeAssistantToolUI } from '@assistant-ui/react'
import { ArrowsClockwise, CaretDown, CaretUp, ArrowSquareOut } from '@phosphor-icons/react'
import { cn } from '@/lib/utils'
import { statusDot } from '@/lib/toolStatusConfig'

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
    // Flat text-line design (ticket "Tool components in chat", P2): no card
    // frame — see GenericToolCall.tsx/toolStatusConfig.tsx for the reference
    // language. The decorative MagnifyingGlass tool-type icon is gone; the
    // leading slot is the status dot/spinner only, same as the other rows.
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
      >
        {isRunning ? (
          <ArrowsClockwise size={12} className="animate-spin text-[var(--color-accent)] shrink-0" />
        ) : content ? (
          statusDot('bg-[var(--color-success)]')
        ) : null}
        <span className="text-[var(--color-muted)] shrink-0">web_search</span>
        <span className="text-[var(--color-secondary)] truncate flex-1 min-w-0 italic">{query}</span>
        <span className="flex items-center gap-1.5 shrink-0">
          {hasStructured && (
            <span className="text-[var(--color-muted)]">{parsed.length} results</span>
          )}
          {!isRunning && content && (
            <span className="ml-1 text-[var(--color-muted)]">
              {expanded ? <CaretUp size={10} /> : <CaretDown size={10} />}
            </span>
          )}
        </span>
      </button>

      {/* Results panel — left-accent block, no bordered card. Inner content
          keeps its identity (numbered result list / raw preview) but the
          old divide-y row dividers are gone; spacing carries the separation. */}
      {expanded && !isRunning && content && (
        <div className="ml-[3px] border-l-2 border-[var(--color-border)] py-1 pl-3">
          {hasStructured ? (
            <div className="space-y-2">
              {parsed.map((item) => (
                <div key={item.index} className="flex items-start gap-1.5">
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
              ))}
            </div>
          ) : (
            <pre className="text-[10px] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-64 overflow-auto">
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

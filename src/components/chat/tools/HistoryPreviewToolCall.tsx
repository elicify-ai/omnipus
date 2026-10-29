// #1452 D2 A2.4: a saved tool row whose result the history page shortened to
// a HistoryResultPreview marker. Every tool, specialized or not, falls back to
// this one display: name, status and the true size; opening the row reads the
// occurrence's full content once (coalesced per position) and shows it in
// place of the preview. Output is plain monospace text, so a result over 1 MiB
// is never highlighted.
import { useState } from 'react'
import { DisclosureRow } from '@/components/ui/disclosure-row'
import type { HistoryResultPreview } from '@/lib/api/generated/asyncapi-types'
import type { ToolCall } from '@/lib/api'
import { humanizeToolName } from '@/lib/humanizeToolName'
import { humanByteSize } from '@/lib/historyPreview'
import { useHistoryEntryRead } from '@/lib/historyEntryReads'
import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'
import { HistoryEntryReadOutcome } from '../historyEntryOutcome'
import { safeJson } from './toolResultDisplay'

/** The full result as text: a plain `text`/`content` string envelope, else its JSON. */
function fullOutputText(result: unknown): string {
  if (typeof result === 'string') return result
  if (result && typeof result === 'object' && !Array.isArray(result)) {
    const record = result as Record<string, unknown>
    for (const key of ['text', 'content'] as const) {
      if (typeof record[key] === 'string' && Object.keys(record).length === 1) return record[key] as string
    }
  }
  return safeJson(result)
}

const outputClass = 'text-[length:var(--type-caption-size)] text-[var(--color-secondary)] whitespace-pre-wrap break-all max-h-96 overflow-auto'

export function HistoryPreviewToolCall({ tc, marker, sessionId, historyPosition }: {
  tc: ToolCall
  marker: HistoryResultPreview
  sessionId: string
  /** The saved occurrence that owns this call; without one the full content cannot be addressed. */
  historyPosition: string | undefined
}) {
  const [expanded, setExpanded] = useState(false)
  const read = useHistoryEntryRead(sessionId, historyPosition ?? '', expanded && !!historyPosition)
  const fullCall = read.data?.role === 'assistant' ? read.data.tool_calls?.find(call => call.id === tc.id) : undefined
  const status = getToolBadgeStatusConfig(tc.status, { size: 12 })
  return (
    <div data-testid="tool-call-badge" data-tool={tc.tool} className="mt-[var(--space-2)] text-[length:var(--type-utility-xs-size)] font-mono">
      <div className="flex w-full items-center gap-[var(--space-2)]">
        <DisclosureRow expanded={expanded} onExpandedChange={setExpanded} expandable data-testid="tool-call-toggle">
          {status.indicator}
          <span className="text-[var(--color-secondary)] font-medium">{humanizeToolName(tc.tool)}</span>
          <span className="text-[var(--color-muted)]">{status.label}</span>
          <span className="text-[var(--color-muted)]">{humanByteSize(marker.original_size_bytes)}</span>
        </DisclosureRow>
      </div>
      {expanded && (
        <div className="ml-[var(--space-1)] space-y-[var(--space-2)] border-l-2 border-[var(--color-border)] py-[var(--space-1)] pl-[var(--space-2-5)]">
          {fullCall ? (
            <pre tabIndex={0} role="region" aria-label="Tool output" className={outputClass}>{fullOutputText(fullCall.result)}</pre>
          ) : (
            <>
              <pre tabIndex={0} role="region" aria-label="Tool output preview" className={outputClass}>{marker.preview}</pre>
              {historyPosition
                ? <HistoryEntryReadOutcome read={read} incomplete={read.isSuccess && !fullCall} />
                : <p className="font-body text-[length:var(--type-caption-size)] text-[var(--color-muted)]">Only the saved preview is available here.</p>}
            </>
          )}
        </div>
      )}
    </div>
  )
}

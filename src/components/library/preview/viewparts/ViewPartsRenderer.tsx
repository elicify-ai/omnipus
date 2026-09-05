// ViewPartsRenderer — walks one ViewResult's part stack in order inside one
// view frame (view-kinds-design-2026-09-03 §7), and owns the three states
// that are not a drawn part:
//
//   REFUSAL   — the server said WHY the view cannot answer (a 200 with
//               `refusal` set, never a transport error). Rendered with the
//               server's code/reason/remedy verbatim; never a blank panel.
//   EMPTY     — zero rows. Lead with the outcome in plain words, then say
//               what was looked for (the wireframe's "A view with nothing in
//               it": "otherwise a broken filter and a clear desk look
//               identical"). Never a bare blank.
//   TRUNCATED — the row set exceeded the server's render bound: the page of
//               rows renders, and a notice states that totals were NOT
//               computed, because a total over part of the rows would be a
//               wrong number that looks right.
//
// The dispatch is an exhaustive switch closed by a `never` assignment — the
// same idiom LibraryPreviewPane uses for kinds, for the same reason: a new
// part member must fail the compile, not render silence.

import { WarningCircle, Prohibit } from '@phosphor-icons/react'
import type { VaultFindRow, ViewResult, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { TablePart } from './TablePart'
import { ListPart } from './ListPart'
import { TilesPart } from './TilesPart'
import { ColumnsPart } from './ColumnsPart'
import { CalendarPart } from './CalendarPart'
import { FiguresPart } from './FiguresPart'
import { ChartPart } from './ChartPart'
import { CrosstabPart } from './CrosstabPart'

function renderPart(
  part: ViewResultPart,
  rows: VaultFindRow[],
  resolveImageUrl?: (vaultPath: string) => string | undefined,
) {
  switch (part.part) {
    case 'table':
      return <TablePart part={part} rows={rows} />
    case 'list':
      return <ListPart part={part} rows={rows} />
    case 'tiles':
      return <TilesPart part={part} rows={rows} {...(resolveImageUrl ? { resolveImageUrl } : {})} />
    case 'columns':
      return <ColumnsPart part={part} rows={rows} />
    case 'calendar':
      return <CalendarPart part={part} rows={rows} />
    case 'figures':
      return <FiguresPart part={part} />
    case 'chart':
      return <ChartPart part={part} />
    case 'crosstab':
      return <CrosstabPart part={part} />
    default: {
      const unhandled: never = part.part
      void unhandled
      return null
    }
  }
}

/** The server's refusal, verbatim: what cannot be done, and what to do. */
function RefusalState({ refusal }: { refusal: NonNullable<ViewResult['refusal']> }) {
  return (
    <div
      role="alert"
      className="flex flex-col items-center gap-2 px-6 py-10 text-center"
      data-testid="view-refusal"
    >
      <Prohibit size={24} className="text-[var(--color-warning)]" />
      <p className="text-[13px] font-medium text-[var(--color-secondary)]">This view can’t answer.</p>
      <p className="max-w-md text-[12px] leading-relaxed text-[var(--color-muted)]">{refusal.reason}</p>
      {refusal.remedy !== '' && (
        <p className="max-w-md text-[12px] leading-relaxed text-[var(--color-muted)]" data-testid="view-refusal-remedy">
          {refusal.remedy}
        </p>
      )}
      <p className="font-mono text-[10px] text-[var(--color-muted)]/60">{refusal.code}</p>
    </div>
  )
}

/** Zero rows: the outcome in plain words, then what was looked for. */
function EmptyState({ result, filterText }: { result: ViewResult; filterText?: string | undefined }) {
  return (
    <div className="flex flex-col gap-1.5 px-4 py-8" data-testid="view-empty">
      <p className="text-[13px] text-[var(--color-secondary)]">
        {result.complete ? 'Nothing matches this view.' : 'Nothing to show yet.'}
      </p>
      {filterText !== undefined && filterText !== '' ? (
        <div className="text-[11px] leading-relaxed text-[var(--color-muted)]">
          <p>What was looked for:</p>
          <pre className="mt-1 max-w-full overflow-x-auto rounded border border-[var(--color-border)] bg-[var(--color-surface-1)] px-2 py-1.5 font-mono text-[11px]">
            {filterText}
          </pre>
        </div>
      ) : (
        <p className="text-[11px] text-[var(--color-muted)]">
          {result.type !== undefined && result.type !== ''
            ? `This view shows every ${result.type} record its filter admits; none matched.`
            : 'This view declares no filter the preview can show; the collection simply has no matching records.'}
        </p>
      )}
      {!result.complete && result.complete_reason !== undefined && result.complete_reason !== '' && (
        <p className="text-[11px] text-[var(--color-warning)]" data-testid="view-empty-incomplete">
          {result.complete_reason}
        </p>
      )}
    </div>
  )
}

export function ViewPartsRenderer({
  result,
  filterText,
  resolveImageUrl,
}: {
  result: ViewResult
  /** Raw filter text from the .base file, for the empty state's "what was
   *  looked for" line. */
  filterText?: string | undefined
  /** Vault-relative image path → servable URL, for the tiles part. */
  resolveImageUrl?: (vaultPath: string) => string | undefined
}) {
  if (result.refusal !== undefined) return <RefusalState refusal={result.refusal} />
  if (result.rows.length === 0) return <EmptyState result={result} filterText={filterText} />

  return (
    <div className="flex flex-col" data-testid="view-parts">
      {result.rows_truncated === true && (
        <p
          className="flex items-start gap-1.5 border-b border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-3 py-1.5 text-[11px] leading-snug text-[var(--color-warning)]"
          data-testid="view-truncated"
        >
          <WarningCircle size={13} weight="fill" className="mt-px shrink-0" />
          <span>
            Only the first {result.rows.length} rows are shown, and no totals were computed — a total over
            part of the rows would be a wrong number that looks right.
          </span>
        </p>
      )}
      {result.parts.map((part, i) => (
        <div
          key={`${part.part}-${i}`}
          className="border-b border-[var(--color-border)] last:border-b-0"
          data-testid={`view-part-${part.part}`}
        >
          {renderPart(part, result.rows, resolveImageUrl)}
        </div>
      ))}
      {result.problems.length > 0 && (
        <div className="px-3 py-1.5" data-testid="view-problems">
          {result.problems.map((p, i) => (
            <p key={`${p.code}-${i}`} className="text-[11px] leading-snug text-[var(--color-warning)]">
              {p.reason}
            </p>
          ))}
        </div>
      )}
    </div>
  )
}

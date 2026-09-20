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
import type { ViewCellLinkResolver } from './ViewCellLink'
import type { RecordEditContext, RecordFieldWriteResult } from './RecordFieldEditor'

/** KB-8a — every part that draws individual records gets a row-open action.
 *  `figures`, `chart` and `crosstab` deliberately do NOT: each one draws a
 *  precomputed AGGREGATE (a sum/avg/count, a date-bucketed series point, a
 *  cross-tabulated cell) that already stands for many rows folded into one
 *  number — there is no single row.path a click on one of those could open. */
function renderPart(
  part: ViewResultPart,
  rows: VaultFindRow[],
  resolveImageUrl?: (vaultPath: string) => string | undefined,
  onOpenPath?: (path: string) => void,
  cellLinks?: ViewCellLinkResolver,
  editContext?: RecordEditContext,
  labels?: ViewResult['property_config'],
) {
  switch (part.part) {
    case 'table':
      return (
        <TablePart
          part={part}
          rows={rows}
          {...(onOpenPath ? { onOpenPath } : {})}
          {...(cellLinks ? { cellLinks } : {})}
          {...(editContext ? { editContext } : {})}
          {...(labels ? { labels } : {})}
        />
      )
    case 'list':
      return (
        <ListPart
          part={part}
          rows={rows}
          {...(onOpenPath ? { onOpenPath } : {})}
          {...(cellLinks ? { cellLinks } : {})}
          {...(editContext ? { editContext } : {})}
        />
      )
    case 'tiles':
      return (
        <TilesPart
          part={part}
          rows={rows}
          {...(resolveImageUrl ? { resolveImageUrl } : {})}
          {...(onOpenPath ? { onOpenPath } : {})}
        />
      )
    case 'columns':
      return <ColumnsPart part={part} rows={rows} {...(onOpenPath ? { onOpenPath } : {})} />
    case 'calendar':
      return <CalendarPart part={part} rows={rows} {...(onOpenPath ? { onOpenPath } : {})} />
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
      className="flex flex-col items-center gap-[var(--space-2)] px-[var(--space-4)] py-[var(--space-6)] text-center"
      data-testid="view-refusal"
    >
      <Prohibit size={24} className="text-[var(--color-warning)]" />
      <p className="text-[13px] font-medium text-[var(--color-secondary)]">This view can’t answer.</p>
      <p className="max-w-md text-[length:var(--type-caption-size)] leading-relaxed text-[var(--color-muted)]">{refusal.reason}</p>
      {refusal.remedy !== '' && (
        <p className="max-w-md text-[length:var(--type-caption-size)] leading-relaxed text-[var(--color-muted)]" data-testid="view-refusal-remedy">
          {refusal.remedy}
        </p>
      )}
      <p className="font-mono text-[length:var(--type-caption-size)] text-[var(--color-muted)]/60">{refusal.code}</p>
    </div>
  )
}

/** Zero rows: the outcome in plain words, then what the view was looking for.
 *
 *  It used to quote the `filters:` block the SPA had parsed out of the .base
 *  file. That parser is gone (findings #3 and #7 — it could not reproduce the
 *  importer's slugs and mistook nested keys for view names), and quoting a
 *  filter nobody re-read would mean re-introducing it for a caption. The view
 *  itself answers the same question honestly: what type it draws, and that
 *  nothing matched. */
function EmptyState({ result }: { result: ViewResult }) {
  return (
    <div className="flex flex-col gap-[var(--space-1)] px-[var(--space-3)] py-[var(--space-5)]" data-testid="view-empty">
      <p className="text-[13px] text-[var(--color-secondary)]">
        {result.complete ? 'Nothing matches this view.' : 'Nothing to show yet.'}
      </p>
      <p className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {result.type !== undefined && result.type !== ''
          ? `This view shows every ${result.type} record its filter admits; none matched.`
          : 'This view declares no filter the preview can show; the collection simply has no matching records.'}
      </p>
      {!result.complete && result.complete_reason !== undefined && result.complete_reason !== '' && (
        <p className="text-[length:var(--type-caption-size)] text-[var(--color-warning)]" data-testid="view-empty-incomplete">
          {result.complete_reason}
        </p>
      )}
    </div>
  )
}

export function ViewPartsRenderer({
  result,
  resolveImageUrl,
  onOpenPath,
  resolveWikilink,
  linkHref,
  workspaceId,
  collectionId,
  onFieldWritten,
}: {
  result: ViewResult
  /** Vault-relative image path → servable URL, for the tiles part. */
  resolveImageUrl?: (vaultPath: string) => string | undefined
  /** Opens a record's own note (KB-8a). Absent renders every part exactly as
   *  before — no row is a click target. */
  onOpenPath?: (path: string) => void
  /** Resolves a relation cell's `[[wikilink]]` target against this view's own
   *  rows (KB-8b) — see BasePreview.tsx for how it is built. */
  resolveWikilink?: ViewCellLinkResolver['resolveWikilink']
  linkHref?: ViewCellLinkResolver['linkHref']
  /** Enables ADR-083 D8 inline record-field editing for every table/list
   *  cell whose own metadata allows one (RecordFieldEditor.tsx's
   *  isEditableCell gate). Absent renders every cell exactly as before —
   *  plain text, never an editor; this is also what a caller gets for FREE
   *  on an untyped view (`result.type` absent), since RecordWriteRequest
   *  needs a record type to write with and a cell in an untyped view never
   *  carries editable metadata anyway. */
  workspaceId?: string
  /** GAP-02 / #700: the collection the view's records live in. Optional on
   *  purpose — the VALUE editors do not need it; the relation/person PICKER
   *  does (its search is scoped by collection). Absent leaves relation cells
   *  inert, exactly as before this prop existed. */
  collectionId?: string
  /** Invoked after a successful inline field write. The intended wiring
   *  point is BasePreview (owner of this view's TanStack Query cache): it
   *  invalidates the per-note caches ADR-083 §4.5 names (content, outline,
   *  links) and this collection's view-result queries, and may patch its
   *  own cached rows so both a dashboard embed and the ordinary view
   *  surface reflect the write at once. This component never touches a
   *  query client itself. */
  onFieldWritten?: (result: RecordFieldWriteResult) => void
}) {
  const cellLinks: ViewCellLinkResolver | undefined =
    resolveWikilink !== undefined || linkHref !== undefined || onOpenPath !== undefined
      ? { resolveWikilink, linkHref, onOpenPath }
      : undefined

  const editContext: RecordEditContext | undefined =
    workspaceId !== undefined
      ? {
          workspaceId,
          recordType: result.type,
          ...(collectionId !== undefined ? { collectionId } : {}),
          onFieldWritten,
        }
      : undefined

  if (result.refusal !== undefined) return <RefusalState refusal={result.refusal} />
  if (result.rows.length === 0) return <EmptyState result={result} />

  return (
    <div className="flex flex-col" data-testid="view-parts">
      {result.rows_truncated === true && (
        <p
          className="flex items-start gap-[var(--space-1)] border-b border-[var(--color-warning)]/40 bg-[var(--color-warning)]/10 px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]"
          data-testid="view-truncated"
        >
          <WarningCircle size={13} weight="fill" className="mt-[var(--border-width-hairline)] shrink-0" />
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
          {renderPart(part, result.rows, resolveImageUrl, onOpenPath, cellLinks, editContext, result.property_config)}
        </div>
      ))}
      {result.aggregates !== undefined && result.aggregates.length > 0 && (
        // UAT D-68 — a `.base` `summaries:` block (imported as the view's
        // own `aggregates:`) is computed and returned by the server, and
        // used to be dropped on the floor here unless the author had ALSO
        // spelled it as a `figures` part. Drawn as a footer, each total in
        // the same sentence as its scope (FR-125a).
        <div
          className="flex flex-col gap-[var(--space-0-5)] border-t border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-2)]"
          data-testid="view-aggregates"
        >
          {result.aggregates.map((t, i) => (
            <p
              key={`${t.label}|${t.unit ?? ' '}|${i}`}
              className="text-[length:var(--type-caption-size)] leading-snug text-[var(--color-secondary)]"
              data-testid="view-aggregate"
            >
              <span className="text-[length:var(--type-caption-size)] uppercase tracking-[0.07em] text-[var(--color-muted)]">{t.label}</span>{' '}
              <span className="font-mono tabular-nums">{t.value}</span>
              {t.unit !== undefined && <span className="ml-[var(--space-1)] text-[var(--color-muted)]">{t.unit}</span>}
              <span className="ml-[var(--space-1)] text-[var(--color-muted)]">{t.scope}</span>
            </p>
          ))}
        </div>
      )}
      {result.problems.length > 0 && (
        <div className="px-[var(--space-2-5)] py-[var(--space-1)]" data-testid="view-problems">
          {result.problems.map((p, i) => (
            <p key={`${p.code}-${i}`} className="text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]">
              {p.reason}
            </p>
          ))}
        </div>
      )}
    </div>
  )
}

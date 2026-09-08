// ListPart — name + one detail per row (view-kinds-design-2026-09-03 §2.2
// list; wireframe "List — for reading, not scanning"). The lightest shape:
// the row's title leads, the first non-name column trails muted. Grouped
// lists keep their group headers; totals, when the part declares them, are
// the same G2/G3 footer every part carries.

import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue, rowExcludedFromTotals, rowsByPath, FILE_NAME_PROPERTY } from './viewResultData'
import { ExcludedRowMark, GroupHeaderLabel, TotalsFooter } from './PartChrome'
import { CellText, type ViewCellLinkResolver } from './ViewCellLink'

function detailProperty(part: ViewResultPart): string | undefined {
  return (part.columns ?? []).find((c) => c !== FILE_NAME_PROPERTY)
}

function ListRow({
  row,
  part,
  detail,
  onOpenPath,
  cellLinks,
}: {
  row: VaultFindRow
  part: ViewResultPart
  detail: string | undefined
  onOpenPath?: ((path: string) => void) | undefined
  cellLinks?: ViewCellLinkResolver | undefined
}) {
  const detailValue = detail === undefined ? '' : cellValue(row, detail)
  return (
    <li
      className={`flex items-baseline gap-2 border-b border-[var(--color-border)] px-3 py-1.5 text-[13px] last:border-b-0 ${
        onOpenPath ? 'cursor-pointer hover:bg-[var(--color-surface-2)]/40' : ''
      }`}
      data-testid="viewpart-list-row"
      {...(onOpenPath ? { onClick: () => onOpenPath(row.path) } : {})}
    >
      {onOpenPath ? (
        <button
          type="button"
          tabIndex={0}
          onClick={(event) => {
            event.stopPropagation()
            onOpenPath(row.path)
          }}
          aria-label={`Open ${row.title}`}
          data-testid="viewpart-row-open"
          className="min-w-0 truncate text-left text-[var(--color-secondary)]"
        >
          {row.title}
        </button>
      ) : (
        <span className="min-w-0 truncate text-[var(--color-secondary)]">{row.title}</span>
      )}
      {detailValue !== '' && (
        <span className="min-w-0 truncate text-[12px] text-[var(--color-muted)]">
          · {cellLinks ? <CellText value={detailValue} resolver={cellLinks} /> : detailValue}
        </span>
      )}
      {rowExcludedFromTotals(row, part) && <ExcludedRowMark />}
    </li>
  )
}

export function ListPart({
  part,
  rows,
  onOpenPath,
  cellLinks,
}: {
  part: ViewResultPart
  rows: VaultFindRow[]
  /** Opens a row's own note (KB-8a). Absent renders every row exactly as
   *  before — inert markup, no button, no cursor change. */
  onOpenPath?: (path: string) => void
  /** Renders the detail cell's raw `[[wikilink]]` as a real link (KB-8b). */
  cellLinks?: ViewCellLinkResolver
}) {
  const detail = detailProperty(part)
  const byPath = rowsByPath(rows)
  const groups = part.groups

  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-list">
      {groups === undefined ? (
        <ul>
          {rows.map((row) => (
            <ListRow key={row.path} row={row} part={part} detail={detail} onOpenPath={onOpenPath} cellLinks={cellLinks} />
          ))}
        </ul>
      ) : (
        groups.map((group) => (
          <div key={`${group.key}|${group.absent === true}`}>
            <div className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1">
              <GroupHeaderLabel label={group.key} count={group.count} absent={group.absent} />
            </div>
            <ul>
              {group.paths
                .map((p) => byPath.get(p))
                .filter((r): r is VaultFindRow => r !== undefined)
                .map((row) => (
                  <ListRow
                    key={row.path}
                    row={row}
                    part={part}
                    detail={detail}
                    onOpenPath={onOpenPath}
                    cellLinks={cellLinks}
                  />
                ))}
            </ul>
          </div>
        ))
      )}
      <TotalsFooter
        totals={part.totals ?? []}
        excludedCount={part.excluded_count}
        excludedReason={part.excluded_reason}
      />
    </div>
  )
}

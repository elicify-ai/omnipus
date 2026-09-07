// ListPart — name + one detail per row (view-kinds-design-2026-09-03 §2.2
// list; wireframe "List — for reading, not scanning"). The lightest shape:
// the row's title leads, the first non-name column trails muted. Grouped
// lists keep their group headers; totals, when the part declares them, are
// the same G2/G3 footer every part carries.

import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue, rowExcludedFromTotals, rowsByPath, FILE_NAME_PROPERTY } from './viewResultData'
import { ExcludedRowMark, GroupHeaderLabel, TotalsFooter } from './PartChrome'

function detailProperty(part: ViewResultPart): string | undefined {
  return (part.columns ?? []).find((c) => c !== FILE_NAME_PROPERTY)
}

function ListRow({ row, part, detail }: { row: VaultFindRow; part: ViewResultPart; detail: string | undefined }) {
  const detailValue = detail === undefined ? '' : cellValue(row, detail)
  return (
    <li
      className="flex items-baseline gap-2 border-b border-[var(--color-border)] px-3 py-1.5 text-[13px] last:border-b-0"
      data-testid="viewpart-list-row"
    >
      <span className="min-w-0 truncate text-[var(--color-secondary)]">{row.title}</span>
      {detailValue !== '' && (
        <span className="min-w-0 truncate text-[12px] text-[var(--color-muted)]">· {detailValue}</span>
      )}
      {rowExcludedFromTotals(row, part) && <ExcludedRowMark />}
    </li>
  )
}

export function ListPart({ part, rows }: { part: ViewResultPart; rows: VaultFindRow[] }) {
  const detail = detailProperty(part)
  const byPath = rowsByPath(rows)
  const groups = part.groups

  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-list">
      {groups === undefined ? (
        <ul>
          {rows.map((row) => (
            <ListRow key={row.path} row={row} part={part} detail={detail} />
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
                  <ListRow key={row.path} row={row} part={part} detail={detail} />
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

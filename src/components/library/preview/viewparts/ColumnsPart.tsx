// ColumnsPart — status columns, read-only (view-kinds-design-2026-09-03 §2.2
// columns; wireframe "Board — group by a choice"). One column per group the
// server sent — the board is drawn from `groups` when the part groups by the
// choice property, else derived by reading each row's choice cell (still no
// arithmetic: distributing rows into named buckets is layout, not
// aggregation). READ-ONLY by design: dragging a card edits the record's
// file, which is the agent's job, not this preview's.

import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue, rowExcludedFromTotals, rowsByPath } from './viewResultData'
import { ExcludedRowMark, GroupHeaderLabel, TotalsFooter } from './PartChrome'

interface Column {
  key: string
  absent: boolean
  rows: VaultFindRow[]
}

function columnsFromGroups(part: ViewResultPart, rows: VaultFindRow[]): Column[] | undefined {
  const groups = part.groups
  if (groups === undefined) return undefined
  const byPath = rowsByPath(rows)
  return groups.map((g) => ({
    key: g.key,
    absent: g.absent === true,
    rows: g.paths.map((p) => byPath.get(p)).filter((r): r is VaultFindRow => r !== undefined),
  }))
}

function columnsFromChoiceCells(part: ViewResultPart, rows: VaultFindRow[]): Column[] {
  const choice = part.source.choice
  const order: string[] = []
  const buckets = new Map<string, VaultFindRow[]>()
  const absentRows: VaultFindRow[] = []
  for (const row of rows) {
    const v = choice === undefined ? '' : cellValue(row, choice)
    if (v === '') {
      absentRows.push(row)
      continue
    }
    if (!buckets.has(v)) {
      buckets.set(v, [])
      order.push(v)
    }
    buckets.get(v)?.push(row)
  }
  const out: Column[] = order.map((key) => ({ key, absent: false, rows: buckets.get(key) ?? [] }))
  if (absentRows.length > 0) out.push({ key: '', absent: true, rows: absentRows })
  return out
}

function Card({ row, part }: { row: VaultFindRow; part: ViewResultPart }) {
  const numberProperty = part.source.number
  const amount = numberProperty === undefined ? '' : cellValue(row, numberProperty)
  return (
    <div
      className="rounded border border-[var(--color-border)] bg-[var(--color-surface-2)] px-2 py-1.5 text-[12px] text-[var(--color-secondary)]"
      data-testid="viewpart-board-card"
    >
      <span className="truncate">{row.title}</span>
      {amount !== '' && <span className="ml-1 text-[var(--color-muted)]">· {amount}</span>}
      {rowExcludedFromTotals(row, part) && <ExcludedRowMark />}
    </div>
  )
}

export function ColumnsPart({ part, rows }: { part: ViewResultPart; rows: VaultFindRow[] }) {
  const columns = columnsFromGroups(part, rows) ?? columnsFromChoiceCells(part, rows)
  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-columns">
      <div className="overflow-x-auto p-3">
        <div className="flex items-start gap-2">
          {columns.map((col) => (
            <div
              key={`${col.key}|${col.absent}`}
              className="flex w-44 shrink-0 flex-col gap-1.5 rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] p-2"
              data-testid="viewpart-board-column"
            >
              <GroupHeaderLabel label={col.key} count={col.rows.length} absent={col.absent} />
              {col.rows.map((row) => (
                <Card key={row.path} row={row} part={part} />
              ))}
            </div>
          ))}
        </div>
      </div>
      <TotalsFooter
        totals={part.totals ?? []}
        excludedCount={part.excluded_count}
        excludedReason={part.excluded_reason}
      />
    </div>
  )
}

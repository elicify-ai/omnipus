// TablePart — rows × columns, with group header rows and per-group per-unit
// subtotal rows (view-kinds-design-2026-09-03 §2.2 table, §3 G2/G3; visual
// spec: the wireframe's "The accounting view" table).
//
// The renderer DRAWS the server's answer and nothing else: groups arrive with
// their subtotals already reduced once per unit value (ViewResultGroup), the
// footer totals arrive the same way (ViewResultPart.totals), and this file
// never adds two numbers. The one judgement it makes locally is WHERE the G3
// warn mark goes — the row whose unit cell is empty — which is presentation,
// not arithmetic; the server already excluded that row from every figure.
//
// A number with a declared companion unit draws as ONE value ("12,480.00
// SGD", design §5), and the unit property loses its own column when both are
// listed.

import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { cellValue, formatNumberText, rowExcludedFromTotals, rowsByPath, FILE_NAME_PROPERTY } from './viewResultData'
import { ExcludedRowMark, GroupHeaderLabel, TotalsFooter, UnitValue } from './PartChrome'

/** Column header text: 'file.name' reads as "Name", the rest as declared. */
function columnLabel(property: string): string {
  return property === FILE_NAME_PROPERTY ? 'Name' : property.replace(/^file\./, '')
}

function numericProperties(part: ViewResultPart): Set<string> {
  const set = new Set<string>()
  if (part.source.number !== undefined && part.source.number !== '') set.add(part.source.number)
  for (const t of part.totals ?? []) set.add(t.property)
  for (const g of part.groups ?? []) for (const s of g.subtotals) set.add(s.property)
  const subtotals = part.source.subtotals
  if (subtotals !== undefined) for (const k of Object.keys(subtotals)) set.add(k)
  return set
}

function Cell({
  row,
  property,
  part,
  numeric,
  excluded,
}: {
  row: VaultFindRow
  property: string
  part: ViewResultPart
  numeric: boolean
  excluded: boolean
}) {
  const value = cellValue(row, property)
  if (!numeric) {
    return (
      <td className="max-w-[16rem] truncate border-b border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">
        {value}
      </td>
    )
  }
  const unitProperty = part.source.unit
  const unit = unitProperty === undefined || unitProperty === '' ? undefined : cellValue(row, unitProperty)
  return (
    <td className="whitespace-nowrap border-b border-[var(--color-border)] px-3 py-1.5 text-right">
      {value === '' ? (
        <span className="text-[var(--color-muted)]">—</span>
      ) : (
        <span className={excluded ? 'text-[var(--color-muted)]' : undefined}>
          <UnitValue value={value} unit={unit === '' ? undefined : unit} />
        </span>
      )}
      {excluded && <ExcludedRowMark />}
    </td>
  )
}

function BodyRows({
  rows,
  columns,
  part,
  numeric,
}: {
  rows: VaultFindRow[]
  columns: string[]
  part: ViewResultPart
  numeric: Set<string>
}) {
  return (
    <>
      {rows.map((row) => {
        const excluded = rowExcludedFromTotals(row, part)
        return (
          <tr key={row.path} data-testid="viewpart-table-row">
            {columns.map((property) => (
              <Cell
                key={property}
                row={row}
                property={property}
                part={part}
                numeric={numeric.has(property)}
                excluded={excluded && numeric.has(property)}
              />
            ))}
          </tr>
        )
      })}
    </>
  )
}

export function TablePart({ part, rows }: { part: ViewResultPart; rows: VaultFindRow[] }) {
  // code-review finding #3(b): `part.columns ?? [FILE_NAME_PROPERTY]` only
  // caught `undefined` — a part that "declares no properties" as the EMPTY
  // ARRAY sailed through with zero columns, which made the group-header
  // row's `colSpan={columns.length}` zero and the subtotal label's
  // `colSpan={columns.length - 1}` negative (both invalid HTML). Guard both
  // shapes the same way.
  const allColumns = part.columns !== undefined && part.columns.length > 0 ? part.columns : [FILE_NAME_PROPERTY]
  // §5: the declared unit property draws inside the number cell, not as its
  // own column — but only when the part actually binds a number to it.
  const unitProperty = part.source.unit
  const filteredColumns =
    unitProperty !== undefined && unitProperty !== '' && part.source.number !== undefined
      ? allColumns.filter((c) => c !== unitProperty)
      : allColumns
  // The unit-column filter above can ALSO empty the list (a one-column part
  // whose sole column happens to be the unit property) — re-apply the same
  // floor rather than trust the filter to always leave something behind.
  const columns = filteredColumns.length > 0 ? filteredColumns : [FILE_NAME_PROPERTY]
  const numeric = numericProperties(part)
  const byPath = rowsByPath(rows)
  const groups = part.groups

  return (
    <div className="flex min-h-0 flex-col" data-testid="viewpart-table">
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[13px]">
          <thead>
            <tr>
              {columns.map((property) => (
                <th
                  key={property}
                  className={`border-b border-[var(--color-border)] px-3 py-1.5 text-[10px] font-medium uppercase tracking-[0.08em] text-[var(--color-muted)] ${
                    numeric.has(property) ? 'text-right' : 'text-left'
                  }`}
                >
                  {columnLabel(property)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {groups === undefined ? (
              <BodyRows rows={rows} columns={columns} part={part} numeric={numeric} />
            ) : (
              groups.map((group) => {
                const memberRows = group.paths
                  .map((p) => byPath.get(p))
                  .filter((r): r is VaultFindRow => r !== undefined)
                return (
                  <FragmentRows
                    key={`${group.key}|${group.absent === true}`}
                    group={group}
                    memberRows={memberRows}
                    columns={columns}
                    part={part}
                    numeric={numeric}
                  />
                )
              })
            )}
          </tbody>
        </table>
      </div>
      <TotalsFooter
        totals={part.totals ?? []}
        excludedCount={part.excluded_count}
        excludedReason={part.excluded_reason}
      />
    </div>
  )
}

function FragmentRows({
  group,
  memberRows,
  columns,
  part,
  numeric,
}: {
  group: NonNullable<ViewResultPart['groups']>[number]
  memberRows: VaultFindRow[]
  columns: string[]
  part: ViewResultPart
  numeric: Set<string>
}) {
  return (
    <>
      {/* Group header row — the wireframe's `tr.grp`. */}
      <tr data-testid="viewpart-group-header">
        <td
          colSpan={columns.length}
          className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1"
        >
          <GroupHeaderLabel label={group.key} count={group.count} absent={group.absent} />
        </td>
      </tr>
      <BodyRows rows={memberRows} columns={columns} part={part} numeric={numeric} />
      {/* Per-group, per-unit subtotal rows — the wireframe's `tr.sub`. ONE ROW
          PER UNIT VALUE (G2): the list shape upstream makes a combined figure
          inexpressible, and this renderer keeps it that way. */}
      {group.subtotals.map((s, i) => (
        <tr key={`${s.property}|${s.op}|${s.unit ?? ' '}|${i}`} data-testid="viewpart-group-subtotal">
          <td
            // A colSpan must be >= 1: with exactly one rendered column (the
            // columns floor above guarantees at least one, never zero) there
            // is no width left over for the label once the value cell takes
            // its own column, so the label claims one anyway rather than an
            // invalid 0.
            colSpan={Math.max(columns.length - 1, 1)}
            className="border-b border-t border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1 text-[11px] text-[var(--color-muted)]"
          >
            Subtotal · {s.property}
            {s.unit !== undefined && ` · ${s.unit}`} · {s.count} {s.count === 1 ? 'row' : 'rows'}
          </td>
          <td className="whitespace-nowrap border-b border-t border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1 text-right font-medium">
            <span className="font-mono text-[13px] tabular-nums text-[var(--color-secondary)]">
              {formatNumberText(s.value)}
            </span>
          </td>
        </tr>
      ))}
      {group.excluded_count !== undefined && group.excluded_count > 0 && (
        <tr data-testid="viewpart-group-excluded">
          <td
            colSpan={columns.length}
            className="border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1 text-[11px] text-[var(--color-warning)]"
          >
            {group.excluded_reason ?? `${group.excluded_count} excluded from this subtotal.`}
          </td>
        </tr>
      )}
    </>
  )
}

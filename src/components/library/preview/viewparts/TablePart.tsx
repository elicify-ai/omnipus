// TablePart — rows × columns, with group header rows and per-group per-unit
// subtotal rows (view-kinds-design-2026-09-03 §2.2 table, §3 G2/G3; visual
// spec: the wireframe's "The accounting view" table).
//
// The renderer DRAWS the server's answer and nothing else: groups arrive with
// their subtotals already reduced once per unit value (ViewResultGroup), the
// footer totals arrive the same way (ViewResultPart.totals), and this file
// never adds two numbers. Even WHICH rows carry the G3 warn mark is the
// server's answer — ViewResultPart.excluded_paths names them — because the
// unit a row is excluded for is resolved from the record type, which this
// side cannot read.
//
// A number with a declared companion unit draws as ONE value ("12,480.00
// SGD", design §5), and the unit property loses its own column when both are
// listed.

import type { ReactNode } from 'react'
import type { VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'
import {
  cellValue,
  findCell,
  formatNumberText,
  partUnitProperty,
  rowExcludedFromTotals,
  rowsByPath,
  FILE_NAME_PROPERTY,
} from './viewResultData'
import { ExcludedRowMark, GroupHeaderLabel, TotalsFooter, UnitValue } from './PartChrome'
import { CellText, type ViewCellLinkResolver } from './ViewCellLink'
import { EditableCell, type RecordEditContext } from './RecordFieldEditor'

/** The row-level click target every openable part shares: mouse convenience
 *  on the row/card itself, plus one real, keyboard-reachable button that is
 *  the row's own identity — same pattern as workspaces/ListView.tsx's
 *  TaskRow, for the same reason (KB-8a): a <tr> cannot itself be a <button>,
 *  so the row stays mouse-clickable while ONE inner button is the real
 *  keyboard/AT entry point, and it stops its own click from bubbling so the
 *  row's onClick never fires twice for one click. */
function RowOpenButton({
  children,
  rowTitle,
  onOpen,
  className,
}: {
  children: ReactNode
  rowTitle: string
  onOpen: () => void
  className: string
}) {
  return (
    <button
      type="button"
      tabIndex={0}
      onClick={(event) => {
        event.stopPropagation()
        onOpen()
      }}
      aria-label={`Open ${rowTitle}`}
      data-testid="viewpart-row-open"
      className={className}
    >
      {children}
    </button>
  )
}

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
  primary,
  onOpenPath,
  cellLinks,
  editContext,
}: {
  row: VaultFindRow
  property: string
  part: ViewResultPart
  numeric: boolean
  excluded: boolean
  /** True for the row's identity column (KB-8a) — the ONE cell that hosts
   *  the real keyboard/AT open button, when `onOpenPath` is supplied. */
  primary: boolean
  onOpenPath?: ((path: string) => void) | undefined
  cellLinks?: ViewCellLinkResolver | undefined
  /** Enables ADR-083 D8 inline editing for this cell, when its own metadata
   *  allows one at all (RecordFieldEditor.tsx's EditableCell decides that,
   *  cell by cell — this is only the wire it needs to write through). */
  editContext?: RecordEditContext | undefined
}) {
  const value = cellValue(row, property)
  // `file.name` (the primary column, typically) has no VaultFindCell at all
  // — findCell returns undefined for it by construction — so it can never
  // reach EditableCell with something to edit (Founder ruling Q5: title is
  // never inline-editable).
  const cell = findCell(row, property)
  const renderCellValue = (v: string): ReactNode => (cellLinks ? <CellText value={v} resolver={cellLinks} /> : v)
  if (!numeric) {
    return (
      <td className="max-w-[16rem] truncate border-b border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">
        {primary && onOpenPath ? (
          <RowOpenButton rowTitle={row.title} onOpen={() => onOpenPath(row.path)} className="block w-full truncate text-left">
            {value}
          </RowOpenButton>
        ) : cell !== undefined ? (
          <EditableCell context={editContext} row={row} cell={cell} renderValue={renderCellValue} />
        ) : (
          renderCellValue(value)
        )}
      </td>
    )
  }
  const unitProperty = partUnitProperty(part)
  const unit = unitProperty === undefined ? undefined : cellValue(row, unitProperty)
  const numberBody =
    value === '' ? (
      <span className="text-[var(--color-muted)]">—</span>
    ) : (
      <span className={excluded ? 'text-[var(--color-muted)]' : undefined}>
        <UnitValue value={value} unit={unit === '' ? undefined : unit} />
      </span>
    )
  return (
    <td className="whitespace-nowrap border-b border-[var(--color-border)] px-3 py-1.5 text-right">
      {primary && onOpenPath ? (
        <RowOpenButton rowTitle={row.title} onOpen={() => onOpenPath(row.path)} className="inline-block text-right">
          {numberBody}
        </RowOpenButton>
      ) : (
        numberBody
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
  onOpenPath,
  cellLinks,
  editContext,
}: {
  rows: VaultFindRow[]
  columns: string[]
  part: ViewResultPart
  numeric: Set<string>
  onOpenPath?: ((path: string) => void) | undefined
  cellLinks?: ViewCellLinkResolver | undefined
  editContext?: RecordEditContext | undefined
}) {
  return (
    <>
      {rows.map((row) => {
        const excluded = rowExcludedFromTotals(row, part)
        return (
          <tr
            key={row.path}
            data-testid="viewpart-table-row"
            className={onOpenPath ? 'cursor-pointer hover:bg-[var(--color-surface-2)]/40' : undefined}
            {...(onOpenPath ? { onClick: () => onOpenPath(row.path) } : {})}
          >
            {columns.map((property, i) => (
              <Cell
                key={property}
                row={row}
                property={property}
                part={part}
                numeric={numeric.has(property)}
                excluded={excluded && numeric.has(property)}
                primary={i === 0}
                onOpenPath={onOpenPath}
                cellLinks={cellLinks}
                editContext={editContext}
              />
            ))}
          </tr>
        )
      })}
    </>
  )
}

export function TablePart({
  part,
  rows,
  onOpenPath,
  cellLinks,
  editContext,
}: {
  part: ViewResultPart
  rows: VaultFindRow[]
  /** Opens a row's own note (KB-8a). Absent renders every row exactly as
   *  before — inert markup, no button, no cursor change. */
  onOpenPath?: (path: string) => void
  /** Renders a relation cell's raw `[[wikilink]]` as a real link (KB-8b). */
  cellLinks?: ViewCellLinkResolver
  /** Enables ADR-083 D8 inline record-field editing. Absent renders every
   *  cell exactly as before — plain text, never an editor. */
  editContext?: RecordEditContext
}) {
  // code-review finding #3(b): `part.columns ?? [FILE_NAME_PROPERTY]` only
  // caught `undefined` — a part that "declares no properties" as the EMPTY
  // ARRAY sailed through with zero columns, which made the group-header
  // row's `colSpan={columns.length}` zero and the subtotal label's
  // `colSpan={columns.length - 1}` negative (both invalid HTML). Guard both
  // shapes the same way.
  const allColumns = part.columns !== undefined && part.columns.length > 0 ? part.columns : [FILE_NAME_PROPERTY]
  // §5: the declared unit property draws inside the number cell, not as its
  // own column — but only when the part actually binds a number to it. The
  // property is the SERVER's resolved one where it resolved any, never the
  // possibly-stale `unit:` stamp (partUnitProperty).
  const unitProperty = partUnitProperty(part)
  const filteredColumns =
    unitProperty !== undefined && part.source.number !== undefined
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
              <BodyRows
                rows={rows}
                columns={columns}
                part={part}
                numeric={numeric}
                onOpenPath={onOpenPath}
                cellLinks={cellLinks}
                editContext={editContext}
              />
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
                    onOpenPath={onOpenPath}
                    cellLinks={cellLinks}
                    editContext={editContext}
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
  onOpenPath,
  cellLinks,
  editContext,
}: {
  group: NonNullable<ViewResultPart['groups']>[number]
  memberRows: VaultFindRow[]
  columns: string[]
  part: ViewResultPart
  numeric: Set<string>
  onOpenPath?: ((path: string) => void) | undefined
  cellLinks?: ViewCellLinkResolver | undefined
  editContext?: RecordEditContext | undefined
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
      <BodyRows
        rows={memberRows}
        columns={columns}
        part={part}
        numeric={numeric}
        onOpenPath={onOpenPath}
        cellLinks={cellLinks}
        editContext={editContext}
      />
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

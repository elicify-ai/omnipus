// viewResultData.ts — pure helpers shared by the view-part renderers
// (view-kinds-design-2026-09-03 §7).
//
// EVERYTHING HERE READS; NOTHING COMPUTES A TOTAL. The server precomputes
// every aggregate under the gate rules (G2: once per unit value, never across
// units; G3: unit-less rows shown, excluded, counted), and a client-side
// reduction would be a second implementation of those rules waiting to
// disagree with the first — the exact failure ViewResultPoint's schema note
// names. The one arithmetic-looking function below, `formatNumberText`,
// re-spaces digits the server already produced; it never adds two of them.

import type {
  VaultFindRow,
  ViewResultPart,
  ViewUnitTotal,
} from '@/lib/api/generated/openapi-types'

/** The engine's synthetic column for the note's own name. */
export const FILE_NAME_PROPERTY = 'file.name'

/**
 * One row's rendered value for one property, or '' when the row carries no
 * cell for it. `file.name` answers the row's own title — the engine reports
 * identity fields on the row, not as cells.
 */
export function cellValue(row: VaultFindRow, property: string): string {
  if (property === FILE_NAME_PROPERTY) return row.title
  const cell = row.cells.find((c) => c.property === property)
  return cell?.value ?? ''
}

/**
 * Rows keyed by path, for resolving a group's / part's path references into
 * the result's shared `rows` list (ViewResultGroup.paths).
 */
export function rowsByPath(rows: VaultFindRow[]): Map<string, VaultFindRow> {
  const m = new Map<string, VaultFindRow>()
  for (const row of rows) if (!m.has(row.path)) m.set(row.path, row)
  return m
}

/**
 * Whether ONE row is excluded from every total of this part under G3.
 *
 * Membership of the server's own list — render-only, deciding where the warn
 * mark goes and never what any total is.
 */
export function rowExcludedFromTotals(row: VaultFindRow, part: ViewResultPart): boolean {
  return (part.excluded_paths ?? []).includes(row.path)
}

/**
 * The unit property this part's numbers are paired with, as the SERVER
 * resolved it — `unit_property` on the part (figures, chart, crosstab), else
 * the one its own totals were keyed by (a grouped table's subtotals).
 *
 * A part's `source.unit` is the composer's STAMP: what was written when the
 * view was authored, which a later edit to the record type can leave stale.
 * The server refuses a total outright when stamp and schema disagree, so
 * wherever it has spoken the answer is here; the stamp is the last resort,
 * used only for a part that totals nothing at all and therefore has no
 * resolved answer to report.
 */
export function partUnitProperty(part: ViewResultPart): string | undefined {
  if (part.unit_property !== undefined && part.unit_property !== '') return part.unit_property
  for (const t of part.totals ?? []) {
    if (t.unit_property !== undefined && t.unit_property !== '') return t.unit_property
  }
  for (const g of part.groups ?? []) {
    for (const s of g.subtotals) {
      if (s.unit_property !== undefined && s.unit_property !== '') return s.unit_property
    }
  }
  const stamped = part.source.unit
  return stamped === undefined || stamped === '' ? undefined : stamped
}

/**
 * Re-spaces an exact decimal text with thousands separators: "12480.00" →
 * "12,480.00". The digits are the server's, byte-for-byte — only commas are
 * inserted, so a value that is not a plain decimal (already separated,
 * signed scientific, non-numeric) is returned unchanged rather than guessed
 * at.
 */
export function formatNumberText(value: string): string {
  const m = /^(-?)(\d+)(\.\d+)?$/.exec(value)
  if (!m) return value
  const [, sign, int, frac] = m
  const grouped = int.replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${sign}${grouped}${frac ?? ''}`
}

/** 'sum' → 'Total', 'avg' → 'Average', … — the reader-facing word per op. */
export function aggregateLabel(op: ViewUnitTotal['op']): string {
  switch (op) {
    case 'sum':
      return 'Total'
    case 'avg':
      return 'Average'
    case 'min':
      return 'Min'
    case 'max':
      return 'Max'
    case 'count':
      return 'Count'
  }
}

/** Distinct unit values among a totals list, in first-appearance order.
 *  A unit-less total contributes nothing (its `unit` is absent, not ''). */
export function distinctUnits(totals: ViewUnitTotal[]): string[] {
  const seen: string[] = []
  for (const t of totals) {
    if (t.unit !== undefined && !seen.includes(t.unit)) seen.push(t.unit)
  }
  return seen
}

/**
 * Whether this part's totals footer must state WHY there is no grand total
 * (G2): more than one unit value present, or rows excluded under G3. With one
 * unit and nothing excluded the single per-unit line already IS the total and
 * needs no apology.
 */
export function needsNoGrandTotalReason(totals: ViewUnitTotal[], excludedCount: number): boolean {
  return distinctUnits(totals).length > 1 || excludedCount > 0
}

/** The G2 footer sentence for one part. Plain words, consequence first. */
export function noGrandTotalReason(totals: ViewUnitTotal[], excludedCount: number): string {
  const units = distinctUnits(totals)
  const bits: string[] = []
  if (units.length > 1) {
    bits.push(
      `${units.length} unit values (${units.join(', ')}) — totals are shown per unit; adding them would produce a number that means nothing`,
    )
  }
  if (excludedCount > 0) {
    bits.push(`${excludedCount} ${excludedCount === 1 ? 'row is' : 'rows are'} excluded for having no unit value`)
  }
  return bits.join('; ') + '.'
}

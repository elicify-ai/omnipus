// CrosstabPart — rows × columns with aggregated cells
// (view-kinds-design-2026-09-03 §2.2 crosstab; wireframe "Aged receivables —
// a cross-table"). The grid arrives precomputed and SPARSE: a (row, column)
// position with no cell renders as an em dash, never as a zero nobody
// computed. A number with a companion unit produces cells ONCE PER UNIT
// VALUE (G2), so the renderer draws ONE GRID PER UNIT — "one table per
// currency, never mixed" is the wireframe's own caption — and a unit-less
// number draws one grid with no unit caption.

import type { ViewResultCrosstab, ViewResultPart } from '@/lib/api/generated/openapi-types'
import { formatNumberText } from './viewResultData'
import { ExcludedLine } from './PartChrome'

/** The distinct unit values the cells carry, absent-unit first-class as ''. */
function cellUnits(crosstab: ViewResultCrosstab): Array<string | undefined> {
  const seen: Array<string | undefined> = []
  for (const c of crosstab.cells) {
    if (!seen.some((u) => u === c.unit)) seen.push(c.unit)
  }
  return seen.length === 0 ? [undefined] : seen
}

function keyLabel(key: string): string {
  return key === '' ? 'Not set' : key
}

function UnitGrid({ crosstab, unit }: { crosstab: ViewResultCrosstab; unit: string | undefined }) {
  const cellFor = (row: string, column: string) =>
    crosstab.cells.find((c) => c.row === row && c.column === column && c.unit === unit)
  return (
    <div className="overflow-x-auto" data-testid="viewpart-crosstab-grid">
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr>
            <th className="border-b border-[var(--color-border)] px-3 py-1.5 text-left text-[10px] font-medium uppercase tracking-[0.08em] text-[var(--color-muted)]">
              {crosstab.row_property}
              {unit !== undefined && ` · ${unit}`}
            </th>
            {crosstab.column_keys.map((ck) => (
              <th
                key={ck}
                className="border-b border-[var(--color-border)] px-3 py-1.5 text-right text-[10px] font-medium uppercase tracking-[0.08em] text-[var(--color-muted)]"
              >
                {keyLabel(ck)}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {crosstab.row_keys.map((rk) => (
            <tr key={rk} data-testid="viewpart-crosstab-row">
              <td className="border-b border-[var(--color-border)] px-3 py-1.5 text-[var(--color-secondary)]">
                {keyLabel(rk)}
              </td>
              {crosstab.column_keys.map((ck) => {
                const cell = cellFor(rk, ck)
                return (
                  <td
                    key={ck}
                    className="whitespace-nowrap border-b border-[var(--color-border)] px-3 py-1.5 text-right font-mono text-[13px] tabular-nums"
                  >
                    {cell === undefined ? (
                      <span className="text-[var(--color-muted)]">—</span>
                    ) : (
                      <span className="text-[var(--color-secondary)]" title={`${cell.count} ${cell.count === 1 ? 'value' : 'values'}`}>
                        {formatNumberText(cell.value)}
                      </span>
                    )}
                  </td>
                )
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function CrosstabPart({ part }: { part: ViewResultPart }) {
  const crosstab = part.crosstab
  if (crosstab === undefined) {
    return (
      <p className="px-3 py-3 text-[12px] text-[var(--color-muted)]" data-testid="viewpart-crosstab">
        No grid was computed for this part.
      </p>
    )
  }
  const units = cellUnits(crosstab)
  return (
    <div className="flex flex-col gap-2" data-testid="viewpart-crosstab">
      {units.map((unit) => (
        <UnitGrid key={unit ?? ' '} crosstab={crosstab} unit={unit} />
      ))}
      {units.filter((u) => u !== undefined).length > 1 && (
        <p className="px-3 text-[11px] leading-snug text-[var(--color-muted)]" data-testid="viewpart-no-grand-total">
          <span className="font-medium text-[var(--color-warning)]">One grid per unit. </span>
          Values in different units are never added into one cell.
        </p>
      )}
      <ExcludedLine count={crosstab.excluded_count ?? 0} reason={crosstab.excluded_reason} />
    </div>
  )
}

// FiguresPart — the headline numbers row (view-kinds-design-2026-09-03 §2.2
// figures; wireframe "Runway, from the numbers you keep" rollup treatment).
// One tile per (property, op, unit value) the server computed — G2 is the
// SHAPE here: two currencies arrive as two tiles, and there is no way to
// draw one combined figure because no combined figure exists in the data.
// The G3 exclusion line renders beneath, in the server's own words.

import type { ViewResultPart } from '@/lib/api/generated/openapi-types'
import { aggregateLabel, formatNumberText, needsNoGrandTotalReason, noGrandTotalReason } from './viewResultData'
import { ExcludedLine } from './PartChrome'

export function FiguresPart({ part }: { part: ViewResultPart }) {
  const totals = part.totals ?? []
  const excluded = part.excluded_count ?? 0
  return (
    <div className="flex flex-col" data-testid="viewpart-figures">
      <div className="flex flex-wrap gap-x-6 gap-y-3 px-3 py-3">
        {totals.length === 0 && (
          <p className="text-[12px] text-[var(--color-muted)]">No figures were computed for this view.</p>
        )}
        {totals.map((t, i) => (
          <div key={`${t.property}|${t.op}|${t.unit ?? ' '}|${i}`} className="flex flex-col gap-0.5" data-testid="viewpart-figure">
            <span className="text-[10px] uppercase tracking-[0.07em] text-[var(--color-muted)]">
              {aggregateLabel(t.op)} {t.property}
              {t.unit !== undefined && ` · ${t.unit}`}
            </span>
            <span className="font-mono text-[1.15rem] tabular-nums text-[var(--color-secondary)]">
              {formatNumberText(t.value)}
              {t.unit !== undefined && (
                <span className="ml-1.5 text-[0.7rem] text-[var(--color-muted)]">{t.unit}</span>
              )}
            </span>
            <span className="text-[10px] text-[var(--color-muted)]">
              {t.count} {t.count === 1 ? 'value' : 'values'}
            </span>
          </div>
        ))}
      </div>
      {needsNoGrandTotalReason(totals, excluded) && (
        <p
          className="flex items-start gap-1.5 px-3 pb-2 text-[11px] leading-snug text-[var(--color-muted)]"
          data-testid="viewpart-no-grand-total"
        >
          <span className="shrink-0 font-medium text-[var(--color-warning)]">No combined figure.</span>
          <span>{noGrandTotalReason(totals, excluded)}</span>
        </p>
      )}
      <ExcludedLine count={excluded} reason={part.excluded_reason} />
    </div>
  )
}

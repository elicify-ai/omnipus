// PartChrome — the furniture every view part shares
// (view-kinds-design-2026-09-03 §3 G2/G3, §7; visual spec: the wireframe's
// `.mny`, `.refuse`, `.warnmark` and subtotal treatments, drawn with the
// SPA's own tokens).
//
// Three pieces, and the reason each exists:
//
//   UnitValue      — a number the server computed, tabular figures, unit
//                    alongside in muted small caps. The digits are rendered
//                    verbatim (re-spaced only); no arithmetic happens here.
//   TotalsFooter   — the per-unit totals line of one part, PLUS the G2
//                    sentence whenever units differ or rows were excluded:
//                    a reader must never see per-unit figures and silently
//                    infer that adding them would mean something.
//   ExcludedLine   — the G3 counter: rows with no unit value are SHOWN in
//                    the part above, excluded from every total, and counted
//                    here in the server's own words (excluded_reason).

import { WarningCircle } from '@phosphor-icons/react'
import type { ViewUnitTotal } from '@/lib/api/generated/openapi-types'
import {
  aggregateLabel,
  formatNumberText,
  needsNoGrandTotalReason,
  noGrandTotalReason,
} from './viewResultData'

/** A computed number: tabular figures, unit alongside. */
export function UnitValue({ value, unit }: { value: string; unit?: string | undefined }) {
  return (
    <span className="whitespace-nowrap font-mono text-[length:var(--type-caption-size)] tabular-nums text-[var(--color-secondary)]">
      {formatNumberText(value)}
      {unit !== undefined && unit !== '' && (
        <span className="ml-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">{unit}</span>
      )}
    </span>
  )
}

/** The wireframe's `!` mark on a row excluded from every total (G3). */
export function ExcludedRowMark() {
  return (
    <span
      title="No unit value set, so this row is excluded from every total."
      data-testid="viewpart-excluded-mark"
      className="ml-[var(--space-1)] inline-flex h-[15px] w-[15px] shrink-0 cursor-help items-center justify-center rounded-full bg-[var(--color-warning)]/15 text-[length:var(--type-caption-size)] font-semibold text-[var(--color-warning)]"
    >
      !
    </span>
  )
}

/** One rendered total: "Total amount · SGD · 4 — 29,230.00". */
function TotalEntry({ total }: { total: ViewUnitTotal }) {
  return (
    <span className="inline-flex items-baseline gap-[var(--space-1)]" data-testid="viewpart-total">
      <span className="text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
        {aggregateLabel(total.op)} {total.property}
        {total.unit !== undefined && ` · ${total.unit}`}
        {` · ${total.count}`}
      </span>
      <UnitValue value={total.value} unit={total.unit} />
    </span>
  )
}

/**
 * The per-unit totals footer of one part, with the G2 reason line when it is
 * owed. Renders nothing when the part computed no totals — "no totals" is a
 * property of the part's definition, stated by its absence of a figures row,
 * not by an empty strip.
 */
export function TotalsFooter({
  totals,
  excludedCount,
  excludedReason,
}: {
  totals: ViewUnitTotal[]
  excludedCount?: number | undefined
  excludedReason?: string | undefined
}) {
  const excluded = excludedCount ?? 0
  if (totals.length === 0 && excluded === 0) return null
  return (
    <div className="shrink-0 border-t border-[var(--color-border)]" data-testid="viewpart-totals-footer">
      {totals.length > 0 && (
        <div className="flex flex-wrap items-baseline gap-x-[var(--space-3)] gap-y-[var(--space-1)] px-[var(--space-2-5)] py-[var(--space-1)]">
          {totals.map((t, i) => (
            <TotalEntry key={`${t.property}|${t.op}|${t.unit ?? ' '}|${i}`} total={t} />
          ))}
        </div>
      )}
      {needsNoGrandTotalReason(totals, excluded) && (
        <p
          className="flex items-start gap-[var(--space-1)] border-t border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-muted)]"
          data-testid="viewpart-no-grand-total"
        >
          <span className="shrink-0 font-medium text-[var(--color-warning)]">No grand total.</span>
          <span>{noGrandTotalReason(totals, excluded)}</span>
        </p>
      )}
      <ExcludedLine count={excluded} reason={excludedReason} />
    </div>
  )
}

/** The G3 counter line, in the server's own words. */
export function ExcludedLine({ count, reason }: { count: number; reason?: string | undefined }) {
  if (count <= 0) return null
  return (
    <p
      className="flex items-start gap-[var(--space-1)] border-t border-[var(--color-border)] px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] leading-snug text-[var(--color-warning)]"
      data-testid="viewpart-excluded-line"
    >
      <WarningCircle size={13} weight="fill" className="mt-[var(--border-width-hairline)] shrink-0" />
      <span>{reason ?? `${count} ${count === 1 ? 'row is' : 'rows are'} excluded from every total.`}</span>
    </p>
  )
}

/** Group-header styling shared by the table part's group rows and the board's
 *  column headers: small caps, muted, count alongside. */
export function GroupHeaderLabel({ label, count, absent }: { label: string; count: number; absent?: boolean | undefined }) {
  return (
    <span className="text-[length:var(--type-caption-size)] font-medium uppercase tracking-[var(--font-letter-spacing-table-header)] text-[var(--color-muted)]">
      {absent ? 'Not set' : label === '' ? '(empty)' : label}
      <span className="ml-[var(--space-1)] normal-case text-[var(--color-muted)]/70">{count}</span>
    </span>
  )
}

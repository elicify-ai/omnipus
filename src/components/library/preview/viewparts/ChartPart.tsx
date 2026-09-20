// ChartPart — a simple SVG line/bar over the PRECOMPUTED series
// (view-kinds-design-2026-09-03 §2.2 chart, §7). No charting library and no
// client-side reduction: the server aggregated the points per date bucket,
// one series per unit value (G2 — "a line that sums euros and hours is a
// wrong picture that looks right"), and this component only places them on
// a coordinate plane. A single-point series draws as a bar (a line needs two
// points to be one); everything else is a polyline.
//
// Scale note (code-review finding #8): the domain is the real min/max across
// every series, NEVER clamped to [0, max] — that clamp used to make a
// negative point map below the viewBox (invisible), give a single negative
// bar a NEGATIVE SVG height, and collapse an all-negative series onto the
// zero gridline by dividing by a zero denominator. Zero is always folded
// into the domain (`Math.min(0, dataMin)` / `Math.max(0, dataMax)`) so a
// normal all-positive series keeps its familiar 0-baseline unchanged, and so
// every bar/point always has a real zero to anchor on. A flat series (every
// point equal — most commonly all-zero) is padded by ±1 so the scale never
// divides by zero; the point still draws at its true value, at mid-height.
// A THIRD gridline (zero) is drawn only when zero sits STRICTLY inside the
// domain (both a positive and a negative point exist) — when zero is one of
// the domain's own edges (all-positive or all-negative data) the existing
// top/bottom axis labels already state it.

import type { ViewResultPart, ViewResultSeries } from '@/lib/api/generated/openapi-types'
import { formatNumberText } from './viewResultData'
import { ExcludedLine } from './PartChrome'

const WIDTH = 560
// UAT D-73: ten more pixels below the plot for tick labels AND an axis
// title; the plot itself keeps its 144 px height (HEIGHT − top − bottom).
const HEIGHT = 190
const PAD = { top: 12, right: 12, bottom: 34, left: 56 }

// Token-based series palette: accent first, then the semantic hues — no new
// colors (brand rule), distinct enough at two-to-four series.
const SERIES_COLORS = [
  'var(--color-accent)',
  'var(--color-info)',
  'var(--color-success)',
  'var(--color-cancelled)',
  'var(--color-error)',
] as const

function numeric(v: string): number {
  const n = Number(v)
  return Number.isFinite(n) ? n : 0
}

/** UAT D-73: a point key that reads as an ISO date — `YYYY`, `YYYY-MM`,
 *  `YYYY-MM-DD`, optionally with a `T`/space time — as UTC milliseconds, or
 *  undefined for anything else. When EVERY key of a chart parses, the x
 *  axis is a TIME axis, so a fortnight and a quarter take proportionate
 *  widths instead of one slot each. Exported as a test seam. */
export function chartKeyTime(key: string): number | undefined {
  const m = /^(\d{4})(?:-(\d{2})(?:-(\d{2}))?)?(?:[T ](\d{2}):(\d{2})(?::(\d{2}))?)?$/.exec(key.trim())
  if (m === null) return undefined
  const num = (s: string | undefined, fallback: number) => (s === undefined ? fallback : Number(s))
  const t = Date.UTC(num(m[1], 0), num(m[2], 1) - 1, num(m[3], 1), num(m[4], 0), num(m[5], 0), num(m[6], 0))
  return Number.isFinite(t) ? t : undefined
}

function isoDay(t: number): string {
  return new Date(t).toISOString().slice(0, 10)
}

export function ChartPart({ part }: { part: ViewResultPart }) {
  const series: ViewResultSeries[] = part.series ?? []
  const allKeys = [...new Set(series.flatMap((s) => s.points.map((p) => p.key)))].sort()
  const allValues = series.flatMap((s) => s.points.map((p) => numeric(p.value)))

  if (allKeys.length === 0) {
    return (
      <div className="px-[var(--space-2-5)] py-[var(--space-2-5)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]" data-testid="viewpart-chart">
        No points to draw — the series is empty.
      </div>
    )
  }

  const dataMin = allValues.length > 0 ? Math.min(...allValues) : 0
  const dataMax = allValues.length > 0 ? Math.max(...allValues) : 0
  const domainMin0 = Math.min(0, dataMin)
  const domainMax0 = Math.max(0, dataMax)
  // A flat series (every value equal, most commonly all-zero) would divide
  // by a zero span — pad it symmetrically so it draws at its true value.
  const flat = domainMin0 === domainMax0
  const domainMin = flat ? domainMin0 - 1 : domainMin0
  const domainMax = flat ? domainMax0 + 1 : domainMax0
  const span = domainMax - domainMin

  const plotW = WIDTH - PAD.left - PAD.right
  const plotH = HEIGHT - PAD.top - PAD.bottom
  // UAT D-73: temporal when every key is a date and they span real time;
  // otherwise the keys are categories and sit one slot apart as before.
  const keyTimes = allKeys.map(chartKeyTime).filter((t): t is number => t !== undefined)
  const tMin = keyTimes.length > 0 ? Math.min(...keyTimes) : 0
  const tMax = keyTimes.length > 0 ? Math.max(...keyTimes) : 0
  const temporal = allKeys.length > 1 && keyTimes.length === allKeys.length && tMin < tMax
  const x = (key: string) => {
    if (allKeys.length === 1) return PAD.left + plotW / 2
    if (temporal) return PAD.left + (((chartKeyTime(key) ?? tMin) - tMin) / (tMax - tMin)) * plotW
    return PAD.left + (allKeys.indexOf(key) / (allKeys.length - 1)) * plotW
  }
  // Ticks: on a time axis, five evenly spaced dates (the extremes plus three
  // between them); on a category axis, the first and last key as before.
  const firstKey = allKeys[0] ?? ''
  const lastKey = allKeys[allKeys.length - 1] ?? ''
  const xTicks: { x: number; label: string }[] = temporal
    ? [0, 0.25, 0.5, 0.75, 1].map((f) => ({ x: PAD.left + f * plotW, label: isoDay(tMin + f * (tMax - tMin)) }))
    : allKeys.length > 1
      ? [
          { x: PAD.left, label: firstKey },
          { x: WIDTH - PAD.right, label: lastKey },
        ]
      : [{ x: PAD.left, label: firstKey }]
  const y = (v: number) => PAD.top + plotH - ((v - domainMin) / span) * plotH
  // Zero is always inside [domainMin, domainMax] by construction, so every
  // bar/point can anchor on a real zero baseline instead of always the
  // plot's bottom edge (the bug that gave a negative single-point bar a
  // negative SVG height).
  const yZero = y(0)
  const zeroIsInterior = domainMin < 0 && domainMax > 0

  return (
    <div className="flex flex-col" data-testid="viewpart-chart">
      <div className="overflow-x-auto px-[var(--space-2-5)] py-[var(--space-2)]">
        <svg
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          className="h-auto w-full max-w-[36rem]"
          role="img"
          aria-label="Chart of the view's series"
        >
          {/* Frame + gridlines at the domain's own edges. */}
          <line x1={PAD.left} y1={PAD.top + plotH} x2={WIDTH - PAD.right} y2={PAD.top + plotH} stroke="var(--color-border)" />
          <line x1={PAD.left} y1={PAD.top} x2={WIDTH - PAD.right} y2={PAD.top} stroke="var(--color-border)" strokeDasharray="2 4" />
          {/* A third zero line only when zero is NOT already one of the edges
              above (i.e. the series has both a positive and a negative
              point) — otherwise the edge label below already states it. */}
          {zeroIsInterior && (
            <line
              x1={PAD.left}
              y1={yZero}
              x2={WIDTH - PAD.right}
              y2={yZero}
              stroke="var(--color-border)"
              data-testid="viewpart-chart-zero-line"
            />
          )}
          <text x={PAD.left - 6} y={PAD.top + 4} textAnchor="end" fontSize="9" fill="var(--color-muted)">
            {formatNumberText(String(domainMax))}
          </text>
          <text x={PAD.left - 6} y={PAD.top + plotH + 4} textAnchor="end" fontSize="9" fill="var(--color-muted)">
            {formatNumberText(String(domainMin))}
          </text>
          {zeroIsInterior && (
            <text
              x={PAD.left - 6}
              y={yZero + 3}
              textAnchor="end"
              fontSize="9"
              fill="var(--color-muted)"
              data-testid="viewpart-chart-zero-label"
            >
              0
            </text>
          )}
          {xTicks.map((tick, i) => (
            <g key={`${tick.label}-${i}`}>
              {temporal && (
                <line
                  x1={tick.x}
                  y1={PAD.top + plotH}
                  x2={tick.x}
                  y2={PAD.top + plotH + 3}
                  stroke="var(--color-border)"
                  data-testid="viewpart-chart-x-tick"
                />
              )}
              <text
                x={tick.x}
                y={PAD.top + plotH + 12}
                textAnchor={i === 0 ? 'start' : i === xTicks.length - 1 ? 'end' : 'middle'}
                fontSize="9"
                fill="var(--color-muted)"
                data-testid="viewpart-chart-x-label"
              >
                {tick.label}
              </text>
            </g>
          ))}
          {/* UAT D-73: axis titles — the property each axis plots. */}
          {part.source.date !== undefined && (
            <text
              x={PAD.left + plotW / 2}
              y={HEIGHT - 2}
              textAnchor="middle"
              fontSize="9"
              fill="var(--color-muted)"
              data-testid="viewpart-chart-x-title"
            >
              {part.source.date}
              {temporal ? '' : ' (categories)'}
            </text>
          )}
          {part.source.number !== undefined && (
            <text
              transform={`translate(9 ${PAD.top + plotH / 2}) rotate(-90)`}
              textAnchor="middle"
              fontSize="9"
              fill="var(--color-muted)"
              data-testid="viewpart-chart-y-title"
            >
              {part.source.number}
            </text>
          )}
          {series.map((s, si) => {
            const color = SERIES_COLORS[si % SERIES_COLORS.length]
            if (s.points.length === 1) {
              const p = s.points[0]
              if (p === undefined) return null
              const barW = 18
              const yValue = y(numeric(p.value))
              // Anchored on zero, not always the plot's bottom edge: a
              // negative value now extends DOWN from zero, never producing
              // a negative SVG height.
              const top = Math.min(yValue, yZero)
              const height = Math.abs(yValue - yZero)
              return (
                <rect
                  key={s.unit ?? `series-${si}`}
                  x={x(p.key) - barW / 2 + si * (barW + 2) - ((series.length - 1) * (barW + 2)) / 2}
                  y={top}
                  width={barW}
                  height={height}
                  fill={color}
                  data-testid="viewpart-chart-bar"
                />
              )
            }
            const pts = s.points.map((p) => `${x(p.key)},${y(numeric(p.value))}`).join(' ')
            return (
              <g key={s.unit ?? `series-${si}`}>
                <polyline
                  points={pts}
                  fill="none"
                  stroke={color}
                  strokeWidth="1.5"
                  data-testid="viewpart-chart-line"
                />
                {s.points.map((p) => (
                  <circle key={p.key} cx={x(p.key)} cy={y(numeric(p.value))} r="2" fill={color} />
                ))}
              </g>
            )
          })}
        </svg>
      </div>
      {/* Legend: one entry per series, unit named — never merged (G2). */}
      <div className="flex flex-wrap gap-x-[var(--space-3)] gap-y-[var(--space-1)] px-[var(--space-2-5)] pb-[var(--space-2)]" data-testid="viewpart-chart-legend">
        {series.map((s, si) => (
          <span key={s.unit ?? `series-${si}`} className="inline-flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-[var(--color-muted)]">
            <span
              className="inline-block h-2 w-2 rounded-sm"
              style={{ backgroundColor: SERIES_COLORS[si % SERIES_COLORS.length] }}
            />
            {s.unit ?? part.source.number ?? 'value'}
            <span className="text-[var(--color-muted)]/70">
              ({s.points.length} {s.points.length === 1 ? 'point' : 'points'})
            </span>
          </span>
        ))}
      </div>
      <ExcludedLine count={part.excluded_count ?? 0} reason={part.excluded_reason} />
    </div>
  )
}

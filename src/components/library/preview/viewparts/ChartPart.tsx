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
const HEIGHT = 180
const PAD = { top: 12, right: 12, bottom: 24, left: 56 }

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

export function ChartPart({ part }: { part: ViewResultPart }) {
  const series: ViewResultSeries[] = part.series ?? []
  const allKeys = [...new Set(series.flatMap((s) => s.points.map((p) => p.key)))].sort()
  const allValues = series.flatMap((s) => s.points.map((p) => numeric(p.value)))

  if (allKeys.length === 0) {
    return (
      <div className="px-3 py-3 text-[12px] text-[var(--color-muted)]" data-testid="viewpart-chart">
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
  const x = (key: string) =>
    PAD.left + (allKeys.length === 1 ? plotW / 2 : (allKeys.indexOf(key) / (allKeys.length - 1)) * plotW)
  const y = (v: number) => PAD.top + plotH - ((v - domainMin) / span) * plotH
  // Zero is always inside [domainMin, domainMax] by construction, so every
  // bar/point can anchor on a real zero baseline instead of always the
  // plot's bottom edge (the bug that gave a negative single-point bar a
  // negative SVG height).
  const yZero = y(0)
  const zeroIsInterior = domainMin < 0 && domainMax > 0

  return (
    <div className="flex flex-col" data-testid="viewpart-chart">
      <div className="overflow-x-auto px-3 py-2">
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
          <text x={PAD.left} y={HEIGHT - 8} textAnchor="start" fontSize="9" fill="var(--color-muted)">
            {allKeys[0]}
          </text>
          {allKeys.length > 1 && (
            <text x={WIDTH - PAD.right} y={HEIGHT - 8} textAnchor="end" fontSize="9" fill="var(--color-muted)">
              {allKeys[allKeys.length - 1]}
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
      <div className="flex flex-wrap gap-x-4 gap-y-1 px-3 pb-2" data-testid="viewpart-chart-legend">
        {series.map((s, si) => (
          <span key={s.unit ?? `series-${si}`} className="inline-flex items-center gap-1.5 text-[11px] text-[var(--color-muted)]">
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

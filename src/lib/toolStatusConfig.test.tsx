import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import {
  getToolBadgeStatusConfig,
  getSpanStatusDot,
  isCancelledStatus,
  type SpanLikeStatus,
} from './toolStatusConfig'

// Direct unit coverage for the shared status→config helpers (previously
// only exercised indirectly via SubagentBlock.test.tsx, ActivityPanel.test.tsx,
// ToolCallBadge.test.tsx, GenericToolCall.test.tsx). Icon/dot fields are real
// JSX elements, so assertions render them into jsdom and inspect the
// resulting element (Phosphor's IconBase maps the `size` prop straight to
// the `width`/`height` SVG attributes and passes `className` through to
// `class`; the flat-design status dot is a plain `<span>`, not an svg).

function renderIconSvg(icon: React.ReactNode) {
  const { container } = render(<div>{icon}</div>)
  return container.querySelector('svg')
}

/** Renders a `statusDot()`-produced indicator and returns its <span>. */
function renderIndicatorSpan(indicator: React.ReactNode) {
  const { container } = render(<div>{indicator}</div>)
  return container.querySelector('span')
}

describe('getToolBadgeStatusConfig — "inline" family (ToolCallBadge, GenericToolCall) — flat text-line dot shape', () => {
  it('running: label "Running...", spinning icon indicator (no dot — a static dot cannot show "in progress")', () => {
    const config = getToolBadgeStatusConfig('running')
    expect(config.label).toBe('Running...')
    const svg = renderIconSvg(config.indicator)
    expect(svg?.getAttribute('class')).toContain('animate-spin')
    expect(svg?.getAttribute('class')).toContain('text-[var(--color-accent)]')
  })

  it('running: spinner carries aria-hidden="true" (matches the span-family spinner)', () => {
    const svg = renderIconSvg(getToolBadgeStatusConfig('running').indicator)
    expect(svg?.getAttribute('aria-hidden')).toBe('true')
  })

  it('error: label "Failed", error-colored 8px dot indicator', () => {
    const config = getToolBadgeStatusConfig('error')
    expect(config.label).toBe('Failed')
    const dot = renderIndicatorSpan(config.indicator)
    expect(dot?.className).toContain('bg-[var(--color-error)]')
    expect(dot?.className).toContain('rounded-full')
    expect(dot?.className).toContain('w-2')
    expect(dot?.className).toContain('h-2')
  })

  describe('success — duration folding into the label', () => {
    it('folds a genuine zero duration into "0ms", not "Done"', () => {
      // Matches formatDuration's canonical zero-duration behavior (0 is a real,
      // if instant, measured duration) — this specific case had zero coverage
      // anywhere in the repo before this file.
      const config = getToolBadgeStatusConfig('success', { durationMs: 0 })
      expect(config.label).toBe('0ms')
    })

    it('folds a sub-second duration into milliseconds', () => {
      expect(getToolBadgeStatusConfig('success', { durationMs: 420 }).label).toBe('420ms')
    })

    it('folds a >=1000ms duration into seconds', () => {
      expect(getToolBadgeStatusConfig('success', { durationMs: 2300 }).label).toBe('2.3s')
    })

    it('falls back to "Done" when no durationMs is supplied', () => {
      expect(getToolBadgeStatusConfig('success').label).toBe('Done')
    })

    it('falls back to "Done" when durationMs is undefined explicitly', () => {
      expect(getToolBadgeStatusConfig('success', { durationMs: undefined }).label).toBe('Done')
    })

    it('success dot color is always the success color regardless of duration', () => {
      const dot = renderIndicatorSpan(getToolBadgeStatusConfig('success', { durationMs: 5 }).indicator)
      expect(dot?.className).toContain('bg-[var(--color-success)]')
    })
  })

  describe('cancelled — cancelledVariant option', () => {
    it('defaults to the dedicated cancelled dot color (ToolCallBadge)', () => {
      const config = getToolBadgeStatusConfig('cancelled')
      expect(config.label).toBe('Cancelled')
      const dot = renderIndicatorSpan(config.indicator)
      expect(dot?.className).toContain('bg-[var(--color-cancelled)]')
    })

    it('explicit cancelledVariant "cancelled" matches the default', () => {
      const config = getToolBadgeStatusConfig('cancelled', { cancelledVariant: 'cancelled' })
      expect(config.label).toBe('Cancelled')
      const dot = renderIndicatorSpan(config.indicator)
      expect(dot?.className).toContain('bg-[var(--color-cancelled)]')
    })

    it('cancelledVariant "muted" uses the muted dot color (GenericToolCall) — previously untested anywhere in the repo', () => {
      // GenericToolCall's `cancelled` case is derived from AssistantUI's
      // incomplete/cancelled reason rather than a dedicated status field, so it
      // is intentionally muted-colored instead of the dedicated cancelled color
      // ToolCallBadge uses. Confirmed via grep (`cancelledVariant`) this branch
      // had zero test coverage before this file.
      const config = getToolBadgeStatusConfig('cancelled', { cancelledVariant: 'muted' })
      expect(config.label).toBe('Cancelled')
      const dot = renderIndicatorSpan(config.indicator)
      expect(dot?.className).toContain('bg-[var(--color-muted)]')
      expect(dot?.className).not.toContain('bg-[var(--color-cancelled)]')
    })
  })

  it('defaults to icon size 13 for the running spinner (ToolCallBadge default)', () => {
    const svg = renderIconSvg(getToolBadgeStatusConfig('running').indicator)
    expect(svg?.getAttribute('width')).toBe('13')
  })

  it('applies a custom icon size via opts.size for the running spinner (GenericToolCall uses 12)', () => {
    const svg = renderIconSvg(getToolBadgeStatusConfig('running', { size: 12 }).indicator)
    expect(svg?.getAttribute('width')).toBe('12')
  })

  it('terminal-state dots are a fixed 8px regardless of opts.size — size only affects the running spinner', () => {
    const dot = renderIndicatorSpan(getToolBadgeStatusConfig('success', { size: 12, durationMs: 1 }).indicator)
    expect(dot?.className).toContain('w-2')
    expect(dot?.className).toContain('h-2')
  })

  it('drops the `border` field entirely — flat text-line rows have no card border to tint', () => {
    // Ticket "Tool components in chat": Family B's return shape moved from
    // { icon, label, border } to { indicator, label, textClass? }.
    const config = getToolBadgeStatusConfig('success')
    expect(config).not.toHaveProperty('border')
    expect(config).not.toHaveProperty('icon')
  })
})

describe('getSpanStatusDot — span-status flat-line dot shape (SubagentBlock, ActivityPanel)', () => {
  it.each([
    ['success', 'done', 'bg-[var(--color-success)]'],
    ['error', 'failed', 'bg-[var(--color-error)]'],
    ['cancelled', 'cancelled', 'bg-[var(--color-cancelled)]'],
    ['interrupted', 'interrupted', 'bg-[var(--color-muted)]'],
    ['timeout', 'timed out', 'bg-[var(--color-muted)]'],
  ] as const)('status=%s → label=%s, dot color=%s', (status, label, dotColor) => {
    const config = getSpanStatusDot(status)
    expect(config.label).toBe(label)
    const dot = renderIndicatorSpan(config.indicator)
    expect(dot?.className).toContain(dotColor)
    expect(dot?.className).toContain('rounded-full')
  })

  it('running keeps the spinning icon (not a dot) in the same indicator slot, default label "working"', () => {
    const config = getSpanStatusDot('running')
    expect(config.label).toBe('working')
    const svg = renderIconSvg(config.indicator)
    expect(svg?.getAttribute('class')).toContain('animate-spin')
  })

  it('overrides the running label via opts.runningLabel (ActivityPanel uses "running")', () => {
    expect(getSpanStatusDot('running', { runningLabel: 'running' }).label).toBe('running')
  })

  it('defaults to icon size 13 for the running spinner (matches getSpanStatusConfig default)', () => {
    const svg = renderIconSvg(getSpanStatusDot('running').indicator)
    expect(svg?.getAttribute('width')).toBe('13')
  })

  it('applies a custom icon size via opts.size (ActivityPanel uses 12)', () => {
    const svg = renderIconSvg(getSpanStatusDot('running', { size: 12 }).indicator)
    expect(svg?.getAttribute('width')).toBe('12')
  })

  it('falls back to a muted "unknown" dot for a status value outside the known 6-value domain (defensive wire guard)', () => {
    const config = getSpanStatusDot('bogus' as SpanLikeStatus)
    expect(config.label).toBe('unknown')
    const dot = renderIndicatorSpan(config.indicator)
    expect(dot?.className).toContain('bg-[var(--color-muted)]')
  })

  it('has no `border`/`pill` fields — Family A pill fields do not leak into the dot shape', () => {
    const config = getSpanStatusDot('success')
    expect(config).not.toHaveProperty('border')
    expect(config).not.toHaveProperty('pill')
  })
})

describe('isCancelledStatus — shared AssistantUI-incomplete/cancelled-reason detector', () => {
  it('true for incomplete status with reason "cancelled"', () => {
    expect(isCancelledStatus({ type: 'incomplete', reason: 'cancelled' })).toBe(true)
  })

  it('false for incomplete status with a different reason (e.g. "error")', () => {
    expect(isCancelledStatus({ type: 'incomplete', reason: 'error' })).toBe(false)
  })

  it('false for incomplete status with no reason field at all', () => {
    expect(isCancelledStatus({ type: 'incomplete' })).toBe(false)
  })

  it('false for a running status', () => {
    expect(isCancelledStatus({ type: 'running' })).toBe(false)
  })

  it('false for a complete status', () => {
    expect(isCancelledStatus({ type: 'complete' })).toBe(false)
  })
})

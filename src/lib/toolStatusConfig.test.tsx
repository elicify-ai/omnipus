import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import {
  getSpanStatusConfig,
  getToolBadgeStatusConfig,
  type SpanLikeStatus,
} from './toolStatusConfig'

// Direct unit coverage for the two shared status→config helpers (previously
// only exercised indirectly via SubagentBlock.test.tsx, ActivityPanel.test.tsx,
// ToolCallBadge.test.tsx, GenericToolCall.test.tsx). Icon fields are real JSX
// elements, so icon assertions render them into jsdom and inspect the
// resulting <svg> (Phosphor's IconBase maps the `size` prop straight to the
// `width`/`height` attributes and passes `className` through to `class`).

function renderIconSvg(icon: React.ReactNode) {
  const { container } = render(<div>{icon}</div>)
  return container.querySelector('svg')
}

describe('getSpanStatusConfig — "pill" family (SubagentBlock, ActivityPanel)', () => {
  it.each([
    ['running', 'working', 'border-[var(--color-border)]', 'bg-[var(--color-accent)]/10 text-[var(--color-accent)]'],
    ['success', 'done', 'border-[var(--color-success)]/20', 'bg-[var(--color-success)]/10 text-[var(--color-success)]'],
    ['error', 'failed', 'border-[var(--color-error)]/20', 'bg-[var(--color-error)]/10 text-[var(--color-error)]'],
    ['cancelled', 'cancelled', 'border-[var(--color-cancelled)]/20', 'bg-[var(--color-cancelled)]/10 text-[var(--color-cancelled)]'],
    ['interrupted', 'interrupted', 'border-[var(--color-muted)]/20', 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]'],
    ['timeout', 'timed out', 'border-[var(--color-muted)]/20', 'bg-[var(--color-muted)]/10 text-[var(--color-muted)]'],
  ] as const)('status=%s → label=%s, border=%s, pill=%s', (status, label, border, pill) => {
    const config = getSpanStatusConfig(status)
    expect(config.label).toBe(label)
    expect(config.border).toBe(border)
    expect(config.pill).toBe(pill)
  })

  it('defaults to icon size 13 (SubagentBlock default)', () => {
    const svg = renderIconSvg(getSpanStatusConfig('running').icon)
    expect(svg?.getAttribute('width')).toBe('13')
  })

  it('applies a custom icon size via opts.size (ActivityPanel uses 12)', () => {
    const svg = renderIconSvg(getSpanStatusConfig('running', { size: 12 }).icon)
    expect(svg?.getAttribute('width')).toBe('12')
  })

  it('defaults the running label to "working" (SubagentBlock)', () => {
    expect(getSpanStatusConfig('running').label).toBe('working')
  })

  it('overrides the running label via opts.runningLabel (ActivityPanel uses "running")', () => {
    expect(getSpanStatusConfig('running', { runningLabel: 'running' }).label).toBe('running')
  })

  it('the running icon spins (animate-spin class)', () => {
    const svg = renderIconSvg(getSpanStatusConfig('running').icon)
    expect(svg?.getAttribute('class')).toContain('animate-spin')
  })

  it('falls back to a muted "unknown" config for a status value outside the known 6-value domain (defensive wire guard)', () => {
    // The `default` branch in getSpanStatusConfig exists to protect against an
    // unexpected status string arriving from the wire despite the SpanLikeStatus
    // union only advertising 6 values — cast past the type to exercise it.
    const config = getSpanStatusConfig('bogus' as SpanLikeStatus)
    expect(config.label).toBe('unknown')
    expect(config.border).toBe('border-[var(--color-muted)]/20')
    expect(config.pill).toBe('bg-[var(--color-muted)]/10 text-[var(--color-muted)]')
  })
})

describe('getToolBadgeStatusConfig — "inline" family (ToolCallBadge, GenericToolCall)', () => {
  it('running: label "Running...", neutral border, spinning icon', () => {
    const config = getToolBadgeStatusConfig('running')
    expect(config.label).toBe('Running...')
    expect(config.border).toBe('border-[var(--color-border)]')
    const svg = renderIconSvg(config.icon)
    expect(svg?.getAttribute('class')).toContain('animate-spin')
  })

  it('error: label "Failed", error-color border', () => {
    const config = getToolBadgeStatusConfig('error')
    expect(config.label).toBe('Failed')
    expect(config.border).toBe('border-[var(--color-error)]/20')
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

    it('success border is always the success color regardless of duration', () => {
      expect(getToolBadgeStatusConfig('success', { durationMs: 5 }).border).toBe(
        'border-[var(--color-success)]/20',
      )
    })
  })

  describe('cancelled — cancelledVariant option', () => {
    it('defaults to the dedicated cancelled color (ToolCallBadge)', () => {
      const config = getToolBadgeStatusConfig('cancelled')
      expect(config.label).toBe('Cancelled')
      expect(config.border).toBe('border-[var(--color-cancelled)]/20')
      const svg = renderIconSvg(config.icon)
      expect(svg?.getAttribute('class')).toContain('text-[var(--color-cancelled)]')
    })

    it('explicit cancelledVariant "cancelled" matches the default', () => {
      const config = getToolBadgeStatusConfig('cancelled', { cancelledVariant: 'cancelled' })
      expect(config.label).toBe('Cancelled')
      expect(config.border).toBe('border-[var(--color-cancelled)]/20')
    })

    it('cancelledVariant "muted" uses the muted color + generic border (GenericToolCall) — previously untested anywhere in the repo', () => {
      // GenericToolCall's `cancelled` case is derived from AssistantUI's
      // incomplete/cancelled reason rather than a dedicated status field, so it
      // is intentionally muted-colored with a generic border instead of the
      // dedicated cancelled color ToolCallBadge uses. Confirmed via grep
      // (`cancelledVariant`) this branch had zero test coverage before this file.
      const config = getToolBadgeStatusConfig('cancelled', { cancelledVariant: 'muted' })
      expect(config.label).toBe('Cancelled')
      expect(config.border).toBe('border-[var(--color-border)]')
      const svg = renderIconSvg(config.icon)
      expect(svg?.getAttribute('class')).toContain('text-[var(--color-muted)]')
      expect(svg?.getAttribute('class')).not.toContain('text-[var(--color-cancelled)]')
    })
  })

  it('defaults to icon size 13 (ToolCallBadge default)', () => {
    const svg = renderIconSvg(getToolBadgeStatusConfig('running').icon)
    expect(svg?.getAttribute('width')).toBe('13')
  })

  it('applies a custom icon size via opts.size (GenericToolCall uses 12)', () => {
    const svg = renderIconSvg(getToolBadgeStatusConfig('running', { size: 12 }).icon)
    expect(svg?.getAttribute('width')).toBe('12')
  })
})

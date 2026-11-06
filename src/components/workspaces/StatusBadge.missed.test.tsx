/**
 * StatusBadge.missed.test.tsx — the `missed` TaskRun status on the badge
 * (founder decision 2026-10-29 Q1 A, Calendar 6b; backend 9bd80b391).
 *
 * A missed run (a scheduled fire that came due while Omnipus was down,
 * recorded at the next boot, never replayed) renders MUTED — the grey
 * inbox token family, the same neutral family the calendar's missed chip
 * uses — and never the amber `cancelled` family that `skipped` owns (a
 * skipped run is the overlap guard's decision; a missed run is downtime).
 * The human label comes from `statusLabel` (taskStatusConfig) and must be
 * the capitalised "Missed", never the raw status string.
 */
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { StatusBadge } from './StatusBadge'
import { statusLabel } from './taskStatusConfig'

describe('StatusBadge — missed run status', () => {
  it('renders the muted grey token family for a missed run', () => {
    const { container } = render(<StatusBadge status="missed">Missed</StatusBadge>)
    expect(container.firstElementChild?.className).toContain('var(--status-inbox-background)')
    expect(container.firstElementChild?.className).toContain('var(--status-inbox-foreground)')
  })

  it('stays distinct from the skipped badge (amber cancelled family)', () => {
    const missed = render(<StatusBadge status="missed">Missed</StatusBadge>)
    const skipped = render(<StatusBadge status="skipped">Skipped</StatusBadge>)
    expect(missed.container.firstElementChild?.className).not.toBe(
      skipped.container.firstElementChild?.className,
    )
    missed.unmount()
    skipped.unmount()
  })
})

describe('statusLabel — missed', () => {
  it('labels a missed run "Missed" — never the raw lowercase status string', () => {
    expect(statusLabel('missed')).toBe('Missed')
    expect(statusLabel('missed')).not.toBe('missed')
  })
})

/**
 * MilestoneProgressBar.test.tsx
 *
 * Tests for the MilestoneProgressBar component covering all BDD scenarios from
 * the project-task-management-level1-spec.
 *
 * Traces to: project-task-management-level1-spec.md — MilestoneProgressBar BDD scenarios
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MilestoneProgressBar } from './MilestoneProgressBar'
import type { MilestoneWithProgress } from '@/lib/api'

// ── Helpers ───────────────────────────────────────────────────────────────────

function makeMilestone(overrides: Partial<MilestoneWithProgress> = {}): MilestoneWithProgress {
  return {
    id: 'ms-1',
    project_id: 'proj-1',
    name: 'Test Milestone',
    progress: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

// ── Tests ─────────────────────────────────────────────────────────────────────

describe('MilestoneProgressBar — renders progress bar at 0%', () => {
  it('bar has 0% width when progress is 0', () => {
    // BDD: Given a milestone with progress=0 and totalTasks=3,
    // When MilestoneProgressBar renders,
    // Then the inner bar element has width 0%.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar 0% scenario
    const { container } = render(
      <MilestoneProgressBar milestone={makeMilestone({ progress: 0 })} />,
    )

    // The bar div has inline style width: 0%
    const bar = container.querySelector<HTMLDivElement>('[style*="width: 0%"]')
    expect(bar).not.toBeNull()
    expect(bar!.style.width).toBe('0%')
  })

  it('shows 0% label text', () => {
    // BDD: Given progress=0,
    // When component renders,
    // Then "0%" is displayed in the percentage label.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar 0% label
    render(<MilestoneProgressBar milestone={makeMilestone({ progress: 0 })} />)
    expect(screen.getByText('0%')).toBeInTheDocument()
  })
})

describe('MilestoneProgressBar — renders progress bar at 50%', () => {
  it('bar has 50% width when progress is 0.5', () => {
    // BDD: Given a milestone with progress=0.5,
    // When MilestoneProgressBar renders,
    // Then the inner bar element has width 50%.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar 50% scenario
    const { container } = render(
      <MilestoneProgressBar milestone={makeMilestone({ progress: 0.5 })} />,
    )

    const bar = container.querySelector<HTMLDivElement>('[style*="width: 50%"]')
    expect(bar).not.toBeNull()
    expect(bar!.style.width).toBe('50%')
  })

  it('shows 50% label text', () => {
    // BDD: Given progress=0.5,
    // When component renders,
    // Then "50%" is displayed in the percentage label.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar 50% label
    render(<MilestoneProgressBar milestone={makeMilestone({ progress: 0.5 })} />)
    expect(screen.getByText('50%')).toBeInTheDocument()
  })

  it('differentiation test: 0% and 50% produce different bar widths', () => {
    // Anti-hardcode: two different progress values must produce different bar widths.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar differentiation
    const { container: c0 } = render(
      <MilestoneProgressBar milestone={makeMilestone({ progress: 0 })} />,
    )
    const { container: c50 } = render(
      <MilestoneProgressBar milestone={makeMilestone({ progress: 0.5 })} />,
    )

    const bar0 = c0.querySelector<HTMLDivElement>('[style*="width: 0%"]')
    const bar50 = c50.querySelector<HTMLDivElement>('[style*="width: 50%"]')

    expect(bar0).not.toBeNull()
    expect(bar50).not.toBeNull()
    // The widths are different — confirming it's not hardcoded
    expect(bar0!.style.width).not.toBe(bar50!.style.width)
  })
})

describe('MilestoneProgressBar — shows due date when set', () => {
  it('renders the formatted due date when due_date is set to a future date', () => {
    // BDD: Given a milestone with due_date="2026-09-01",
    // When MilestoneProgressBar renders,
    // Then "Sep 1" is visible (or locale-equivalent short date).
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar due-date scenario
    render(
      <MilestoneProgressBar
        milestone={makeMilestone({ due_date: '2026-09-01', progress: 0 })}
      />,
    )

    // The component formats via toLocaleDateString with month:'short', day:'numeric'
    // In any locale: Sep 1, 1. Sep., etc. — check that "1" and "Sep"/"9" appear together.
    // We use a flexible check: just verify that the component renders some date-like text
    // that includes "Sep" or "9" (month) and "1" (day).
    const dateText = screen.queryByText(/sep/i) ?? screen.queryByText(/9\/1|1\.9\.|1 sep/i)
    expect(dateText).not.toBeNull()
  })
})

describe('MilestoneProgressBar — shows overdue style when past due', () => {
  it('applies red color class when due_date is in the past and task is not complete', () => {
    // BDD: Given a milestone with a due_date in the past and progress < 100%,
    // When MilestoneProgressBar renders,
    // Then a red/error color class is applied to the date text.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar overdue-style scenario
    const { container } = render(
      <MilestoneProgressBar
        milestone={makeMilestone({
          due_date: '2020-01-01', // Clearly in the past
          progress: 0.5,
        })}
      />,
    )

    // The component applies 'text-red-400' to the due date span when overdue
    const redElements = container.querySelectorAll('.text-red-400')
    expect(redElements.length).toBeGreaterThan(0)
  })

  it('overdue bar uses red fill class', () => {
    // BDD: Given a past-due milestone with progress < 100%,
    // When the component renders,
    // Then the progress bar fill is red-colored (bg-red-400).
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar overdue-bar-color
    const { container } = render(
      <MilestoneProgressBar
        milestone={makeMilestone({
          due_date: '2020-06-01',
          progress: 0.3,
        })}
      />,
    )

    const redBar = container.querySelector('.bg-red-400')
    expect(redBar).not.toBeNull()
  })
})

describe('MilestoneProgressBar — omits due date section when not set', () => {
  it('renders a dash placeholder instead of a date when due_date is absent', () => {
    // BDD: Given a milestone with no due_date,
    // When MilestoneProgressBar renders,
    // Then no formatted date text is rendered (only the "—" placeholder).
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar no-due-date scenario
    render(
      <MilestoneProgressBar
        milestone={makeMilestone({ due_date: undefined, progress: 0.2 })}
      />,
    )

    // Should render "—" placeholder, not a real date
    expect(screen.getByText('—')).toBeInTheDocument()

    // No month name rendered
    expect(screen.queryByText(/jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec/i)).toBeNull()
  })

  it('does not show overdue style when no due_date is set', () => {
    // BDD: Given a milestone with no due_date,
    // When the component renders,
    // Then no red styling is applied.
    // Traces to: project-task-management-level1-spec.md — MilestoneProgressBar no-due-no-overdue
    const { container } = render(
      <MilestoneProgressBar
        milestone={makeMilestone({ due_date: undefined, progress: 0 })}
      />,
    )

    // No red elements for overdue state (the bar itself won't be red-400 since isOverdue=false)
    const redElements = container.querySelectorAll('.text-red-400')
    expect(redElements.length).toBe(0)
  })
})

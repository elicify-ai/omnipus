// Stepper — spec order 15 (issue #1967 B1). Oracle: spec "Accessibility and
// Keyboard" ("Stepper is an ordered list; the current step has
// aria-current="step"; a visually hidden "Step n of m" text accompanies it")
// and "Design-System Components" (presentational composite: labels + current
// index, no navigation logic).
import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { Stepper } from './stepper'

const exportSteps = ['Choose agent', 'Review', 'Download']
const importSteps = ['Choose file', 'Preview', 'Confirm', 'Result']

describe('Stepper', () => {
  it('renders an ORDERED list (an <ol> element, not just any list role) of the step labels in order', () => {
    render(<Stepper steps={exportSteps} current={0} label="Export steps" />)
    const list = screen.queryByRole('list')
    expect(list, 'the stepper renders a list element').not.toBeNull()
    // The spec says ORDERED list: role="list" alone is also true of a <ul>,
    // so the tag name itself is asserted (CHECK W02).
    expect(list!.tagName.toUpperCase(), 'the stepper is an <ol>').toBe('OL')
    const items = within(list as HTMLElement).getAllByRole('listitem')
    expect(items.map((li) => li.textContent)).toEqual(exportSteps)
  })

  it('marks the current step with aria-current="step" and no other', () => {
    render(<Stepper steps={importSteps} current={2} label="Import steps" />)
    const list = screen.getByRole('list')
    const items = within(list).getAllByRole('listitem')
    items.forEach((li, i) => {
      if (i === 2) {
        expect(li).toHaveAttribute('aria-current', 'step')
      } else {
        expect(li).not.toHaveAttribute('aria-current')
      }
    })
  })

  it('accompanies the current step with a VISUALLY HIDDEN "Step n of m"', () => {
    render(<Stepper steps={exportSteps} current={1} label="Export steps" />)
    // "Step 2 of 3" — 1-based n, m = total steps. Visually hidden is asserted,
    // not assumed: the element carrying the text must carry the project's
    // sr-only utility (CHECK W02), so a visible duplicate label cannot pass.
    const stepText = screen.getByText('Step 2 of 3')
    expect(stepText).toBeInTheDocument()
    expect(stepText.className, 'the "Step n of m" text is visually hidden via sr-only').toContain('sr-only')
  })

  it('exposes the list under its accessible label', () => {
    render(<Stepper steps={importSteps} current={0} label="Import steps" />)
    expect(screen.getByRole('list', { name: 'Import steps' })).toBeInTheDocument()
  })
})

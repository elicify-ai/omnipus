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
  it('renders an ordered list of the step labels in order', () => {
    render(<Stepper steps={exportSteps} current={0} label="Export steps" />)
    const list = screen.queryByRole('list')
    expect(list, 'the stepper renders a list element (an ol for an ordered list)').not.toBeNull()
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

  it('accompanies the current step with a visually hidden "Step n of m"', () => {
    render(<Stepper steps={exportSteps} current={1} label="Export steps" />)
    // "Step 2 of 3" — 1-based n, m = total steps.
    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()
  })

  it('exposes the list under its accessible label', () => {
    render(<Stepper steps={importSteps} current={0} label="Import steps" />)
    expect(screen.getByRole('list', { name: 'Import steps' })).toBeInTheDocument()
  })
})

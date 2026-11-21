// Stepper — the shared wizard step indicator (spec "Design-System
// Components": ordered list, `aria-current="step"` on the current step, a
// visually hidden "Step n of m" text; presentational only — labels and the
// current index, no navigation logic).
//
// RED STUB (issue #1967 slice B1, test author scaffold): renders nothing.
// GREEN implements the composite and publishes it through the four-part
// design-system contract (rule 9: src/index.ts export, catalog.json entry,
// @source line in library.css, design-system/manifests/stepper.json) plus a
// story.

export interface StepperProps {
  /** Step labels in display order. */
  steps: string[]
  /** Zero-based index of the current step. */
  current: number
  /** Accessible name for the list ("Export steps", "Import steps"). */
  label?: string
}

export function Stepper({ steps, current, label }: StepperProps) {
  void steps
  void current
  void label
  return null
}

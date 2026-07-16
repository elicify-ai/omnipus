import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

/**
 * Tripwire: DOM order must equal visual order and Tab order in the shared
 * footer primitives.
 *
 * `flex-col-reverse` flips the *visual* stacking of a column layout without
 * touching DOM order or Tab order. Applied to a footer with "secondary
 * button, primary button" markup (the universal convention across every
 * consumer in this repo — Cancel/secondary first, Confirm/primary/
 * destructive last), it makes the primary button render ABOVE the secondary
 * one on phone widths (<sm) while Tab still visits Cancel before Confirm.
 * That is the exact "CSS reordering breaks reading/tab order" class of bug
 * this project forbids (see the fix that replaced `flex-col-reverse` with
 * `flex-col` in dialog.tsx / sheet.tsx / alert-dialog.tsx — DOM order now
 * stacks Cancel above Confirm, which is also the thumb-friendly mobile
 * standard; the `sm:flex-row sm:justify-end` row layout at sm+ is
 * unchanged).
 *
 * This test statically scans the three shared footer primitives for the
 * banned class. It intentionally does NOT scan consumers (dozens of
 * screens compose these primitives with their own className overrides) —
 * it guards the primitives, which is where every consumer inherits the
 * default from.
 *
 * If a future change has a genuine, deliberate need to reverse a footer's
 * mobile stacking, it must NOT reintroduce `flex-col-reverse` bare — instead
 * reorder the actual DOM children (so Tab order and visual order agree) and
 * add an explicit exemption comment right here explaining why, updating the
 * file list below.
 */
describe('shared footer primitives: no flex-col-reverse', () => {
  const guardedFiles = [
    'src/components/ui/dialog.tsx',
    'src/components/ui/sheet.tsx',
    'src/components/ui/alert-dialog.tsx',
  ]

  it.each(guardedFiles)('%s contains no flex-col-reverse', (relPath) => {
    const contents = readFileSync(resolve(process.cwd(), relPath), 'utf-8')
    expect(contents).not.toMatch(/flex-col-reverse/)
  })
})

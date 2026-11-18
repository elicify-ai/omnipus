/**
 * Select `field`/`ghost` trigger variants — W2-1 contract §2.3 (R39, R41).
 *
 * Oracle: the contract, never the implementation. The one literal below (the
 * `field` class string) is the contract's own requirement — "Byte-identical
 * classes to today's trigger" — captured from `select.tsx` at 69aa07d9a
 * (cn() of the base string was verified byte-identical in this task, so the
 * pre-change DOM className is exactly this string).
 *
 * File note: contract §4 names `select.test.tsx` for these cases. They live
 * in a separate file instead: importing a not-yet-existing named export
 * (`selectTriggerVariants`) fails the whole module at collection, which
 * would take the existing GREEN tests in `select.test.tsx` down with it.
 * Content matches the contract's RED item 1 exactly; the file split is the
 * only deviation, made to keep the existing suite green.
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  selectTriggerVariants,
} from './select'

// Contract §2.3 `field`: "Byte-identical classes to today's trigger" — the
// pre-change base string from select.tsx at 69aa07d9a (verified: cn() of this
// single string is byte-identical, so a default trigger's DOM className is
// exactly this).
const FIELD_TRIGGER_CLASSES =
  'flex h-9 w-full items-center justify-between rounded-md border border-[var(--color-border)] bg-[var(--color-surface-1)] px-[var(--space-2-5)] py-[var(--space-2)] text-[length:var(--type-body-compact-size)] text-[var(--color-secondary)] [@media(pointer:coarse)]:h-[44px] placeholder:text-[var(--color-muted)] disabled:cursor-not-allowed disabled:opacity-50 [&>span]:line-clamp-1'

function renderTrigger(variant?: 'field' | 'ghost', active?: boolean) {
  return render(
    <Select>
      <SelectTrigger variant={variant} active={active} aria-label="Project">
        <SelectValue placeholder="Choose a project" />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="alpha">Alpha</SelectItem>
      </SelectContent>
    </Select>,
  )
}

describe('SelectTrigger variants — W2-1 §2.3', () => {
  it('exports selectTriggerVariants as a cva function with field and ghost variants', () => {
    expect(typeof selectTriggerVariants).toBe('function')
    // The cva exposes both variants by name; field is the default.
    expect(selectTriggerVariants({ variant: 'field' })).toContain('h-9')
    expect(selectTriggerVariants({ variant: 'ghost' })).toContain('h-8')
    expect(selectTriggerVariants()).toContain('h-9')
  })

  it('renders data-variant="field" by default with today’s byte-identical class string', () => {
    renderTrigger()
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveAttribute('data-variant', 'field')
    // Exact equality: any drift in the default trigger's classes is a
    // visible change to all 18 current callers, which the contract forbids.
    expect(trigger.className).toBe(FIELD_TRIGGER_CLASSES)
  })

  it('renders data-variant="field" explicitly with the same byte-identical class string', () => {
    renderTrigger('field')
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveAttribute('data-variant', 'field')
    expect(trigger.className).toBe(FIELD_TRIGGER_CLASSES)
  })

  it('ghost variant carries the contract’s compact ghost classes and no field border/width', () => {
    renderTrigger('ghost')
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveAttribute('data-variant', 'ghost')
    // §2.3 `ghost`: 32px, no border, transparent background, content-sized,
    // 8px padding, 4px gap, utility-xs type, medium weight, muted text,
    // 44px touch minimum on a coarse pointer (real box, skill rule 12).
    expect(trigger).toHaveClass(
      'h-8',
      'w-auto',
      'border-transparent',
      'px-[var(--space-2)]',
      'gap-[var(--space-1)]',
      'text-[length:var(--type-utility-xs-size)]',
      'font-medium',
      'text-[var(--color-muted)]',
      'pointer-coarse:min-h-[var(--target-touch-minimum)]',
      'pointer-coarse:min-w-[var(--target-touch-minimum)]',
    )
    // The field's 36px height and visible border must not leak into ghost.
    expect(trigger.className).not.toContain('h-9')
    expect(trigger.className).not.toContain('border-[var(--color-border)]')
    expect(trigger.className).not.toContain('w-full')
  })

  it('ghost variant with active shows accent text and not muted', () => {
    renderTrigger('ghost', true)
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveClass('text-[var(--color-accent)]')
    expect(trigger.className).not.toContain('text-[var(--color-muted)]')
  })

  it('ghost caret renders at 11px with opacity-60 (FilterMenu parity)', () => {
    renderTrigger('ghost')
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    const caret = trigger.querySelector('svg')
    expect(caret).not.toBeNull()
    expect(caret).toHaveAttribute('width', '11')
    expect(caret).toHaveAttribute('height', '11')
    expect(caret).toHaveClass('opacity-60')
  })

  // Contract §2.3 `ghost`: "forced colours keep `ButtonText`" — the ghost
  // trigger must pin its own text to the ButtonText system colour in
  // forced-colors mode (precedent: the kit's forced-colors:text-[CanvasText]
  // pattern). Asserted as the Tailwind spelling of that requirement; if the
  // implemented utility spelling differs, that is a finding to reconcile
  // against the contract sentence, not a test weakening.
  it('ghost trigger keeps ButtonText in forced-colors mode', () => {
    renderTrigger('ghost')
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger.className).toMatch(/forced-colors:text-\[ButtonText\]/)
  })

  it('ghost still opens the same Radix content and selects an item (root behaviour unchanged)', () => {
    render(
      <Select>
        <SelectTrigger variant="ghost" aria-label="Project">
          <SelectValue placeholder="Choose a project" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="alpha">Alpha</SelectItem>
        </SelectContent>
      </Select>,
    )
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    // WebKit tabbability convention survives the variant split.
    expect(trigger).toHaveAttribute('tabindex', '0')
    fireEvent.click(trigger)
    const alpha = screen.getByRole('option', { name: 'Alpha' })
    fireEvent.pointerDown(alpha, { pointerId: 1, button: 0 })
    fireEvent.click(alpha)
    expect(trigger).toHaveTextContent('Alpha')
  })
})

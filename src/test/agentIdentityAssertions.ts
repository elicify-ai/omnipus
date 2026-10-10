// Test-only assertions from W1-6 / FR-020 and the spec's locked vocabulary,
// re-pointed at the ONE identity editor (founder 2026-10-10: AgentLookPicker
// — figure thumbnails, searchable "Role badge" dropdown, ten colour dots).
// Keep these out of the grandfathered AgentProfile test file; the real picker
// remains mounted by its calling tests, with only the network mocked.
import { expect } from 'vitest'
import { within } from '@testing-library/react'

export const IDENTITY_COLOUR_NAMES = [
  'Azure', 'Sky', 'Cyan', 'Indigo', 'Violet', 'Purple', 'Fuchsia', 'Pink', 'Orange', 'Grey',
] as const

export const FOUNDER_FIGURE_ORDER = ['Omnipus', 'Man', 'Robot', 'Woman', 'Monogram'] as const

/** All ten colour dots present, `selected` pressed, every dot disabled (built-in lock). */
export function expectLockedIdentityColours(root: HTMLElement, selected = 'Azure') {
  const basics = within(root)
  for (const name of IDENTITY_COLOUR_NAMES) {
    const choice = basics.getByRole('button', { name })
    expect(choice, name).toBeDisabled()
    expect(choice, name).toHaveAttribute('aria-pressed', String(name === selected))
  }
}

/**
 * Built-in lock, figure and role fields: the five Look thumbnails are visible
 * but disabled with `figure` pressed, and the Role badge combobox is visible
 * but disabled, showing `roleLabel` (docs/agents.md "Built-in identity is
 * locked": choices remain visible and cannot be changed).
 */
export function expectLockedFigureAndRole(
  root: HTMLElement,
  { figure = 'Omnipus', roleLabel = 'General assistant' }: { figure?: string; roleLabel?: string } = {},
) {
  const basics = within(root)
  for (const name of FOUNDER_FIGURE_ORDER) {
    const choice = basics.getByRole('button', { name })
    expect(choice, name).toBeDisabled()
    expect(choice, name).toHaveAttribute('aria-pressed', String(name === figure))
  }
  const role = basics.getByRole('combobox', { name: 'Role badge' })
  expect(role).toBeDisabled()
  expect(role, 'role badge shows the stored role').toHaveTextContent(roleLabel)
}

/** Custom-agent editor: every identity field editable, with the defaults selected. */
export function expectEditableIdentityChoices(root: HTMLElement) {
  const basics = within(root)
  // The 5th figure (Monogram) is offered like the other four: enabled, and not
  // pressed by default (Omnipus stays the default). ARCH-RULING-monogram AC-13.
  for (const figure of FOUNDER_FIGURE_ORDER) {
    const choice = basics.getByRole('button', { name: figure })
    expect(choice, figure).toBeEnabled()
    expect(choice, figure).toHaveAttribute('aria-pressed', String(figure === 'Omnipus'))
  }
  // The role is ONE searchable dropdown, not word buttons (founder 2026-10-10);
  // the combobox carries the field name and shows the selected role's label.
  const role = basics.getByRole('combobox', { name: 'Role badge' })
  expect(role).toBeEnabled()
  expect(role, 'default role is General assistant').toHaveTextContent('General assistant')
  expect(basics.getByRole('button', { name: 'Azure' })).toBeEnabled()
  expect(basics.getByRole('button', { name: 'Azure' })).toHaveAttribute('aria-pressed', 'true')
}

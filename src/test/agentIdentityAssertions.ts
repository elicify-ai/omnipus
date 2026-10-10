// Test-only assertions from W1-6 / FR-020 and the spec's locked vocabulary.
// Keep these out of the grandfathered AgentProfile test file; the real picker
// remains mounted by its calling tests, with only the network mocked.
import { expect } from 'vitest'
import { within } from '@testing-library/react'

export function expectLockedIdentityColours(root: HTMLElement) {
  const basics = within(root)
  const names = ['Azure', 'Sky', 'Cyan', 'Indigo', 'Violet', 'Purple', 'Fuchsia', 'Pink', 'Orange', 'Grey']
  for (const name of names) {
    const choice = basics.getByRole('button', { name })
    expect(choice, name).toBeDisabled()
    expect(choice, name).toHaveAttribute('aria-pressed', String(name === 'Azure'))
  }
}

export function expectEditableIdentityChoices(root: HTMLElement) {
  const basics = within(root)
  for (const figure of ['Robot', 'Man', 'Woman', 'Omnipus']) {
    const choice = basics.getByRole('button', { name: figure })
    expect(choice, figure).toBeEnabled()
    expect(choice, figure).toHaveAttribute('aria-pressed', String(figure === 'Omnipus'))
  }
  expect(basics.getByRole('button', { name: 'General assistant' })).toBeEnabled()
  expect(basics.getByRole('button', { name: 'General assistant' })).toHaveAttribute('aria-pressed', 'true')
  expect(basics.getByRole('button', { name: 'Azure' })).toBeEnabled()
  expect(basics.getByRole('button', { name: 'Azure' })).toHaveAttribute('aria-pressed', 'true')
}

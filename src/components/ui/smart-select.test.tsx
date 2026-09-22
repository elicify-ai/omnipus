import * as React from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import { SmartSelect } from './smart-select'
import { Field } from './field'

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})
const items = Array.from({ length: 6 }, (_, index) => ({ value: String(index), label: `Option ${index}` }))

describe('SmartSelect — controlled threshold contract', () => {
  it('keeps accessible identity and selected value across plain and searchable branches', () => {
    const onValueChange = vi.fn()
    const { rerender } = render(<SmartSelect value="0" onValueChange={onValueChange} ariaLabel="Project" items={items.slice(0, 5)} />)
    expect(screen.getByRole('combobox', { name: 'Project' })).toHaveTextContent('Option 0')
    rerender(<SmartSelect value="0" onValueChange={onValueChange} ariaLabel="Project" items={items} />)
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveTextContent('Option 0')
    fireEvent.click(trigger)
    fireEvent.click(screen.getByText('Option 4'))
    expect(onValueChange).toHaveBeenCalledWith('4')
  })

  it.each([
    ['plain', items.slice(0, 5)],
    ['searchable', items],
  ])('forwards Field control metadata to the %s combobox trigger', (_branch, branchItems) => {
    render(
      <Field label="Project" description="Choose one" error="Selection required" required>
        <SmartSelect value="0" onValueChange={vi.fn()} ariaLabel="Explicit project" items={branchItems} />
      </Field>,
    )
    const trigger = screen.getByRole('combobox', { name: 'Explicit project' })
    expect(trigger.id).not.toBe('')
    expect(trigger).toHaveAttribute('aria-required', 'true')
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    const descriptions = (trigger.getAttribute('aria-describedby') ?? '').split(/\s+/).map((id) => document.getElementById(id)?.textContent)
    expect(descriptions).toEqual(expect.arrayContaining(['Choose one', 'Selection required']))
  })
})

describe('SmartSelect — searchable branch', () => {
  it('hides trigger and selection icons from assistive technology', () => {
    render(<SmartSelect value="0" onValueChange={() => {}} ariaLabel="Project" items={items} />)
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    fireEvent.click(trigger)
    for (const icon of document.querySelectorAll('svg')) expect(icon).toHaveAttribute('aria-hidden', 'true')
  })

  it('narrows the list by query and selects a filtered item', () => {
    function Harness() {
      const [value, setValue] = React.useState('0')
      return <SmartSelect value={value} onValueChange={setValue} ariaLabel="Project" items={items} />
    }
    render(<Harness />)
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    fireEvent.click(trigger)
    fireEvent.change(screen.getByRole('combobox', { name: 'Command search' }), { target: { value: '3' } })
    expect(screen.getByText('Option 3')).toBeInTheDocument()
    expect(screen.queryByText('Option 1')).not.toBeInTheDocument()
    fireEvent.click(screen.getByText('Option 3'))
    expect(trigger).toHaveTextContent('Option 3')
  })
})

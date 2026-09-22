import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from './select'

beforeAll(() => {
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

describe('Select — controlled form contract', () => {
  it('preserves name, accessible identity and reports a selected value', () => {
    const onValueChange = vi.fn()
    render(<form><Select value="alpha" onValueChange={onValueChange} name="project"><SelectTrigger aria-label="Project"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="alpha">Alpha</SelectItem><SelectItem value="beta">Beta</SelectItem></SelectContent></Select></form>)
    const trigger = screen.getByRole('combobox', { name: 'Project' })
    expect(trigger).toHaveTextContent('Alpha')
    fireEvent.click(trigger)
    const beta = screen.getByRole('option', { name: 'Beta' })
    fireEvent.pointerDown(beta, { pointerId: 1, button: 0 }); fireEvent.click(beta)
    expect(onValueChange).toHaveBeenCalledWith('beta')
    expect(document.querySelector('select[name="project"]')).toHaveValue('alpha')
  })

  it('hides trigger and selection icons from assistive technology', () => {
    render(<Select defaultValue="alpha"><SelectTrigger aria-label="Project"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="alpha">Alpha</SelectItem></SelectContent></Select>)
    expect(screen.getByRole('combobox').querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    fireEvent.click(screen.getByRole('combobox'))
    for (const icon of document.querySelectorAll('svg')) expect(icon).toHaveAttribute('aria-hidden', 'true')
  })

  it('uses the native required select for validity and form submission', () => {
    const { container } = render(
      <form>
        <Select required name="project">
          <SelectTrigger aria-label="Project"><SelectValue placeholder="Choose a project" /></SelectTrigger>
          <SelectContent><SelectItem value="alpha">Alpha</SelectItem></SelectContent>
        </Select>
      </form>,
    )
    const form = container.querySelector('form')!
    const nativeSelect = form.querySelector('select[name="project"]')!
    const trigger = screen.getByRole('combobox', { name: 'Project' })

    expect(trigger).toHaveAttribute('aria-required', 'true')
    expect(nativeSelect).toBeRequired()
    expect(form.checkValidity()).toBe(false)

    fireEvent.click(trigger)
    const alpha = screen.getByRole('option', { name: 'Alpha' })
    fireEvent.pointerDown(alpha, { pointerId: 1, button: 0 })
    fireEvent.click(alpha)

    expect(form.checkValidity()).toBe(true)
    expect(new FormData(form).get('project')).toBe('alpha')
  })
})

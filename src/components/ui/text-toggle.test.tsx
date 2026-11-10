import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TextToggle } from './text-toggle'

describe('TextToggle — labelled on/off button', () => {
  it('shows the word and reports pressed when on', () => {
    render(
      <TextToggle pressed onPressedChange={() => {}}>
        Auto
      </TextToggle>,
    )
    const toggle = screen.getByRole('button', { name: 'Auto' })
    expect(toggle).toHaveAttribute('aria-pressed', 'true')
    expect(toggle).toHaveAttribute('data-state', 'on')
  })

  it('reports not pressed when off', () => {
    render(
      <TextToggle pressed={false} onPressedChange={() => {}}>
        Auto
      </TextToggle>,
    )
    expect(screen.getByRole('button', { name: 'Auto' })).toHaveAttribute('aria-pressed', 'false')
  })

  it('asks for the opposite state when clicked', async () => {
    const onPressedChange = vi.fn()
    render(
      <TextToggle pressed={false} onPressedChange={onPressedChange}>
        Auto
      </TextToggle>,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Auto' }))
    expect(onPressedChange).toHaveBeenCalledTimes(1)
    expect(onPressedChange).toHaveBeenCalledWith(true)
  })

  it('does not change when disabled', async () => {
    const onPressedChange = vi.fn()
    render(
      <TextToggle pressed disabled onPressedChange={onPressedChange}>
        Auto
      </TextToggle>,
    )
    const toggle = screen.getByRole('button', { name: 'Auto' })
    expect(toggle).toBeDisabled()
    await userEvent.click(toggle)
    expect(onPressedChange).not.toHaveBeenCalled()
  })
})

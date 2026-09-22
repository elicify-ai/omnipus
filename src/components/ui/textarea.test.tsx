import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { Textarea } from './textarea'

describe('Textarea — native form contract', () => {
  it('preserves identity, controlled value, read-only, required and invalid state', () => {
    const onChange = vi.fn()
    const { rerender } = render(<Textarea id="notes" name="notes" aria-label="Notes" value="Alpha" onChange={onChange} readOnly required aria-invalid />)
    const textarea = screen.getByRole('textbox', { name: 'Notes' })
    expect(textarea).toHaveAttribute('id', 'notes')
    expect(textarea).toHaveAttribute('name', 'notes')
    expect(textarea).toHaveAttribute('readonly')
    expect(textarea).toBeRequired()
    expect(textarea).toHaveAttribute('aria-invalid', 'true')
    fireEvent.change(textarea, { target: { value: 'Beta' } })
    expect(onChange).toHaveBeenCalledOnce()
    rerender(<Textarea aria-label="Notes" value="Beta" onChange={onChange} />)
    expect(textarea).toHaveValue('Beta')
  })

  it('preserves its value and rejects editing while disabled', async () => {
    const user = userEvent.setup()
    const onChange = vi.fn()
    render(<Textarea aria-label="Notes" value="Alpha" onChange={onChange} disabled />)
    const textarea = screen.getByRole('textbox', { name: 'Notes' })

    expect(textarea).toBeDisabled()
    await user.click(textarea)
    await user.keyboard('Beta')
    expect(textarea).toHaveValue('Alpha')
    expect(onChange).not.toHaveBeenCalled()
  })
})

import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Field } from './field'
import { Input } from './input'
import { Select, SelectTrigger, SelectValue } from './select'

describe('Field — association contract', () => {
  it('connects label, description and error to the control without owning its value', () => {
    const { rerender } = render(
      <Field label="Workspace name" description="Visible to teammates" error="Name is required" required>
        <Input value="Alpha" onChange={() => {}} />
      </Field>,
    )
    const input = screen.getByRole('textbox', { name: /workspace name/i })
    expect(input).toHaveValue('Alpha')
    expect(input).toBeRequired()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input.getAttribute('aria-describedby')?.split(' ')).toHaveLength(2)
    expect(screen.getByRole('alert')).toHaveTextContent('Name is required')

    rerender(<Field label="Workspace name"><Input value="Beta" onChange={() => {}} /></Field>)
    const clearedInput = screen.getByRole('textbox', { name: /workspace name/i })
    expect(clearedInput).toHaveValue('Beta')
    expect(clearedInput).not.toHaveAttribute('aria-invalid')
    expect(clearedInput).not.toHaveAttribute('aria-describedby')
    expect(screen.queryByRole('alert')).toBeNull()
  })

  it('preserves caller identity, read-only state and described-by references', () => {
    render(
      <Field label="Slug" description="Stable identifier">
        <Input id="custom-slug" name="slug" readOnly aria-describedby="external-help" defaultValue="alpha" />
      </Field>,
    )
    const input = screen.getByRole('textbox', { name: 'Slug' })
    expect(input).toHaveAttribute('id', 'custom-slug')
    expect(input).toHaveAttribute('name', 'slug')
    expect(input).toHaveAttribute('readonly')
    expect(input.getAttribute('aria-describedby')).toContain('external-help')
  })

  it('supports composite controls through explicit identity props without imposing layout', () => {
    render(
      <Field label="Amount" description="Whole dollars">
        {(controlProps) => <div><input {...controlProps} readOnly value="25" /><span>USD</span></div>}
      </Field>,
    )
    expect(screen.getByRole('textbox', { name: 'Amount' })).toHaveValue('25')
    expect(screen.getByText('USD')).toBeVisible()
  })

  it('rejects intrinsic wrappers that would receive control identity silently', () => {
    expect(() => render(<Field label="Broken"><div><Input /></div></Field>)).toThrow(/control element or render function/)
  })

  it('keeps visible and programmatic required state consistent when child props disagree', () => {
    const { rerender } = render(<Field label="Name" required><Input required={false} /></Field>)
    expect(screen.getByRole('textbox', { name: /name/i })).toBeRequired()
    expect(screen.getByText('*', { exact: false })).toBeInTheDocument()

    rerender(<Field label="Name"><Input required /></Field>)
    expect(screen.getByRole('textbox', { name: /name/i })).toBeRequired()
    expect(screen.getByText('*', { exact: false })).toBeInTheDocument()
  })
})

// D13: Field owns validation; an explicit child state is retained only without a Field error.
describe('Field — validation precedence and compound wiring', () => {
  it('marks an explicit false child invalid while its Field error exists, then restores the child state', () => {
    const { rerender } = render(<Field label="Name" error="Enter a name"><Input aria-invalid={false} /></Field>)
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveAttribute('aria-invalid', 'true')
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveAccessibleDescription('Enter a name')
    rerender(<Field label="Name"><Input aria-invalid={false} /></Field>)
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveAttribute('aria-invalid', 'false')
    expect(screen.getByRole('textbox', { name: 'Name' })).not.toHaveAttribute('aria-describedby')
  })

  it('preserves child validation when Field supplies no error', () => {
    render(<Field label="Name"><Input aria-invalid="spelling" /></Field>)
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveAttribute('aria-invalid', 'spelling')
  })

  it('wires Select root required state and trigger identity separately', () => {
    render(
      <form>
        <Field label="Team" description="Choose your team" error="Select a team" required>
          {({ required, ...triggerProps }) => (
            <Select name="team" required={required}>
              <SelectTrigger {...triggerProps}><SelectValue placeholder="Choose" /></SelectTrigger>
            </Select>
          )}
        </Field>
      </form>,
    )
    const trigger = screen.getByRole('combobox', { name: 'Team' })
    expect(trigger).toHaveAttribute('aria-required', 'true')
    expect(trigger).not.toHaveAttribute('required')
    expect(trigger).toHaveAttribute('aria-invalid', 'true')
    expect(trigger).toHaveAccessibleDescription('Choose your team Select a team')
    expect(document.querySelector('label')).toHaveAttribute('for', trigger.id)
    expect(document.querySelector('select[name="team"]')).toBeRequired()
  })
})

// D13 requires optional copy owned by Field, with caller-localized strings and preserved form geometry.
describe('Field — optional indication', () => {
  it('shows caller-localized optional copy without making the control required', () => {
    render(<Field label="Notes" optionalLabel="(facultatif)"><Input /></Field>)
    const input = screen.getByRole('textbox', { name: 'Notes (facultatif)' })
    expect(input).not.toBeRequired()
    expect(screen.getByText('(facultatif)')).toBeVisible()
    expect(input.closest('div')).not.toHaveAttribute('optionalLabel')
  })

  it('suppresses optional copy when either Field or its child is required', () => {
    const { rerender } = render(<Field label="Notes" optionalLabel="(optional)" required><Input /></Field>)
    expect(screen.queryByText('(optional)')).toBeNull()
    expect(screen.getByRole('textbox', { name: 'Notes' })).toBeRequired()
    rerender(<Field label="Notes" optionalLabel="(optional)"><Input required /></Field>)
    expect(screen.queryByText('(optional)')).toBeNull()
    expect(screen.getByRole('textbox', { name: 'Notes' })).toBeRequired()
    rerender(<Field label="Notes" optionalLabel="(optional)"><Input /></Field>)
    expect(screen.getByRole('textbox', { name: 'Notes (optional)' })).not.toBeRequired()
  })

  it('leaves existing labels unchanged when optional copy is omitted', () => {
    render(<Field label="Notes"><Input /></Field>)
    expect(screen.getByRole('textbox', { name: 'Notes' })).not.toBeRequired()
    expect(document.querySelector('label')).toHaveTextContent(/^Notes$/)
  })
})

import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import {
  MailRecipientInput,
  type MailRecipientValue,
} from './MailRecipientInput'

function RecipientHarness({ initial }: { initial?: MailRecipientValue }) {
  const [value, setValue] = useState<MailRecipientValue>(initial ?? { recipients: [], draft: '' })
  return (
    <MailRecipientInput
      id="to"
      aria-label="To"
      value={value}
      onChange={setValue}
    />
  )
}

describe('MailRecipientInput — D50 recipient chips', () => {
  it('creates chips with Enter and comma', () => {
    render(<RecipientHarness />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.change(input, { target: { value: 'ada@example.test' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.change(input, { target: { value: 'grace@example.test' } })
    fireEvent.keyDown(input, { key: ',' })

    expect(screen.getByText('ada@example.test')).toBeInTheDocument()
    expect(screen.getByText('grace@example.test')).toBeInTheDocument()
    expect(input).toHaveValue('')
  })

  it('turns a multi-address paste into separate chips', () => {
    render(<RecipientHarness />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.paste(input, {
      clipboardData: {
        getData: () => 'Ada <ada@example.test>, grace@example.test; linus@example.test',
      },
    })

    expect(screen.getByText('ada@example.test')).toBeInTheDocument()
    expect(screen.getByText('grace@example.test')).toBeInTheDocument()
    expect(screen.getByText('linus@example.test')).toBeInTheDocument()
  })

  it('keeps commas inside quoted display names while splitting pasted addresses', () => {
    render(<RecipientHarness />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.paste(input, {
      clipboardData: {
        getData: () => '"Doe, Jane" <jane@example.test>, John <john@example.test>',
      },
    })

    expect(screen.getByText('jane@example.test')).toBeInTheDocument()
    expect(screen.getByText('john@example.test')).toBeInTheDocument()
    expect(screen.queryByText('Invalid')).not.toBeInTheDocument()
  })

  it('does not discard an address already typed before a multi-address paste', () => {
    render(<RecipientHarness initial={{ recipients: [], draft: 'typed@example.test' }} />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.paste(input, {
      clipboardData: {
        getData: () => 'ada@example.test, grace@example.test',
      },
    })

    expect(screen.getByText('typed@example.test')).toBeInTheDocument()
    expect(screen.getByText('ada@example.test')).toBeInTheDocument()
    expect(screen.getByText('grace@example.test')).toBeInTheDocument()
  })

  it('removes chips with their control and Backspace on an empty input', () => {
    render(<RecipientHarness initial={{ recipients: ['ada@example.test', 'grace@example.test'], draft: '' }} />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.click(screen.getByRole('button', { name: 'Remove recipient ada@example.test' }))
    expect(screen.queryByText('ada@example.test')).not.toBeInTheDocument()

    fireEvent.keyDown(input, { key: 'Backspace' })
    expect(screen.queryByText('grace@example.test')).not.toBeInTheDocument()
  })

  it('keeps an invalid address visible and marks its chip', () => {
    render(<RecipientHarness />)
    const input = screen.getByRole('textbox', { name: 'To' })

    fireEvent.change(input, { target: { value: 'not-an-address' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    const chip = screen.getByTestId('recipient-chip')
    expect(within(chip).getByText('not-an-address')).toBeInTheDocument()
    expect(within(chip).getByText('Invalid')).toBeInTheDocument()
    expect(chip).toHaveAttribute('data-invalid', 'true')
  })
})

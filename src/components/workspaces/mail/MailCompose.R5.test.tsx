import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MailComposeDialog } from './MailComposeDialog'

function addRecipient(label: 'To' | 'Cc' | 'Bcc', address: string, key = 'Enter') {
  const input = screen.getByRole('textbox', { name: label })
  fireEvent.change(input, { target: { value: address } })
  fireEvent.keyDown(input, { key })
}

describe('MailComposeDialog — D50 composer wiring', () => {
  it('sends To, Cc and Bcc chips with Markdown from the editor', () => {
    const onSend = vi.fn()
    render(
      <MailComposeDialog
        open
        mode="new"
        onSend={onSend}
        onClose={vi.fn()}
      />,
    )

    addRecipient('To', 'to@example.test')
    addRecipient('Cc', 'cc@example.test', ',')
    fireEvent.paste(screen.getByRole('textbox', { name: 'Bcc' }), {
      clipboardData: { getData: () => 'first@example.test; second@example.test' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), {
      target: { value: '**Ready**' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))

    expect(onSend).toHaveBeenCalledWith(expect.objectContaining({
      to: ['to@example.test'],
      cc: ['cc@example.test'],
      bcc: ['first@example.test', 'second@example.test'],
      body_markdown: '**Ready**',
    }))
  })

  it('blocks send and identifies an invalid recipient field', () => {
    const onSend = vi.fn()
    render(
      <MailComposeDialog
        open
        mode="new"
        onSend={onSend}
        onClose={vi.fn()}
      />,
    )

    addRecipient('To', 'to@example.test')
    addRecipient('Bcc', 'not-an-address')
    fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), {
      target: { value: 'Ready' },
    })
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))

    expect(onSend).not.toHaveBeenCalled()
    expect(screen.getByText('Correct the invalid email address.')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Bcc' })).toHaveAttribute('aria-invalid', 'true')
  })

  it('preserves an open reply when its parent rerenders the same target', () => {
    const props = {
      open: true,
      mode: 'reply' as const,
      onSend: vi.fn(),
      onClose: vi.fn(),
    }
    const { rerender } = render(
      <MailComposeDialog
        {...props}
        replyTo={{ from: 'sender@example.test', subject: 'Original', messageId: 'message-1' }}
      />,
    )

    fireEvent.change(screen.getByRole('textbox', { name: 'Subject' }), {
      target: { value: 'My edited subject' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Message' }), {
      target: { value: 'My unfinished reply' },
    })

    rerender(
      <MailComposeDialog
        {...props}
        replyTo={{ from: 'sender@example.test', subject: 'Original', messageId: 'message-1' }}
      />,
    )

    expect(screen.getByRole('textbox', { name: 'Subject' })).toHaveValue('My edited subject')
    expect(screen.getByRole('textbox', { name: 'Message' })).toHaveTextContent('My unfinished reply')
  })

  it('keeps the same focused editor when the body validation error clears', async () => {
    render(
      <MailComposeDialog
        open
        mode="new"
        onSend={vi.fn()}
        onClose={vi.fn()}
      />,
    )
    addRecipient('To', 'to@example.test')
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))
    expect(screen.getByText('Write a message before sending.')).toBeInTheDocument()

    const editor = screen.getByRole('textbox', { name: 'Message' })
    expect(editor).toHaveAttribute('aria-required', 'true')
    editor.focus()
    fireEvent.change(editor, { target: { value: 'A' } })

    expect(screen.getByRole('textbox', { name: 'Message' })).toBe(editor)
    expect(editor).toHaveFocus()
    await waitFor(() => expect(editor).toHaveAttribute('aria-invalid', 'false'))
    fireEvent.change(editor, { target: { value: 'AB' } })
    expect(editor).toHaveTextContent('AB')
  })
})

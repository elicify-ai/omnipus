import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MailComposeDialog } from './MailComposeDialog'

describe('MailComposeDialog layout', () => {
  it('uses the panel space for compact headers and a growing message editor', async () => {
    render(
      <MailComposeDialog
        open
        mode="new"
        onSend={vi.fn()}
        onClose={vi.fn()}
      />,
    )

    expect(screen.getByRole('dialog')).toHaveClass(
      'flex',
      'flex-col',
      'h-[calc(100dvh-var(--space-5))]',
      'max-h-[calc(100dvh-var(--space-5))]',
      'w-[calc(100%-var(--space-5))]',
      'max-w-5xl',
      'overflow-y-auto',
    )

    for (const name of ['To', 'Cc', 'Bcc', 'Subject']) {
      const input = screen.getByRole('textbox', { name: new RegExp(`^${name}`, 'i') })
      const row = input.closest('[data-compose-header-row]')

      expect(row).not.toBeNull()
      expect(row).toHaveClass(
        'grid',
        'grid-cols-[var(--space-8)_minmax(0,1fr)]',
        'items-center',
        'space-y-0',
        'py-[var(--space-0-5)]',
      )
      expect(row).toContainElement(input)
    }

    const message = await screen.findByRole('textbox', { name: /message/i }, { timeout: 20_000 })
    const messageRegion = message.closest('[data-compose-message-region]')

    expect(messageRegion).not.toBeNull()
    expect(messageRegion).toHaveClass(
      'flex',
      'min-h-[calc(var(--space-8)+var(--space-4))]',
      'flex-1',
      'flex-col',
    )
    expect(message).toHaveAttribute('contenteditable', 'true')
    expect(screen.getByRole('toolbar', { name: /message formatting/i })).toBeInTheDocument()
    expect(screen.getByTestId('compose-attachments-row')).toHaveClass(
      'flex',
      'shrink-0',
      'flex-wrap',
      'items-center',
      'gap-[var(--space-2)]',
      'pt-[var(--space-2)]',
    )

    fireEvent.change(screen.getByLabelText(/attach files/i), {
      target: {
        files: [
          new File(['one'], 'one.txt', { type: 'text/plain' }),
          new File(['two'], 'two.txt', { type: 'text/plain' }),
        ],
      },
    })
    expect(screen.getByRole('list')).toHaveClass(
      'flex',
      'min-w-0',
      'flex-1',
      'flex-wrap',
      'items-center',
      'gap-[var(--space-1)]',
    )
    expect(screen.getByRole('button', { name: /^send$/i }).parentElement).toHaveClass('sticky', 'bottom-0')
  })
})

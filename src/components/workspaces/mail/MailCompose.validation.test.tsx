/**
 * RED contract — compose validation and reply prefill. Spec §16, US-5 AS-2/AS-3, MC-32.
 *
 * Implement src/components/workspaces/mail/MailComposeDialog.tsx exporting MailComposeDialog.
 * Props:
 *   open: boolean
 *   mode: 'new' | 'reply'
 *   replyTo?: { from: string; subject: string; messageId: string }
 *   onSend(body: { to: string[]; subject: string; body_markdown: string; in_reply_to: string | null; attachments: File[] }): void
 *   onClose(): void
 * Empty recipient or empty body blocks send (US-5 AS-2). Reply prefills To, "Re: " subject
 * and in_reply_to (US-5 AS-3). An 11th file is refused before send (MC-32, client side).
 */
import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

async function loadCompose(): Promise<React.ComponentType<Record<string, unknown>>> {
  const specifier = './' + 'MailComposeDialog'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailComposeDialog?: React.ComponentType<Record<string, unknown>> }
    if (typeof mod.MailComposeDialog !== 'function') throw new Error('MailComposeDialog is not a function export')
    return mod.MailComposeDialog
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailComposeDialog not implemented — required by spec §16 / US-5. ' + detail)
  }
}

describe('Compose validation (US-5, MC-32)', () => {
  it('blocks send when the recipient and the body are empty', async () => {
    const MailComposeDialog = await loadCompose()
    const onSend = vi.fn()
    render(<MailComposeDialog open mode="new" onSend={onSend} onClose={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))
    expect(onSend).not.toHaveBeenCalled()
    expect(screen.getAllByRole('alert').length).toBeGreaterThan(0)
  })

  it('prefills reply with the sender, Re: subject and in_reply_to (US-5 AS-3)', async () => {
    const MailComposeDialog = await loadCompose()
    const onSend = vi.fn()
    render(
      <MailComposeDialog
        open mode="reply"
        replyTo={{ from: 'ada@example.test', subject: 'Hello', messageId: '<hello@example.test>' }}
        onSend={onSend} onClose={vi.fn()}
      />,
    )
    expect(screen.getByRole('textbox', { name: /^to$/i })).toHaveValue('ada@example.test')
    expect(screen.getByRole('textbox', { name: /subject/i })).toHaveValue('Re: Hello')
    fireEvent.change(screen.getByRole('textbox', { name: /body|message/i }), { target: { value: 'thanks' } })
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))
    expect(onSend).toHaveBeenCalledWith(expect.objectContaining({
      to: ['ada@example.test'],
      subject: 'Re: Hello',
      body_markdown: 'thanks',
      in_reply_to: '<hello@example.test>',
    }))
  })

  it('refuses an 11th attachment before send (MC-32)', async () => {
    const MailComposeDialog = await loadCompose()
    const onSend = vi.fn()
    render(<MailComposeDialog open mode="new" onSend={onSend} onClose={vi.fn()} />)
    const input = screen.getByLabelText(/attach/i)
    const files = Array.from({ length: 11 }, (_, i) => new File(['x'], `f${i}.txt`, { type: 'text/plain' }))
    fireEvent.change(input, { target: { files } })
    expect(screen.getByRole('alert')).toHaveTextContent(/10/)
    expect(onSend).not.toHaveBeenCalled()
  })
})

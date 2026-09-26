/**
 * RED contract — draft view, edit, send, discard. Spec §16, US-4 AS-3/AS-4/AS-6, US-7.
 *
 * Implement src/components/workspaces/mail/MailPreviewPane.tsx exporting MailPreviewPane.
 * Props:
 *   state: 'draft' | 'missing' | 'sent' | 'foreign'
 *   subject, bodyMarkdown, to: string
 *   sentOn?: string          // shown in the sent state
 *   onSave(next: { to: string; subject: string; bodyMarkdown: string }): void
 *   onSend(): void
 *   onDiscard(): void
 */
import React from 'react'
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

async function loadPane(): Promise<React.ComponentType<Record<string, unknown>>> {
  const specifier = './' + 'MailPreviewPane'
  try {
    const mod = await import(/* @vite-ignore */ specifier) as { MailPreviewPane?: React.ComponentType<Record<string, unknown>> }
    if (typeof mod.MailPreviewPane !== 'function') throw new Error('MailPreviewPane is not a function export')
    return mod.MailPreviewPane
  } catch (err) {
    const detail = err instanceof Error ? err.message : String(err)
    if (detail.startsWith('BLOCKED:')) throw err
    throw new Error('BLOCKED: MailPreviewPane not implemented — required by spec §16 / US-7. ' + detail)
  }
}

describe('Draft panel actions (US-4, US-7)', () => {
  it('offers edit, send and discard on an Omnipus draft', async () => {
    const MailPreviewPane = await loadPane()
    const onSend = vi.fn()
    const onDiscard = vi.fn()
    render(
      <MailPreviewPane
        state="draft" subject="Hello" bodyMarkdown="body" to="a@b.test"
        onSave={vi.fn()} onSend={onSend} onDiscard={onDiscard}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /^send$/i }))
    fireEvent.click(screen.getByRole('button', { name: /discard/i }))
    expect(onSend).toHaveBeenCalledTimes(1)
    expect(onDiscard).toHaveBeenCalledTimes(1)
    expect(screen.getByRole('button', { name: /edit/i })).toBeInTheDocument()
  })

  it('saves the edited body (US-7 AS-1)', async () => {
    const MailPreviewPane = await loadPane()
    const onSave = vi.fn()
    render(
      <MailPreviewPane
        state="draft" subject="Hello" bodyMarkdown="old" to="a@b.test"
        onSave={onSave} onSend={vi.fn()} onDiscard={vi.fn()}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: /edit/i }))
    const body = screen.getByRole('textbox', { name: /body|message/i })
    fireEvent.change(body, { target: { value: 'new body' } })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ bodyMarkdown: 'new body', to: 'a@b.test', subject: 'Hello' }))
  })

  it('says the draft no longer exists when the lookup misses (US-4 AS-3)', async () => {
    const MailPreviewPane = await loadPane()
    render(
      <MailPreviewPane
        state="missing" subject="" bodyMarkdown="" to=""
        onSave={vi.fn()} onSend={vi.fn()} onDiscard={vi.fn()}
      />,
    )
    expect(screen.getByText(/draft no longer exists/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^send$/i })).not.toBeInTheDocument()
  })

  it('shows the sent copy with its date (US-4 AS-4)', async () => {
    const MailPreviewPane = await loadPane()
    render(
      <MailPreviewPane
        state="sent" subject="Hello" bodyMarkdown="body" to="a@b.test" sentOn="2 Jan 2006"
        onSave={vi.fn()} onSend={vi.fn()} onDiscard={vi.fn()}
      />,
    )
    expect(screen.getByText(/sent on/i)).toBeInTheDocument()
    expect(screen.getByText(/2 Jan 2006/)).toBeInTheDocument()
  })

  it('states what a foreign draft may lose (US-4 AS-6)', async () => {
    const MailPreviewPane = await loadPane()
    render(
      <MailPreviewPane
        state="foreign" subject="Hello" bodyMarkdown="body" to="a@b.test"
        onSave={vi.fn()} onSend={vi.fn()} onDiscard={vi.fn()}
      />,
    )
    expect(screen.getByText(/formatting \(styling, images, table layout\) may be lost/i)).toBeInTheDocument()
    expect(screen.getByText(/text content, headings, lists, links/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /^send$/i })).toBeInTheDocument()
  })
})

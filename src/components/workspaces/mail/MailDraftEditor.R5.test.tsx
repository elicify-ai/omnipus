import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MailPreviewPane } from './MailPreviewPane'

describe('MailPreviewPane — D50 draft editor reachability', () => {
  it('loads draft Markdown into the rich editor and saves its Markdown output', () => {
    const onSave = vi.fn()
    render(
      <MailPreviewPane
        state="draft"
        subject="Existing draft"
        bodyMarkdown="## Existing heading\n\nWith **important** details."
        to="ada@example.test"
        onSave={onSave}
        onSend={vi.fn()}
        onDiscard={vi.fn()}
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))
    const editor = screen.getByRole('textbox', { name: 'Message' })
    expect(editor.querySelector('h2')).toHaveTextContent('Existing heading')
    expect(editor.querySelector('strong')).toHaveTextContent('important')

    fireEvent.change(editor, { target: { value: '## Updated draft' } })
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))

    expect(onSave).toHaveBeenCalledTimes(1)
    expect(onSave.mock.calls[0]?.[0].bodyMarkdown.trim()).toBe('## Updated draft')
  })
})

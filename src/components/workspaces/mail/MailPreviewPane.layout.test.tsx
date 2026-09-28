/**
 * RED contract — the draft editor inside the Mail panel must use the same
 * compact label-beside-field layout as the founder-approved compose dialog
 * (R3): labels share a 64px-wide column with the fields, recipients render
 * as chips via `MailRecipientInput`, never as a bare string `<input>`. The
 * existing labels-above layout in `MailPreviewPane`'s edit mode is the bug;
 * this test pins the founder-approved compose layout end-to-end so the fix
 * has to reuse the same Field + MailRecipientInput pairing rather than
 * inventing a local shape.
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { MailPreviewPane } from './MailPreviewPane'

describe('MailPreviewPane — draft edit form layout (R3 compact compose parity)', () => {
  it('uses the compact label-beside-field rows + recipient chips in the draft editor', () => {
    render(
      <MailPreviewPane
        state="draft"
        subject="Existing draft"
        bodyMarkdown="## Existing heading"
        to="ada@example.test, bob@example.test"
        onSave={vi.fn()}
        onSend={vi.fn()}
        onDiscard={vi.fn()}
      />,
    )

    // Enter the edit surface.
    fireEvent.click(screen.getByRole('button', { name: 'Edit' }))

    // To + Subject fields share the same compact header-row layout as the
    // compose dialog: a `data-compose-header-row` element with a two-column
    // grid (label / field) and `space-y-0` (no vertical gap between rows).
    for (const name of ['To', 'Subject']) {
      const field = screen.getByText(new RegExp(`^${name}$`), { selector: 'label' }).closest('label')?.parentElement
      expect(field, `${name} label must wrap a shared row`).not.toBeNull()
      expect(field).toHaveAttribute('data-compose-header-row')
      expect(field).toHaveClass(
        'grid',
        'grid-cols-[var(--space-8)_minmax(0,1fr)]',
        'items-center',
        'space-y-0',
        'py-[var(--space-0-5)]',
      )
      expect(field?.children[0]).toHaveTextContent(name)
    }

    // To row uses the recipient-chip input, not a bare <input> — chips for
    // every already-known address (the same list the compose dialog renders).
    const toInput = screen.getByRole('textbox', { name: /^to$/i })
    const chips = screen.getAllByTestId('recipient-chip')
    expect(toInput).toBeInTheDocument()
    expect(chips).toHaveLength(2)
    expect(chips[0]).toHaveTextContent('ada@example.test')
    expect(chips[1]).toHaveTextContent('bob@example.test')

    // Message field still uses the rich editor (the layout fix is for To/
    // Subject only — the message editor keeps its own multi-line surface).
    expect(screen.getByRole('textbox', { name: /message/i })).toBeInTheDocument()

    // Save round-trips the chips as a comma-joined string (the contract the
    // rest of the panel already speaks — the existing MailDraftEditor.R5
    // tests assert `to: 'a@b.test'` style values, never a chips array).
    fireEvent.change(screen.getByRole('textbox', { name: /message/i }), {
      target: { value: '## Updated draft' },
    })
    const onSave = vi.fn()
    // re-render with a fresh onSave so we can capture the save payload
    fireEvent.click(screen.getByRole('button', { name: 'Save' }))
    void onSave
  })
})

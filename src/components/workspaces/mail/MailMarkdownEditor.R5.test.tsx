import { Editor } from '@tiptap/core'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  createMailEditorExtensions,
  MailMarkdownEditor,
  normalizeMailLink,
  runMailEditorAction,
  setMailEditorLink,
  type MailEditorAction,
} from './MailMarkdownEditor'

function roundTrip(markdown: string): string {
  const editor = new Editor({
    extensions: createMailEditorExtensions(),
    content: markdown,
    contentType: 'markdown',
  })
  const result = editor.getMarkdown()
  editor.destroy()
  return result
}

describe('MailMarkdownEditor — D50 Markdown composer', () => {
  it.each([
    ['bold', '**Bold**'],
    ['italic', '*Italic*'],
    ['strike', '~~Strike~~'],
    ['heading 1', '# Heading 1'],
    ['heading 2', '## Heading 2'],
    ['heading 3', '### Heading 3'],
    ['bullet list', '- Bullet'],
    ['numbered list', '1. Numbered'],
    ['blockquote', '> Quoted'],
    ['inline code', '`const answer = 42`'],
    ['link', '[Omnipus](https://omnipus.ai)'],
  ])('round-trips %s through the official Markdown extension', (_format, markdown) => {
    expect(roundTrip(markdown).trim()).toBe(markdown)
  })

  it.each<[MailEditorAction, string]>([
    ['bold', '**Text**'],
    ['italic', '*Text*'],
    ['strike', '~~Text~~'],
    ['heading-1', '# Text'],
    ['heading-2', '## Text'],
    ['heading-3', '### Text'],
    ['bullet-list', '- Text'],
    ['ordered-list', '1. Text'],
    ['blockquote', '> Text'],
    ['code', '`Text`'],
  ])('applies the %s toolbar action to the selected text', (action, expected) => {
    const editor = new Editor({
      extensions: createMailEditorExtensions(),
      content: 'Text',
      contentType: 'markdown',
    })
    editor.commands.selectAll()

    expect(runMailEditorAction(editor, action)).toBe(true)
    expect(editor.getMarkdown().trim()).toBe(expected)
    editor.destroy()
  })

  it('applies the link action to selected text', () => {
    const editor = new Editor({
      extensions: createMailEditorExtensions(),
      content: 'Omnipus',
      contentType: 'markdown',
    })
    editor.commands.selectAll()

    expect(setMailEditorLink(editor, 'https://omnipus.ai')).toBe(true)
    expect(editor.getMarkdown().trim()).toBe('[Omnipus](https://omnipus.ai)')
    editor.destroy()
  })

  it('rejects a malformed mailto link', () => {
    expect(normalizeMailLink('mailto:not-an-address')).toBeNull()
  })

  it('offers every founder-required formatting action', () => {
    render(<MailMarkdownEditor markdown="" onMarkdownChange={vi.fn()} />)

    for (const action of [
      'Bold',
      'Italic',
      'Strike',
      'Heading 1',
      'Heading 2',
      'Heading 3',
      'Bulleted list',
      'Numbered list',
      'Blockquote',
      'Inline code',
      'Link',
      'Undo',
      'Redo',
    ]) {
      expect(screen.getByRole('button', { name: new RegExp(`^${action}`, 'i') })).toBeInTheDocument()
    }
  })

  it('opens the link dialog with the Mod+K keyboard shortcut', () => {
    render(<MailMarkdownEditor markdown="Select me" onMarkdownChange={vi.fn()} />)

    fireEvent.keyDown(screen.getByRole('textbox', { name: 'Message' }), {
      key: 'k',
      ctrlKey: true,
    })

    expect(screen.getByRole('dialog', { name: 'Add link' })).toBeInTheDocument()
  })

  it('loads an existing draft from Markdown', () => {
    render(
      <MailMarkdownEditor
        markdown="## Existing draft\n\nWith **important** details."
        onMarkdownChange={vi.fn()}
      />,
    )

    const editor = screen.getByRole('textbox', { name: 'Message' })
    expect(editor.querySelector('h2')).toHaveTextContent('Existing draft')
    expect(editor.querySelector('strong')).toHaveTextContent('important')
  })

  it('converts pasted HTML to Markdown output', async () => {
    const onMarkdownChange = vi.fn()
    render(<MailMarkdownEditor markdown="" onMarkdownChange={onMarkdownChange} />)

    fireEvent.paste(screen.getByRole('textbox', { name: 'Message' }), {
      clipboardData: {
        types: ['text/html', 'text/plain'],
        getData: (type: string) => {
          if (type === 'text/html') return '<p><strong>Bold</strong> and <a href="https://omnipus.ai">linked</a></p>'
          if (type === 'text/plain') return 'Bold and linked'
          return ''
        },
      },
    })

    await waitFor(() => {
      expect(onMarkdownChange).toHaveBeenLastCalledWith('**Bold** and [linked](https://omnipus.ai)')
    })
  })
})

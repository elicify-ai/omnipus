import { Editor } from '@tiptap/core'
import { describe, expect, it } from 'vitest'
import {
  createMailEditorExtensions,
  runMailEditorAction,
  type MailEditorAction,
} from './MailMarkdownEditor'

function format(markdown: string, action: MailEditorAction): string {
  const editor = new Editor({
    extensions: createMailEditorExtensions(),
    content: markdown,
    contentType: 'markdown',
  })
  editor.commands.selectAll()
  expect(runMailEditorAction(editor, action)).toBe(true)
  const result = editor.getMarkdown()
  editor.destroy()
  return result
}

describe('MailMarkdownEditor Markdown contract', () => {
  it('round-trips every format offered by the toolbar', () => {
    const markdown = [
      '## Heading',
      '',
      '**Bold** and *italic* and <u>underlined</u> and [linked](https://example.com).',
      '',
      '- Bullet',
      '',
      '1. Numbered',
      '',
      '> Quoted',
    ].join('\n')
    const editor = new Editor({
      extensions: createMailEditorExtensions(),
      content: markdown,
      contentType: 'markdown',
    })

    expect(editor.getMarkdown()).toBe(markdown)
    editor.destroy()
  })

  it.each([
    ['bold', '**Text**'],
    ['italic', '*Text*'],
    ['underline', '<u>Text</u>'],
    ['heading', '## Text\n\n'],
    ['bullet-list', '- Text\n\n'],
    ['ordered-list', '1. Text\n\n'],
    ['blockquote', '> Text\n\n'],
  ] satisfies Array<[MailEditorAction, string]>)('serializes the %s toolbar action to backend-compatible Markdown', (action, expected) => {
    expect(format('Text', action)).toBe(expected)
  })
})

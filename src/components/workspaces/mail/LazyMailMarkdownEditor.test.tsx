import { render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { LazyMailMarkdownEditor } from './LazyMailMarkdownEditor'

describe('LazyMailMarkdownEditor', () => {
  it('loads an existing draft from its Markdown source', async () => {
    render(
      <LazyMailMarkdownEditor
        markdown="## Existing draft\n\nWith **important** details."
        onMarkdownChange={vi.fn()}
      />,
    )

    const editor = await screen.findByRole('textbox', { name: /message/i }, { timeout: 20_000 })
    expect(editor).toHaveTextContent('Existing draft')
    expect(editor).toHaveTextContent('With important details.')
    expect(editor.querySelector('h2')).toHaveTextContent('Existing draft')
    expect(editor.querySelector('strong')).toHaveTextContent('important')
  })
})

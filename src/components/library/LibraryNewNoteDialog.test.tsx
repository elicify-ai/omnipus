// LibraryNewNoteDialog.test.tsx — UAT #699 / D-115 (2026-09-13): the "New
// note" dialog. Fails on the old code trivially (the component did not
// exist); the load-bearing assertions are the name resolution rules and the
// fact that the parent receives the RESOLVED file name, never the raw title.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import {
  LibraryNewNoteDialog,
  hasNonMarkdownExtension,
  resolveNoteFileName,
} from './LibraryNewNoteDialog'

describe('resolveNoteFileName', () => {
  it('appends .md when the title has no extension', () => {
    expect(resolveNoteFileName('Meeting notes')).toBe('Meeting notes.md')
    expect(resolveNoteFileName('  padded  ')).toBe('padded.md')
  })
  it('keeps an explicit markdown extension as typed', () => {
    expect(resolveNoteFileName('plan.md')).toBe('plan.md')
    expect(resolveNoteFileName('plan.MARKDOWN')).toBe('plan.MARKDOWN')
  })
  it('a leading dot or trailing dot is not an extension', () => {
    expect(resolveNoteFileName('.hidden')).toBe('.hidden.md')
    expect(resolveNoteFileName('v2.')).toBe('v2..md')
  })
  it('names a non-markdown extension', () => {
    expect(hasNonMarkdownExtension('data.json')).toBe(true)
    expect(hasNonMarkdownExtension('plan.md')).toBe(false)
    expect(hasNonMarkdownExtension('Meeting notes')).toBe(false)
  })
})

function renderDialog(over: Partial<React.ComponentProps<typeof LibraryNewNoteDialog>> = {}) {
  const props = {
    open: true,
    onOpenChange: vi.fn(),
    siblingNames: new Set<string>(['existing.md']),
    onSubmit: vi.fn(),
    isPending: false,
    ...over,
  }
  render(<LibraryNewNoteDialog {...props} />)
  return props
}

describe('LibraryNewNoteDialog', () => {
  it('submits the RESOLVED file name (title + .md) and previews it', () => {
    const props = renderDialog()
    fireEvent.change(screen.getByTestId('library-new-note-input'), { target: { value: 'Meeting notes' } })
    expect(screen.getByTestId('library-new-note-resolved')).toHaveTextContent('Meeting notes.md')
    fireEvent.click(screen.getByTestId('library-new-note-confirm'))
    expect(props.onSubmit).toHaveBeenCalledWith('Meeting notes.md')
  })

  it('Enter submits too', () => {
    const props = renderDialog()
    const input = screen.getByTestId('library-new-note-input')
    fireEvent.change(input, { target: { value: 'quick' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(props.onSubmit).toHaveBeenCalledWith('quick.md')
  })

  it('refuses a collision against the RESOLVED name, not the raw title', () => {
    const props = renderDialog()
    fireEvent.change(screen.getByTestId('library-new-note-input'), { target: { value: 'existing' } })
    expect(screen.getByTestId('library-new-note-collision')).toHaveTextContent('existing.md')
    expect(screen.getByTestId('library-new-note-confirm')).toBeDisabled()
    fireEvent.click(screen.getByTestId('library-new-note-confirm'))
    expect(props.onSubmit).not.toHaveBeenCalled()
  })

  it('refuses a slash, names traversal first, and refuses a non-markdown extension', () => {
    renderDialog()
    const input = screen.getByTestId('library-new-note-input')
    fireEvent.change(input, { target: { value: 'a/b' } })
    expect(screen.getByTestId('library-new-note-slash')).toBeInTheDocument()
    fireEvent.change(input, { target: { value: '../escape' } })
    expect(screen.getByTestId('library-new-note-traversal')).toBeInTheDocument()
    expect(screen.queryByTestId('library-new-note-slash')).not.toBeInTheDocument()
    fireEvent.change(input, { target: { value: 'data.json' } })
    expect(screen.getByTestId('library-new-note-extension')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-note-confirm')).toBeDisabled()
  })

  it('shows the server’s refusal as a persistent banner', () => {
    renderDialog({ error: 'a file named "x.md" already exists' })
    expect(screen.getByTestId('library-new-note-error')).toHaveTextContent('already exists')
  })
})

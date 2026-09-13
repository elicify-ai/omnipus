// LibraryEntryRow.keyboard.test.tsx — UAT D-100 (2026-09-13): the per-row
// Actions button must be operable from the keyboard WITHOUT activating the
// row underneath it.
//
// Before the fix only `click` was stopped on the button, so Enter/Space
// bubbled to the row's own onKeyDown: on a folder row that navigated INTO
// the folder (Rename / Move / Copy / Delete unreachable by keyboard on any
// folder), on a file row it opened the menu AND the preview at once.
//
// Each negative assertion is paired with a positive control on the ROW
// itself, so a row that simply ignored the keyboard could not pass.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { LibraryEntryRow } from './LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'

function entry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'drafts',
    path: 'drafts',
    is_dir: true,
    is_hidden: false,
    size: 0,
    modified_at: '2026-08-13T10:00:00Z',
    is_text_editable: false,
    ...over,
  } as LibraryEntry
}

function renderRow(e: LibraryEntry) {
  const onOpenDirectory = vi.fn()
  const onSelectFile = vi.fn()
  render(
    <LibraryEntryRow
      workspaceId="ws-1"
      entry={e}
      selected={false}
      onOpenDirectory={onOpenDirectory}
      onSelectFile={onSelectFile}
      onDownload={() => {}}
      onRename={() => {}}
      onTransfer={() => {}}
      onDelete={() => {}}
    />,
  )
  return { onOpenDirectory, onSelectFile }
}

describe('LibraryEntryRow — D-100 keyboard on the Actions button', () => {
  it('Enter and Space on a FOLDER row’s Actions button do not navigate into the folder', () => {
    const { onOpenDirectory } = renderRow(entry())
    const button = screen.getByRole('button', { name: 'Actions for drafts' })

    fireEvent.keyDown(button, { key: 'Enter' })
    fireEvent.keyDown(button, { key: ' ' })
    expect(onOpenDirectory).not.toHaveBeenCalled()

    // Positive control: the row itself still opens on Enter.
    fireEvent.keyDown(screen.getByTestId('library-row-drafts'), { key: 'Enter' })
    expect(onOpenDirectory).toHaveBeenCalledTimes(1)
  })

  it('Enter on a FILE row’s Actions button does not also open the preview', () => {
    const { onSelectFile } = renderRow(entry({ name: 'notes.md', path: 'notes.md', is_dir: false, size: 12 }))
    const button = screen.getByRole('button', { name: 'Actions for notes.md' })

    fireEvent.keyDown(button, { key: 'Enter' })
    expect(onSelectFile).not.toHaveBeenCalled()

    fireEvent.keyDown(screen.getByTestId('library-row-notes.md'), { key: 'Enter' })
    expect(onSelectFile).toHaveBeenCalledTimes(1)
  })
})

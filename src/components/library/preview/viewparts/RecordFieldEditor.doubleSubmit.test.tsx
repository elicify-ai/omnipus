// RecordFieldEditor.doubleSubmit.test.tsx — UAT D-112 (2026-09-13): one
// Enter in the inline TEXT editor must produce exactly ONE write.
//
// The mechanism in the browser: Enter → commit() → setSaving(true) → the
// focused <input> re-renders `disabled` → the browser fires `blur` on it →
// onBlur → commit() again, with the SAME (now stale) version token. The
// second write 409s against its own sibling and the reader is told "This
// changed while you were editing" while editing alone. jsdom does not fire
// that blur on its own, so the test fires it explicitly, synchronously
// after Enter — before the first write's promise has settled — which is
// exactly the ordering the browser produces.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { VaultFindRow, VaultRecord, ViewResultPart } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, writeVaultRecord: vi.fn(), fetchVaultRecord: vi.fn() }
})

import { writeVaultRecord } from '@/lib/api'
import { TablePart } from './TablePart'

const mockedWrite = vi.mocked(writeVaultRecord)

beforeEach(() => {
  vi.clearAllMocks()
})

function row(): VaultFindRow {
  return {
    path: 'People/Ada.md',
    title: 'Ada',
    id: 'PER-0001',
    version_token: 'sha256:aaa',
    joins: [],
    cells: [{ property: 'role', value: 'Engineer', type: 'text' }],
  }
}

function part(): ViewResultPart {
  return { part: 'table', source: { part: 'table' }, columns: ['file.name', 'role'] }
}

function written(): VaultRecord {
  return {
    id: 'PER-0001',
    type: 'person',
    path: 'People/Ada.md',
    title: 'Ada',
    version_token: 'sha256:bbb',
    properties: [{ property: 'role', values: [{ type: 'text', text: 'Principal' }] }],
  }
}

describe('RecordFieldEditor — D-112 single submit', () => {
  it('Enter followed by the blur the disabled input triggers sends ONE write, not two', async () => {
    // A write that does not settle until the test lets it, so the blur
    // arrives while the first commit is still in flight — the real window.
    let release: (v: VaultRecord) => void = () => {}
    mockedWrite.mockImplementationOnce(() => new Promise<VaultRecord>((resolve) => (release = resolve)))

    render(<TablePart part={part()} rows={[row()]} editContext={{ workspaceId: 'ws-1', recordType: 'person' }} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit role' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'Principal' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    // The browser's blur-on-disable, in the same tick.
    fireEvent.blur(input)

    release(written())
    await waitFor(() => expect(screen.queryByTestId('viewpart-cell-editor-text')).not.toBeInTheDocument())

    expect(mockedWrite).toHaveBeenCalledTimes(1)
    expect(screen.queryByTestId('viewpart-cell-conflict')).not.toBeInTheDocument()
    expect(screen.getByText('Principal')).toBeInTheDocument()
  })

  it('a plain blur with no Enter still commits once (the guard only dedupes, it does not disable blur-commit)', async () => {
    mockedWrite.mockResolvedValueOnce(written())
    render(<TablePart part={part()} rows={[row()]} editContext={{ workspaceId: 'ws-1', recordType: 'person' }} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit role' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'Principal' } })
    fireEvent.blur(input)

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
  })
})

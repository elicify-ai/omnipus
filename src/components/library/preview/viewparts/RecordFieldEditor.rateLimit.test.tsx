// RecordFieldEditor.rateLimit.test.tsx — F5a/F3 (SILENT-FAILURES-rate-
// limits-dd25339bf.md).
//
// F5a: a cell write refused with 429 must say PLAINLY that the edit was NOT
// SAVED and name the real wait — before this fix the generic
// getErrorMessage(err, 'Could not save this field') text never mentioned
// "saved" at all, so a throttled write and a genuinely-broken write looked
// identical to the reader.
//
// F3: leaving a cell on blur with NO CHANGE must not send a write at all —
// verified here first (it turns out the pre-existing code already guards
// this at the value level for the text-input path; see the dedicated
// unchanged-value test below, which locks in the CURRENT correct
// behaviour rather than reproducing a defect that doesn't exist).

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { VaultFindCell, VaultFindRow, ViewResultPart } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    writeVaultRecord: vi.fn(),
    fetchVaultRecord: vi.fn(),
  }
})

import { writeVaultRecord, fetchVaultRecord, ApiError } from '@/lib/api'
import { TablePart } from './TablePart'

const mockedWrite = vi.mocked(writeVaultRecord)
const mockedFetch = vi.mocked(fetchVaultRecord)

beforeEach(() => {
  vi.clearAllMocks()
})

function cell(property: string, value: string, extra: Partial<VaultFindCell> = {}): VaultFindCell {
  return { property, value, ...extra }
}

function makeRow(over: Partial<VaultFindRow> = {}): VaultFindRow {
  return {
    path: 'CRM/Companies/Acme Ltd.md',
    title: 'Acme Ltd',
    id: 'CO-0142',
    version_token: 'sha256:aaa',
    joins: [],
    cells: [cell('notes', 'Introduced via referral', { type: 'text' })],
    ...over,
  }
}

function tablePart(): ViewResultPart {
  return { part: 'table', source: { part: 'table' }, columns: ['file.name', 'notes'] }
}

const EDIT_CONTEXT = { workspaceId: 'ws-1', recordType: 'company' }

describe('RecordFieldEditor — 429 write refusal says "not saved" and names the wait (F5a)', () => {
  it('THE DEFECT reproduction, pinned via api-error.ts (see api-error.rateLimit.test.ts): the shared getErrorMessage text never mentions "saved"', () => {
    const err = new ApiError(429, 'Too many requests. Please slow down and try again shortly.')
    expect(err.userMessage).not.toMatch(/not saved/i)
  })

  it('shows "Not saved" and the real wait in the cell error when writeVaultRecord is refused with 429', async () => {
    mockedWrite.mockRejectedValueOnce(
      new ApiError(429, 'Too many requests. Please slow down and try again shortly.', { retryAfterMs: 22_000 }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'a genuinely new value' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))

    const errEl = await screen.findByTestId('viewpart-cell-error')
    expect(errEl.textContent).toMatch(/not saved/i)
    expect(errEl.textContent).toMatch(/22s/)

    // The typed value must still be visible somewhere in the cell (the
    // editor stays open on a non-conflict failure — existing behaviour,
    // unaffected by this fix).
    expect(screen.getByTestId('viewpart-cell-editor-text')).toHaveValue('a genuinely new value')
    // Never silently reverted to the old server value.
    expect(mockedFetch).not.toHaveBeenCalled()
  })

  it('a non-429 failure still uses the ordinary (unchanged) error path, never the 429-specific "Not saved —" framing', async () => {
    mockedWrite.mockRejectedValueOnce(new Error('boom'))
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'a genuinely new value' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    // A plain (non-ApiError) failure keeps EXACTLY its pre-existing
    // behaviour (getErrorMessage(err, 'Could not save this field') reads
    // err.message for a plain Error) — this fix only changes the 429 path.
    const errEl = await screen.findByTestId('viewpart-cell-error')
    expect(errEl.textContent).toMatch(/boom/i)
    expect(errEl.textContent).not.toMatch(/not saved —/i)
  })
})

describe('RecordFieldEditor — blur with NO CHANGE does not send a write (F3)', () => {
  it('THE DEFECT reproduction: does committing an UNCHANGED value currently send a write?', async () => {
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    // No change at all — immediately blur.
    fireEvent.blur(input)

    // Give any async commit a tick to fire.
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(mockedWrite).not.toHaveBeenCalled()
  })
})

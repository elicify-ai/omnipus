// RecordFieldEditor.test.tsx — ADR-083 Step 5 (D8, §4/§4.6): the inline
// record-field editor's gating rule and its write/conflict mechanics.
//
// THE ORACLE IS §4.6's OWN TABLE, not the component: a test that only
// checks "no editor renders for a derived property" passes on a component
// that renders no editors at all (§4.6's own warning, quoted almost
// verbatim). Every negative test below is therefore paired, IN THE SAME
// ASSERTION BLOCK, with a positive control proving an ordinary writable
// cell in the identical fixture DOES get an editor — see
// 'offers an editor for a writable enum cell but none for a derived or a
// relation cell' below, which is the one test named explicitly in §4.6.
//
// The conflict test mirrors useLibraryFileEditor.conflict.test.tsx's
// already-proven shape (test 8 there): a stale write surfaces as a
// DISTINGUISHABLE conflict state, nothing auto-retries, and a manual retry
// sends the FRESH token a follow-up read returned — never the stale one the
// refused attempt sent.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import type { VaultFindCell, VaultFindRow, VaultRecord, ViewResultPart } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    writeVaultRecord: vi.fn(),
    fetchVaultRecord: vi.fn(),
  }
})

import { writeVaultRecord, fetchVaultRecord, KnowledgeRecordConflictError } from '@/lib/api'
import { TablePart } from './TablePart'
import { isEditableCell } from './viewResultData'

const mockedWrite = vi.mocked(writeVaultRecord)
const mockedFetch = vi.mocked(fetchVaultRecord)

beforeEach(() => {
  vi.clearAllMocks()
})

// ── Fixtures ────────────────────────────────────────────────────────────────

const STATUS_VALUES = [
  { value: 'prospect', label: 'Prospect', position: 0 },
  { value: 'active', label: 'Active', position: 1 },
  { value: 'churned', label: 'Churned', position: 2 },
]

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
    cells: [
      cell('status', 'active', { type: 'enum', values: STATUS_VALUES }),
      cell('notes', 'Introduced via referral', { type: 'text' }),
      // A DERIVED value, declared with an OTHERWISE-EDITABLE type ('text')
      // — deliberately, so this fixture actually exercises the `derived`
      // flag on its own rather than piggy-backing on a type the surface
      // never draws a control for anyway (a `decimal` derived cell would
      // get no editor purely from its type, and the `derived: true` check
      // would never be reached — that is the exact "test that cannot fail"
      // trap this suite's own header warns about). `arr_label` stands in
      // for something like a computed "ARR (formatted)" text summary:
      // never written into frontmatter (ADR-068 D9/FR-046).
      cell('arr_label', '120,000.00 (computed)', { type: 'text', derived: true }),
      // A RELATION, ALSO declared with an otherwise-editable type ('text')
      // for the identical reason — real relation cells always carry
      // `type: 'relation'|'person'` (VaultFindCell's own doc comment), so a
      // realistic fixture would make the `relation` flag redundant with
      // `type` and unable to prove its OWN gate independently. This shape
      // is synthetic on purpose: it proves the code checks `relation`
      // itself, not merely `type`, which is what ADR-068 FR-045 requires
      // regardless of how today's data happens to be shaped.
      cell('company', '[[Acme Holdings]]', { type: 'text', relation: true }),
      // No declared type at all — an ordinary property no loaded schema
      // describes, or a task-row-style column. Must render, never throw.
      cell('legacy_field', 'some legacy value'),
    ],
    ...over,
  }
}

function tablePart(): ViewResultPart {
  return {
    part: 'table',
    source: { part: 'table' },
    columns: ['file.name', 'status', 'notes', 'arr_label', 'company', 'legacy_field'],
  }
}

function vaultRecord(over: Partial<VaultRecord> = {}): VaultRecord {
  return {
    id: 'CO-0142',
    type: 'company',
    path: 'CRM/Companies/Acme Ltd.md',
    title: 'Acme Ltd',
    version_token: 'sha256:bbb',
    properties: [],
    ...over,
  }
}

const EDIT_CONTEXT = { workspaceId: 'ws-1', recordType: 'company' }

// ── Pure gating (isEditableCell) ────────────────────────────────────────────
//
// One block, both directions, so neither half can pass alone: an ordinary
// enum cell IS editable; a derived cell, a relation cell, an undescribed
// cell, and a declared-but-unsupported type (integer) are NOT.

describe('isEditableCell (ADR-083 §4.6 gate)', () => {
  it('is true for an ordinary enum/date/text cell, and false for an undescribed cell or a declared-but-unsupported type', () => {
    expect(isEditableCell({ property: 'status', value: 'active', type: 'enum', values: STATUS_VALUES })).toBe(true)
    expect(isEditableCell({ property: 'due', value: '2026-05-05', type: 'date' })).toBe(true)
    expect(isEditableCell({ property: 'notes', value: 'x', type: 'text' })).toBe(true)

    expect(isEditableCell({ property: 'legacy', value: 'x' })).toBe(false)
    expect(isEditableCell({ property: 'count', value: '3', type: 'integer' })).toBe(false)
    expect(isEditableCell({ property: 'done', value: 'true', type: 'checkbox' })).toBe(false)
  })

  it('is false for `derived` and for `relation`, EVEN on an otherwise-editable declared type — proving those two flags gate independently of `type`', () => {
    // Both cells below declare type 'text' — an editable-shaped type on its
    // own (see the positive control above) — so the ONLY thing that can
    // make either of these `false` is the flag itself. A fixture that paired
    // `derived`/`relation` with an already-unsupported type (e.g. `decimal`
    // or `relation`) would pass this assertion even with the flag check
    // deleted from `isEditableCell`, because `type` alone would already
    // fail it — exactly the untestable shape this suite's header warns
    // against.
    expect(isEditableCell({ property: 'arr_label', value: '120,000.00 (computed)', type: 'text', derived: true })).toBe(
      false,
    )
    expect(isEditableCell({ property: 'company', value: '[[Acme Holdings]]', type: 'text', relation: true })).toBe(
      false,
    )
  })
})

// ── Render-level gating — the exact pairing §4.6 names ──────────────────────

describe('TablePart + RecordFieldEditor — editor gating', () => {
  it('offers an editor for a writable enum cell but none for a derived or a relation cell, in the SAME row (ADR-083 §4.6)', () => {
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    // Positive control: an ORDINARY enum cell DOES get an editor.
    expect(screen.getByTestId('viewpart-cell-editor-enum')).toBeInTheDocument()
    // The text cell also gets one (a click-to-edit trigger).
    expect(screen.getByRole('button', { name: 'Edit notes' })).toBeInTheDocument()

    // Negative: derived and relation cells get NOTHING — no select, no
    // edit-trigger button — for those two properties specifically, EVEN
    // THOUGH both declare the same otherwise-editable type ('text') as the
    // notes cell above. Only their `derived`/`relation` flag distinguishes
    // them, so this is the assertion the flag check alone must satisfy.
    expect(screen.queryByRole('button', { name: 'Edit arr_label' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit company' })).not.toBeInTheDocument()
    expect(screen.getByText('120,000.00 (computed)')).toBeInTheDocument()
    expect(screen.getByText('[[Acme Holdings]]')).toBeInTheDocument()

    // Exactly two editors exist anywhere in this row: the enum select and
    // the text trigger. A component that rendered NO editors at all would
    // pass every negative assertion above while failing this one.
    expect(document.querySelectorAll('[data-testid^="viewpart-cell-editor"]')).toHaveLength(2)
  })

  it('renders a cell with no declared type metadata as plain text, offers no editor, and does not throw', () => {
    expect(() => render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)).not.toThrow()
    expect(screen.getByText('some legacy value')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit legacy_field' })).not.toBeInTheDocument()
  })

  it('renders every cell as plain text with no editor at all when editContext is absent', () => {
    render(<TablePart part={tablePart()} rows={[makeRow()]} />)
    expect(screen.queryByTestId('viewpart-cell-editor-enum')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit notes' })).not.toBeInTheDocument()
    expect(screen.getByText('active')).toBeInTheDocument()
  })
})

// ── Write mechanics ──────────────────────────────────────────────────────────

describe('RecordFieldEditor — writes', () => {
  it('an enum cell offers a dropdown of its declared values, in declared order, and writes the selection through RecordWriteRequest', async () => {
    mockedWrite.mockResolvedValueOnce(
      vaultRecord({
        version_token: 'sha256:ddd',
        properties: [{ property: 'status', values: [{ type: 'enum', enum: 'churned' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    const select = screen.getByTestId('viewpart-cell-editor-enum') as HTMLSelectElement
    expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual([
      'Prospect',
      'Active',
      'Churned',
    ])

    fireEvent.change(select, { target: { value: 'churned' } })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    const [wsArg, body] = mockedWrite.mock.calls[0]
    expect(wsArg).toBe('ws-1')
    expect(body).toMatchObject({
      type: 'company',
      id: 'CO-0142',
      version_token: 'sha256:aaa',
      properties: [{ property: 'status', values: [{ type: 'enum', enum: 'churned' }] }],
    })
  })

  it('a text cell edits in place: click reveals an input, Enter commits, and the response value (not the raw draft) is what renders after', async () => {
    mockedWrite.mockResolvedValueOnce(
      vaultRecord({
        version_token: 'sha256:eee',
        properties: [{ property: 'notes', values: [{ type: 'text', text: 'Normalized by the server' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'my typed text' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      properties: [{ property: 'notes', values: [{ type: 'text', text: 'my typed text' }] }],
    })

    await waitFor(() => expect(screen.queryByTestId('viewpart-cell-editor-text')).not.toBeInTheDocument())
    expect(screen.getByText('Normalized by the server')).toBeInTheDocument()
  })

  it('Escape cancels a text edit without writing anything', () => {
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'discarded' } })
    fireEvent.keyDown(input, { key: 'Escape' })

    expect(screen.queryByTestId('viewpart-cell-editor-text')).not.toBeInTheDocument()
    expect(screen.getByText('Introduced via referral')).toBeInTheDocument()
    expect(mockedWrite).not.toHaveBeenCalled()
  })
})

// ── Conflict handling ────────────────────────────────────────────────────────

describe('RecordFieldEditor — 409 conflict', () => {
  it('surfaces a distinguishable conflict, never auto-retries, and a manual Retry resends with the FRESH token, never the stale one', async () => {
    mockedWrite.mockRejectedValueOnce(
      new KnowledgeRecordConflictError(
        {
          error: 'CRM/Companies/Acme Ltd.md changed on disk since you opened it',
          code: 'knowledge_version_conflict',
          path: 'CRM/Companies/Acme Ltd.md',
          expected_version: 'sha256:aaa',
          actual_version: 'sha256:fresh',
        },
        JSON.stringify({ error: 'conflict' }),
      ),
    )
    mockedFetch.mockResolvedValueOnce(
      vaultRecord({
        version_token: 'sha256:fresh',
        properties: [{ property: 'notes', values: [{ type: 'text', text: 'Someone else edited this first' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'my local edit' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    // (a) the FIRST attempt sent the token this editor actually read.
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({ version_token: 'sha256:aaa' })

    // (b) the refusal is a DISTINGUISHABLE conflict — not a generic error —
    // and it re-reads the record to show what's on the server now.
    await waitFor(() => expect(screen.getByTestId('viewpart-cell-conflict')).toBeInTheDocument())
    expect(mockedFetch).toHaveBeenCalledWith('ws-1', 'CO-0142')
    expect(screen.getByText('Someone else edited this first')).toBeInTheDocument()

    // (c) MUTATION THIS DIES ON: any auto-retry. Still exactly one call.
    expect(mockedWrite).toHaveBeenCalledTimes(1)

    // (d) the user presses Retry — the editor reopens pre-filled with the
    // FRESH server value, never the value the refused attempt sent.
    fireEvent.click(screen.getByTestId('viewpart-cell-conflict-retry'))
    const reopened = screen.getByTestId('viewpart-cell-editor-text') as HTMLInputElement
    expect(reopened.value).toBe('Someone else edited this first')

    mockedWrite.mockResolvedValueOnce(
      vaultRecord({
        version_token: 'sha256:after-retry',
        properties: [{ property: 'notes', values: [{ type: 'text', text: 'Reapplied edit' }] }],
      }),
    )
    fireEvent.change(reopened, { target: { value: 'Reapplied edit' } })
    fireEvent.keyDown(reopened, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(2))
    // THE LOAD-BEARING ASSERTION: the retry carries the FRESH token the
    // conflict's own follow-up read returned — never the stale one the
    // first, refused attempt sent.
    const secondBody = mockedWrite.mock.calls[1][1]
    expect(secondBody).toMatchObject({ version_token: 'sha256:fresh' })
    expect(secondBody.version_token).not.toBe('sha256:aaa')
  })
})

// ── The conflict path's FAILURE branch (silent-failure audit C1) ────────────
//
// The happy conflict path above is already covered. What was not: what the
// reader sees when the follow-up `fetchVaultRecord` — the read whose ENTIRE
// job is to show them the server's current value — fails. That was an empty
// catch, and the render it produced was the worst available one.

function conflictError() {
  return new KnowledgeRecordConflictError(
    {
      error: 'changed on disk',
      code: 'knowledge_version_conflict',
      path: 'CRM/Companies/Acme Ltd.md',
      expected_version: 'sha256:aaa',
      actual_version: 'sha256:fresh',
    },
    JSON.stringify({ error: 'conflict' }),
  )
}

describe('RecordFieldEditor — the post-conflict re-read fails', () => {
  it('states the read failed instead of showing the pre-edit value under a "this changed" banner', async () => {
    mockedWrite.mockRejectedValue(conflictError())
    mockedFetch.mockRejectedValue(new Error('network down'))

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'my local edit' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedFetch).toHaveBeenCalledTimes(1))

    // 1. The failure is VISIBLE. Previously: nothing at all — no error
    //    element, no role=alert, no console entry.
    const err = await screen.findByTestId('viewpart-cell-error')
    expect(err.textContent ?? '').toMatch(/could not be read/i)
    // …and it names the real underlying cause too, not just the situation.
    expect(err.textContent ?? '').toContain('network down')

    // 2. The conflict banner is NOT up. Its claim is "here is the server's
    //    current value", and with no successful re-read there is no such
    //    value — showing it beside the reader's own pre-edit copy asserted
    //    something false.
    expect(screen.queryByTestId('viewpart-cell-conflict')).not.toBeInTheDocument()
    expect(screen.queryByText('This changed while you were editing.')).not.toBeInTheDocument()
  })

  it('positive control — a re-read that SUCCEEDS still shows the conflict banner with the SERVER value', async () => {
    mockedWrite.mockRejectedValue(conflictError())
    mockedFetch.mockResolvedValue(
      vaultRecord({
        version_token: 'sha256:fresh',
        properties: [{ property: 'notes', values: [{ type: 'text', text: 'the other writer value' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'my local edit' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    const banner = await screen.findByTestId('viewpart-cell-conflict')
    expect(banner.textContent).toContain('This changed while you were editing.')
    // The SERVER's value, not the pre-edit one and not the local edit.
    expect(banner.textContent).toContain('the other writer value')
    expect(banner.textContent).not.toContain('Introduced via referral')
    expect(screen.queryByTestId('viewpart-cell-error')).not.toBeInTheDocument()
  })

  it('after a failed re-read the next write is REFUSED locally — the stale token never reaches the server again', async () => {
    mockedWrite.mockRejectedValue(conflictError())
    mockedFetch.mockRejectedValue(new Error('network down'))

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'my local edit' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await screen.findByTestId('viewpart-cell-error')

    // Reopen and try again. The old behaviour re-sent `sha256:aaa` forever,
    // 409ing every time under the same unexplained banner.
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const again = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(again, { target: { value: 'second attempt' } })
    fireEvent.keyDown(again, { key: 'Enter' })

    await waitFor(() => expect(screen.getByTestId('viewpart-cell-error').textContent ?? '').toMatch(/version/i))
    // Still exactly ONE write: the second never left the browser.
    expect(mockedWrite).toHaveBeenCalledTimes(1)
  })
})

// ── A 200 that carries no version_token (silent-failure audit H1) ───────────

describe('RecordFieldEditor — a successful write whose response omits version_token', () => {
  const TOKENLESS = {
    id: 'CO-0142',
    type: 'company',
    path: 'CRM/Companies/Acme Ltd.md',
    title: 'Acme Ltd',
    properties: [{ property: 'notes', values: [{ type: 'text', text: 'saved value' }] }],
  } as VaultRecord

  it('still reports the write upward (the only thing that invalidates caches) and states the anomaly', async () => {
    const onFieldWritten = vi.fn()
    mockedWrite.mockResolvedValue(TOKENLESS)

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={{ ...EDIT_CONTEXT, onFieldWritten }} />)
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const input = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'saved value' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    await screen.findByText('saved value')

    // 1. The callback FIRED. It used to sit inside `if (version_token !==
    //    undefined)`, so no cache was invalidated and a second embed of the
    //    same view kept showing the old value indefinitely.
    expect(onFieldWritten).toHaveBeenCalledTimes(1)
    expect(onFieldWritten.mock.calls[0][0]).toMatchObject({
      path: 'CRM/Companies/Acme Ltd.md',
      recordId: 'CO-0142',
      property: 'notes',
      value: 'saved value',
      versionToken: undefined,
    })

    // 2. The anomaly is stated, not rendered as a clean success.
    expect((await screen.findByTestId('viewpart-cell-error')).textContent ?? '').toMatch(
      /version could not be confirmed/i,
    )
  })

  it('and the next edit is refused locally rather than re-sending the pre-write token', async () => {
    mockedWrite.mockResolvedValue(TOKENLESS)

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const first = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(first, { target: { value: 'saved value' } })
    fireEvent.keyDown(first, { key: 'Enter' })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const second = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(second, { target: { value: 'second edit' } })
    fireEvent.keyDown(second, { key: 'Enter' })

    // No second call at all. Previously call #2 carried `sha256:aaa`, the
    // PRE-write token, and 409'd.
    await waitFor(() => expect(screen.getByTestId('viewpart-cell-error').textContent ?? '').toMatch(/version/i))
    expect(mockedWrite).toHaveBeenCalledTimes(1)
  })

  it('positive control — a response WITH a token reports it upward and lets the next edit through carrying the NEW token', async () => {
    const onFieldWritten = vi.fn()
    mockedWrite.mockResolvedValue(
      vaultRecord({
        version_token: 'sha256:bbb',
        properties: [{ property: 'notes', values: [{ type: 'text', text: 'saved value' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={{ ...EDIT_CONTEXT, onFieldWritten }} />)
    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const first = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(first, { target: { value: 'saved value' } })
    fireEvent.keyDown(first, { key: 'Enter' })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))

    expect(onFieldWritten).toHaveBeenCalledWith(expect.objectContaining({ versionToken: 'sha256:bbb' }))
    expect(screen.queryByTestId('viewpart-cell-error')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Edit notes' }))
    const second = screen.getByTestId('viewpart-cell-editor-text')
    fireEvent.change(second, { target: { value: 'second edit' } })
    fireEvent.keyDown(second, { key: 'Enter' })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(2))
    expect(mockedWrite.mock.calls[1][1]).toMatchObject({ version_token: 'sha256:bbb' })
  })
})

// ── EMB-089: a record's title and path are never editable ───────────────────

describe('RecordFieldEditor — title and path are never editable (EMB-089)', () => {
  it('renders the row title as plain text with no editor, while an ordinary property in the SAME row does get one', () => {
    // The structural guarantee is that `file.name` has no entry in
    // `row.cells`, so no VaultFindCell is ever constructed for it — exactly
    // the kind of guarantee a refactor removes silently, and it had no test.
    // Asserted against BEHAVIOUR (what renders), not internals, so it
    // survives `isEditableCell` becoming a real type guard.
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    // The title IS displayed…
    expect(screen.getByText('Acme Ltd')).toBeInTheDocument()
    // …and carries no editor of any kind.
    expect(screen.queryByRole('button', { name: 'Edit file.name' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit title' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Edit path' })).not.toBeInTheDocument()

    // Positive half, same fixture: an ordinary property DOES. Without it, a
    // component rendering no editors anywhere would pass all three above.
    expect(screen.getByRole('button', { name: 'Edit notes' })).toBeInTheDocument()
    expect(screen.getByTestId('viewpart-cell-editor-enum')).toBeInTheDocument()
  })

  it('a schema that DECLARES a property named "title" gets an editor for that property — the row identity title still does not', () => {
    const base = makeRow()
    const row = makeRow({
      cells: [...base.cells, cell('title', 'a declared property called title', { type: 'text' })],
    })
    const part: ViewResultPart = {
      part: 'table',
      source: { part: 'table' },
      columns: ['file.name', 'status', 'notes', 'title'],
    }
    render(<TablePart part={part} rows={[row]} editContext={EDIT_CONTEXT} />)

    // The row identity title still renders as text.
    expect(screen.getByText('Acme Ltd')).toBeInTheDocument()
    // The DECLARED property named `title` is an ordinary editable cell, and
    // editing it addresses the PROPERTY's value, never the row's identity.
    fireEvent.click(screen.getByRole('button', { name: 'Edit title' }))
    expect(screen.getByTestId('viewpart-cell-editor-text')).toHaveValue('a declared property called title')
  })
})

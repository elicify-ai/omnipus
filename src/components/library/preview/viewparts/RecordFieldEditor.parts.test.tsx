// RecordFieldEditor.parts.test.tsx — ADR-083 review I5, I7, I8, I9, I10.
//
// WHAT THIS FILE COVERS that RecordFieldEditor.test.tsx does not:
//
//   I5  resolveEditTarget's two ROW-level refusals (absent `id`, absent
//       `version_token`). Neither was tested. Absent-`version_token` is
//       precisely what a server regression produces, and its symptom is
//       EVERY EDITOR IN THE PRODUCT DISAPPEARING with no error anywhere.
//   I7  The clear-to-empty path (`raw === '' ? [] : [...]`) — the client half
//       of RemoveProperty, which DELETES data. No test cleared a field.
//   I8  ListPart's editor path, both threading sites (ungrouped and grouped).
//       `viewparts.test.tsx` contains zero occurrences of `editContext`, and
//       RecordFieldEditor.test.tsx imports TablePart alone.
//   I9  The ListPart/TablePart divergence on an editable-but-EMPTY field.
//   I10 TablePart's `primary && onOpenPath` precedence over the editor.
//
// PAIRING DISCIPLINE, inherited from RecordFieldEditor.test.tsx's header:
// every negative assertion below sits in the same block as a positive control
// proving an ordinary writable cell in the IDENTICAL fixture does get an
// editor. Without that, "no editor rendered" passes on a part that renders no
// editors at all.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import type { VaultFindCell, VaultFindRow, VaultRecord, ViewResultPart } from '@/lib/api/generated/openapi-types'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return { ...actual, writeVaultRecord: vi.fn(), fetchVaultRecord: vi.fn() }
})

import { writeVaultRecord } from '@/lib/api'
import { TablePart } from './TablePart'
import { ListPart } from './ListPart'

const mockedWrite = vi.mocked(writeVaultRecord)

beforeEach(() => {
  vi.clearAllMocks()
})

// ── Fixtures ────────────────────────────────────────────────────────────────

function cell(property: string, value: string, extra: Partial<VaultFindCell> = {}): VaultFindCell {
  return { property, value, ...extra }
}

/** A row that IS a complete edit target: id + version_token both present. */
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

function tablePart(columns: string[] = ['file.name', 'notes']): ViewResultPart {
  return { part: 'table', source: { part: 'table' }, columns }
}

/** ListPart reads its detail column from the part's own column list. */
function listPart(over: Partial<ViewResultPart> = {}): ViewResultPart {
  return { part: 'list', source: { part: 'list' }, columns: ['file.name', 'notes'], ...over }
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

/** A TEXT cell offers its editor as a click-to-edit TRIGGER button; the
 *  `viewpart-cell-editor-text` input only exists once that trigger is
 *  clicked. "An editor is offered" is therefore the trigger's presence —
 *  asserting the input directly would report "no editor" for every cell that
 *  simply is not being edited yet. */
const EDITOR_TRIGGER = 'viewpart-cell-editor-trigger'

// ═══════════════════════════════════════════════════════════════════════════
// I5 — the row-level refusals
// ═══════════════════════════════════════════════════════════════════════════

describe('resolveEditTarget — a row that is not a complete edit target (review I5)', () => {
  // Each case renders the SAME part with the SAME editable cell, changing one
  // row field only, so the difference in outcome can only be that field.

  it('offers NO editor when the row has no id — and the identical row WITH an id does get one', () => {
    const { unmount } = render(
      <TablePart part={tablePart()} rows={[makeRow({ id: undefined })]} editContext={EDIT_CONTEXT} />,
    )
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    // The value must still RENDER — refusing an editor is not refusing the data.
    expect(screen.getByText('Introduced via referral')).toBeInTheDocument()
    unmount()

    // POSITIVE CONTROL — the only change is that `id` is present.
    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
  })

  it('offers NO editor when the row has no version_token — the exact shape a server regression produces', () => {
    // WHY THIS ONE MATTERS MOST. attachRowVersionTokens is best-effort by
    // design and swallows read failures, so a regression there drops the
    // token on every row at once. The symptom is every editor in the product
    // vanishing — silently, with a green suite on both sides of the wire.
    const { unmount } = render(
      <TablePart part={tablePart()} rows={[makeRow({ version_token: undefined })]} editContext={EDIT_CONTEXT} />,
    )
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    expect(screen.getByText('Introduced via referral')).toBeInTheDocument()
    unmount()

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
  })

  it('offers NO editor when the context names no recordType, and one when it does', () => {
    const { unmount } = render(
      <TablePart part={tablePart()} rows={[makeRow()]} editContext={{ workspaceId: 'ws-1' }} />,
    )
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    unmount()

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
  })
})

// ═══════════════════════════════════════════════════════════════════════════
// I7 — clearing a field (the client half of RemoveProperty)
// ═══════════════════════════════════════════════════════════════════════════

describe('RecordFieldEditor — clearing a field to empty (review I7)', () => {
  it('sends an EMPTY values array, which is what removes the property — never a single empty string', async () => {
    // THE ORACLE IS ADR-083 §4.2c / D3.2: an empty `values` array CLEARS the
    // property (server-side: knowledge.RemoveProperty). A single value whose
    // text is "" would instead WRITE an empty string into frontmatter — the
    // property would still exist, holding nothing, which is a different
    // document and a different query result.
    mockedWrite.mockResolvedValue(
      vaultRecord({ properties: [{ property: 'notes', type: 'text', values: [] }] }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByTestId(EDITOR_TRIGGER))
    const input = await screen.findByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: '' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))

    const body = mockedWrite.mock.calls[0][1] as { properties: { property: string; values: unknown[] }[] }
    expect(body.properties).toHaveLength(1)
    expect(body.properties[0].property).toBe('notes')
    expect(body.properties[0].values).toEqual([])
    // The assertion that actually bites: an empty-string VALUE is the wrong
    // request and would leave the property in place.
    expect(body.properties[0].values).not.toEqual([{ type: 'text', text: '' }])
    expect(body.properties[0].values).toHaveLength(0)
  })

  it('POSITIVE CONTROL — a non-empty edit still sends exactly one value', async () => {
    // Without this, the test above passes on a component that sends an empty
    // array for EVERY write, clearing a property whenever anyone edits one.
    mockedWrite.mockResolvedValue(
      vaultRecord({
        properties: [{ property: 'notes', type: 'text', values: [{ type: 'text', text: 'Renewed' }] }],
      }),
    )

    render(<TablePart part={tablePart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByTestId(EDITOR_TRIGGER))
    const input = await screen.findByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'Renewed' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    const body = mockedWrite.mock.calls[0][1] as { properties: { values: unknown[] }[] }
    expect(body.properties[0].values).toHaveLength(1)
    expect(body.properties[0].values).toEqual([{ type: 'text', text: 'Renewed' }])
  })
})

// ═══════════════════════════════════════════════════════════════════════════
// I8 — ListPart's editor path, BOTH threading sites
// ═══════════════════════════════════════════════════════════════════════════

describe('ListPart — the editor path (review I8)', () => {
  it('threads editContext to the UNGROUPED list, and renders plain text without it', () => {
    const { unmount } = render(<ListPart part={listPart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
    unmount()

    // Paired negative: the same list with no context offers no editor at all,
    // while still rendering the value.
    render(<ListPart part={listPart()} rows={[makeRow()]} />)
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    expect(screen.getByText(/Introduced via referral/)).toBeInTheDocument()
  })

  it('threads editContext to the GROUPED branch — deleting it there alone used to be invisible', () => {
    // The grouped branch is a SECOND, independent `editContext={editContext}`
    // site. A test that only renders the ungrouped list passes with the
    // grouped one deleted.
    const row = makeRow()
    const grouped = listPart({
      groups: [{ key: 'Active', count: 1, paths: [row.path], subtotals: [] }],
    })

    const { unmount } = render(<ListPart part={grouped} rows={[row]} editContext={EDIT_CONTEXT} />)
    // Prove we really are in the grouped branch, not silently falling back.
    expect(screen.getByText('Active')).toBeInTheDocument()
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
    unmount()

    render(<ListPart part={grouped} rows={[row]} />)
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
  })

  it('a ListPart edit writes through the same RecordWriteRequest as a table edit', async () => {
    // Reaching EditableCell is not the same as being able to WRITE from it.
    mockedWrite.mockResolvedValue(
      vaultRecord({
        properties: [{ property: 'notes', type: 'text', values: [{ type: 'text', text: 'From the list' }] }],
      }),
    )

    render(<ListPart part={listPart()} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)

    fireEvent.click(screen.getByTestId(EDITOR_TRIGGER))
    const input = await screen.findByTestId('viewpart-cell-editor-text')
    fireEvent.change(input, { target: { value: 'From the list' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      id: 'CO-0142',
      type: 'company',
      version_token: 'sha256:aaa',
    })
  })
})

// ═══════════════════════════════════════════════════════════════════════════
// I9 — the divergence, now resolved in TablePart's favour
// ═══════════════════════════════════════════════════════════════════════════

describe('an editable-but-EMPTY field offers an editor in BOTH parts (review I9)', () => {
  // THE DECISION, recorded here because the review found neither behaviour
  // asserted: TablePart's is correct and ListPart was changed to match.
  //
  // WHY. ADR-083 D3.2 treats an ABSENT property as a legitimate edit target,
  // and §4.2c's empty-`values` clear exists precisely to reach it. If an
  // empty editable field offered no editor, clearing a field would be a
  // ONE-WAY DOOR: the user clears it, the editor disappears with the value,
  // and there is no way to put a value back from that view. A data-entry
  // surface that can delete but not restore is worse than one that can do
  // neither. ListPart's `detailValue !== ''` gate was a layout rule (do not
  // draw a dangling separator for an absent detail) that silently acquired
  // an editing consequence it was never meant to have.

  const emptyRow = makeRow({ cells: [cell('notes', '', { type: 'text' })] })

  it('TablePart offers one for an empty editable cell', () => {
    render(<TablePart part={tablePart()} rows={[emptyRow]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
  })

  it('ListPart offers one too — clearing a field must not remove the way to refill it', () => {
    render(<ListPart part={listPart()} rows={[emptyRow]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
  })

  it('but ListPart still hides an empty detail when there is no editing context at all', () => {
    // The layout rule the gate was actually for is PRESERVED. Without this
    // assertion the fix above would be indistinguishable from deleting the
    // gate outright, which would put a bare "·" on every row of every
    // read-only list whose detail happens to be absent.
    const { container } = render(<ListPart part={listPart()} rows={[emptyRow]} />)
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    expect(container.textContent).not.toContain('·')
  })

  it('and still hides an empty detail that is NOT editable, even with a context', () => {
    // A derived cell is never editable, so the layout rule must still apply.
    const derivedEmpty = makeRow({ cells: [cell('notes', '', { type: 'text', derived: true })] })
    const { container } = render(<ListPart part={listPart()} rows={[derivedEmpty]} editContext={EDIT_CONTEXT} />)
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
    expect(container.textContent).not.toContain('·')
  })
})

// ═══════════════════════════════════════════════════════════════════════════
// I10 — `primary && onOpenPath` wins over the editor
// ═══════════════════════════════════════════════════════════════════════════

describe('TablePart — the first column when it is a real editable property (review I10)', () => {
  // The existing fixture always puts `file.name` first and never passes
  // onOpenPath, so NEITHER half of this precedence was covered. It matters
  // for a view whose first column is an ordinary property: whether that cell
  // is editable then depends on something unrelated to editing.

  const firstColumnEditable = tablePart(['notes'])

  it('renders the row-open button instead of an editor when onOpenPath is present', () => {
    render(
      <TablePart
        part={firstColumnEditable}
        rows={[makeRow()]}
        editContext={EDIT_CONTEXT}
        onOpenPath={() => {}}
      />,
    )
    expect(screen.getByTestId('viewpart-row-open')).toBeInTheDocument()
    expect(screen.queryByTestId(EDITOR_TRIGGER)).not.toBeInTheDocument()
  })

  it('renders the editor when onOpenPath is absent — the SAME part and row', () => {
    // Paired with the case above: together they pin the precedence as a real,
    // observable rule rather than an accident. Either alone proves nothing.
    render(<TablePart part={firstColumnEditable} rows={[makeRow()]} editContext={EDIT_CONTEXT} />)
    expect(screen.getByTestId(EDITOR_TRIGGER)).toBeInTheDocument()
    expect(screen.queryByTestId('viewpart-row-open')).not.toBeInTheDocument()
  })
})

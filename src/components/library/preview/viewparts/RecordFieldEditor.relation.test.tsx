// RecordFieldEditor.relation.test.tsx — GAP-02 / #700 (2026-09-14 fix
// round). Relation and person cells had NO editor at all ("Relation — change
// it through the agent; a picker is not available here yet"): the verbs
// existed only behind the agent's knowledge_edit tool. These tests pin the
// picker against the new relation write door:
//
//   - opening the cell reads the record and shows its current targets;
//   - searching queries the EXISTING find endpoint scoped to the collection;
//   - picking a result commits op add (or op replace on a filled SCALAR
//     slot — FR-035 forbids a second add);
//   - a chip's × commits op remove;
//   - a successful write refreshes the chips from the response's stored
//     spelling and reports the fresh version token upward;
//   - a 409 shows the conflict banner, not a generic error.
//
// The cell-edit context supplies collectionId (the find endpoint's required
// scope); without it the cell falls back to the inert rendering — pinned in
// the last test.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'

const { mockWriteRelation, mockFetchRecord, mockSearchVault } = vi.hoisted(() => ({
  mockWriteRelation: vi.fn(),
  mockFetchRecord: vi.fn(),
  mockSearchVault: vi.fn(),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    writeVaultRecordRelation: mockWriteRelation,
    fetchVaultRecord: mockFetchRecord,
    searchVault: mockSearchVault,
  }
})

import { EditableCell, type RecordEditContext } from './RecordFieldEditor'
import type { VaultFindCell, VaultFindRow } from '@/lib/api/generated/openapi-types'
import { KnowledgeRecordConflictError } from '@/lib/api'

const CONTEXT: RecordEditContext = {
  workspaceId: 'ws-1',
  recordType: 'deal',
  collectionId: 'kb_deals',
  onFieldWritten: vi.fn(),
}

function relationRow(): VaultFindRow {
  return {
    path: 'd1.md',
    title: 'Primary',
    cells: [],
    joins: [],
    id: 'DE-0001',
    version_token: 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  } as VaultFindRow
}

function relationCell(over: Partial<VaultFindCell> = {}): VaultFindCell {
  return {
    property: 'partners',
    value: '[[Acme]]',
    type: 'relation',
    relation: true,
    many: true,
    ...over,
  } as VaultFindCell
}

/** A record read whose named property holds the given links. */
function recordWith(links: string[], token = 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', property = 'partners') {
  return {
    id: 'DE-0001',
    type: 'deal',
    path: 'd1.md',
    properties: [
      {
        property,
        values: links.map((link) => ({ type: 'relation', relation: { link, resolved: false } })),
      },
    ],
    version_token: token,
  }
}

function relationResponse(links: string[], token: string) {
  return {
    record: recordWith(links, token),
    changed: true,
    stored_targets: links,
    warnings: [],
  }
}

function searchHit(title: string, recordType = 'deal') {
  return {
    path: `${title.toLowerCase()}.md`,
    title,
    record_type: recordType,
    cells: [],
  }
}

function openPicker() {
  fireEvent.click(screen.getByTestId('viewpart-relation-trigger'))
}

beforeEach(() => {
  vi.clearAllMocks()
  mockFetchRecord.mockResolvedValue(recordWith(['[[Acme]]']))
  mockSearchVault.mockResolvedValue({
    collection_id: 'kb_deals',
    complete: true,
    notes: [],
    records: [],
    views: [],
  })
})

describe('RecordFieldEditor relation picker (GAP-02 / #700)', () => {
  it('opens on click, reads the record, and shows the current targets as chips', async () => {
    render(
      <table>
        <tbody>
          <tr>
            <td>
              <EditableCell
                context={CONTEXT}
                row={relationRow()}
                cell={relationCell()}
                renderValue={(v) => v}
              />
            </td>
          </tr>
        </tbody>
      </table>,
    )

    openPicker()
    expect(await screen.findByTestId('viewpart-relation-chip-Acme')).toBeInTheDocument()
    expect(mockFetchRecord).toHaveBeenCalledWith('ws-1', 'DE-0001')
  })

  it('searching queries the find endpoint scoped to the collection, and picking a result commits op add', async () => {
    mockSearchVault.mockResolvedValue({
      collection_id: 'kb_deals',
      complete: true,
      notes: [],
      records: [searchHit('Bolt')],
      views: [],
    })
    mockWriteRelation.mockResolvedValue(relationResponse(['[[Acme]]', '[[Bolt]]'], 'v1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'))
    render(
      <EditableCell context={CONTEXT} row={relationRow()} cell={relationCell()} renderValue={(v) => v} />,
    )

    openPicker()
    await screen.findByTestId('viewpart-relation-chip-Acme')

    fireEvent.change(screen.getByTestId('viewpart-relation-search'), { target: { value: 'Bol' } })
    expect(mockSearchVault).toHaveBeenCalledWith(
      'ws-1',
      expect.objectContaining({ query: 'Bol', collection_id: 'kb_deals' }),
    )
    fireEvent.click(await screen.findByTestId('viewpart-relation-option-Bolt'))

    await waitFor(() =>
      expect(mockWriteRelation).toHaveBeenCalledWith('ws-1', {
        id: 'DE-0001',
        version_token: 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        property: 'partners',
        op: 'add',
        targets: ['Bolt'],
      }),
    )
    // Chips refresh from the RESPONSE's stored spelling, and the fresh token
    // is reported upward so the caches around the edit invalidate.
    expect(await screen.findByTestId('viewpart-relation-chip-Bolt')).toBeInTheDocument()
    expect(CONTEXT.onFieldWritten).toHaveBeenCalledWith(
      expect.objectContaining({
        recordId: 'DE-0001',
        property: 'partners',
        versionToken: 'v1:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      }),
    )
  })

  it('a chip × commits op remove with the stored spelling', async () => {
    mockWriteRelation.mockResolvedValue(relationResponse([], 'v1:cccccccccccccccccccccccccccccccc'))
    render(
      <EditableCell context={CONTEXT} row={relationRow()} cell={relationCell()} renderValue={(v) => v} />,
    )

    openPicker()
    const chip = await screen.findByTestId('viewpart-relation-chip-Acme')
    fireEvent.click(chip.querySelector('[data-testid="viewpart-relation-remove-Acme"]') as HTMLElement)

    await waitFor(() =>
      expect(mockWriteRelation).toHaveBeenCalledWith('ws-1', {
        id: 'DE-0001',
        version_token: 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        property: 'partners',
        op: 'remove',
        targets: ['[[Acme]]'],
      }),
    )
    await waitFor(() =>
      expect(screen.queryByTestId('viewpart-relation-chip-Acme')).not.toBeInTheDocument(),
    )
  })

  it('a SCALAR slot: picking a different target commits op replace (FR-035 — a second add is refused)', async () => {
    mockFetchRecord.mockResolvedValue(recordWith(['[[Dana]]'], 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', 'contact'))
    mockSearchVault.mockResolvedValue({
      collection_id: 'kb_deals',
      complete: true,
      notes: [],
      records: [searchHit('Lee', 'person')],
      views: [],
    })
    mockWriteRelation.mockResolvedValue(relationResponse(['[[Lee]]'], 'v1:dddddddddddddddddddddddddddddddd'))
    const scalarCell = relationCell({ property: 'contact', value: '[[Dana]]', type: 'person', many: false })
    render(<EditableCell context={CONTEXT} row={relationRow()} cell={scalarCell} renderValue={(v) => v} />)

    openPicker()
    await screen.findByTestId('viewpart-relation-chip-Dana')

    fireEvent.change(screen.getByTestId('viewpart-relation-search'), { target: { value: 'Lee' } })
    fireEvent.click(await screen.findByTestId('viewpart-relation-option-Lee'))

    await waitFor(() =>
      expect(mockWriteRelation).toHaveBeenCalledWith('ws-1', {
        id: 'DE-0001',
        version_token: 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
        property: 'contact',
        op: 'replace',
        targets: ['Lee'],
      }),
    )
  })

  it('a stale token shows the conflict banner, never a generic error', async () => {
    mockSearchVault.mockResolvedValue({
      collection_id: 'kb_deals',
      complete: true,
      notes: [],
      records: [searchHit('Bolt')],
      views: [],
    })
    // The mocked fn bypasses api.ts's own 409→KnowledgeRecordConflictError
    // translation, so reject with the typed error the real client would
    // have thrown — what isKnowledgeRecordConflict actually recognises.
    mockWriteRelation.mockRejectedValue(
      new KnowledgeRecordConflictError(
        {
          error: 'changed since you read it',
          code: 'knowledge_version_conflict',
          path: 'd1.md',
          expected_version: 'v1:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
          actual_version: 'v1:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
        },
        '{"code":"knowledge_version_conflict"}',
      ),
    )
    render(
      <EditableCell context={CONTEXT} row={relationRow()} cell={relationCell()} renderValue={(v) => v} />,
    )

    openPicker()
    await screen.findByTestId('viewpart-relation-chip-Acme')
    fireEvent.change(screen.getByTestId('viewpart-relation-search'), { target: { value: 'Bol' } })
    fireEvent.click(await screen.findByTestId('viewpart-relation-option-Bolt'))

    await waitFor(() =>
      expect(screen.getByTestId('viewpart-relation-conflict')).toBeInTheDocument(),
    )
  })

  it('without a collectionId the cell stays inert (the find endpoint cannot be scoped)', () => {
    const context: RecordEditContext = { workspaceId: 'ws-1', recordType: 'deal' }
    render(
      <EditableCell context={context} row={relationRow()} cell={relationCell()} renderValue={(v) => v} />,
    )
    expect(screen.getByTestId('viewpart-cell-inert')).toBeInTheDocument()
    expect(screen.queryByTestId('viewpart-relation-trigger')).not.toBeInTheDocument()
  })
})

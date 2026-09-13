// RecordFieldEditor.uat.test.tsx — UAT 2026-09-13 findings on the inline
// cell editors:
//   D-71   an enum cell can be CLEARED (blank option → `values: []`)
//   #700   integer / decimal / checkbox cells get real editors that write
//          the contract's exact shapes (digits as strings, checkbox as bool)
//   D-113  a schema-described cell with NO editor on an editing surface is
//          inert: default cursor, a reason, and a click that does not leave
//          the table — while the row's own Open button still opens the note
//          and a read-only surface (no edit context) keeps its row click.

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

const STATUS_VALUES = [
  { value: 'active', label: 'Active', position: 0 },
  { value: 'paused', label: 'Paused', position: 1 },
]

function row(): VaultFindRow {
  return {
    path: 'Kitchen/Sink.md',
    title: 'Sink',
    id: 'KS-0001',
    version_token: 'sha256:aaa',
    joins: [],
    cells: [
      { property: 'status', value: 'active', type: 'enum', values: STATUS_VALUES },
      { property: 'count', value: '3', type: 'integer' },
      { property: 'budget', value: '12.50', type: 'decimal' },
      { property: 'done', value: '', type: 'checkbox' },
      { property: 'owner', value: '[[Ada]]', type: 'person', relation: true },
    ],
  }
}

function part(): ViewResultPart {
  return {
    part: 'table',
    source: { part: 'table' },
    columns: ['file.name', 'status', 'count', 'budget', 'done', 'owner'],
  }
}

function written(property: string, values: VaultRecord['properties'][number]['values']): VaultRecord {
  return {
    id: 'KS-0001',
    type: 'kitchen_sink',
    path: 'Kitchen/Sink.md',
    title: 'Sink',
    version_token: 'sha256:bbb',
    properties: [{ property, values }],
  }
}

const EDIT = { workspaceId: 'ws-1', recordType: 'kitchen_sink' }

describe('D-71 — enum cell can be cleared', () => {
  it('offers a blank option and choosing it writes an empty values array', async () => {
    mockedWrite.mockResolvedValueOnce(written('status', []))
    render(<TablePart part={part()} rows={[row()]} editContext={EDIT} />)

    const select = screen.getByTestId('viewpart-cell-editor-enum') as HTMLSelectElement
    const blank = Array.from(select.options).find((o) => o.value === '')
    expect(blank, 'a blank option must exist').toBeDefined()

    fireEvent.change(select, { target: { value: '' } })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      properties: [{ property: 'status', values: [] }],
    })
    // Positive control: a declared value still writes normally.
    mockedWrite.mockResolvedValueOnce(written('status', [{ type: 'enum', enum: 'paused' }]))
    fireEvent.change(select, { target: { value: 'paused' } })
    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(2))
    expect(mockedWrite.mock.calls[1][1]).toMatchObject({
      properties: [{ property: 'status', values: [{ type: 'enum', enum: 'paused' }] }],
    })
  })
})

describe('#700 — integer, decimal and checkbox editors', () => {
  it('integer: opens a numeric text editor and writes the digits as a string', async () => {
    mockedWrite.mockResolvedValueOnce(written('count', [{ type: 'integer', integer: '42' }]))
    render(<TablePart part={part()} rows={[row()]} editContext={EDIT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit count' }))
    const input = screen.getByTestId('viewpart-cell-editor-integer') as HTMLInputElement
    expect(input.getAttribute('inputmode')).toBe('numeric')
    fireEvent.change(input, { target: { value: '42' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      mode: 'update',
      id: 'KS-0001',
      version_token: 'sha256:aaa',
      properties: [{ property: 'count', values: [{ type: 'integer', integer: '42' }] }],
    })
    await waitFor(() => expect(screen.getByText('42')).toBeInTheDocument())
  })

  it('decimal: writes the exact digits typed, never a float', async () => {
    mockedWrite.mockResolvedValueOnce(written('budget', [{ type: 'decimal', decimal: '1200.50' }]))
    render(<TablePart part={part()} rows={[row()]} editContext={EDIT} />)

    fireEvent.click(screen.getByRole('button', { name: 'Edit budget' }))
    const input = screen.getByTestId('viewpart-cell-editor-decimal') as HTMLInputElement
    expect(input.getAttribute('inputmode')).toBe('decimal')
    fireEvent.change(input, { target: { value: '1200.50' } })
    fireEvent.keyDown(input, { key: 'Enter' })

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      properties: [{ property: 'budget', values: [{ type: 'decimal', decimal: '1200.50' }] }],
    })
  })

  it('checkbox: an absent value renders unchecked and one tick writes checkbox: true', async () => {
    mockedWrite.mockResolvedValueOnce(written('done', [{ type: 'checkbox', checkbox: true }]))
    render(<TablePart part={part()} rows={[row()]} editContext={EDIT} />)

    const box = screen.getByTestId('viewpart-cell-editor-checkbox') as HTMLInputElement
    expect(box.checked).toBe(false)
    fireEvent.click(box)

    await waitFor(() => expect(mockedWrite).toHaveBeenCalledTimes(1))
    expect(mockedWrite.mock.calls[0][1]).toMatchObject({
      properties: [{ property: 'done', values: [{ type: 'checkbox', checkbox: true }] }],
    })
    await waitFor(() => expect((screen.getByTestId('viewpart-cell-editor-checkbox') as HTMLInputElement).checked).toBe(true))
  })
})

describe('D-113 — inert cells on an editing surface', () => {
  it('a person cell is inert: default cursor, a reason, no navigation on click; the Open button still opens', () => {
    const onOpenPath = vi.fn()
    render(<TablePart part={part()} rows={[row()]} editContext={EDIT} onOpenPath={onOpenPath} />)

    const inert = screen.getByTestId('viewpart-cell-inert')
    expect(inert.getAttribute('title')).toMatch(/relation/i)
    const td = inert.closest('td') as HTMLTableCellElement
    expect(td.className).toContain('cursor-default')
    expect(td.getAttribute('data-inert')).toBe('true')

    fireEvent.click(inert)
    fireEvent.click(td)
    expect(onOpenPath).not.toHaveBeenCalled()

    // Positive controls: the row's Open button and the row itself still open.
    fireEvent.click(screen.getByTestId('viewpart-row-open'))
    expect(onOpenPath).toHaveBeenCalledWith('Kitchen/Sink.md')
  })

  it('with NO edit context (a read-only surface) a typed cell keeps the row click', () => {
    const onOpenPath = vi.fn()
    render(<TablePart part={part()} rows={[row()]} onOpenPath={onOpenPath} />)
    expect(screen.queryByTestId('viewpart-cell-inert')).not.toBeInTheDocument()
    fireEvent.click(screen.getByText('[[Ada]]'))
    expect(onOpenPath).toHaveBeenCalledWith('Kitchen/Sink.md')
  })
})

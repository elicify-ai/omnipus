// KnowledgeViewsList.test.tsx — UAT D-13 (web half): a collection without a
// `.base` file still owns saved views, and this list is the surface where
// they exist.
//
// ORACLE. Expected values come from the CONTRACT
// (contracts/components/schemas/KnowledgeCollectionViews.yaml — every `name`
// is the server's slug, passed VERBATIM to the view endpoint; an unservable
// view is listed and disabled with its reason; rejected files are counted and
// named) and from the defect itself: an authored view (no `source`) is
// listed and openable, which no earlier surface could do.
//
// MOCK BOUNDARY. Only the network is mocked — `loadViews`/`loadViewResult`
// are the injected fetches. KnowledgeViewsList, its dialog and
// ViewPartsRenderer are real.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  KnowledgeCollectionViews as KnowledgeCollectionViewsSchema,
  ViewResult as ViewResultSchema,
} from '@/lib/api/generated/schemas'
import type { KnowledgeCollectionViews, ViewResult } from '@/lib/api/generated/openapi-types'

import { KnowledgeViewsList } from './KnowledgeViewsList'

function views(over: Partial<KnowledgeCollectionViews> = {}): KnowledgeCollectionViews {
  const base: KnowledgeCollectionViews = {
    collection_id: 'kb_1',
    views: [],
    unloadable_count: 0,
    ...over,
  }
  return KnowledgeCollectionViewsSchema.parse(base) as KnowledgeCollectionViews
}

function evaluated(over: Partial<ViewResult> = {}): ViewResult {
  const base: ViewResult = {
    view: 'authored--active',
    label: 'Active invoices',
    parts: [{ part: 'table', source: { part: 'table' }, columns: ['file.name'] }],
    rows: [{ path: 'a.md', title: 'INV-A', cells: [], joins: [] }],
    complete: true,
    problems: [],
    ...over,
  }
  return ViewResultSchema.parse(base) as ViewResult
}

function renderList(opts: { res?: KnowledgeCollectionViews; rejectsWith?: Error; viewRes?: ViewResult } = {}) {
  const loadViews = opts.rejectsWith
    ? vi.fn().mockRejectedValue(opts.rejectsWith)
    : vi.fn().mockResolvedValue(opts.res ?? views())
  const loadViewResult = vi.fn().mockResolvedValue(opts.viewRes ?? evaluated())
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={client}>
      <KnowledgeViewsList
        workspaceId="ws-1"
        collectionId="kb_1"
        collectionRootPath="vault"
        loadViews={loadViews}
        loadViewResult={loadViewResult}
      />
    </QueryClientProvider>,
  )
  return { ...utils, loadViews, loadViewResult }
}

describe('UAT D-13 — the collection Views list', () => {
  it('lists a view with NO .base behind it (the D-13 case) beside an imported one that names its source', async () => {
    renderList({
      res: views({
        views: [
          { name: 'authored--active', label: 'Active invoices' },
          { name: 'invoices--outstanding', label: 'Outstanding', source: 'CRM/Invoices.base', kind: 'table' },
        ],
      }),
    })
    const list = await screen.findByTestId('knowledge-views-list')
    const items = within(list).getAllByTestId('knowledge-views-item')
    expect(items).toHaveLength(2)
    // DIES ON the old code: no surface anywhere listed a `.base`-less view.
    expect(within(list).getByText('Active invoices')).toBeInTheDocument()
    expect(within(list).getByText('authored in this knowledge base')).toBeInTheDocument()
    expect(within(list).getByText('Outstanding')).toBeInTheDocument()
    expect(within(list).getByText('from CRM/Invoices.base')).toBeInTheDocument()
  })

  it('opens the clicked view\'s evaluated result, addressed by the SERVER slug', async () => {
    const { loadViewResult } = renderList({
      res: views({ views: [{ name: 'authored--active', label: 'Active invoices' }] }),
    })
    fireEvent.click(await screen.findByTestId('knowledge-views-item'))
    const dialog = await screen.findByTestId('knowledge-views-dialog')
    await waitFor(() => expect(within(dialog).getByTestId('viewpart-table')).toBeInTheDocument())
    // VERBATIM — the contract's no-rederivation rule.
    expect(loadViewResult).toHaveBeenCalledWith('ws-1', 'kb_1', 'authored--active', expect.anything())
  })

  it('an unservable view stays listed, visibly disabled, with its reason — never silently omitted', async () => {
    renderList({
      res: views({
        views: [
          {
            name: 'broken',
            label: 'Broken view',
            unservable: true,
            unservable_reason: 'the view names a property the record type does not declare',
          },
        ],
      }),
    })
    const item = await screen.findByTestId('knowledge-views-item')
    expect(item).toBeDisabled()
    expect(screen.getByTestId('knowledge-views-unservable')).toHaveTextContent(
      'the view names a property the record type does not declare',
    )
    fireEvent.click(item)
    expect(screen.queryByTestId('knowledge-views-dialog')).not.toBeInTheDocument()
  })

  it('counts and names view files that could not be loaded (the D-70 rule)', async () => {
    renderList({
      res: views({
        views: [{ name: 'ok', label: 'OK' }],
        unloadable_count: 1,
        unloadable: [{ paths: ['invoices--broken.yaml'], code: 'view_invalid', reason: 'unknown key "group-by"' }],
      }),
    })
    const line = await screen.findByTestId('knowledge-views-unloadable')
    expect(line.textContent).toContain('1 view file could not be loaded')
    expect(line.textContent).toContain('unknown key "group-by"')
  })

  it('renders nothing at all when the collection owns no views', async () => {
    renderList({ res: views() })
    // Give a failed-mount a chance to fire; the assertion is the absence.
    await waitFor(() => expect(screen.queryByTestId('knowledge-views-error')).toBeNull())
    expect(screen.queryByTestId('knowledge-views-list')).toBeNull()
  })

  it('a failed list request is a visible error with a Retry — never a silent empty list', async () => {
    renderList({ rejectsWith: new Error('gateway down') })
    const banner = await screen.findByTestId('knowledge-views-error-banner')
    expect(banner).toHaveTextContent('gateway down')
    expect(screen.getByTestId('knowledge-views-retry')).toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-views-list')).toBeNull()
  })
})

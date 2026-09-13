// LibraryExplorer.address.test.tsx — the 2026-09-13 UAT findings on how an
// ADDRESSED explorer follows its URL: D-108 (a same-route change of `folder=`
// updated the address bar and nothing else), D-64 (switching workspace by URL
// kept the previous workspace's folder in the breadcrumb) and D-36 (a `path=`
// naming a folder was announced as "not found" while listed on screen).
//
// Each fails on the old code: `folder` was a one-time seed, the workspace
// change left `browsedDir` untouched, and a folder path fell into the
// missing-file branch.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { useKnowledgeIndexStore } from '@/store/knowledgeIndex'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchLibraryWorkspaces: vi.fn(),
    fetchLibraryEntries: vi.fn(),
    fetchLibraryContent: vi.fn(),
    fetchKnowledgeBaseInfo: vi.fn(),
    fetchKnowledgeOutline: vi.fn(),
    fetchKnowledgeGraph: vi.fn(),
    searchVault: vi.fn(),
    searchFiles: vi.fn(),
    libraryDownloadUrl: vi.fn((wsId: string, path: string) => `/api/v1/library/${wsId}/download?path=${path}`),
    mintLibraryPreviewToken: vi.fn(),
  }
})

import { fetchLibraryWorkspaces, fetchLibraryEntries, fetchKnowledgeBaseInfo } from '@/lib/api'
import { LibraryExplorer } from './LibraryExplorer'

const mockedFetchWorkspaces = vi.mocked(fetchLibraryWorkspaces)
const mockedFetchEntries = vi.mocked(fetchLibraryEntries)
const mockedKnowledgeInfo = vi.mocked(fetchKnowledgeBaseInfo)

type Address = { workspaceId?: string; path?: string; folder?: string }

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function entry(name: string, path: string, over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name,
    path,
    is_dir: false,
    is_hidden: false,
    size: 10,
    modified_at: '2026-07-28T10:15:00Z',
    is_text_editable: true,
    ...over,
  }
}

function dir(name: string, path: string): LibraryEntry {
  return entry(name, path, { is_dir: true, is_text_editable: false, size: 0 })
}

function node(id: string, name: string): LibraryWorkspaceNode {
  return { id, name, entry_count: 1 }
}

/** Listings per (workspace, folder). */
function serve(map: Record<string, Record<string, LibraryEntry[]>>) {
  mockedFetchEntries.mockImplementation(async (wsId: string, folder?: string) => map[wsId]?.[folder ?? ''] ?? [])
}

function renderAddressed(address: Address) {
  const client = makeClient()
  const onAddressChange = vi.fn()
  const tree = (a: Address) => (
    <QueryClientProvider client={client}>
      <LibraryExplorer address={a} onAddressChange={onAddressChange} />
    </QueryClientProvider>
  )
  const utils = render(tree(address))
  return {
    onAddressChange,
    navigateTo: (next: Address) => utils.rerender(tree(next)),
  }
}

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
  useKnowledgeIndexStore.setState({ byCollection: {} })
  mockedKnowledgeInfo.mockResolvedValue({
    workspace_id: 'ws-1',
    root_path: '',
    is_knowledge_base: false,
    marker: 'none',
  })
  mockedFetchWorkspaces.mockResolvedValue([node('ws-1', 'UAT Build'), node('ws-2', 'My Workspace')])
})

describe('LibraryExplorer — addressed navigation (D-108, D-64, D-36)', () => {
  it('D-108: a same-route change of `folder=` (no file selected) moves the listing to that folder', async () => {
    serve({
      'ws-1': {
        'UAT Vault/Assets': [entry('page.html', 'UAT Vault/Assets/page.html')],
        'UAT Vault/Dashboards': [entry('Board.md', 'UAT Vault/Dashboards/Board.md')],
      },
    })
    const { navigateTo } = renderAddressed({ workspaceId: 'ws-1', folder: 'UAT Vault/Assets' })
    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/Assets/page.html')).toBeInTheDocument())

    navigateTo({ workspaceId: 'ws-1', folder: 'UAT Vault/Dashboards' })

    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/Dashboards/Board.md')).toBeInTheDocument())
    expect(screen.queryByTestId('library-row-UAT Vault/Assets/page.html')).not.toBeInTheDocument()
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-1', 'UAT Vault/Dashboards', false)
  })

  it('D-64: switching workspace by URL drops the previous workspace’s folder', async () => {
    serve({
      'ws-1': { 'UAT Vault/Projects': [entry('Alpha.md', 'UAT Vault/Projects/Alpha.md')] },
      'ws-2': { '': [entry('readme.md', 'readme.md')] },
    })
    const { navigateTo } = renderAddressed({ workspaceId: 'ws-1', folder: 'UAT Vault/Projects' })
    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/Projects/Alpha.md')).toBeInTheDocument())

    navigateTo({ workspaceId: 'ws-2' })

    // The new workspace is listed from ITS root, never from the old folder.
    await waitFor(() => expect(screen.getByTestId('library-row-readme.md')).toBeInTheDocument())
    expect(mockedFetchEntries).toHaveBeenCalledWith('ws-2', '', false)
    expect(mockedFetchEntries).not.toHaveBeenCalledWith('ws-2', 'UAT Vault/Projects', false)
    expect(screen.queryByText('Projects')).not.toBeInTheDocument()
  })

  it('D-36: a `path=` that names a FOLDER opens that folder instead of announcing it was not found', async () => {
    serve({
      'ws-1': {
        '': [dir('UAT Vault', 'UAT Vault'), entry('loose.md', 'loose.md')],
        'UAT Vault': [entry('Home.md', 'UAT Vault/Home.md')],
      },
    })
    const { onAddressChange } = renderAddressed({ workspaceId: 'ws-1', path: 'UAT Vault' })

    await waitFor(() => expect(screen.getByTestId('library-row-UAT Vault/Home.md')).toBeInTheDocument())
    expect(screen.queryByTestId('library-deeplink-unresolved')).not.toBeInTheDocument()
    // The URL is brought in line with the screen: the folder is browsed, no file is selected.
    expect(onAddressChange).toHaveBeenCalledWith({ workspaceId: 'ws-1', path: undefined })
  })

  it('a `path=` that names a genuinely missing file still gets the honest banner (regression guard)', async () => {
    serve({ 'ws-1': { '': [dir('UAT Vault', 'UAT Vault')] } })
    renderAddressed({ workspaceId: 'ws-1', path: 'ghost.md' })
    await waitFor(() => expect(screen.getByTestId('library-deeplink-unresolved')).toHaveTextContent(/"ghost.md" was not found/))
  })
})

// UAT D-122 (2026-09-13): an `.obsidian` mount never said an import was needed.
describe('LibraryExplorer — D-122 an un-imported Obsidian vault says so', () => {
  it('shows the import notice for marker=obsidian', async () => {
    serve({ 'ws-1': { '': [entry('Home.md', 'Home.md')] } })
    mockedKnowledgeInfo.mockResolvedValue({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: true,
      marker: 'obsidian',
      collection_id: 'kb_obs',
    })
    renderAddressed({ workspaceId: 'ws-1' })
    const notice = await screen.findByTestId('library-obsidian-not-imported')
    expect(notice).toHaveTextContent(/not been imported/)
    expect(notice).toHaveTextContent(/records import-obsidian/)
  })

  it('shows nothing of the sort for an Omnipus vault (control)', async () => {
    serve({ 'ws-1': { '': [entry('Home.md', 'Home.md')] } })
    mockedKnowledgeInfo.mockResolvedValue({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: true,
      marker: 'omnipus_vault',
      collection_id: 'kb_1',
    })
    renderAddressed({ workspaceId: 'ws-1' })
    await waitFor(() => expect(screen.getByTestId('library-row-Home.md')).toBeInTheDocument())
    await waitFor(() => expect(mockedKnowledgeInfo).toHaveBeenCalled())
    expect(screen.queryByTestId('library-obsidian-not-imported')).not.toBeInTheDocument()
  })
})

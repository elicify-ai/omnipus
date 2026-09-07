// LibraryEntryRow icon-kind selection (C3,
// docs/internal/specs/library-b-c-design-2026-09-07.md §"Icon system —
// LOCKED"). Row-menu/mount-badge behaviour is covered by
// LibraryMounts.test.tsx; this file covers ONLY which of the four locked
// icons (Workspace/Vault/Folder/Mount) — plus the Phosphor file-type icon —
// a row picks, and that vault detection is a passive query-cache read (never
// a fetch of its own).
import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { LibraryEntryRow } from './LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'
import type { KnowledgeBaseInfo } from '@/lib/api/generated/openapi-types'

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

function knowledgeInfo(over: Partial<KnowledgeBaseInfo> = {}): KnowledgeBaseInfo {
  return {
    root_path: 'knowledge',
    is_knowledge_base: true,
    marker: 'omnipus_vault',
    collection_id: 'col-1',
    ...over,
  } as KnowledgeBaseInfo
}

function renderRow(e: LibraryEntry, client: QueryClient = new QueryClient()) {
  const props = {
    workspaceId: 'ws-1',
    entry: e,
    selected: false,
    onOpenDirectory: vi.fn(),
    onSelectFile: vi.fn(),
    onDownload: vi.fn(),
    onRename: vi.fn(),
    onTransfer: vi.fn(),
    onDelete: vi.fn(),
    onUnmount: vi.fn(),
  }
  render(
    <QueryClientProvider client={client}>
      <LibraryEntryRow {...props} />
    </QueryClientProvider>,
  )
  return props
}

describe('LibraryEntryRow icon selection', () => {
  it('an ordinary, never-visited directory renders FolderIcon', () => {
    renderRow(entry({ path: 'drafts' }))
    expect(screen.getByRole('img', { hidden: true, name: 'Folder' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { hidden: true, name: 'Vault' })).toBeNull()
  })

  it('a directory the operator already opened this session (cached is_knowledge_base=true) renders VaultIcon', () => {
    const client = new QueryClient()
    // Simulates KnowledgePanel having already asked, and cached the answer,
    // for this exact folder while the operator was browsing INSIDE it — the
    // signal LibraryEntryRow reuses passively, never a fetch of its own.
    client.setQueryData(['knowledge-base-info', 'ws-1', 'knowledge'], knowledgeInfo())
    renderRow(entry({ name: 'Knowledge', path: 'knowledge' }), client)
    expect(screen.getByRole('img', { hidden: true, name: 'Vault' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { hidden: true, name: 'Folder' })).toBeNull()
  })

  it('a directory cached as is_knowledge_base=false stays FolderIcon (an ordinary-folder answer is a real answer)', () => {
    const client = new QueryClient()
    client.setQueryData(
      ['knowledge-base-info', 'ws-1', 'drafts'],
      knowledgeInfo({ is_knowledge_base: false, marker: 'none', collection_id: undefined }),
    )
    renderRow(entry({ path: 'drafts' }), client)
    expect(screen.getByRole('img', { hidden: true, name: 'Folder' })).toBeInTheDocument()
  })

  it('a mounted directory renders MountIcon regardless of any cached vault info', () => {
    const client = new QueryClient()
    client.setQueryData(['knowledge-base-info', 'ws-1', 'team-drive'], knowledgeInfo())
    renderRow(
      entry({
        name: 'Team Drive',
        path: 'team-drive',
        mount: { name: 'team-drive', host_path: '/Users/dana/Sync', broad: false },
      } as Partial<LibraryEntry>),
      client,
    )
    expect(screen.getByRole('img', { hidden: true, name: 'Mount' })).toBeInTheDocument()
    expect(screen.queryByRole('img', { hidden: true, name: 'Vault' })).toBeNull()
  })

  it('never issues a fetch of its own to determine vault status', () => {
    const client = new QueryClient()
    const fetchSpy = vi.spyOn(client, 'fetchQuery')
    renderRow(entry({ path: 'drafts' }), client)
    expect(fetchSpy).not.toHaveBeenCalled()
  })

  it('a file renders its Phosphor file-type icon, not one of the four container icons', () => {
    renderRow(entry({ name: 'notes.md', path: 'notes.md', is_dir: false }))
    expect(screen.queryByRole('img', { hidden: true, name: 'Folder' })).toBeNull()
    expect(screen.queryByRole('img', { hidden: true, name: 'Vault' })).toBeNull()
    expect(screen.queryByRole('img', { hidden: true, name: 'Mount' })).toBeNull()
    expect(screen.queryByRole('img', { hidden: true, name: 'Workspace' })).toBeNull()
  })
})

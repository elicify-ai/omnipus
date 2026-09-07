// LibraryNewVaultDialog.test.tsx — the New vault dialog (feature C2).
//
// Covers: client-side name validation, calling createVault with the right
// (workspaceId, {name, parent_rel_path}) shape, landing the user in the new
// vault on success (onCreated), and the honest 409-collision message.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { LibraryNewVaultDialog } from './LibraryNewVaultDialog'
import type { LibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    createVault: vi.fn(),
  }
})

import { createVault, ApiError } from '@/lib/api'

const mockedCreateVault = vi.mocked(createVault)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const workspaces = [
  { id: 'ws-1', name: 'Research' },
  { id: 'ws-2', name: 'Ops' },
]

function makeEntry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'Field notes',
    path: 'Field notes',
    is_dir: true,
    is_hidden: false,
    size: 0,
    modified_at: '2026-08-13T10:00:00Z',
    is_text_editable: false,
    ...over,
  } as LibraryEntry
}

function renderDialog(over: { defaultWorkspaceId?: string | null; onCreated?: (w: string, e: LibraryEntry) => void } = {}) {
  const onOpenChange = vi.fn()
  const onCreated = over.onCreated ?? vi.fn()
  render(
    <QueryClientProvider client={makeClient()}>
      <LibraryNewVaultDialog
        open
        onOpenChange={onOpenChange}
        workspaces={workspaces}
        defaultWorkspaceId={over.defaultWorkspaceId ?? 'ws-1'}
        onCreated={onCreated}
      />
    </QueryClientProvider>,
  )
  return { onOpenChange, onCreated }
}

beforeEach(() => {
  useUiStore.setState({ toasts: [] })
  mockedCreateVault.mockReset()
})

describe('LibraryNewVaultDialog', () => {
  it('disables Create until a valid name is entered', async () => {
    renderDialog()
    expect(screen.getByTestId('library-new-vault-confirm')).toBeDisabled()

    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    expect(screen.getByTestId('library-new-vault-confirm')).not.toBeDisabled()
  })

  it('rejects a name containing a path separator', async () => {
    renderDialog()
    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'a/b')
    expect(screen.getByTestId('library-new-vault-name-slash')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-vault-confirm')).toBeDisabled()
  })

  it('rejects a folder path containing ".."', async () => {
    renderDialog()
    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.type(screen.getByTestId('library-new-vault-folder-input'), '../escape')
    expect(screen.getByTestId('library-new-vault-folder-traversal')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-vault-confirm')).toBeDisabled()
  })

  it('defaults Location to the current workspace', () => {
    renderDialog({ defaultWorkspaceId: 'ws-2' })
    expect(screen.getByTestId('library-new-vault-workspace-select')).toHaveTextContent('Ops')
  })

  it('calls createVault with the workspace, name, and folder, then lands in the new vault', async () => {
    const created = makeEntry({ path: 'projects/Field notes' })
    mockedCreateVault.mockResolvedValue(created)
    const { onOpenChange, onCreated } = renderDialog()

    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.type(screen.getByTestId('library-new-vault-folder-input'), 'projects')
    await userEvent.click(screen.getByTestId('library-new-vault-confirm'))

    await waitFor(() => expect(mockedCreateVault).toHaveBeenCalledWith('ws-1', {
      name: 'Field notes',
      parent_rel_path: 'projects',
    }))
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith('ws-1', created))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it('omits parent_rel_path when the folder field is left blank', async () => {
    mockedCreateVault.mockResolvedValue(makeEntry())
    renderDialog()

    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.click(screen.getByTestId('library-new-vault-confirm'))

    await waitFor(() => expect(mockedCreateVault).toHaveBeenCalledWith('ws-1', {
      name: 'Field notes',
      parent_rel_path: undefined,
    }))
  })

  it('shows an honest, specific message on a 409 name collision', async () => {
    mockedCreateVault.mockRejectedValue(new ApiError(409, 'Conflict', { body: '{"error":"an entry already exists at that path"}' }))
    renderDialog()

    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.click(screen.getByTestId('library-new-vault-confirm'))

    expect(await screen.findByTestId('library-new-vault-error')).toHaveTextContent(
      'A folder or vault with that name already exists here.',
    )
  })

  it('surfaces a non-409 server error verbatim rather than the collision message', async () => {
    mockedCreateVault.mockRejectedValue(
      new ApiError(400, 'Bad request', { body: '{"error":"invalid parent_rel_path"}' }),
    )
    renderDialog()

    await userEvent.type(screen.getByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.click(screen.getByTestId('library-new-vault-confirm'))

    const banner = await screen.findByTestId('library-new-vault-error')
    expect(banner).toHaveTextContent('invalid parent_rel_path')
    expect(banner).not.toHaveTextContent('already exists here')
  })
})

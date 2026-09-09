// LibraryCreateMenu.test.tsx — the unified "+" create control (feature C2;
// KB-4 fix, 2026-09-08).
//
// Covers: every action requires a workspace to be open (none is offered at
// the virtual root — "New workspace" is gone entirely, and "New knowledge
// base" now joins the other workspace-scoped actions rather than being a
// global action with its own picker); disabled rather than hidden when the
// current folder/mount state forbids them; each callback prop actually fires
// on click.

import type { ComponentProps } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { LibraryCreateMenu } from './LibraryCreateMenu'
import { createVault, type LibraryEntry } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    createVault: vi.fn(),
  }
})

const mockedCreateVault = vi.mocked(createVault)

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderMenu(over: Partial<ComponentProps<typeof LibraryCreateMenu>> = {}) {
  const props = {
    workspaceId: 'ws-1',
    browsedDir: '',
    isReservedLibraryDir: false,
    mountedCount: 0,
    uploadPending: false,
    onNewFolder: vi.fn(),
    onAddMount: vi.fn(),
    onManageMounts: vi.fn(),
    onUpload: vi.fn(),
    onVaultCreated: vi.fn(),
    ...over,
  }
  render(
    <QueryClientProvider client={makeClient()}>
      <LibraryCreateMenu {...props} />
    </QueryClientProvider>,
  )
  return props
}

beforeEach(() => {
  useUiStore.setState({ toasts: [] })
  mockedCreateVault.mockReset()
})

describe('LibraryCreateMenu', () => {
  it('never offers "New workspace" — the sidebar is the only entry point for that', async () => {
    renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    expect(screen.queryByTestId('library-create-menu-new-workspace')).not.toBeInTheDocument()
    expect(screen.queryByText('New workspace')).not.toBeInTheDocument()
  })

  it('hides every action at the virtual root, including New knowledge base', async () => {
    renderMenu({ workspaceId: null })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

    expect(screen.queryByTestId('library-create-menu-new-vault')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-create-menu-new-folder')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-create-menu-upload')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-create-menu-add-mount')).not.toBeInTheDocument()
    expect(screen.queryByTestId('library-create-menu-manage-mounts')).not.toBeInTheDocument()
  })

  it('offers every action once a workspace is open', async () => {
    renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

    for (const testId of [
      'library-create-menu-new-vault',
      'library-create-menu-new-folder',
      'library-create-menu-upload',
      'library-create-menu-add-mount',
      'library-create-menu-manage-mounts',
    ]) {
      expect(screen.getByTestId(testId)).toBeInTheDocument()
    }
  })

  it('disables New knowledge base, New folder, and Upload inside the reserved .library folder', async () => {
    renderMenu({ isReservedLibraryDir: true })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

    expect(screen.getByTestId('library-create-menu-new-vault')).toHaveAttribute('data-disabled')
    expect(screen.getByTestId('library-create-menu-new-folder')).toHaveAttribute('data-disabled')
    expect(screen.getByTestId('library-create-menu-upload')).toHaveAttribute('data-disabled')
    // Add mount is unaffected by the reserved-folder rule — it targets the
    // workspace root, not the browsed directory.
    expect(screen.getByTestId('library-create-menu-add-mount')).not.toHaveAttribute('data-disabled')
  })

  it('disables Manage mounted folders when nothing is mounted', async () => {
    renderMenu({ mountedCount: 0 })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    expect(screen.getByTestId('library-create-menu-manage-mounts')).toHaveAttribute('data-disabled')
  })

  it('enables Manage mounted folders and shows the count once something is mounted', async () => {
    renderMenu({ mountedCount: 3 })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    const item = screen.getByTestId('library-create-menu-manage-mounts')
    expect(item).not.toHaveAttribute('data-disabled')
    expect(item).toHaveTextContent('Manage 3 mounted folders')
  })

  it('calls onNewFolder when New folder is selected', async () => {
    const props = renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-new-folder'))
    expect(props.onNewFolder).toHaveBeenCalledTimes(1)
  })

  it('calls onUpload when Upload files is selected', async () => {
    const props = renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-upload'))
    expect(props.onUpload).toHaveBeenCalledTimes(1)
  })

  it('calls onAddMount when Add a folder from your Mac is selected', async () => {
    const props = renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-add-mount'))
    expect(props.onAddMount).toHaveBeenCalledTimes(1)
  })

  it('calls onManageMounts when Manage mounted folders is selected', async () => {
    const props = renderMenu({ mountedCount: 1 })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-manage-mounts'))
    expect(props.onManageMounts).toHaveBeenCalledTimes(1)
  })

  it('opens the New knowledge base dialog when New knowledge base is selected, showing only a name field — no Location display (WL-3)', async () => {
    renderMenu({ browsedDir: 'projects' })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-new-vault'))

    expect(await screen.findByTestId('library-new-vault-dialog')).toBeInTheDocument()
    expect(screen.getByTestId('library-new-vault-name-input')).toBeInTheDocument()
    expect(screen.queryByTestId('library-new-vault-destination')).not.toBeInTheDocument()
  })

  it('still seeds the dialog with the current workspace and browsed folder even though neither is displayed', async () => {
    const created = { name: 'Field notes', path: 'projects/Field notes', is_dir: true } as LibraryEntry
    mockedCreateVault.mockResolvedValue(created)
    renderMenu({ workspaceId: 'ws-1', browsedDir: 'projects' })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-new-vault'))

    await userEvent.type(await screen.findByTestId('library-new-vault-name-input'), 'Field notes')
    await userEvent.click(screen.getByTestId('library-new-vault-confirm'))

    await waitFor(() =>
      expect(mockedCreateVault).toHaveBeenCalledWith('ws-1', {
        name: 'Field notes',
        parent_rel_path: 'projects',
      }),
    )
  })
})

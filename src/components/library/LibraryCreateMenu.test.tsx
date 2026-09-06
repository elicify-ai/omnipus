// LibraryCreateMenu.test.tsx — the unified "+" create control (feature C2).
//
// Covers: the six actions collapse into one menu, scoped to the current
// location (workspace-only actions absent at the virtual root; disabled
// rather than hidden when the current folder/mount state forbids them), and
// each callback prop actually fires on click.

import type { ComponentProps } from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import { LibraryCreateMenu } from './LibraryCreateMenu'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([]),
    createWorkspace: vi.fn(),
    createVault: vi.fn(),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

const workspaces = [
  { id: 'ws-1', name: 'Research' },
  { id: 'ws-2', name: 'Ops' },
]

function renderMenu(over: Partial<ComponentProps<typeof LibraryCreateMenu>> = {}) {
  const props = {
    workspaceId: 'ws-1',
    workspaces,
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
})

describe('LibraryCreateMenu', () => {
  it('always offers New vault and New workspace, even at the virtual root', async () => {
    renderMenu({ workspaceId: null })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

    expect(screen.getByTestId('library-create-menu-new-vault')).toBeInTheDocument()
    expect(screen.getByTestId('library-create-menu-new-workspace')).toBeInTheDocument()
  })

  it('hides workspace-scoped actions at the virtual root', async () => {
    renderMenu({ workspaceId: null })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

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
      'library-create-menu-new-workspace',
      'library-create-menu-new-folder',
      'library-create-menu-upload',
      'library-create-menu-add-mount',
      'library-create-menu-manage-mounts',
    ]) {
      expect(screen.getByTestId(testId)).toBeInTheDocument()
    }
  })

  it('disables New folder and Upload inside the reserved .library folder', async () => {
    renderMenu({ isReservedLibraryDir: true })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))

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

  it('disables New vault when there are no workspaces to target', async () => {
    renderMenu({ workspaceId: null, workspaces: [] })
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    expect(screen.getByTestId('library-create-menu-new-vault')).toHaveAttribute('data-disabled')
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

  it('opens the New vault dialog when New vault is selected', async () => {
    renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-new-vault'))
    expect(await screen.findByTestId('library-new-vault-dialog')).toBeInTheDocument()
  })

  it('opens the New workspace slide-over when New workspace is selected', async () => {
    renderMenu()
    await userEvent.click(screen.getByTestId('library-create-menu-trigger'))
    await userEvent.click(screen.getByTestId('library-create-menu-new-workspace'))
    expect(await screen.findByText('New workspace')).toBeInTheDocument()
  })
})

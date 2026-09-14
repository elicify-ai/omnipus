// LibraryDialogs.uat.test.tsx — regression tests for the 2026-09-13 UAT
// findings against the Library's create/transfer/mount dialogs:
//   D-116  New knowledge base refuses a dot-prefixed (hidden) name
//   D-128  New folder names traversal before the slash rule for "../x"
//   D-120  Move/Copy to another workspace warns about what is lost
//   D-127  Add-mount clears the server's error once the path is edited
//   D-117  Add-mount looks up a typed path's verdict; broad needs a 2nd click
//   D-104  New folder / New knowledge base return focus to the Create menu
//
// Each negative assertion is paired with a positive control in the same test
// so a dialog that simply disabled everything could not pass.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import React, { useState } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useUiStore } from '@/store/ui'
import type { LibraryEntry, LibraryWorkspaceNode } from '@/lib/api'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    createVault: vi.fn(),
    fetchLibraryEntries: vi.fn(async () => []),
    fetchHostFolders: vi.fn(),
  }
})


// Radix Select cannot be opened under jsdom (no pointer-capture APIs); the
// same native stub AcceptanceCriteriaEditor.test.tsx uses lets a test pick a
// destination through the dialog's REAL onValueChange path.
vi.mock('@/components/ui/select', () => {
  type SelectProps = { value?: string; onValueChange?: (value: string) => void; children?: React.ReactNode }
  type SelectItemProps = { value: string; children?: React.ReactNode }
  const SelectCtx = React.createContext<((v: string) => void) | undefined>(undefined)
  const Select = ({ onValueChange, children }: SelectProps) =>
    React.createElement(SelectCtx.Provider, { value: onValueChange }, children)
  const SelectTrigger = ({ children, ...rest }: { children?: React.ReactNode; [key: string]: unknown }) =>
    React.createElement('div', rest, children)
  const SelectValue = () => React.createElement('span', {})
  const SelectContent = ({ children }: { children?: React.ReactNode }) =>
    React.createElement('div', { role: 'listbox' }, children)
  const SelectItem = ({ value, children }: SelectItemProps) => {
    const onValueChange = React.useContext(SelectCtx)
    return React.createElement('div', { role: 'option', 'data-value': value, onClick: () => onValueChange?.(value) }, children)
  }
  return { Select, SelectTrigger, SelectValue, SelectContent, SelectItem }
})

import { fetchHostFolders } from '@/lib/api'
import { LibraryNewVaultDialog } from './LibraryNewVaultDialog'
import { LibraryNewFolderDialog } from './LibraryNewFolderDialog'
import { LibraryTransferDialog } from './LibraryTransferDialog'
import { LibraryAddMountDialog } from './LibraryAddMountDialog'

const mockedHostFolders = vi.mocked(fetchHostFolders)

function client() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function entry(over: Partial<LibraryEntry> = {}): LibraryEntry {
  return {
    name: 'notes.md',
    path: 'notes.md',
    is_dir: false,
    is_hidden: false,
    size: 5,
    modified_at: '2026-08-13T10:00:00Z',
    is_text_editable: true,
    ...over,
  } as LibraryEntry
}

const WORKSPACES: LibraryWorkspaceNode[] = [
  { id: 'ws-1', name: 'UAT Build' } as LibraryWorkspaceNode,
  { id: 'ws-2', name: 'My Workspace' } as LibraryWorkspaceNode,
]

beforeEach(() => {
  vi.clearAllMocks()
  useUiStore.setState({ toasts: [] })
})

describe('D-116 — New knowledge base refuses a hidden (dot-prefixed) name', () => {
  it('shows the reason and disables Create for ".hidden"; a plain name is accepted', () => {
    render(
      <QueryClientProvider client={client()}>
        <LibraryNewVaultDialog open onOpenChange={vi.fn()} workspaceId="ws-1" parentPath="" onCreated={vi.fn()} />
      </QueryClientProvider>,
    )
    const input = screen.getByTestId('library-new-vault-name-input')

    fireEvent.change(input, { target: { value: '.hidden' } })
    expect(screen.getByTestId('library-new-vault-name-hidden')).toHaveTextContent(/hidden folder/i)
    expect(screen.getByTestId('library-new-vault-confirm')).toBeDisabled()

    fireEvent.change(input, { target: { value: 'Field notes' } })
    expect(screen.queryByTestId('library-new-vault-name-hidden')).not.toBeInTheDocument()
    expect(screen.getByTestId('library-new-vault-confirm')).not.toBeDisabled()
  })
})

describe('D-128 — New folder names the traversal rule for "../x"', () => {
  it('"../x" is refused as traversal, not as a slash; "a/b" is still the slash rule', () => {
    render(
      <LibraryNewFolderDialog open onOpenChange={vi.fn()} siblingNames={new Set()} onSubmit={vi.fn()} isPending={false} />,
    )
    const input = screen.getByTestId('library-new-folder-input')

    fireEvent.change(input, { target: { value: '../x' } })
    expect(screen.getByTestId('library-new-folder-traversal')).toBeInTheDocument()
    expect(screen.queryByTestId('library-new-folder-slash')).not.toBeInTheDocument()
    expect(screen.getByTestId('library-new-folder-confirm')).toBeDisabled()

    fireEvent.change(input, { target: { value: 'a/b' } })
    expect(screen.getByTestId('library-new-folder-slash')).toBeInTheDocument()
    expect(screen.queryByTestId('library-new-folder-traversal')).not.toBeInTheDocument()
  })
})

describe('D-120 — Move/Copy to another workspace warns', () => {
  function renderTransfer(mode: 'move' | 'copy') {
    return render(
      <QueryClientProvider client={client()}>
        <LibraryTransferDialog
          open
          onOpenChange={vi.fn()}
          mode={mode}
          entry={entry()}
          sourceWorkspaceId="ws-1"
          workspaces={WORKSPACES}
          onSubmit={vi.fn()}
          isPending={false}
        />
      </QueryClientProvider>,
    )
  }

  it('no warning for a same-workspace move; a warning naming what is lost once another workspace is chosen', async () => {
    renderTransfer('move')
    expect(screen.queryByTestId('library-transfer-workspace-warning')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('option', { name: 'My Workspace' }))
    await waitFor(() =>
      expect(screen.getByTestId('library-transfer-workspace-warning')).toHaveTextContent(/leave this workspace/i),
    )
    expect(screen.getByTestId('library-transfer-workspace-warning')).toHaveTextContent(/stop resolving/i)

    // And back to this workspace, the warning goes away.
    fireEvent.click(screen.getByRole('option', { name: 'UAT Build' }))
    await waitFor(() => expect(screen.queryByTestId('library-transfer-workspace-warning')).not.toBeInTheDocument())
  })

  it('a cross-workspace COPY says the original stays', async () => {
    renderTransfer('copy')
    fireEvent.click(screen.getByRole('option', { name: 'My Workspace' }))
    await waitFor(() =>
      expect(screen.getByTestId('library-transfer-workspace-warning')).toHaveTextContent(/original stays here/i),
    )
  })
})

describe('D-127 / D-117 — Add a folder from your Mac', () => {
  it('D-127: the server error is shown for the path it was about, and hidden once the path is edited', async () => {
    mockedHostFolders.mockResolvedValue({ path: '/', entries: [] })
    function Host() {
      const [error, setError] = useState<string>()
      return (
        <LibraryAddMountDialog
          open
          onOpenChange={vi.fn()}
          onConfirm={() => setError('That path has no folder name to use.')}
          isPending={false}
          {...(error !== undefined ? { error } : {})}
        />
      )
    }
    render(<Host />)
    const input = screen.getByTestId('library-add-mount-path')
    fireEvent.change(input, { target: { value: '/' } })
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    await waitFor(() => expect(screen.getByTestId('library-add-mount-error')).toBeInTheDocument())

    fireEvent.change(input, { target: { value: '/tmp' } })
    expect(screen.queryByTestId('library-add-mount-error')).not.toBeInTheDocument()
  })

  it('D-117: a typed broad path is looked up, warned about, and needs a second explicit click; a scoped one submits at once', async () => {
    mockedHostFolders.mockImplementation(async (path?: string) => ({
      path: path ?? '/',
      entries: [
        { name: 'tmp', path: '/tmp', mountable: true, broad: true, reason: 'This is a system directory.' },
        { name: 'proj', path: '/proj', mountable: true, broad: false },
      ],
    }))
    const onConfirm = vi.fn()
    render(<LibraryAddMountDialog open onOpenChange={vi.fn()} onConfirm={onConfirm} isPending={false} />)
    const input = screen.getByTestId('library-add-mount-path')

    fireEvent.change(input, { target: { value: '/tmp' } })
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    await waitFor(() => expect(screen.getByTestId('library-add-mount-broad')).toBeInTheDocument())
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getByTestId('library-add-mount-confirm')).toHaveTextContent('Add anyway')

    // The second click — "Add anyway" — is the acknowledgement and the submit.
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('/tmp'))
    expect(onConfirm).toHaveBeenCalledTimes(1)

    // Positive control: a scoped path goes straight through.
    onConfirm.mockClear()
    fireEvent.change(input, { target: { value: '/proj' } })
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('/proj'))
    expect(screen.queryByTestId('library-add-mount-broad')).not.toBeInTheDocument()
  })

  // Claude review 2026-09-14, cut-list: the verdict lookup matched the TYPED
  // string against the listing, so any non-canonical spelling ("/tmp/",
  // "/tmp/.", "/private/tmp/.." on a Mac aside — here "/tmp/../tmp") dodged
  // the breadth gate entirely: no banner, no second click, straight to
  // onConfirm. The dialog canonicalizes lexically before the check now, and
  // submits the canonical path, so every spelling of the same folder gets
  // the same verdict. (System dirs stay caught server-side regardless.)
  it('a non-canonical spelling of a broad path gets the same verdict and the same second click', async () => {
    mockedHostFolders.mockImplementation(async (path?: string) => ({
      path: path ?? '/',
      entries: [{ name: 'tmp', path: '/tmp', mountable: true, broad: true, reason: 'This is a system directory.' }],
    }))
    const onConfirm = vi.fn()
    render(<LibraryAddMountDialog open onOpenChange={vi.fn()} onConfirm={onConfirm} isPending={false} />)
    const input = screen.getByTestId('library-add-mount-path')

    for (const spelling of ['/tmp/', '/tmp/.', '/tmp/../tmp']) {
      onConfirm.mockClear()
      fireEvent.change(input, { target: { value: spelling } })
      fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
      await waitFor(() => expect(screen.getByTestId('library-add-mount-broad')).toBeInTheDocument())
      expect(onConfirm).not.toHaveBeenCalled()
      expect(screen.getByTestId('library-add-mount-confirm')).toHaveTextContent('Add anyway')

      fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
      await waitFor(() => expect(onConfirm).toHaveBeenCalledWith('/tmp'))
      expect(onConfirm).toHaveBeenCalledTimes(1)
    }
  })
})

// D-117, RESPONSE half (2026-09-14 fix round): the pre-submit banners above
// cover what the folder LISTING knows. This block covers what the SERVER's
// own answer says — the 201 body's `warning` (a broad grant the server let
// through) and the 403 body's reason (a grant the server refused). Both must
// render inside this dialog, not in a toast that auto-dismisses: the operator
// just granted (or was refused) disk access and needs to read what happened.
describe('D-117 — Add-mount renders the SERVER response (broad warning / 403 refusal)', () => {
  it('a 201 whose body carries `warning` keeps the dialog open, shows it as the broad banner, and Done acknowledges it', () => {
    const onAcknowledge = vi.fn()
    render(
      <LibraryAddMountDialog
        open
        onOpenChange={vi.fn()}
        onConfirm={vi.fn()}
        isPending={false}
        createdWarning={'mounting "/Users/operator" makes every file under it writable by any agent on this workspace'}
        onAcknowledgeWarning={onAcknowledge}
      />,
    )

    const banner = screen.getByTestId('library-add-mount-dialog-broad')
    expect(banner).toHaveTextContent('every file under it writable')
    // The pre-submit verdict banner is a DIFFERENT surface — it must not fire
    // for a server response (nothing was looked up in a listing).
    expect(screen.queryByTestId('library-add-mount-broad')).not.toBeInTheDocument()

    // The mount already happened; the only sane footer is the acknowledgement.
    const confirm = screen.getByTestId('library-add-mount-confirm')
    expect(confirm).toHaveTextContent('Done')
    fireEvent.click(confirm)
    expect(onAcknowledge).toHaveBeenCalledTimes(1)
  })

  it('a 403 refusal reason renders as the refused banner, not the generic error, and clears when the path is edited', async () => {
    mockedHostFolders.mockResolvedValue({ path: '/', entries: [] })
    function Host() {
      const [refusal, setRefusal] = useState<string>()
      return (
        <LibraryAddMountDialog
          open
          onOpenChange={vi.fn()}
          onConfirm={() => setRefusal('mounting "/Users/operator/.omnipus" is refused: it is this installation’s own data directory.')}
          isPending={false}
          {...(refusal !== undefined ? { refusal } : {})}
        />
      )
    }
    render(<Host />)

    fireEvent.change(screen.getByTestId('library-add-mount-path'), { target: { value: '/Users/operator/.omnipus' } })
    fireEvent.click(screen.getByTestId('library-add-mount-confirm'))
    const banner = await screen.findByTestId('library-add-mount-dialog-refused')
    expect(banner).toHaveTextContent('installation’s own data directory')
    // A refusal is a policy verdict, not a generic transport error.
    expect(screen.queryByTestId('library-add-mount-error')).not.toBeInTheDocument()

    // Same D-127 scoping: the refusal is about the submitted path, so editing
    // the path retires it.
    fireEvent.change(screen.getByTestId('library-add-mount-path'), { target: { value: '/tmp' } })
    await waitFor(() => expect(screen.queryByTestId('library-add-mount-dialog-refused')).not.toBeInTheDocument())
  })
})

describe('D-104 — dialogs return focus to the Create menu trigger', () => {
  it('closing New folder with Escape focuses the Create menu trigger, not <body>', async () => {
    function Host() {
      const [open, setOpen] = useState(true)
      return (
        <>
          <button type="button" data-testid="library-create-menu-trigger">
            +
          </button>
          <LibraryNewFolderDialog open={open} onOpenChange={setOpen} siblingNames={new Set()} onSubmit={vi.fn()} isPending={false} />
        </>
      )
    }
    render(<Host />)
    const dialog = await screen.findByTestId('library-new-folder-dialog')
    await act(async () => {
      fireEvent.keyDown(dialog, { key: 'Escape' })
    })
    await waitFor(() => expect(screen.queryByTestId('library-new-folder-dialog')).not.toBeInTheDocument())
    expect(document.activeElement).toBe(screen.getByTestId('library-create-menu-trigger'))
  })

  it('closing New knowledge base with Escape focuses the Create menu trigger', async () => {
    function Host() {
      const [open, setOpen] = useState(true)
      return (
        <QueryClientProvider client={client()}>
          <button type="button" data-testid="library-create-menu-trigger">
            +
          </button>
          <LibraryNewVaultDialog open={open} onOpenChange={setOpen} workspaceId="ws-1" parentPath="" onCreated={vi.fn()} />
        </QueryClientProvider>
      )
    }
    render(<Host />)
    const dialog = await screen.findByTestId('library-new-vault-dialog')
    await act(async () => {
      fireEvent.keyDown(dialog, { key: 'Escape' })
    })
    await waitFor(() => expect(screen.queryByTestId('library-new-vault-dialog')).not.toBeInTheDocument())
    expect(document.activeElement).toBe(screen.getByTestId('library-create-menu-trigger'))
  })
})

// Tests for the mounted-folder surface in the Library (ADR-063).
//
// The through-line: a mount looks like a folder and is not one. A write inside
// it lands on the operator's real disk, and the control where "Delete" normally
// sits must revoke access instead of removing their files. These tests assert
// the differences a user can actually see and act on.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { LibraryEntryRow } from './LibraryEntryRow'
import { mountNameFromPath } from './libraryMountName'
import type { LibraryEntry } from '@/lib/api'

// LibraryEntryRow reads the knowledge-base-info query cache (C3, a passive
// read for Vault-icon detection — see LibraryEntryRow.tsx), so it now needs a
// QueryClientProvider ancestor even where no query actually runs.
function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

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

const mounted = entry({
  name: 'omnipus-repo',
  path: 'omnipus-repo',
  mount: {
    name: 'omnipus-repo',
    host_path: '/Users/dana/Documents/projects/api',
    broad: false,
  },
} as Partial<LibraryEntry>)

const broadMount = entry({
  name: 'home',
  path: 'home',
  mount: { name: 'home', host_path: '/Users/dana', broad: true },
} as Partial<LibraryEntry>)

function renderRow(e: LibraryEntry, over: Partial<Record<string, unknown>> = {}) {
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
    ...over,
  }
  render(
    <QueryClientProvider client={makeClient()}>
      <LibraryEntryRow {...props} />
    </QueryClientProvider>,
  )
  return props
}

describe('a mounted folder is visibly not an ordinary folder', () => {
  it('shows the real destination in the row rather than behind a tooltip', () => {
    renderRow(mounted)
    // The path IS the reason this entry behaves differently. A grant the
    // operator cannot see is a grant they cannot review.
    expect(screen.getByTestId('library-mount-target-omnipus-repo')).toHaveTextContent(
      '/Users/dana/Documents/projects/api',
    )
    expect(screen.getByTestId('library-mount-badge-omnipus-repo')).toHaveTextContent(/mounted/i)
  })

  it('marks a broad grant distinctly instead of silently accepting it', () => {
    renderRow(broadMount)
    const badge = screen.getByTestId('library-mount-badge-home')
    expect(badge).toHaveTextContent(/broad grant/i)
    expect(screen.getByTestId('library-mount-target-home')).toHaveTextContent(
      /covers your entire home folder/i,
    )
  })

  it('puts no mount marking on an ordinary folder', () => {
    renderRow(entry())
    expect(screen.queryByTestId('library-mount-badge-drafts')).toBeNull()
    expect(screen.queryByTestId('library-mount-target-drafts')).toBeNull()
  })
})

describe('the row menu, where this gets dangerous', () => {
  it('offers Unmount and never Delete on a mount', async () => {
    const user = userEvent.setup()
    const props = renderRow(mounted)

    await user.click(screen.getByTestId('library-row-menu-omnipus-repo'))
    const menu = await screen.findByRole('menu')

    // The whole risk is that "Delete" on this row reads like "Delete" on the
    // row above, while sitting over the operator's real repository.
    expect(within(menu).queryByText(/^Delete$/)).toBeNull()
    expect(screen.queryByTestId('library-row-delete-omnipus-repo')).toBeNull()

    const unmount = screen.getByTestId('library-row-unmount-omnipus-repo')
    expect(unmount).toHaveTextContent(/unmount/i)
    expect(unmount).toHaveTextContent(/files stay/i)

    await user.click(unmount)
    expect(props.onUnmount).toHaveBeenCalledWith(mounted)
    expect(props.onDelete).not.toHaveBeenCalled()
  })

  it('does not offer Move or Copy of the grant itself', async () => {
    const user = userEvent.setup()
    renderRow(mounted)
    await user.click(screen.getByTestId('library-row-menu-omnipus-repo'))
    const menu = await screen.findByRole('menu')

    // Moving or copying a grant has no meaning; showing them and failing later
    // is worse than not offering them.
    expect(within(menu).queryByText(/Move…/)).toBeNull()
    expect(within(menu).queryByText(/Copy…/)).toBeNull()
  })

  it('still offers the full destructive menu on an ordinary folder', async () => {
    const user = userEvent.setup()
    const props = renderRow(entry())

    await user.click(screen.getByTestId('library-row-menu-drafts'))
    const del = screen.getByTestId('library-row-delete-drafts')
    expect(del).toHaveTextContent(/delete/i)

    await user.click(del)
    expect(props.onDelete).toHaveBeenCalled()
    expect(props.onUnmount).not.toHaveBeenCalled()
  })
})

describe('mountNameFromPath', () => {
  it('uses the folder’s own name so the operator is not asked to invent one', () => {
    expect(mountNameFromPath('/Users/dana/Documents/projects/api')).toBe('api')
    expect(mountNameFromPath('/Users/dana/Documents/projects/api/')).toBe('api')
  })

  it('strips characters that could not be a single path segment', () => {
    // The SERVER owns validity; this only avoids proposing something obviously
    // unusable, so a slash or a space cannot smuggle in a second segment.
    expect(mountNameFromPath('/tmp/my project')).toBe('my-project')
    expect(mountNameFromPath('/tmp/.hidden')).toBe('hidden')
  })

  it('returns empty for a path with no folder name to use', () => {
    expect(mountNameFromPath('/')).toBe('')
    expect(mountNameFromPath('')).toBe('')
  })
})

// ── The durable register (ADR-063) ──────────────────────────────────────────
//
// Approving a grant happens once, in a modal, in a moment. Revoking happens
// months later, by someone who has forgotten it exists. Only the second needs a
// permanent home, and this is it.

import { LibraryMountsDialog } from './LibraryMountsDialog'

describe('the mounted-folders register', () => {
  const mounts = [
    mounted,
    entry({
      name: 'home',
      path: 'home',
      mount: { name: 'home', host_path: '/Users/dana', broad: true },
    } as Partial<LibraryEntry>),
  ]

  function renderDialog(over: Record<string, unknown> = {}) {
    const props = {
      open: true,
      onOpenChange: vi.fn(),
      mounts,
      workspaceName: 'Client Project',
      onUnmount: vi.fn(),
      isPending: false,
      ...over,
    }
    render(<LibraryMountsDialog {...props} />)
    return props
  }

  it('lists every grant with its real path, not just its name', () => {
    renderDialog()
    // The name alone does not tell you what you granted.
    expect(screen.getByTestId('library-mounts-row-omnipus-repo')).toHaveTextContent(
      '/Users/dana/Documents/projects/api',
    )
    expect(screen.getByTestId('library-mounts-row-home')).toHaveTextContent('/Users/dana')
  })

  it('flags a broad grant so it cannot hide in the list', () => {
    renderDialog()
    expect(screen.getByTestId('library-mounts-row-home')).toHaveTextContent(/broad grant/i)
    expect(screen.getByTestId('library-mounts-row-omnipus-repo')).not.toHaveTextContent(
      /broad grant/i,
    )
  })

  it('states that files survive, because the control sits where delete normally lives', () => {
    renderDialog()
    expect(screen.getByText(/files stay exactly where they are/i)).toBeInTheDocument()
  })

  it('hands the revoke back to the caller rather than acting on its own', async () => {
    const user = userEvent.setup()
    const props = renderDialog()
    await user.click(screen.getByTestId('library-mounts-unmount-home'))
    expect(props.onUnmount).toHaveBeenCalledWith(mounts[1])
  })

  it('says so plainly when nothing is granted', () => {
    renderDialog({ mounts: [] })
    expect(screen.getByText(/no folders are mounted/i)).toBeInTheDocument()
  })
})

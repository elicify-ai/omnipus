// LibraryEntryRow icon-kind selection (icon-consistency pass, 2026-09-07).
// Row-menu/mount-badge behaviour is covered by LibraryMounts.test.tsx; this
// file covers ONLY which container icon (Books/FolderSimple/MountFolderIcon)
// — plus the Phosphor file-type icon — a row picks, and that vault-ness comes
// from the LISTING'S OWN wire field (LibraryEntry.is_knowledge_base), never a
// react-query cache read.
//
// The repro case for the icon-consistency defect this file guards against:
// a knowledge base used to render as a plain folder unless the SPA had
// already, in THIS session, opened GET .../knowledge for that exact path and
// cached the answer — so a vault right after creation, or after a reload
// evicted the cache, silently downgraded to a plain folder. Renders below
// use is_knowledge_base straight off the entry, with no QueryClientProvider
// at all, precisely the state that used to fail.
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { LibraryEntryRow } from './LibraryEntryRow'
import type { LibraryEntry } from '@/lib/api'

const FOLDER_SIMPLE_REGULAR_D =
  'M216,72H130.67L102.93,51.2a16.12,16.12,0,0,0-9.6-3.2H40A16,16,0,0,0,24,64V200a16,16,0,0,0,16,16H216.89A15.13,15.13,0,0,0,232,200.89V88A16,16,0,0,0,216,72Zm0,128H40V64H93.33L123.2,86.4A8,8,0,0,0,128,88h88Z'
const BOOKS_REGULAR_D =
  'M231.65,194.55,198.46,36.75a16,16,0,0,0-19-12.39L132.65,34.42a16.08,16.08,0,0,0-12.3,19l33.19,157.8A16,16,0,0,0,169.16,224a16.25,16.25,0,0,0,3.38-.36l46.81-10.06A16.09,16.09,0,0,0,231.65,194.55ZM136,50.15c0-.06,0-.09,0-.09l46.8-10,3.33,15.87L139.33,66Zm6.62,31.47,46.82-10.05,3.34,15.9L146,97.53Zm6.64,31.57,46.82-10.06,13.3,63.24-46.82,10.06ZM216,197.94l-46.8,10-3.33-15.87L212.67,182,216,197.85C216,197.91,216,197.94,216,197.94ZM104,32H56A16,16,0,0,0,40,48V208a16,16,0,0,0,16,16h48a16,16,0,0,0,16-16V48A16,16,0,0,0,104,32ZM56,48h48V64H56Zm0,32h48v96H56Zm48,128H56V192h48v16Z'

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

function renderRow(e: LibraryEntry) {
  return render(
    <LibraryEntryRow
      workspaceId="ws-1"
      entry={e}
      selected={false}
      onOpenDirectory={() => {}}
      onSelectFile={() => {}}
      onDownload={() => {}}
      onRename={() => {}}
      onTransfer={() => {}}
      onDelete={() => {}}
      onUnmount={() => {}}
    />,
  )
}

function hasPath(container: HTMLElement, d: string): boolean {
  return Array.from(container.querySelectorAll('path')).some((p) => p.getAttribute('d') === d)
}

describe('LibraryEntryRow icon selection', () => {
  it('a directory with no is_knowledge_base field renders the plain FolderSimple icon', () => {
    const { container } = renderRow(entry({ path: 'drafts' }))
    expect(hasPath(container, FOLDER_SIMPLE_REGULAR_D)).toBe(true)
    expect(hasPath(container, BOOKS_REGULAR_D)).toBe(false)
  })

  // THE REPRO CASE (Decision 2): is_knowledge_base=true on the wire payload
  // must render the Books icon with NO react-query cache primed at all —
  // no QueryClientProvider even wraps this render. Before the fix this was
  // impossible to express: the row read the answer from
  // queryClient.getQueryData(['knowledge-base-info', ...]), so a directory
  // never opened this session had no way to say "I am a vault" and fell
  // back to FolderSimple regardless of what the server actually knew.
  it('a directory whose wire payload says is_knowledge_base=true renders Books, with no cache involved', () => {
    const { container } = renderRow(
      entry({ name: 'UAT Vault', path: 'UAT Vault', is_knowledge_base: true } as Partial<LibraryEntry>),
    )
    expect(hasPath(container, BOOKS_REGULAR_D)).toBe(true)
    expect(hasPath(container, FOLDER_SIMPLE_REGULAR_D)).toBe(false)
  })

  it('a directory whose wire payload says is_knowledge_base=false stays FolderSimple (an ordinary-folder answer is a real answer)', () => {
    const { container } = renderRow(
      entry({ path: 'drafts', is_knowledge_base: false } as Partial<LibraryEntry>),
    )
    expect(hasPath(container, FOLDER_SIMPLE_REGULAR_D)).toBe(true)
    expect(hasPath(container, BOOKS_REGULAR_D)).toBe(false)
  })

  it('a mounted directory renders MountFolderIcon regardless of is_knowledge_base', () => {
    const { container, getByRole } = renderRow(
      entry({
        name: 'Team Drive',
        path: 'team-drive',
        is_knowledge_base: true,
        mount: { name: 'team-drive', host_path: '/Users/dana/Sync', broad: false },
      } as Partial<LibraryEntry>),
    )
    // MountFolderIcon is COMPOSED from FolderSimple's own path data (see its
    // doc comment), so FolderSimple's `d` is legitimately present here — the
    // mount icon still has to read as "a folder that holds files". What
    // distinguishes it is the accessible name and the absence of Books.
    expect(getByRole('img', { hidden: true, name: 'Mounted folder' })).toBeInTheDocument()
    expect(hasPath(container, BOOKS_REGULAR_D)).toBe(false)
  })

  it('a file renders its Phosphor file-type icon, not one of the container icons', () => {
    const { container, queryByRole } = renderRow(
      entry({ name: 'notes.md', path: 'notes.md', is_dir: false }),
    )
    expect(hasPath(container, FOLDER_SIMPLE_REGULAR_D)).toBe(false)
    expect(hasPath(container, BOOKS_REGULAR_D)).toBe(false)
    expect(queryByRole('img', { hidden: true, name: 'Mounted folder' })).toBeNull()
  })
})

// Operator direction (icon-consistency pass): "the icons were not pure
// visible but all of them are placed on a rectangular shape like a button or
// small card, that should not be, it should just display the icon." The
// tinted `color-mix(...)` backdrop + rounded box behind every row icon was
// that card. A real media THUMBNAIL legitimately keeps a clipped, rounded
// frame (object-cover needs somewhere to crop into); an icon must render bare
// on the row's own background — same colour, no backdrop.
describe('LibraryEntryRow icon wrapper has no card/tile chrome', () => {
  it('an icon (non-thumbnail) row has no background tint and no rounded/clipped frame', () => {
    const { container } = renderRow(entry({ path: 'drafts' }))
    const iconWrapper = container.querySelector('[data-testid="library-row-drafts"]')?.firstElementChild
    expect(iconWrapper).not.toBeNull()
    const el = iconWrapper as HTMLElement
    expect(el.style.backgroundColor).toBe('')
    expect(el.className).not.toMatch(/\brounded-md\b/)
    expect(el.className).not.toMatch(/\boverflow-hidden\b/)
    // Not chrome — this is how the icon itself gets its currentColor tint.
    expect(el.style.color).not.toBe('')
  })

  it('a media thumbnail row keeps its clipped, rounded frame', () => {
    const { container } = renderRow(
      entry({ name: 'photo.png', path: 'photo.png', is_dir: false, mime: 'image/png' } as Partial<LibraryEntry>),
    )
    const thumbWrapper = container.querySelector('[data-testid="library-row-photo.png"]')?.firstElementChild
    expect(thumbWrapper).not.toBeNull()
    const el = thumbWrapper as HTMLElement
    expect(el.className).toMatch(/\brounded-md\b/)
    expect(el.className).toMatch(/\boverflow-hidden\b/)
    expect(container.querySelector('[data-testid="library-thumb-photo.png"]')).not.toBeNull()
  })
})

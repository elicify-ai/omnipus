// Coverage for the Library's locked custom icon set (C3,
// docs/internal/specs/library-b-c-design-2026-09-07.md §"Icon system —
// LOCKED"). Renders each icon and asserts:
//   - it draws exactly one <path> (the "single path per icon" knockout rule)
//   - the knockout is carved via fillRule="evenodd", not a second
//     fixed-colour shape (the whole point of the "reads on any surface" fix)
//   - the outer container shape is the founder-locked base geometry for its
//     kind (tile for Workspace, folder for Vault/Folder/Mount)
//   - `size`/`className` are honoured, defaulting to 16px — the size the
//     spec's own #1 rule says every icon is judged at first
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { WorkspaceIcon, VaultIcon, FolderIcon, MountIcon } from './index'

const FOLDER_BASE = 'M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'
const TILE_BASE = 'M7,3 H17 A4,4 0 0 1 21,7'

describe('WorkspaceIcon', () => {
  it('renders a rounded tile (not a folder) as its base shape', () => {
    const { container } = render(<WorkspaceIcon />)
    const path = container.querySelector('path')
    expect(path).not.toBeNull()
    expect(path?.getAttribute('d')).toContain(TILE_BASE)
    expect(path?.getAttribute('d')).not.toContain(FOLDER_BASE)
  })

  it('carves its 2x2 knockout via fillRule=evenodd, filled with currentColor', () => {
    const { container } = render(<WorkspaceIcon />)
    const path = container.querySelector('path')
    expect(path?.getAttribute('fill-rule')).toBe('evenodd')
    expect(path?.getAttribute('fill')).toBe('currentColor')
    // The outer tile plus four knockout squares, each a separate closed
    // subpath ("M...Z") — 5 closes in total.
    expect(path?.getAttribute('d')?.match(/Z/gi)?.length).toBe(5)
  })

  it('is exactly one <path> — the "single path per icon" knockout rule', () => {
    const { container } = render(<WorkspaceIcon />)
    expect(container.querySelectorAll('path').length).toBe(1)
  })

  it('defaults to 16px and honours size/className', () => {
    const { container: def } = render(<WorkspaceIcon />)
    const defaultSvg = def.querySelector('svg')
    expect(defaultSvg?.getAttribute('width')).toBe('16')
    expect(defaultSvg?.getAttribute('height')).toBe('16')

    const { container } = render(<WorkspaceIcon size={20} className="my-class" />)
    const svg = container.querySelector('svg')
    expect(svg?.getAttribute('width')).toBe('20')
    expect(svg?.getAttribute('height')).toBe('20')
    expect(svg?.getAttribute('class')).toBe('my-class')
  })
})

describe('VaultIcon', () => {
  it('renders the folder base shape with a spark knockout, one path, evenodd', () => {
    const { container } = render(<VaultIcon />)
    const paths = container.querySelectorAll('path')
    expect(paths.length).toBe(1)
    const path = paths[0]
    expect(path.getAttribute('d')).toContain(FOLDER_BASE)
    expect(path.getAttribute('d')).toContain('M12 8.2')
    expect(path.getAttribute('fill-rule')).toBe('evenodd')
    expect(path.getAttribute('fill')).toBe('currentColor')
  })
})

describe('FolderIcon', () => {
  it('renders the folder base as an unfilled stroke outline (no knockout)', () => {
    const { container } = render(<FolderIcon />)
    const paths = container.querySelectorAll('path')
    expect(paths.length).toBe(1)
    const path = paths[0]
    expect(path.getAttribute('d')).toBe(FOLDER_BASE)
    expect(path.getAttribute('fill')).toBe('none')
    expect(path.getAttribute('stroke')).toBe('currentColor')
  })
})

describe('MountIcon', () => {
  it('renders the folder base shape with an external-arrow knockout, one path, evenodd', () => {
    const { container } = render(<MountIcon />)
    const paths = container.querySelectorAll('path')
    expect(paths.length).toBe(1)
    const path = paths[0]
    expect(path.getAttribute('d')).toContain(FOLDER_BASE)
    expect(path.getAttribute('fill-rule')).toBe('evenodd')
    expect(path.getAttribute('fill')).toBe('currentColor')
    // The folder subpath (closed with lowercase "z") plus the arrow
    // subpath (closed with uppercase "Z") — 2 closes in total.
    expect(path.getAttribute('d')?.match(/Z/gi)?.length).toBe(2)
  })
})

describe('all four icons', () => {
  it.each([
    ['WorkspaceIcon', WorkspaceIcon],
    ['VaultIcon', VaultIcon],
    ['FolderIcon', FolderIcon],
    ['MountIcon', MountIcon],
  ] as const)('%s shares the 24x24 viewBox grid', (_name, Icon) => {
    const { container } = render(<Icon />)
    expect(container.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 24 24')
  })
})

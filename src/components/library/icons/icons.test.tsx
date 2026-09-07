// Coverage for the Library's one surviving custom icon, MountFolderIcon
// (icon-consistency, 2026-09-07). Every other container concept (Knowledge
// Base, Workspace, Folder) is now a bare Phosphor component with no wrapper
// — see LibraryEntryRow.tsx / LibraryExplorer.tsx. Renders the icon and
// asserts:
//   - it composes exactly the FolderSimple (regular) + ArrowUpRight (bold)
//     path data copied from @phosphor-icons/react/dist/defs, not invented
//     artwork
//   - the badge is a decorative corner mark (aria-hidden), scaled/translated
//     via <g transform>, not a second full-size shape
//   - the accessible name lives on the outer <svg>, identifying the icon as
//     a mounted folder regardless of the decorative badge
//   - `size`/`className` are honoured, defaulting to 16px
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { MountFolderIcon } from './index'

const FOLDER_SIMPLE_REGULAR_D =
  'M216,72H130.67L102.93,51.2a16.12,16.12,0,0,0-9.6-3.2H40A16,16,0,0,0,24,64V200a16,16,0,0,0,16,16H216.89A15.13,15.13,0,0,0,232,200.89V88A16,16,0,0,0,216,72Zm0,128H40V64H93.33L123.2,86.4A8,8,0,0,0,128,88h88Z'
const ARROW_UP_RIGHT_BOLD_D =
  'M204,64V168a12,12,0,0,1-24,0V93L72.49,200.49a12,12,0,0,1-17-17L163,76H88a12,12,0,0,1,0-24H192A12,12,0,0,1,204,64Z'

describe('MountFolderIcon', () => {
  it('has an accessible name identifying it as a mounted folder', () => {
    const { container } = render(<MountFolderIcon />)
    const svg = container.querySelector('svg')
    expect(svg?.getAttribute('role')).toBe('img')
    expect(svg?.getAttribute('aria-label')).toBe('Mounted folder')
  })

  it('renders the FolderSimple base shape as a bare currentColor path (no card/backdrop)', () => {
    const { container } = render(<MountFolderIcon />)
    const paths = container.querySelectorAll('path')
    expect(paths.length).toBe(2)
    expect(paths[0].getAttribute('d')).toBe(FOLDER_SIMPLE_REGULAR_D)
    expect(paths[0].getAttribute('fill')).toBe('currentColor')
  })

  it('renders the ArrowUpRight badge as a decorative, scaled-down corner mark', () => {
    const { container } = render(<MountFolderIcon />)
    const group = container.querySelector('g')
    expect(group).not.toBeNull()
    expect(group?.getAttribute('aria-hidden')).toBe('true')
    // Scaled down (not a second full-size icon) and translated into a
    // corner, not left at the origin.
    expect(group?.getAttribute('transform')).toMatch(/scale\(0\.\d+\)/)
    expect(group?.getAttribute('transform')).toMatch(/translate\(-?\d/)
    const badgePath = group?.querySelector('path')
    expect(badgePath?.getAttribute('d')).toBe(ARROW_UP_RIGHT_BOLD_D)
    expect(badgePath?.getAttribute('fill')).toBe('currentColor')
  })

  it('shares Phosphor\'s own 256x256 viewBox grid, not a hand-picked canvas', () => {
    const { container } = render(<MountFolderIcon />)
    expect(container.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 256 256')
  })

  it('defaults to 16px and honours size/className', () => {
    const { container: def } = render(<MountFolderIcon />)
    const defaultSvg = def.querySelector('svg')
    expect(defaultSvg?.getAttribute('width')).toBe('16')
    expect(defaultSvg?.getAttribute('height')).toBe('16')

    const { container } = render(<MountFolderIcon size={20} className="my-class" />)
    const svg = container.querySelector('svg')
    expect(svg?.getAttribute('width')).toBe('20')
    expect(svg?.getAttribute('height')).toBe('20')
    expect(svg?.getAttribute('class')).toBe('my-class')
  })
})

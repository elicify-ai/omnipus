// IconRenderer — case-insensitive lookup tests
//
// W6-B3 / C8: the previous `ICON_MAP[icon]` lookup was case-sensitive, so
// any agent whose `agent.icon` was set to "Robot" / "Brain" / "MagnifyingGlass"
// (the catalog's canonical PascalCase) resolved correctly, but a value like
// "ROBOT" or "robot" — or anything the backend might emit from a stored
// SOUL.md or older config that lowercased the icon — silently fell back to
// the default Robot icon. That's the bug that made Mia, Jim, Ava, and Ray
// all render the same default.
//
// `getIconComponent` in @/lib/agentIcons already locks the
// case-insensitivity contract (see src/lib/agentIcons.test.ts). This file
// locks the same contract on the OTHER side of the catalog boundary:
// IconRenderer.tsx, which uses a much larger BRD Appendix D catalog that
// the 10-entry create-modal catalog does not cover. Both paths must agree
// — if they drift, Mia shows one icon in the create modal preview and a
// different one on the Agents list card.

import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { IconRenderer } from './IconRenderer'
import { ICON_OPTIONS } from '@/lib/agentIcons'

describe('IconRenderer (W6-B3 / C8 case-insensitive contract)', () => {
  it('resolves the 10 catalog names from the shared agentIcons module', () => {
    for (const { name } of ICON_OPTIONS) {
      const { container } = render(<IconRenderer icon={name} size={18} />)
      // Phosphor icons render an <svg>; the fallback path would still render
      // an <svg> (the Robot icon), so we can't assert svg count alone.
      // Instead: ensure the renderer never crashes for a valid catalog name.
      expect(container.querySelector('svg')).toBeTruthy()
    }
  })

  it('resolves the same icon regardless of case (the C8 fix)', () => {
    // Same lowercase + kebab-case names that appear in the ICON_MAP
    // keys (the larger BRD catalog). Case-insensitive lookup must map
    // "robot", "ROBOT", and "Robot" all to the same Phosphor component.
    const samples = ['robot', 'ROBOT', 'Robot', 'brain', 'BRAIN', 'Brain', 'shield', 'SHIELD', 'Shield']
    for (const name of samples) {
      const { container: a } = render(<IconRenderer icon={name} size={18} />)
      const { container: b } = render(<IconRenderer icon={name.toLowerCase()} size={18} />)
      // If the lookup is case-sensitive, the uppercase variant would fall
      // back to the default Robot and the two rendered SVGs would diverge
      // (different <path> sets). We assert on the rendered SVG markup as a
      // proxy: identical icons produce identical output.
      expect(a.innerHTML).toBe(b.innerHTML)
    }
  })

  it('does not throw on unknown names — falls back to a default icon', () => {
    expect(() => render(<IconRenderer icon="definitely-not-an-icon" size={18} />)).not.toThrow()
    expect(() => render(<IconRenderer icon={undefined} size={18} />)).not.toThrow()
    expect(() => render(<IconRenderer icon={null} size={18} />)).not.toThrow()
    expect(() => render(<IconRenderer icon="" size={18} />)).not.toThrow()
  })
})

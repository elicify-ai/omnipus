// agentIcons — shared avatar icon catalog tests
//
// Wave 6 / A-fix: ICON_OPTIONS was duplicated byte-for-byte in
// CreateAgentModal.tsx and AgentProfile.tsx. This module extracts the
// catalog + a case-insensitive lookup that Wave B3 (icon-case fix)
// will rely on. Lock the case-insensitivity contract here so future
// work can build on it without re-deriving the lookup logic.

import { describe, it, expect } from 'vitest'
import { ICON_OPTIONS, getIconComponent } from './agentIcons'

describe('agentIcons', () => {
  it('exports exactly 10 icons', () => {
    expect(ICON_OPTIONS).toHaveLength(10)
  })

  it('every entry has a name and a component', () => {
    for (const entry of ICON_OPTIONS) {
      expect(entry.name).toBeTruthy()
      // Phosphor icon components are forwardRef objects, not bare functions;
      // truthy is the right check.
      expect(entry.component).toBeTruthy()
    }
  })

  describe('getIconComponent', () => {
    it('resolves exact-case names', () => {
      const Icon = getIconComponent('Robot')
      expect(Icon).toBeDefined()
      // Same identity as the catalog entry
      expect(Icon).toBe(ICON_OPTIONS.find((o) => o.name === 'Robot')!.component)
    })

    it('is case-insensitive (the Wave B3 contract)', () => {
      const lower = getIconComponent('robot')
      const upper = getIconComponent('ROBOT')
      const mixed = getIconComponent('RoBoT')
      expect(lower).toBeDefined()
      expect(upper).toBe(lower)
      expect(mixed).toBe(lower)
    })

    it('falls back to Robot for unknown names', () => {
      const fallback = getIconComponent('NotAnIcon')
      const robot = ICON_OPTIONS.find((o) => o.name === 'Robot')!.component
      expect(fallback).toBe(robot)
    })

    it('handles empty / undefined input by returning Robot', () => {
      const fallbackEmpty = getIconComponent('')
      const fallbackUndef = getIconComponent(undefined as unknown as string)
      const robot = ICON_OPTIONS.find((o) => o.name === 'Robot')!.component
      expect(fallbackEmpty).toBe(robot)
      expect(fallbackUndef).toBe(robot)
    })
  })
})

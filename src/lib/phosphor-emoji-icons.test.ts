import { describe, it, expect } from 'vitest'
import { PHOSPHOR_EMOJI_ICONS } from './phosphor-emoji-icons'
import { EMOJI_MAP } from './rehype-phosphor-emoji'

// Belt-and-suspenders runtime drift guard. The compile-time guard already types
// EMOJI_MAP's values as `PhosphorIconName` (a key union of PHOSPHOR_EMOJI_ICONS),
// so a missing icon is a tsc error — but this asserts the invariant at runtime
// too in case the allow-list and the map ever diverge through a type cast.
describe('phosphor emoji icon allow-list', () => {
  it('every EMOJI_MAP icon value resolves to a real component in PHOSPHOR_EMOJI_ICONS', () => {
    for (const [emoji, iconName] of Object.entries(EMOJI_MAP)) {
      expect(
        PHOSPHOR_EMOJI_ICONS,
        `emoji ${emoji} maps to "${iconName}" which is not in PHOSPHOR_EMOJI_ICONS`,
      ).toHaveProperty(iconName)
      expect(typeof PHOSPHOR_EMOJI_ICONS[iconName]).not.toBe('undefined')
    }
  })

  it('exposes a non-empty map', () => {
    expect(Object.keys(PHOSPHOR_EMOJI_ICONS).length).toBeGreaterThan(0)
    expect(Object.keys(EMOJI_MAP).length).toBeGreaterThan(0)
  })
})

// SkillsToolsPage.levels.test.tsx — order 26 (S-37, S-39, S-44, S-51; C-6).
// RED guard: the page does not exist yet. Oracles for its GREEN tests: each
// tool row shows its level (Full / Name and description / Name only /
// Hidden) with the menu meaning next to it; locked rows show the fixed
// texts instead of a level control (C-6 permission_fixed); the denied-items
// note shows with the "What the model sees" filter on (FR-006).
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — levels (order 26)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec C-6, S-37, S-39, S-44, S-51, FR-006',
    ).toHaveLength(1)
  })
})

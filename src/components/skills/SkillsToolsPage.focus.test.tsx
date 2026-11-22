// SkillsToolsPage.focus.test.tsx — order 45 (S-100). RED guard: the page
// does not exist yet. Oracle: skills-tools-redesign-spec.md S-100 —
// "Given Permission filter = Ask showing 3 rows, focus on the Deny badge of
// the last row; When the person presses Enter and the save is acknowledged;
// Then focus moves to the Deny badge of the previous row; And the status
// region announces '<name> set to Deny. It no longer matches the filter.
// 2 shown.'" Its GREEN test drives the real page through the disclosure
// controls (the F3 ruling) — never mounted internals.
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — focus after the focused row leaves a filtered list (order 45)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-100',
    ).toHaveLength(1)
  })
})

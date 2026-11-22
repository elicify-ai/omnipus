// InstallReview.category.test.tsx — order 28 (S-55, S-56, S-27; Dataset D5;
// C-13). RED guard: the install review does not exist yet. Oracles for its
// GREEN tests: a SKILL.md with metadata.category opens the review with that
// category preselected; a skill without one requires the category before
// approve (S-55); the review shows the trust check for an unverified skill
// (S-27's screen half; the tool/API halves are orders 6/17).
import { describe, it, expect } from 'vitest'

describe('InstallReview — category (order 28)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/InstallReview.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: InstallReview is not implemented — required by skills-tools-redesign-spec C-13, S-55, S-56, S-27 and Dataset D5',
    ).toHaveLength(1)
  })
})

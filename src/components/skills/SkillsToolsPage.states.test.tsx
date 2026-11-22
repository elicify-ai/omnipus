// SkillsToolsPage.states.test.tsx — order 30 (S-66, S-67). RED guard: the
// page does not exist yet. Oracles for its GREEN tests: the page's loading,
// refused and empty states per S-66/S-67 (a refused load shows the server
// reason with Retry; an empty catalogue shows the empty-state text; a
// saved-but-inactive configuration shows the activation warning of C-16).
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — states (order 30)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec S-66, S-67',
    ).toHaveLength(1)
  })
})

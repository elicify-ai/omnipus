// SkillsToolsPage.trust.test.tsx — order 25 (S-26, S-32; FR-027, C-3). RED
// guard: the page does not exist yet. Oracles for its GREEN tests: the trust
// control offers exactly allow / ask / deny with their meanings ("Allow",
// "Ask every time", "Deny"), a warn_unverified stored value renders Ask
// after the migration, and an unverified-skill warning shows where the spec
// places it.
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — trust (order 25)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-027, C-3, S-26, S-32',
    ).toHaveLength(1)
  })
})

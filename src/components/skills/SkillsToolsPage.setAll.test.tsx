// SkillsToolsPage.setAll.test.tsx — order 23 (S-02, S-05, S-06, S-48; FR-003,
// FR-010; Dataset D4). RED guard: the rewritten Skills & Tools page does not
// exist yet, so the file fails loudly naming the spec until the component
// lands; the S-row assertions below are the oracles its GREEN tests must
// carry (Set all changes exactly the on-screen applicable rows in one
// request with "<k> of <n>" in label and accessible name and names skipped
// rows; execute_plan confirm before any Allow that resolves it; Cancel
// changes no row).
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — Set all (order 23)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-003, FR-010, S-02, S-05, S-06, S-48 and Dataset D4',
    ).toHaveLength(1)
  })
})

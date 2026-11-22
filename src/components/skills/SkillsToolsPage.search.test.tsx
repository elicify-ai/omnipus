// SkillsToolsPage.search.test.tsx — order 27 (S-45 to S-50; FR-002, FR-004,
// FR-005). RED guard: the page does not exist yet. Oracles for its GREEN
// tests: one search field and two flat filters above one grouped list with
// collapsed groups; a search opens groups with matches; Escape and the
// clear button empty it; an empty result shows
// "No <items> match "<text>"" with "Clear search and filters"; a row whose
// saved value no longer matches the active filter leaves the list.
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — search and filters (order 27)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-002, FR-004, FR-005, S-45 to S-50',
    ).toHaveLength(1)
  })
})

// SkillsToolsPage.autosave.test.tsx — order 24 (S-01, S-07, S-08; FR-007,
// FR-008, FR-009). RED guard: the page does not exist yet. The oracles its
// GREEN tests must carry: per-value save ("Saving…" within 100 ms of the
// click, the request within 1,000 ms when nothing is pending, "Saved" only
// on acknowledgement), no Save bar/Discard/leave prompt; a refused save
// shows "Not saved: <server reason>." with Retry and the stored values
// stay; a 503 shows the saved-but-not-applied text with Retry.
import { describe, it, expect } from 'vitest'

describe('SkillsToolsPage — autosave (order 24)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/SkillsToolsPage.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: SkillsToolsPage is not implemented — required by skills-tools-redesign-spec FR-007, FR-008, FR-009, S-01, S-07, S-08 and Dataset D12',
    ).toHaveLength(1)
  })
})

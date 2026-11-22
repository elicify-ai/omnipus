// NewSkillMenu.test.tsx — order 29 (S-52, S-54; US-7). RED guard: the
// component does not exist yet. Oracles for its GREEN tests: "Write with
// Omnipus" sends the person's message "Write a new skill" into the Omnipus
// chat and starts W5 without a workspace target (S-52, C-8); offline the
// warning "Not connected — "Write a new skill" was not sent. Press the
// button again once you are reconnected." appears and nothing is queued
// (S-54).
import { describe, it, expect } from 'vitest'

describe('NewSkillMenu — write with Omnipus (order 29)', () => {
  it('BLOCKED until the component exists', () => {
    const mod = import.meta.glob('/src/components/skills/NewSkillMenu.tsx', { eager: false })
    expect(
      Object.keys(mod),
      'BLOCKED: NewSkillMenu is not implemented — required by skills-tools-redesign-spec S-52, S-54 and C-8',
    ).toHaveLength(1)
  })
})

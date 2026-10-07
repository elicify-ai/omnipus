import { describe, expect, it } from 'vitest'

// The module is required by ARCH-DECISIONS 3.5 and does not exist yet.
// Vite's static analysis would drop the suite before the tests are collected,
// so the import is runtime-only. The failure is the missing module, then the
// assertions below once it exists.
async function loadIdentity() {
  try {
    const specifier = './agentIdentity'
    return await import(/* @vite-ignore */ specifier)
  } catch (err) {
    throw new Error(
      `BLOCKED: src/lib/agentIdentity.ts is not implemented — required by the spec locked identity vocabulary and ARCH-DECISIONS 3.5. ${err}`,
    )
  }
}

// Oracles: spec "Locked identity vocabulary" and ARCH-DECISIONS 1.3 / 3.1 / 3.5.
// Labels are the display strings. Slugs are the wire values. Group is not on the wire.

const SPEC_ROLES: { group: string; slug: string; label: string }[] = [
  { group: 'Create', slug: 'writer', label: 'Writer' },
  { group: 'Create', slug: 'designer', label: 'Designer' },
  { group: 'Create', slug: 'image', label: 'Image creator' },
  { group: 'Create', slug: 'video', label: 'Video producer' },
  { group: 'Create', slug: 'audio', label: 'Audio and voice' },
  { group: 'Create', slug: 'social', label: 'Social media' },
  { group: 'Build', slug: 'developer', label: 'Developer' },
  { group: 'Build', slug: 'data', label: 'Data engineer' },
  { group: 'Build', slug: 'analyst', label: 'Data analyst' },
  { group: 'Build', slug: 'itops', label: 'IT and operations' },
  { group: 'Build', slug: 'automation', label: 'Automation' },
  { group: 'Build', slug: 'security', label: 'Security' },
  { group: 'Build', slug: 'quality', label: 'Quality and QA' },
  { group: 'Build', slug: 'science', label: 'Science and lab' },
  { group: 'Business', slug: 'orchestrator', label: 'Orchestrator' },
  { group: 'Business', slug: 'project', label: 'Project manager' },
  { group: 'Business', slug: 'product', label: 'Product manager' },
  { group: 'Business', slug: 'sales', label: 'Sales' },
  { group: 'Business', slug: 'marketing', label: 'Marketing' },
  { group: 'Business', slug: 'finance', label: 'Finance' },
  { group: 'Business', slug: 'legal', label: 'Legal and compliance' },
  { group: 'Business', slug: 'support', label: 'Customer support' },
  { group: 'Business', slug: 'documents', label: 'Documents' },
  { group: 'Business', slug: 'researcher', label: 'Researcher' },
  { group: 'People', slug: 'people', label: 'People and HR' },
  { group: 'People', slug: 'tutor', label: 'Tutor' },
  { group: 'People', slug: 'knowledge', label: 'Knowledge and library' },
  { group: 'People', slug: 'translator', label: 'Translator' },
  { group: 'Personal', slug: 'general', label: 'General assistant' },
  { group: 'Personal', slug: 'personal', label: 'Personal assistant' },
  { group: 'Personal', slug: 'office', label: 'Office assistant' },
]

describe('agent identity vocabulary', () => {
  it('lists the five groups in published order', async () => {
    const { IDENTITY_GROUP_ORDER } = await loadIdentity()
    expect([...IDENTITY_GROUP_ORDER]).toEqual(['Create', 'Build', 'Business', 'People', 'Personal'])
  })

  it('has 31 roles in groups of 6, 8, 10, 4 and 3, with the spec labels', async () => {
    const { ROLE_VOCABULARY } = await loadIdentity()
    expect(ROLE_VOCABULARY).toHaveLength(31)
    expect(ROLE_VOCABULARY.map((row: { group: string; slug: string; label: string }) => ({ group: row.group, slug: row.slug, label: row.label }))).toEqual(SPEC_ROLES)
    const counts = new Map<string, number>()
    for (const row of ROLE_VOCABULARY as { group: string }[]) counts.set(row.group, (counts.get(row.group) ?? 0) + 1)
    expect(counts.get('Create')).toBe(6)
    expect(counts.get('Build')).toBe(8)
    expect(counts.get('Business')).toBe(10)
    expect(counts.get('People')).toBe(4)
    expect(counts.get('Personal')).toBe(3)
  })

  it('maps each figure to its art key and face, and does not treat Omnipus as a silent default', async () => {
    const { FIGURE_ART } = await loadIdentity()
    expect(FIGURE_ART.Robot).toEqual({ art: 'robotSolid', face: 'eyes' })
    expect(FIGURE_ART.Man).toEqual({ art: 'man', face: 'eyes' })
    expect(FIGURE_ART.Woman).toEqual({ art: 'woman', face: 'eyes' })
    expect(FIGURE_ART.Omnipus).toEqual({ art: 'octopus', face: 'none' })
    expect(Object.keys(FIGURE_ART).sort()).toEqual(['Man', 'Omnipus', 'Robot', 'Woman'])
  })
})

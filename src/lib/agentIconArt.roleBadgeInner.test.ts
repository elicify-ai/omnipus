/**
 * agentIconArt.roleBadgeInner.test.ts — the badge glyph alone (task: the
 * Role badge picker shows each option's badge icon without a figure).
 *
 * Oracle (dispatch brief / ARCH-DECISIONS "Locked identity vocabulary"):
 * roleBadgeInner(role) returns the SAME path markup the badge group on the
 * agent mark carries, WITHOUT the corner transform, for every one of the 31
 * roles; an unknown role throws. The 31 role slugs are the AgentRole wire
 * enum from the ARCH-DECISIONS spec table — not read off the implementation.
 */
import { describe, expect, it } from 'vitest'

import { agentIconInner, roleBadgeInner } from './agentIconArt'

/** The 31 spec role slugs (ARCH-DECISIONS "Locked identity vocabulary"). */
const SPEC_ROLES = [
  'writer', 'designer', 'image', 'video', 'audio', 'social',
  'developer', 'data', 'analyst', 'itops', 'automation', 'security', 'quality', 'science',
  'orchestrator', 'project', 'product', 'sales', 'marketing', 'finance', 'legal', 'support',
  'documents', 'researcher',
  'people', 'tutor', 'knowledge', 'translator',
  'general', 'personal', 'office',
] as const

/** Path `d` attributes of an element's own `<path>` children, in order. */
function pathData(element: Element): string[] {
  return Array.from(element.querySelectorAll('path')).map((path) => path.getAttribute('d') ?? '')
}

describe('roleBadgeInner — the badge glyph alone, for all 31 roles', () => {
  it('offers exactly the 31 spec roles', () => {
    // The vocabulary is closed: 6 Create + 8 Build + 10 Business + 4 People
    // + 3 Personal = 31 (docs/agents.md "How an agent looks").
    expect(SPEC_ROLES).toHaveLength(31)
  })

  it.each(SPEC_ROLES)(
    'returns the same path markup as the badge on the mark without the corner transform (%s)',
    (role) => {
      // The mark composes figure + badge; the badge group on the mark is
      // the one carrying this role (agent-icon.tsx stamps it data-role).
      const markSvg = new DOMParser().parseFromString(
        `<svg xmlns="http://www.w3.org/2000/svg">${agentIconInner('man', role, 'badge-check-mask')}</svg>`,
        'image/svg+xml',
      ).documentElement
      const badgeOnMark = markSvg.querySelector(`g[data-role="${role}"]`)
      expect(badgeOnMark, `mark badge group for ${role}`).not.toBeNull()

      // The badge on the mark sits in the corner via the shrink transform.
      expect(badgeOnMark?.getAttribute('transform')).toContain('translate(140 140)')
      expect(badgeOnMark?.getAttribute('transform')).toContain('scale(0.44)')

      // The standalone glyph is the SAME paths, with no corner transform.
      const standalone = roleBadgeInner(role)
      expect(standalone).not.toContain('translate(140 140)')
      const glyphSvg = new DOMParser().parseFromString(
        `<svg xmlns="http://www.w3.org/2000/svg">${standalone}</svg>`,
        'image/svg+xml',
      ).documentElement
      expect(glyphSvg.querySelectorAll('path')).toHaveLength(
        badgeOnMark!.querySelectorAll('path').length,
      )
      // Exact equality of the path markup, order included.
      expect(pathData(glyphSvg)).toEqual(pathData(badgeOnMark!))
      // ...and it is the full glyph, not a stub: a badge has actual art.
      expect(pathData(glyphSvg).length).toBeGreaterThan(0)
    },
  )

  it('throws for an unknown role', () => {
    expect(() => roleBadgeInner('nonexistent-role' as Parameters<typeof roleBadgeInner>[0]))
      .toThrowError(/no badge for/i)
  })
})

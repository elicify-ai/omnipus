import { describe, expect, it } from 'vitest'

import { contrastRatio, findColorVisionAmbiguities, simulateColorVision, statusContract, validateStatusContract } from './status'
import type { StatusPresentation } from './status'

// Named (not inline-anonymous) so each summary field reads off a review-stable
// call site rather than an anonymous .map() callback.
const toColorSummary = ([key, status]: [string, StatusPresentation]) => [key, {
  color: status.resolvedColor,
  label: status.label,
  cue: status.nonColorCue,
}] as const

const D4_EXPECTED = {
  inbox: { color: '#9CA3AF', label: 'Inbox', cue: 'quiet-circle' },
  next: { color: '#3B82F6', label: 'Next', cue: 'ready-info' },
  inProgress: { color: '#D4AF37', label: 'In progress', cue: 'live-work' },
  blocked: { color: '#F97316', label: 'Blocked', cue: 'prohibit' },
  done: { color: '#10B981', label: 'Done', cue: 'check' },
  failed: { color: '#EF4444', label: 'Failed', cue: 'x' },
  cancelled: { color: '#EAB308', label: 'Cancelled', cue: 'stopped-by-user' },
} as const

describe('D4 status presentation contract', () => {
  it('defines the exact task-palette winner for all seven workflow presentations', () => {
    expect(Object.fromEntries(Object.entries(statusContract).map(toColorSummary))).toEqual(D4_EXPECTED)
  })

  it('defines every required presentation role as a generated token reference', () => {
    expect(Object.keys(statusContract)).toHaveLength(7)
    for (const status of Object.values(statusContract)) {
      expect(status.tokens).toEqual({
        foreground: expect.stringMatching(/^var\(--status-.+-foreground\)$/),
        background: expect.stringMatching(/^var\(--status-.+-background\)$/),
        border: expect.stringMatching(/^var\(--status-.+-border\)$/),
        icon: expect.stringMatching(/^var\(--status-.+-icon\)$/),
        label: expect.stringMatching(/^var\(--status-.+-label\)$/),
        hover: expect.stringMatching(/^var\(--status-.+-hover\)$/),
        focus: expect.stringMatching(/^var\(--status-.+-focus\)$/),
        filledForeground: expect.stringMatching(/^var\(--status-.+-filled-foreground\)$/),
      })
    }
  })

  it('passes WCAG AA for tinted, hover, and filled labels plus non-text border contrast', () => {
    expect(Object.keys(statusContract)).toHaveLength(7)
    for (const [name, status] of Object.entries(statusContract)) {
      expect(status.contrast.tintedLabel, `${name} tinted label`).toBeGreaterThanOrEqual(4.5)
      expect(status.contrast.hoverLabel, `${name} hover label`).toBeGreaterThanOrEqual(4.5)
      expect(status.contrast.filledLabel, `${name} filled label`).toBeGreaterThanOrEqual(4.5)
      expect(status.contrast.border, `${name} border`).toBeGreaterThanOrEqual(3)
    }
  })

  it('keeps labels and non-colour cues unique so hue is never the only signal', () => {
    expect(new Set(Object.values(statusContract).map((status) => status.label)).size).toBe(7)
    expect(new Set(Object.values(statusContract).map((status) => status.nonColorCue)).size).toBe(7)
  })

  it.each(['protanopia', 'deuteranopia', 'tritanopia'] as const)(
    'gives every colour-vision ambiguity a distinct non-colour cue under %s',
    (deficiency) => {
      const ambiguities = findColorVisionAmbiguities(statusContract, deficiency)
      expect(ambiguities.length, `${deficiency} must exercise the second-cue contract`).toBeGreaterThan(0)
      for (const [first, second] of ambiguities) {
        expect(statusContract[first].nonColorCue).not.toBe(statusContract[second].nonColorCue)
        expect(statusContract[first].label).not.toBe(statusContract[second].label)
      }
    },
  )

  it('validates the complete contract without diagnostics', () => {
    expect(validateStatusContract(statusContract)).toEqual([])
  })
})

describe('status contract diagnostics', () => {
  it('reports duplicate labels, duplicate cues, low contrast, and incomplete roles', () => {
    const invalid = {
      inbox: {
        ...statusContract.inbox,
        label: 'Done',
        nonColorCue: 'check',
        resolvedColor: '#111113',
        tokens: { foreground: 'var(--status-inbox-foreground)' },
      },
      done: statusContract.done,
    }

    // Deliberately incomplete runtime input: cross the type boundary so the
    // validator's diagnostics can be exercised without weakening its API.
    expect(validateStatusContract(invalid as unknown as Parameters<typeof validateStatusContract>[0])).toEqual([
      'status "inbox" has contrast below 4.5:1 on #0A0A0B',
      'status "inbox" is missing token role "background"',
      'status "inbox" is missing token role "border"',
      'status "inbox" is missing token role "icon"',
      'status "inbox" is missing token role "label"',
      'status "inbox" is missing token role "hover"',
      'status "inbox" is missing token role "focus"',
      'status "inbox" is missing token role "filledForeground"',
      'status label "Done" is duplicated by "inbox" and "done"',
      'status non-colour cue "check" is duplicated by "inbox" and "done"',
    ])
  })

  it('rejects malformed colours with an exact diagnostic', () => {
    expect(() => contrastRatio('#12345', '#0A0A0B')).toThrowError('invalid six-digit hex colour: #12345')
    expect(() => simulateColorVision('red', 'protanopia')).toThrowError('invalid six-digit hex colour: red')
  })
})

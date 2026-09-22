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

// `next` (design-system/tokens/colors.json): `color.status.next` refs
// `primitive.color.blue-label` (#60A5FA, one step lighter than
// `primitive.color.blue`), clearing >=7:1 against the near-black chip text
// used across the app (calendar's CHIP_TEXT_COLOR, badge/pill text
// elsewhere) — the >=7:1 AAA floor those surfaces document.
// `primitive.color.blue` itself stays reserved for `color.info`.
//
// `failed`: `color.status.failed` refs `primitive.color.red-label`
// (#F87171), clearing 7.15:1 against the same near-black chip text — the
// same lighter-label treatment `next` uses, applied to the same >=7:1 AAA
// floor.
const D4_EXPECTED = {
  inbox: { color: '#9CA3AF', label: 'Inbox', cue: 'quiet-circle' },
  next: { color: '#60A5FA', label: 'Next', cue: 'ready-info' },
  inProgress: { color: '#D4AF37', label: 'In progress', cue: 'live-work' },
  blocked: { color: '#F97316', label: 'Blocked', cue: 'prohibit' },
  done: { color: '#10B981', label: 'Done', cue: 'check' },
  failed: { color: '#F87171', label: 'Failed', cue: 'x' },
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
      // Zero ambiguities is an allowed, passing outcome — this only checks
      // that whichever ambiguities DO exist each carry a distinct cue and
      // label. The check firing on a genuinely ambiguous pair is proven
      // separately, on a synthetic fixture below, so a palette with zero
      // real ambiguities can't silently turn this into a vacuous pass.
      const ambiguities = findColorVisionAmbiguities(statusContract, deficiency)
      assertDistinctCues(statusContract, ambiguities)
    },
  )

  it('validates the complete contract without diagnostics', () => {
    expect(validateStatusContract(statusContract)).toEqual([])
  })
})

// ─── Coverage canary: findColorVisionAmbiguities + the cue-uniqueness check
// are actually exercised, independent of how many ambiguities the real
// palette happens to have under a given deficiency. Runs on a synthetic
// fixture contract, never on statusContract — the real contract's own
// ambiguities (or lack of them) are asserted above.
function assertDistinctCues<T extends Readonly<Record<string, StatusPresentation>>>(contract: T, ambiguities: [keyof T, keyof T][]): void {
  for (const [first, second] of ambiguities) {
    expect(contract[first].nonColorCue).not.toBe(contract[second].nonColorCue)
    expect(contract[first].label).not.toBe(contract[second].label)
  }
}

function fixtureStatus(resolvedColor: string, label: string, nonColorCue: string): StatusPresentation {
  const tokenRole = 'var(--fixture)'
  return {
    label,
    resolvedColor,
    nonColorCue,
    tokens: {
      foreground: tokenRole, background: tokenRole, border: tokenRole, icon: tokenRole,
      label: tokenRole, hover: tokenRole, focus: tokenRole, filledForeground: tokenRole,
    },
    contrast: { tintedLabel: 21, hoverLabel: 21, filledLabel: 21, border: 21 },
  }
}

describe('colour-vision ambiguity detection — synthetic fixture coverage canary', () => {
  it('findColorVisionAmbiguities detects a known-ambiguous pair (identical colour under every simulated deficiency)', () => {
    const fixture = {
      alpha: fixtureStatus('#886644', 'Alpha', 'cue-alpha'),
      beta: fixtureStatus('#886644', 'Beta', 'cue-beta'),
    }
    for (const deficiency of ['protanopia', 'deuteranopia', 'tritanopia'] as const) {
      expect(findColorVisionAmbiguities(fixture, deficiency), deficiency).toEqual([['alpha', 'beta']])
    }
  })

  it('the cue-uniqueness check passes when the ambiguous fixture pair has distinct cues and labels', () => {
    const fixture = {
      alpha: fixtureStatus('#886644', 'Alpha', 'cue-alpha'),
      beta: fixtureStatus('#886644', 'Beta', 'cue-beta'),
    }
    const ambiguities = findColorVisionAmbiguities(fixture, 'tritanopia')
    expect(ambiguities.length).toBeGreaterThan(0)
    expect(() => assertDistinctCues(fixture, ambiguities)).not.toThrow()
  })

  it('the cue-uniqueness check FAILS when the ambiguous fixture pair shares the same cue', () => {
    const fixture = {
      alpha: fixtureStatus('#886644', 'Alpha', 'same-cue'),
      beta: fixtureStatus('#886644', 'Beta', 'same-cue'),
    }
    const ambiguities = findColorVisionAmbiguities(fixture, 'tritanopia')
    expect(ambiguities.length).toBeGreaterThan(0)
    expect(() => assertDistinctCues(fixture, ambiguities)).toThrow()
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

// CriteriaBreakdown.test.tsx — shared presentational criteria list
// (ADR-074 D5.4, judgment-first spec US-7 S4 / US-6; TDD test 20).
//
// The component presents agent-drafted or compiled criteria for confirmation:
// plain-language text first, a mono "verifies via:" chip under any criterion
// carrying a technical payload, and NO user-facing `[kind]` classification
// labels (spec §4 prohibition). The goal confirmation card consumes this same
// component (contract stream); these tests pin the shared rendering contract.

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import {
  CriteriaBreakdown,
  formatVerifiesVia,
  JUDGMENT_ICON,
  type CriteriaBreakdownItem,
} from './CriteriaBreakdown'

const PROSE: CriteriaBreakdownItem = { text: 'The summary reads clearly' }
const CHECK: CriteriaBreakdownItem = {
  text: 'All tests pass',
  check: { command: 'go test ./...', expected_exit_code: 0 },
}
const BEHAVIOR: CriteriaBreakdownItem = {
  text: 'The web was actually searched',
  behavior: { tool: 'search_web', min_count: 3, max_count: 5, scope: 'task_session' },
}

describe('CriteriaBreakdown — itemized list, plain language first', () => {
  it('renders every criterion text as the primary content, in order', () => {
    render(<CriteriaBreakdown criteria={[PROSE, CHECK, BEHAVIOR]} />)
    const items = screen.getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent('The summary reads clearly')
    expect(items[1]).toHaveTextContent('All tests pass')
    expect(items[2]).toHaveTextContent('The web was actually searched')
  })

  it('renders a "verifies via:" chip for a technical check, verbatim command + exit code', () => {
    render(<CriteriaBreakdown criteria={[CHECK]} />)
    expect(screen.getByText('verifies via:')).toBeInTheDocument()
    expect(screen.getByText('go test ./... -> exit 0')).toBeInTheDocument()
  })

  it('renders a "verifies via:" chip for an action-count check: tool xMin-Max', () => {
    render(<CriteriaBreakdown criteria={[BEHAVIOR]} />)
    expect(screen.getByText('search_web x3-5')).toBeInTheDocument()
  })

  it('renders NO chip for a plain prose criterion', () => {
    render(<CriteriaBreakdown criteria={[PROSE]} />)
    expect(screen.queryByText('verifies via:')).not.toBeInTheDocument()
  })

  it('shows no user-facing kind classification labels (spec §4)', () => {
    render(<CriteriaBreakdown criteria={[PROSE, CHECK, BEHAVIOR]} />)
    for (const label of ['prose', 'PROSE', 'check', 'CHECK', 'behavior', 'BEHAVIOR']) {
      expect(screen.queryByText(label)).not.toBeInTheDocument()
    }
  })
})

// ADR-080 D-TYPES: judgment renders as a small muted icon (not text) — the
// redesign (operator report 2026-09-07) that replaced the old uppercase
// text badge, which added visual weight without adding information once
// every row carries one. Legible via `title`/`aria-label`, never via
// visible text content.
describe('CriteriaBreakdown — judgment icon (ADR-080 D-TYPES)', () => {
  it('renders an icon (no visible judgment word) for each judgment kind, with the matching accessible name', () => {
    render(
      <CriteriaBreakdown
        criteria={[
          { text: 'a', judgment: 'boolean' },
          { text: 'b', judgment: 'quantitative' },
          { text: 'c', judgment: 'artifact' },
        ]}
      />,
    )
    const badges = screen.getAllByTestId('criterion-judgment-badge')
    expect(badges).toHaveLength(3)
    for (const badge of badges) expect(badge.textContent).toBe('')
    expect(badges[0]).toHaveAttribute('aria-label', JUDGMENT_ICON.boolean.label)
    expect(badges[1]).toHaveAttribute('aria-label', JUDGMENT_ICON.quantitative.label)
    expect(badges[2]).toHaveAttribute('aria-label', JUDGMENT_ICON.artifact.label)
    // Same string surfaces as the native hover tooltip.
    expect(badges[0]).toHaveAttribute('title', JUDGMENT_ICON.boolean.label)
  })

  it('renders no judgment icon when the criterion carries none', () => {
    render(<CriteriaBreakdown criteria={[PROSE]} />)
    expect(screen.queryByTestId('criterion-judgment-badge')).not.toBeInTheDocument()
  })
})

// ADR-080 D-DOD: an `inferred` DoD item is flagged per-row, never silently
// dropped from the render.
describe('CriteriaBreakdown — inferred provenance flag (ADR-080 D-DOD)', () => {
  it('flags a provenance:inferred item as "inferred — confirm or drop"', () => {
    render(<CriteriaBreakdown criteria={[{ text: 'a', provenance: 'inferred' }]} />)
    expect(screen.getByTestId('criterion-inferred-flag')).toHaveTextContent('inferred — confirm or drop')
  })

  it('does not flag a stated/workspace/floor item', () => {
    render(<CriteriaBreakdown criteria={[{ text: 'a', provenance: 'stated' }]} />)
    expect(screen.queryByTestId('criterion-inferred-flag')).not.toBeInTheDocument()
  })
})

describe('CriteriaBreakdown — empty and edge cases', () => {
  it('renders nothing at all for zero criteria without emptyText', () => {
    const { container } = render(<CriteriaBreakdown criteria={[]} />)
    expect(container).toBeEmptyDOMElement()
  })

  it('renders emptyText instead of a list for zero criteria', () => {
    render(<CriteriaBreakdown criteria={[]} emptyText="No criteria compiled." />)
    expect(screen.getByText('No criteria compiled.')).toBeInTheDocument()
    expect(screen.queryByRole('list')).not.toBeInTheDocument()
  })

  it('keys by criterion id when present without changing the rendered content', () => {
    render(
      <CriteriaBreakdown
        criteria={[{ ...PROSE, id: 'c-1' }, { ...CHECK, id: 'c-2' }]}
      />,
    )
    expect(screen.getAllByRole('listitem')).toHaveLength(2)
  })
})

describe('formatVerifiesVia — chip formatting contract', () => {
  it('formats a check payload as "command -> exit N"', () => {
    expect(formatVerifiesVia(CHECK)).toBe('go test ./... -> exit 0')
  })

  it('returns null for prose (no payload)', () => {
    expect(formatVerifiesVia(PROSE)).toBeNull()
  })

  it('formats a bounded range as xMin-Max', () => {
    expect(formatVerifiesVia(BEHAVIOR)).toBe('search_web x3-5')
  })

  it('formats an unbounded min as xMin+', () => {
    expect(
      formatVerifiesVia({ text: 't', behavior: { tool: 'search_web', min_count: 3 } }),
    ).toBe('search_web x3+')
  })

  it('formats min == max as an exact count, including the explicit 0/0 never-call case', () => {
    expect(
      formatVerifiesVia({ text: 't', behavior: { tool: 'bash', min_count: 0, max_count: 0 } }),
    ).toBe('bash x0')
    expect(
      formatVerifiesVia({ text: 't', behavior: { tool: 'bash', min_count: 2, max_count: 2 } }),
    ).toBe('bash x2')
  })

  it('treats an absent min_count as the wire default 1', () => {
    expect(formatVerifiesVia({ text: 't', behavior: { tool: 'search_web' } })).toBe('search_web x1+')
  })

  it('appends " per attempt" only for the attempt scope', () => {
    expect(
      formatVerifiesVia({
        text: 't',
        behavior: { tool: 'search_web', min_count: 1, max_count: 2, scope: 'attempt' },
      }),
    ).toBe('search_web x1-2 per attempt')
    expect(formatVerifiesVia(BEHAVIOR)).not.toContain('per attempt')
  })
})

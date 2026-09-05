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

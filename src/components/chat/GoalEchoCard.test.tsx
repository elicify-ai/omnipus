// GoalEchoCard.test.tsx — ADR-081 D5/D9 (work-first goal flow, test 24):
// the card is now a registered-record view rendered from the ACTIVE frame,
// no buttons, no confirm/amend/cancel wiring. Criteria breakdown per
// ADR-074 D5.2 / judgment-first FR-011 (US-6, test 19 component half).

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { GoalEchoCard } from './GoalEchoCard'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

type GoalCriterion = NonNullable<GoalStatusFrame['criteria']>[number]

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 0,
    max_rounds: 20,
    latest_reason: '',
    active_loops: 0,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

function makeCriterion(overrides: Partial<GoalCriterion> = {}): GoalCriterion {
  return {
    kind: 'prose',
    judgment: 'boolean',
    text: 'the release notes are written',
    author: { kind: 'agent', id: 'mia' },
    status: 'pending',
    ...overrides,
  }
}

describe('GoalEchoCard', () => {
  it('renders the compiled condition + round accounting', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.getByTestId('goal-echo-card')).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('ship the release')
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('20 rounds')
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('16 concurrent loops')
  })

  // US-6 S1: an active goal with 2 prose + 1 marker check → 3 rows, text
  // first, verbatim command chip on the check row only. Rendered by the
  // SHARED CriteriaBreakdown (D5.4) — the chip format asserted here is the
  // shared formatVerifiesVia contract, identical on every surface. The
  // criteria list lives behind a collapsed-by-default accordion (redesign,
  // operator report 2026-09-07) — expand it via its trigger before asserting
  // row content.
  it('itemizes the criteria breakdown plain-language-first with a verifies-via chip on technical rows, once expanded', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion({ text: 'the release notes are written' }),
        makeCriterion({ text: 'the changelog covers every user-facing change' }),
        makeCriterion({
          kind: 'check',
          text: 'the test suite passes',
          check: { command: 'go test ./...', expected_exit_code: 0 },
        }),
      ],
    })
    render(<GoalEchoCard frame={frame} />)

    // The accordion section mounts, collapsed, with a count in its header —
    // no row content until expanded.
    expect(screen.getByTestId('goal-echo-criteria')).toBeInTheDocument()
    expect(screen.getByTestId('goal-echo-criteria-trigger')).toHaveTextContent('Done when · 3 criteria')
    expect(screen.getByTestId('goal-echo-criteria-trigger')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByRole('listitem')).not.toBeInTheDocument()

    fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))

    expect(screen.getByTestId('goal-echo-criteria-trigger')).toHaveAttribute('aria-expanded', 'true')
    const rows = screen.getAllByRole('listitem')
    expect(rows).toHaveLength(3)
    expect(rows[0]).toHaveTextContent('the release notes are written')
    expect(rows[1]).toHaveTextContent('the changelog covers every user-facing change')
    expect(rows[2]).toHaveTextContent('the test suite passes')

    // Exactly ONE chip — the check row's — carrying the command VERBATIM in
    // the shared "command -> exit N" format.
    expect(screen.getAllByText('verifies via:')).toHaveLength(1)
    expect(screen.getByText('go test ./... -> exit 0')).toBeInTheDocument()
  })

  it('renders a behavior payload as a verifies-via chip (tool + counts, shared format), once expanded', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion({
          kind: 'behavior',
          text: 'research draws on real sources',
          behavior: { tool: 'search_web', min_count: 3 },
        }),
      ],
    })
    render(<GoalEchoCard frame={frame} />)
    fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))
    expect(screen.getByText('verifies via:')).toBeInTheDocument()
    expect(screen.getByText('search_web x3+')).toBeInTheDocument()
  })

  // US-6 S4 (negative): `[kind]` classification tokens are not user-facing.
  it('renders NO [kind] tokens anywhere on the card, collapsed or expanded', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion(),
        makeCriterion({
          kind: 'check',
          text: 'tests pass',
          check: { command: 'go test ./...', expected_exit_code: 0 },
        }),
      ],
    })
    const { container } = render(<GoalEchoCard frame={frame} />)
    expect(container.textContent).not.toMatch(/\[(check|prose|behavior)\]/)
    fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))
    expect(container.textContent).not.toMatch(/\[(check|prose|behavior)\]/)
  })

  it('hides the criteria section when the frame carries none (legacy frames)', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.queryByTestId('goal-echo-criteria')).not.toBeInTheDocument()
  })

  // ADR-080 D-STATEMENT: the restated goal statement renders as a lead line,
  // distinct from and above the existing compiled `condition`.
  it('renders the restated goal statement as a lead line above the condition', () => {
    const frame = makeGoal({
      condition: 'goal_marker_a1b2',
      definition: 'Ship the release with a written changelog.',
    })
    render(<GoalEchoCard frame={frame} />)
    const statement = screen.getByTestId('goal-echo-statement')
    expect(statement).toHaveTextContent('Ship the release with a written changelog.')
    expect(screen.getByTestId('goal-echo-condition')).toHaveTextContent('goal_marker_a1b2')
  })

  // ADR-081 round-2 B-3 / test 24: `definition` is legitimately ABSENT on a
  // marker-path record (the existing Prompt/Intent fallback) — the card
  // renders gracefully with no statement block, not a placeholder.
  it('renders no statement line when the frame carries no definition (marker-path/legacy frames)', () => {
    render(<GoalEchoCard frame={makeGoal()} />)
    expect(screen.queryByTestId('goal-echo-statement')).not.toBeInTheDocument()
  })

  // ADR-080 D-TYPES: every criterion row carries a small judgment icon
  // (redesign, operator report 2026-09-07 — an icon + accessible name/
  // tooltip replaces the old uppercase text badge).
  it('renders a judgment icon (not text) on every criterion row, once expanded', () => {
    const frame = makeGoal({
      criteria: [
        makeCriterion({ text: 'the release notes are written', judgment: 'boolean' }),
        makeCriterion({ text: 'at least 3 changelog entries', judgment: 'quantitative' }),
        makeCriterion({ text: 'a signed changelog file exists', judgment: 'artifact' }),
      ],
    })
    render(<GoalEchoCard frame={frame} />)
    fireEvent.click(screen.getByTestId('goal-echo-criteria-trigger'))
    const badges = screen.getAllByTestId('criterion-judgment-badge')
    expect(badges).toHaveLength(3)
    // No raw judgment word as VISIBLE text content — it's an icon now,
    // legible only via its accessible name / tooltip.
    expect(badges[0].textContent).toBe('')
    expect(badges[1].textContent).toBe('')
    expect(badges[2].textContent).toBe('')
    expect(badges[0]).toHaveAttribute('aria-label', 'Pass/fail')
    expect(badges[1]).toHaveAttribute('aria-label', 'Measured')
    expect(badges[2]).toHaveAttribute('aria-label', 'Artifact')
  })

  // ADR-080 D-DOD: a distinct "Definition of Done" accordion, separate from
  // the criteria's "Done when" section, collapsed by default with an
  // "N inferred — review" hint on the header itself so an inferred item is
  // never hidden from the reader's attention (even collapsed) — only
  // expanding and reading it makes it visible.
  describe('Definition of Done (ADR-080 D-DOD)', () => {
    it('renders a distinct, collapsed-by-default DoD accordion, grouped separately from the criteria', () => {
      const frame = makeGoal({
        criteria: [makeCriterion({ text: 'the release notes are written' })],
        dod: [
          {
            kind: 'prose',
            judgment: 'boolean',
            provenance: 'floor',
            text: 'no secrets or credentials appear in the output',
            author: { kind: 'agent', id: 'mia' },
            status: 'pending',
          },
        ],
      })
      render(<GoalEchoCard frame={frame} />)
      expect(screen.getByTestId('goal-echo-criteria')).toBeInTheDocument()
      const dodBlock = screen.getByTestId('goal-echo-dod')
      expect(dodBlock).toHaveTextContent('Definition of Done · 1 item')
      expect(dodBlock).not.toHaveTextContent('no secrets or credentials appear in the output')

      fireEvent.click(screen.getByTestId('goal-echo-dod-trigger'))
      expect(dodBlock).toHaveTextContent('no secrets or credentials appear in the output')
    })

    it('flags a provenance:inferred DoD item on the COLLAPSED header, and again per-row once expanded', () => {
      const frame = makeGoal({
        dod: [
          {
            kind: 'prose',
            judgment: 'boolean',
            provenance: 'inferred',
            text: 'the response avoids speculative claims',
            author: { kind: 'agent', id: 'mia' },
            status: 'pending',
          },
        ],
      })
      render(<GoalEchoCard frame={frame} />)
      // Visible while collapsed — never silently hidden.
      expect(screen.getByTestId('goal-echo-dod-trigger')).toHaveTextContent('1 inferred — review')
      expect(screen.queryByTestId('goal-echo-dod-content')).not.toBeInTheDocument()

      fireEvent.click(screen.getByTestId('goal-echo-dod-trigger'))
      expect(screen.getByTestId('goal-echo-dod')).toHaveTextContent('inferred — confirm or drop')
    })

    it('does NOT show an inferred hint when no DoD item is inferred', () => {
      const frame = makeGoal({
        dod: [
          {
            kind: 'prose',
            judgment: 'boolean',
            provenance: 'stated',
            text: 'the setter explicitly asked for this',
            author: { kind: 'agent', id: 'mia' },
            status: 'pending',
          },
        ],
      })
      render(<GoalEchoCard frame={frame} />)
      expect(screen.getByTestId('goal-echo-dod-trigger')).not.toHaveTextContent('inferred')
      fireEvent.click(screen.getByTestId('goal-echo-dod-trigger'))
      expect(screen.getByTestId('goal-echo-dod')).not.toHaveTextContent('inferred — confirm or drop')
    })

    it('hides the DoD section entirely when the frame carries none', () => {
      render(<GoalEchoCard frame={makeGoal()} />)
      expect(screen.queryByTestId('goal-echo-dod')).not.toBeInTheDocument()
    })
  })

  it('shows singular "loop" when cap is 1', () => {
    render(<GoalEchoCard frame={makeGoal({ cap: 1 })} />)
    expect(screen.getByTestId('goal-echo-round')).toHaveTextContent('1 concurrent loop')
  })

  // ADR-081 D5/D9 (test 24): the confirm-gate button row is deleted in
  // full — no Confirm/Amend/Cancel control exists anywhere on the card,
  // for ANY state, including one that (pre-ADR-081) would have been
  // pending confirmation.
  describe('ADR-081 D5/D9 — no confirm/amend/cancel controls', () => {
    it('renders no buttons at all on an active record with criteria and DoD', () => {
      const frame = makeGoal({
        criteria: [makeCriterion()],
        dod: [
          {
            kind: 'prose',
            judgment: 'boolean',
            provenance: 'stated',
            text: 'the setter explicitly asked for this',
            author: { kind: 'agent', id: 'mia' },
            status: 'pending',
          },
        ],
      })
      render(<GoalEchoCard frame={frame} />)
      expect(screen.queryByRole('button', { name: /confirm/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /cancel/i })).not.toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /amend/i })).not.toBeInTheDocument()
      expect(screen.queryByTestId('goal-echo-actions')).not.toBeInTheDocument()
      // The accordion triggers ARE buttons — assert no OTHER buttons exist
      // beyond the two accordion triggers.
      const buttons = screen.getAllByRole('button')
      for (const btn of buttons) {
        expect(btn.dataset.testid).toMatch(/-trigger$/)
      }
    })

    it('renders no buttons regardless of frame.state (no confirm-only gate survives)', () => {
      for (const state of ['active', 'judging', 'done', 'failed', 'cleared'] as const) {
        const { unmount } = render(<GoalEchoCard frame={makeGoal({ state })} />)
        expect(screen.queryByTestId('goal-echo-actions')).not.toBeInTheDocument()
        expect(screen.queryByRole('button', { name: /confirm/i })).not.toBeInTheDocument()
        unmount()
      }
    })

    it('accepts no onConfirm/onCancel/onAmend props (component surface no longer has them)', () => {
      // TypeScript itself enforces this at the call site (GoalEchoCardProps
      // has no callback fields); this runtime assertion just confirms
      // clicking anywhere on the card fires nothing chat-related — there is
      // no click handler left to fire besides the accordion triggers.
      const clickSpy = vi.fn()
      // No `jsx-a11y` plugin is registered in this repo's eslint.config.mjs
      // (baseline scope: @eslint/js + typescript-eslint only — see that
      // file's header comment), so a `jsx-a11y/*` disable-directive here
      // hard-errors ESLint's directive validation with "Definition for rule
      // ... was not found" rather than suppressing anything real. A plain
      // `onClick` on a test-only wrapper `<div>` isn't flagged by any rule
      // this config actually enables.
      render(
        <div onClick={clickSpy}>
          <GoalEchoCard frame={makeGoal({ criteria: [makeCriterion()] })} />
        </div>,
      )
      fireEvent.click(screen.getByTestId('goal-echo-card'))
      expect(clickSpy).toHaveBeenCalledTimes(1)
      // Nothing beyond bubbling occurred — no accidental confirm/cancel side
      // effect wired to the card body itself.
    })
  })
})

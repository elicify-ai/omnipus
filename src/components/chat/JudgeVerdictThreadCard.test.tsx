// JudgeVerdictThreadCard.test.tsx — ADR-049 D2/D4/US-13/SD-C10.
//
// Inline thread rendering of a `Message.type === 'judge_verdict'` entry
// (verbose-chat-only surface — the panel-only-by-default gating itself is
// `shouldRenderJudgeVerdictInThread`, exercised in ChatScreen's own tests;
// this file covers the CARD's own rendering given a verdict).
//
// NOTE (intentional spec deviation): the component deliberately OMITS a
// token/cost "spend" line, even though spec Test 20 mentioned one — see
// JudgeVerdictThreadCard.tsx's file header: `JudgeVerdict`/`JudgeVerdictFrame`
// carry no tokens/cost field on the wire (only `Message.tokens`/`Message.cost`
// do, and only on the persisted transcript twin). This test asserts what the
// component ACTUALLY renders — no spend line — rather than the spec's
// original (now-superseded) expectation.

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { JudgeVerdictThreadCard } from './JudgeVerdictThreadCard'
import type { JudgeVerdict } from '@/lib/api'

// Toolui-analysis item 5 (founder-approved 2026-09-26): the card starts
// COLLAPSED — the header line "Judge verdict · <scope> round N · met/unmet"
// is all that renders; the criteria list and model/judge footer sit inside
// the DisclosureRow accordion. Tests that assert body content must expand
// via the `judge-verdict-toggle` testid first.
function expandCard() {
  fireEvent.click(screen.getByTestId('judge-verdict-toggle'))
}

function makeVerdict(overrides: Partial<JudgeVerdict> = {}): JudgeVerdict {
  return {
    id: 'verdict-1',
    scope: 'task',
    task_id: 'task-1',
    round: 1,
    met: true,
    per_criterion: [
      { criterion_id: 'crit-1', met: true, reason: 'go test output shows all tests passing.' },
    ],
    model: 'z-ai/glm-5-turbo',
    judged_at: '2026-07-19T12:05:00Z',
    judge_agent_id: 'judge',
    ...overrides,
  }
}

describe('JudgeVerdictThreadCard — composition', () => {
  it('renders the scope + round heading and the overall met/unmet indicator', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict({ scope: 'task', round: 2, met: true })} />)
    expect(screen.getByText('Judge verdict · task round 2')).toBeInTheDocument()
    expect(screen.getByText('met')).toBeInTheDocument()
  })

  it('renders "unmet" in the one-line header when the overall verdict failed', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict({ met: false })} />)
    expect(screen.getByText('unmet')).toBeInTheDocument()
    expect(screen.queryByText('met')).not.toBeInTheDocument()
  })

  it('renders a per-criterion row with its id and reason (after expanding)', () => {
    render(
      <JudgeVerdictThreadCard
        verdict={makeVerdict({
          per_criterion: [
            { criterion_id: 'crit-abc', met: false, reason: '3 tests still failing.' },
          ],
        })}
      />,
    )
    expandCard()
    expect(screen.getByText('crit-abc')).toBeInTheDocument()
    expect(screen.getByText('3 tests still failing.')).toBeInTheDocument()
  })

  it('renders every criterion when there are several, in order (after expanding)', () => {
    render(
      <JudgeVerdictThreadCard
        verdict={makeVerdict({
          per_criterion: [
            { criterion_id: 'crit-1', met: true, reason: 'ok' },
            { criterion_id: 'crit-2', met: false, reason: 'not ok' },
          ],
        })}
      />,
    )
    expandCard()
    expect(screen.getByText('crit-1')).toBeInTheDocument()
    expect(screen.getByText('crit-2')).toBeInTheDocument()
  })

  it('renders the model + judged-by line (after expanding)', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict({ model: 'anthropic/claude-3.5-haiku', judge_agent_id: 'judge' })} />)
    expandCard()
    expect(screen.getByText('anthropic/claude-3.5-haiku · judged by judge')).toBeInTheDocument()
  })

  it('renders scope=plan and scope=goal headings distinctly', () => {
    const { rerender } = render(<JudgeVerdictThreadCard verdict={makeVerdict({ scope: 'plan', plan_id: 'plan-1', task_id: undefined, round: 1 })} />)
    expect(screen.getByText('Judge verdict · plan round 1')).toBeInTheDocument()

    rerender(<JudgeVerdictThreadCard verdict={makeVerdict({ scope: 'goal', task_id: undefined, round: 4 })} />)
    expect(screen.getByText('Judge verdict · goal round 4')).toBeInTheDocument()
  })

  it('carries the data-testid used by callers/snapshots', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    expect(screen.getByTestId('judge-verdict-thread-card')).toBeInTheDocument()
  })
})

describe('JudgeVerdictThreadCard — collapsed-by-default accordion (toolui-analysis item 5)', () => {
  it('starts collapsed: only the one-line header renders, criteria list and model footer are absent', () => {
    const { container } = render(
      <JudgeVerdictThreadCard
        verdict={makeVerdict({
          per_criterion: [{ criterion_id: 'crit-hidden', met: true, reason: 'hidden until expanded' }],
        })}
      />,
    )
    expect(screen.getByTestId('judge-verdict-toggle')).toHaveAttribute('aria-expanded', 'false')
    expect(screen.getByText('Judge verdict · task round 1')).toBeInTheDocument()
    expect(screen.getByText('met')).toBeInTheDocument()
    expect(screen.queryByText('crit-hidden')).not.toBeInTheDocument()
    expect(screen.queryByText('z-ai/glm-5-turbo · judged by judge')).not.toBeInTheDocument()
    // The whole card is a single compact line — no criteria content leaked
    // into the collapsed DOM at all.
    expect(container.textContent).not.toContain('hidden until expanded')
  })

  it('expands on click: aria-expanded flips true and the body renders', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    const toggle = screen.getByTestId('judge-verdict-toggle')
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('crit-1')).toBeInTheDocument()
    expect(screen.getByText('z-ai/glm-5-turbo · judged by judge')).toBeInTheDocument()
  })

  it('collapses again on a second click', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    const toggle = screen.getByTestId('judge-verdict-toggle')
    fireEvent.click(toggle)
    expect(screen.getByText('crit-1')).toBeInTheDocument()
    fireEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('crit-1')).not.toBeInTheDocument()
  })

  it('toggle is a native, focusable <button> — keyboard activation is the catalogued Button contract (Enter/Space)', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    const toggle = screen.getByTestId('judge-verdict-toggle')
    expect(toggle.tagName).toBe('BUTTON')
    toggle.focus()
    expect(document.activeElement).toBe(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
  })
})

describe('JudgeVerdictThreadCard — intentional spec deviation: no spend/cost line', () => {
  it('renders no token/cost "spend" text anywhere in the card, even though the verdict carries no such field to begin with', () => {
    const { container } = render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    // JudgeVerdict has no tokens/cost field on the wire (see file header) —
    // this pins the component to NEVER growing a spend line even if a future
    // caller starts passing a cast, or grows a spend line from nothing.
    expect(screen.queryByText(/spend/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/token/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/cost/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument()
    expandCard()
    expect(container).toHaveTextContent('z-ai/glm-5-turbo · judged by judge')
  })
})

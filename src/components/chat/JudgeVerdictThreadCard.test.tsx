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
import { render, screen } from '@testing-library/react'
import { JudgeVerdictThreadCard } from './JudgeVerdictThreadCard'
import type { JudgeVerdict } from '@/lib/api'

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
    expect(screen.getByText('Judge verdict — task round 2')).toBeInTheDocument()
    expect(screen.getByText('met')).toBeInTheDocument()
  })

  it('renders "unmet" when the overall verdict failed', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict({ met: false })} />)
    expect(screen.getByText('unmet')).toBeInTheDocument()
    expect(screen.queryByText('met')).not.toBeInTheDocument()
  })

  it('renders a per-criterion row with its id and reason', () => {
    render(
      <JudgeVerdictThreadCard
        verdict={makeVerdict({
          per_criterion: [
            { criterion_id: 'crit-abc', met: false, reason: '3 tests still failing.' },
          ],
        })}
      />,
    )
    expect(screen.getByText('crit-abc')).toBeInTheDocument()
    expect(screen.getByText('3 tests still failing.')).toBeInTheDocument()
  })

  it('renders every criterion when there are several, in order', () => {
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
    expect(screen.getByText('crit-1')).toBeInTheDocument()
    expect(screen.getByText('crit-2')).toBeInTheDocument()
  })

  it('renders the model + judged-by line', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict({ model: 'anthropic/claude-3.5-haiku', judge_agent_id: 'judge' })} />)
    expect(screen.getByText('anthropic/claude-3.5-haiku · judged by judge')).toBeInTheDocument()
  })

  it('renders scope=plan and scope=goal headings distinctly', () => {
    const { rerender } = render(<JudgeVerdictThreadCard verdict={makeVerdict({ scope: 'plan', plan_id: 'plan-1', task_id: undefined, round: 1 })} />)
    expect(screen.getByText('Judge verdict — plan round 1')).toBeInTheDocument()

    rerender(<JudgeVerdictThreadCard verdict={makeVerdict({ scope: 'goal', task_id: undefined, round: 4 })} />)
    expect(screen.getByText('Judge verdict — goal round 4')).toBeInTheDocument()
  })

  it('carries the data-testid used by callers/snapshots', () => {
    render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    expect(screen.getByTestId('judge-verdict-thread-card')).toBeInTheDocument()
  })
})

describe('JudgeVerdictThreadCard — intentional spec deviation: no spend/cost line', () => {
  it('renders no token/cost "spend" text anywhere in the card, even though the verdict carries no such field to begin with', () => {
    const { container } = render(<JudgeVerdictThreadCard verdict={makeVerdict()} />)
    // JudgeVerdict has no tokens/cost field on the wire (see file header) —
    // this pins the component to NEVER growing a spend line even if a future
    // caller starts passing a shape with extra fields; it must key off
    // `verdict.model`/`judge_agent_id` only, not fabricate cost data.
    expect(screen.queryByText(/spend/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/token/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/cost/i)).not.toBeInTheDocument()
    expect(screen.queryByText(/\$/)).not.toBeInTheDocument()
    expect(container).toHaveTextContent('z-ai/glm-5-turbo · judged by judge')
  })
})

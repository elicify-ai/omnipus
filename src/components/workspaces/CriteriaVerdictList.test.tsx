/**
 * CriteriaVerdictList.test.tsx
 *
 * ADR-049 FR-088 (US-11 AS-3): per-attempt criterion verdicts (met/unmet +
 * judge reason) and the "attempt N/M" counter; FR-089 (US-11 AS-4): the
 * evidence viewer expands per criterion with redaction + truncation markers,
 * never a raw secret.
 */

import { describe, it, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CriteriaVerdictList, DEFAULT_TASK_MAX_ATTEMPTS } from './CriteriaVerdictList'
import type { AcceptanceCriterion, JudgeVerdict, EvidenceRecord } from '@/lib/api'

function makeCriterion(overrides: Partial<AcceptanceCriterion> = {}): AcceptanceCriterion {
  return {
    id: 'crit-1',
    kind: 'check',
    text: 'Tests pass',
    author: { kind: 'user', id: 'alice' },
    status: 'pending',
    ...overrides,
  }
}

function makeVerdict(overrides: Partial<JudgeVerdict> = {}): JudgeVerdict {
  return {
    id: 'verdict-1',
    scope: 'task',
    task_id: 'task-1',
    round: 1,
    met: false,
    per_criterion: [],
    model: 'z-ai/glm-5-turbo',
    judged_at: '2026-07-19T12:00:00Z',
    judge_agent_id: 'judge',
    ...overrides,
  }
}

function makeEvidence(overrides: Partial<EvidenceRecord> = {}): EvidenceRecord {
  return {
    id: 'ev-1',
    task_id: 'task-1',
    criterion_id: 'crit-1',
    attempt: 1,
    command: 'go test ./... [REDACTED]',
    exit_code: 1,
    output: 'FAIL: 3 tests failing',
    truncated: false,
    timed_out: false,
    policy_denied: false,
    recorded_at: '2026-07-19T12:00:00Z',
    ...overrides,
  }
}

describe('CriteriaVerdictList — attempt counter', () => {
  it('renders "attempt N/M" from attemptCount + maxAttempts', () => {
    render(
      <CriteriaVerdictList
        criteria={[makeCriterion()]}
        attemptCount={2}
        maxAttempts={3}
      />,
    )
    expect(screen.getByTestId('attempt-counter')).toHaveTextContent('attempt 2/3')
  })

  it('falls back to the default max attempts (3) when maxAttempts is absent', () => {
    render(<CriteriaVerdictList criteria={[makeCriterion()]} attemptCount={1} />)
    expect(screen.getByTestId('attempt-counter')).toHaveTextContent(`attempt 1/${DEFAULT_TASK_MAX_ATTEMPTS}`)
  })

  it('does not render an attempt counter when attemptCount is absent', () => {
    render(<CriteriaVerdictList criteria={[makeCriterion()]} />)
    expect(screen.queryByTestId('attempt-counter')).toBeNull()
  })

  it('renders nothing at all when there are zero criteria', () => {
    const { container } = render(<CriteriaVerdictList criteria={[]} attemptCount={1} />)
    expect(container.firstChild).toBeNull()
  })
})

describe('CriteriaVerdictList — per-criterion met/unmet + judge reason', () => {
  it('shows a met criterion with no reason needed to be present', () => {
    render(<CriteriaVerdictList criteria={[makeCriterion({ status: 'met' })]} />)
    expect(screen.getByText('Tests pass')).toBeInTheDocument()
  })

  it('shows the latest judge reason for an unmet criterion', () => {
    const criteria = [makeCriterion({ id: 'crit-1', status: 'unmet' })]
    const verdicts = [
      makeVerdict({
        round: 1,
        per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'go test output shows 3 failing tests' }],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    expect(screen.getByText('go test output shows 3 failing tests')).toBeInTheDocument()
  })

  it('uses the HIGHEST round verdict when multiple rounds exist (latest reason wins)', () => {
    const criteria = [makeCriterion({ id: 'crit-1', status: 'unmet' })]
    const verdicts = [
      makeVerdict({ round: 1, per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'first attempt reason' }] }),
      makeVerdict({ round: 2, per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'second attempt reason' }] }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    expect(screen.getByText('second attempt reason')).toBeInTheDocument()
    expect(screen.queryByText('first attempt reason')).toBeNull()
  })

  it('renders multiple criteria independently', () => {
    const criteria = [
      makeCriterion({ id: 'crit-1', text: 'Check A', status: 'met' }),
      makeCriterion({ id: 'crit-2', text: 'Check B', status: 'unmet', kind: 'prose' }),
    ]
    render(<CriteriaVerdictList criteria={criteria} />)
    expect(screen.getByText('Check A')).toBeInTheDocument()
    expect(screen.getByText('Check B')).toBeInTheDocument()
  })

  it('renders the reason at criterion-text size, not 10px muted (ADR-074 D5.3)', () => {
    const criteria = [makeCriterion({ id: 'crit-1', status: 'unmet' })]
    const verdicts = [
      makeVerdict({
        per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'the verdict statement' }],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    const reason = screen.getByTestId('verdict-reason')
    expect(reason).toHaveTextContent('the verdict statement')
    // The old rendering forced 10px; criterion-text size means NO explicit
    // font-size override below the list's text-xs.
    expect(reason.className).not.toMatch(/text-\[10px\]/)
  })

  it('renders no reason line for an id-less criterion even when verdicts exist (explicit absence handling)', () => {
    const criteria = [makeCriterion({ id: undefined, status: 'unmet' })]
    const verdicts = [
      makeVerdict({
        per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'someone else reason' }],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    expect(screen.queryByTestId('verdict-reason')).toBeNull()
    expect(screen.queryByTestId('verdict-evidence-quote')).toBeNull()
  })
})

describe('CriteriaVerdictList — evidence quote (ADR-074 D7 / US-5)', () => {
  it('renders a non-empty evidence_quote as an inert quoted line under the reason', () => {
    const hostile = '--- PASS: TestX <img src=x onerror="alert(1)"> **not markdown**'
    const criteria = [makeCriterion({ id: 'crit-1', status: 'met' })]
    const verdicts = [
      makeVerdict({
        per_criterion: [
          { criterion_id: 'crit-1', met: true, reason: 'tests pass', evidence_quote: hostile },
        ],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    const quote = screen.getByTestId('verdict-evidence-quote')
    // Inert: the hostile payload appears as literal TEXT — never parsed as
    // HTML or markdown (no img element materializes).
    expect(quote).toHaveTextContent('<img src=x onerror="alert(1)">')
    expect(quote.querySelector('img')).toBeNull()
    expect(quote.textContent).toContain('**not markdown**')
  })

  it('renders NO quote line when evidence_quote is absent (pre-D7 / old-soul verdicts)', () => {
    const criteria = [makeCriterion({ id: 'crit-1', status: 'unmet' })]
    const verdicts = [
      makeVerdict({
        per_criterion: [{ criterion_id: 'crit-1', met: false, reason: 'fail-closed reason' }],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    expect(screen.getByTestId('verdict-reason')).toBeInTheDocument()
    expect(screen.queryByTestId('verdict-evidence-quote')).toBeNull()
  })

  it('renders NO quote line when evidence_quote is the empty string (fail-closed verdicts)', () => {
    const criteria = [makeCriterion({ id: 'crit-1', status: 'unmet' })]
    const verdicts = [
      makeVerdict({
        per_criterion: [
          { criterion_id: 'crit-1', met: false, reason: 'nothing to quote', evidence_quote: '' },
        ],
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} verdicts={verdicts} />)
    expect(screen.queryByTestId('verdict-evidence-quote')).toBeNull()
  })
})

describe('CriteriaVerdictList — evidence viewer expand (US-11 AS-4)', () => {
  it('does not render an expand control when there is no evidence for a criterion', () => {
    render(<CriteriaVerdictList criteria={[makeCriterion()]} />)
    expect(screen.queryByRole('button', { name: /expand evidence/i })).toBeNull()
  })

  it('expands to show the evidence viewer with redaction + truncation markers, never a raw secret', () => {
    const criteria = [makeCriterion({ id: 'crit-1' })]
    const evidence = [
      makeEvidence({
        criterion_id: 'crit-1',
        command: 'curl https://api.example.com --header "Authorization: [REDACTED]"',
        output: 'ok [REDACTED]...[truncated 500 bytes]',
        truncated: true,
      }),
    ]
    render(<CriteriaVerdictList criteria={criteria} evidence={evidence} />)

    const expandBtn = screen.getByRole('button', { name: /expand evidence/i })
    expect(expandBtn).toHaveAttribute('aria-expanded', 'false')

    fireEvent.click(expandBtn)

    const viewer = screen.getByTestId('evidence-viewer')
    expect(viewer).toBeInTheDocument()
    expect(screen.getByTestId('evidence-truncated')).toBeInTheDocument()
    expect(viewer.textContent).toMatch(/\[REDACTED\]/)
    // The raw secret this fixture would otherwise carry never appears anywhere.
    expect(screen.queryByText(/sk-live-/)).toBeNull()
    expect(expandBtn).toHaveAttribute('aria-expanded', 'true')
  })

  it('collapses again on a second click', () => {
    const criteria = [makeCriterion({ id: 'crit-1' })]
    const evidence = [makeEvidence({ criterion_id: 'crit-1' })]
    render(<CriteriaVerdictList criteria={criteria} evidence={evidence} />)

    const expandBtn = screen.getByRole('button', { name: /expand evidence/i })
    fireEvent.click(expandBtn)
    expect(screen.getByTestId('evidence-viewer')).toBeInTheDocument()

    const collapseBtn = screen.getByRole('button', { name: /collapse evidence/i })
    fireEvent.click(collapseBtn)
    expect(screen.queryByTestId('evidence-viewer')).toBeNull()
  })

  it('shows the LATEST attempt evidence when multiple attempts recorded evidence for the same criterion', () => {
    const criteria = [makeCriterion({ id: 'crit-1' })]
    const evidence = [
      makeEvidence({ id: 'ev-1', criterion_id: 'crit-1', attempt: 1, output: 'attempt one output' }),
      makeEvidence({ id: 'ev-2', criterion_id: 'crit-1', attempt: 2, output: 'attempt two output' }),
    ]
    render(<CriteriaVerdictList criteria={criteria} evidence={evidence} />)
    fireEvent.click(screen.getByRole('button', { name: /expand evidence/i }))
    expect(screen.getByText('attempt two output')).toBeInTheDocument()
    expect(screen.queryByText('attempt one output')).toBeNull()
  })

  it('surfaces a timed-out check distinctly from a failing exit code', () => {
    const criteria = [makeCriterion({ id: 'crit-1' })]
    const evidence = [makeEvidence({ criterion_id: 'crit-1', timed_out: true, exit_code: -1 })]
    render(<CriteriaVerdictList criteria={criteria} evidence={evidence} />)
    fireEvent.click(screen.getByRole('button', { name: /expand evidence/i }))
    expect(screen.getByTestId('evidence-timed-out')).toBeInTheDocument()
  })

  it('surfaces a policy-denied check', () => {
    const criteria = [makeCriterion({ id: 'crit-1' })]
    const evidence = [makeEvidence({ criterion_id: 'crit-1', policy_denied: true, exit_code: -1 })]
    render(<CriteriaVerdictList criteria={criteria} evidence={evidence} />)
    fireEvent.click(screen.getByRole('button', { name: /expand evidence/i }))
    expect(screen.getByTestId('evidence-policy-denied')).toBeInTheDocument()
  })
})

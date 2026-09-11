// GoalPillTray.test.tsx — ADR-053 FE-1 / US-14.
//
// Covers the bottom-right per-goal-id pill tray: one pill per goal-id, all 8
// pill states, click-to-expand, and the latest-verdict enrichment from the
// judgeActivity store.

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import { GoalPillTray } from './GoalPillTray'
import { useChatStore } from '@/store/chat'
import { useJudgeActivityStore } from '@/store/judgeActivity'
import type { GoalStatusFrame } from '@/lib/api/generated/asyncapi-types'

function makeGoal(overrides: Partial<GoalStatusFrame> = {}): GoalStatusFrame {
  return {
    type: 'goal_status',
    session_id: 's1',
    condition: 'ship the release',
    round: 3,
    max_rounds: 20,
    latest_reason: 'tests still failing',
    active_loops: 1,
    cap: 16,
    state: 'active',
    ...overrides,
  }
}

describe('GoalPillTray — empty', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('renders nothing when no goals are active', () => {
    const { container } = render(<GoalPillTray />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('GoalPillTray — per-goal-id pills', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('renders one pill for a single goal', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)
    expect(screen.getByTestId('goal-pill-tray')).toBeInTheDocument()
    expect(screen.getByTestId('goal-pill-active')).toBeInTheDocument()
  })

  it('renders TWO pills for a session with two goals (per-goal-id)', () => {
    useChatStore.setState({
      goalPills: {
        g1: makeGoal({ goal_id: 'g1', condition: 'ship release' }),
        g2: makeGoal({ goal_id: 'g2', condition: 'fix bug #42' }),
      },
    })
    render(<GoalPillTray />)
    const pills = screen.getAllByTestId('goal-pill-wrapper')
    expect(pills).toHaveLength(2)
    expect(screen.getByText('ship release')).toBeInTheDocument()
    expect(screen.getByText('fix bug #42')).toBeInTheDocument()
  })

  it('uses _default key for a frame with no goal_id', () => {
    const frame = makeGoal()
    useChatStore.setState({ goalPills: { _default: frame } })
    render(<GoalPillTray />)
    const pill = screen.getByTestId('goal-pill-active')
    expect(pill).toHaveAttribute('data-goal-id', '_default')
  })
})

describe('GoalPillTray — 13-state rendering (ADR-081 D5/D9: `queued` retired; C-39: five states added)', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  // 13 of the 14 wire states (contracts/components/schemas/GoalStatusFrame.yaml)
  // render a dedicated pill, including `cleared` — the UAT S3 post-ADR-053
  // addition — and the five states added by the joint ADR-084/ADR-085/
  // ADR-086 delivery (C-39): `judge_refused_god_mode`, `judge_cas_loss`,
  // `blocked`, `claim_overturned`, `expired`. The 14th, `queued`, is
  // retired (ADR-081 D5/D9) — see the dedicated describe block below for
  // its now-renders-nothing coverage.
  const states: Array<[GoalStatusFrame['state'], string]> = [
    ['active', 'goal-pill-active'],
    ['waiting_on_user', 'goal-pill-waiting'],
    ['judge_unavailable', 'goal-pill-judge-unavailable'],
    ['re-planning', 'goal-pill-replanning'],
    ['judging', 'goal-pill-judging'],
    ['done', 'goal-pill-done'],
    ['failed', 'goal-pill-failed'],
    ['cleared', 'goal-pill-cleared'],
    ['judge_refused_god_mode', 'goal-pill-judge-refused-god-mode'],
    ['judge_cas_loss', 'goal-pill-judge-cas-loss'],
    ['blocked', 'goal-pill-blocked'],
    ['claim_overturned', 'goal-pill-claim-overturned'],
    ['expired', 'goal-pill-expired'],
  ]

  for (const [state, testId] of states) {
    it(`renders the ${state} pill with the correct testId`, () => {
      useChatStore.setState({ goalPills: { g1: makeGoal({ state, goal_id: 'g1' }) } })
      render(<GoalPillTray />)
      expect(screen.getByTestId(testId)).toBeInTheDocument()
    })
  }

  // Regression guard (mutation-testing finding): a mutation that rendered
  // `cleared` identically to `failed` (same testid `goal-pill-failed`, same
  // XCircle icon, same error/red tone) passed every other test in this
  // suite. `cleared` means "the user deliberately stopped this goal" — it
  // MUST NOT read as a failure. This test fails under that exact mutation.
  it('renders cleared DISTINCTLY from failed — different testid, different icon, not the error/red tone', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'cleared' }) } })
    const { unmount } = render(<GoalPillTray />)

    const clearedPill = screen.getByTestId('goal-pill-cleared')
    expect(screen.queryByTestId('goal-pill-failed')).not.toBeInTheDocument()
    expect(clearedPill.getAttribute('aria-label')).toContain('state cleared')
    expect(clearedPill.className).toContain('color-muted')
    expect(clearedPill.className).not.toContain('color-error')
    const clearedIconHtml = clearedPill.querySelector('svg')?.outerHTML
    unmount()

    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'failed' }) } })
    render(<GoalPillTray />)
    const failedPill = screen.getByTestId('goal-pill-failed')
    expect(failedPill.getAttribute('aria-label')).toContain('state failed')
    expect(failedPill.className).toContain('color-error')
    const failedIconHtml = failedPill.querySelector('svg')?.outerHTML

    expect(clearedIconHtml).toBeTruthy()
    expect(failedIconHtml).toBeTruthy()
    expect(clearedIconHtml).not.toEqual(failedIconHtml)
  })
})

// ADR-081 D5/D9: `queued` is retired — the backend never emits it anymore
// (the pending-confirm state it represented is deleted in full). The
// wire-enum value survives untouched in the generated type (Constraint #8),
// so the tray still filters it out defensively rather than the type simply
// not existing.
describe('GoalPillTray — queued state (retired, ADR-081 D5/D9)', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('renders no tray at all when the only pill is queued', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'queued' }) } })
    const { container } = render(<GoalPillTray />)
    expect(container).toBeEmptyDOMElement()
    expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
    expect(screen.queryByTestId('goal-pill-queued')).not.toBeInTheDocument()
  })

  it('renders only the non-queued pill when a queued pill and a live pill coexist', () => {
    useChatStore.setState({
      goalPills: {
        stale: makeGoal({ goal_id: 'stale', state: 'queued' }),
        g1: makeGoal({ goal_id: 'g1', state: 'active' }),
      },
    })
    render(<GoalPillTray />)
    expect(screen.getAllByTestId('goal-pill-wrapper')).toHaveLength(1)
    expect(screen.getByTestId('goal-pill-active')).toBeInTheDocument()
    expect(screen.queryByTestId('goal-pill-queued')).not.toBeInTheDocument()
  })
})

describe('GoalPillTray — click to expand', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  it('shows full condition + round accounting on click', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)

    expect(screen.queryByTestId('goal-pill-expanded')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('goal-pill-active'))

    const expanded = screen.getByTestId('goal-pill-expanded')
    expect(expanded).toBeInTheDocument()
    expect(screen.getByTestId('goal-pill-condition')).toHaveTextContent('ship the release')
    expect(screen.getByTestId('goal-pill-round')).toHaveTextContent('round 3/20')
    expect(screen.getByTestId('goal-pill-round')).toHaveTextContent('active loops 1/16')
  })

  it('shows the latest reason when expanded', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', latest_reason: 'blocked on tests' }) } })
    render(<GoalPillTray />)
    fireEvent.click(screen.getByTestId('goal-pill-active'))
    expect(screen.getByText('blocked on tests')).toBeInTheDocument()
  })

  it('collapses on a second click', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    render(<GoalPillTray />)
    const pill = screen.getByTestId('goal-pill-active')

    fireEvent.click(pill)
    expect(screen.getByTestId('goal-pill-expanded')).toBeInTheDocument()

    fireEvent.click(pill)
    expect(screen.queryByTestId('goal-pill-expanded')).not.toBeInTheDocument()
  })

  it('shows per-criterion verdict when a goal-scoped JudgeVerdict exists', () => {
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1' }) } })
    useJudgeActivityStore.getState().apply({
      type: 'judge_verdict',
      id: 'v1',
      scope: 'goal',
      round: 2,
      met: false,
      per_criterion: [
        { criterion_id: 'c1', met: true, reason: 'tests pass' },
        { criterion_id: 'c2', met: false, reason: 'lint failing' },
      ],
      model: 'glm-5',
      judged_at: '2026-07-22T10:00:00Z',
      judge_agent_id: 'judge',
    })
    render(<GoalPillTray />)
    fireEvent.click(screen.getByTestId('goal-pill-active'))

    expect(screen.getByTestId('goal-pill-verdict-criteria')).toBeInTheDocument()
    expect(screen.getByText('tests pass')).toBeInTheDocument()
    expect(screen.getByText('lint failing')).toBeInTheDocument()
  })
})

// ── Regression coverage: bc66345f follow-up ─────────────────────────────────
//
// bc66345f gave every goal a stable, unique-per-generation `goal_id`
// (correctly — the multi-goal tray cannot disambiguate goals without it),
// which incidentally made `goalPills` grow WITHOUT BOUND: every terminated
// goal (done/failed/cleared) left a permanent, undismissable pill, since
// nothing ever removed an entry and this tray had no filter for terminal
// states. These tests prove the fix: a terminal pill is still briefly
// visible (the user's confirmation that something just happened), then
// this component stops rendering it — without deleting it from the store
// (chat.ts's own `evictGoalPillsOverCap` bounds the map size separately;
// see chat.goal-status-frame.test.ts for that half of the fix).
describe('GoalPillTray — terminal pill display window (regression fix, bc66345f follow-up)', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it.each(['done', 'failed', 'cleared'] as const)(
    'keeps a %s pill visible right after it terminates, then stops rendering it once the display window elapses',
    (state) => {
      vi.useFakeTimers()
      useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state }) } })
      render(<GoalPillTray />)

      // Still visible immediately — this IS the user's confirmation that the
      // goal just reached a terminal state. Do NOT hide it instantly.
      expect(screen.getByTestId('goal-pill-tray')).toBeInTheDocument()
      expect(screen.getByTestId(`goal-pill-${state}`)).toBeInTheDocument()

      // TERMINAL_PILL_DISPLAY_MS in GoalPillTray.tsx is 4000ms.
      act(() => {
        vi.advanceTimersByTime(4000)
      })

      // Gone from the tray — but (proven in chat.goal-status-frame.test.ts)
      // NOT deleted from the store; this is a render-layer decision only.
      expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
    },
  )

  it('does not evict a still-active pill early — only terminal pills get the display-window treatment', () => {
    vi.useFakeTimers()
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'active' }) } })
    render(<GoalPillTray />)

    act(() => {
      vi.advanceTimersByTime(60_000)
    })

    // A goal that never terminated stays on screen indefinitely — there is
    // no display window for a non-terminal state.
    expect(screen.getByTestId('goal-pill-active')).toBeInTheDocument()
  })

  it('does not accumulate a rendered pill per terminated goal — a session with 5 sequential goals never renders more than one pill at once, and none once every window has elapsed', () => {
    vi.useFakeTimers()
    render(<GoalPillTray />)

    // Mirrors chat.ts's real accumulation pattern: `case 'goal_status'`
    // merges every new goal_id into the EXISTING map
    // (`{ ...b.goalPills, [pillKey]: goalFrame }`), so by the end of this
    // loop the store holds all 5 terminated goals at once (this is exactly
    // the regression — proven separately, at the store level, in
    // chat.goal-status-frame.test.ts). What this test proves is that this
    // component never renders more than the currently-still-displaying
    // pill(s), regardless of how much terminal history the store retains.
    let accumulated: Record<string, ReturnType<typeof makeGoal>> = {}
    for (let i = 1; i <= 5; i++) {
      accumulated = {
        ...accumulated,
        [`g${i}`]: makeGoal({ goal_id: `g${i}`, state: 'done', condition: `goal ${i}` }),
      }
      act(() => {
        useChatStore.setState({ goalPills: accumulated })
      })

      // However many goals have terminated so far, at most the one that
      // JUST arrived is rendered — no tombstone pileup from the earlier ones.
      expect(screen.getAllByTestId('goal-pill-wrapper')).toHaveLength(1)
      expect(screen.getByText(`goal ${i}`)).toBeInTheDocument()

      act(() => {
        vi.advanceTimersByTime(4000)
      })
    }

    // The store retained the full 5-goal history (the fix does not lose
    // data)...
    expect(Object.keys(accumulated)).toHaveLength(5)
    // ...but the tray itself renders nothing once every pill's display
    // window has elapsed — this fix is render-layer only, not data-loss.
    expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
  })
})

// ── OQ-17: `expired` joins the terminal set; `blocked`/`claim_overturned` do NOT ──
//
// Plan OQ-17's assumption, taken as an instruction: `blocked` and
// `claim_overturned` are deliberately NOT added to GOAL_TERMINAL_STATES,
// because the goal is still live in both states and a returning operator
// MUST still see them (JUDGE-FR-102's whole point). `expired` IS added —
// it is the fourth distinguishable terminal ending (ADR-086 GOAL-FR-028).
// A mutation that folded any of these three the wrong way would pass every
// OTHER test in this file (each state's own dedicated testId still
// renders), so this coverage asserts the display-window BEHAVIOUR
// specifically, not just presence.
describe('GoalPillTray — terminal-set membership for the five new states (OQ-17)', () => {
  beforeEach(() => {
    useChatStore.setState({ goalPills: {} })
    useJudgeActivityStore.getState().reset()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('hides an expired pill once the terminal display window elapses — expired IS terminal', () => {
    vi.useFakeTimers()
    useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state: 'expired' }) } })
    render(<GoalPillTray />)

    expect(screen.getByTestId('goal-pill-expired')).toBeInTheDocument()
    act(() => {
      vi.advanceTimersByTime(4000)
    })
    expect(screen.queryByTestId('goal-pill-tray')).not.toBeInTheDocument()
  })

  it.each(['blocked', 'claim_overturned'] as const)(
    'never hides a %s pill, even long past the terminal display window — %s is NOT terminal (OQ-17)',
    (state) => {
      vi.useFakeTimers()
      useChatStore.setState({ goalPills: { g1: makeGoal({ goal_id: 'g1', state }) } })
      render(<GoalPillTray />)

      expect(screen.getByTestId(`goal-pill-${state === 'blocked' ? 'blocked' : 'claim-overturned'}`)).toBeInTheDocument()
      act(() => {
        vi.advanceTimersByTime(60_000)
      })
      // Still rendered — no display-window timer was ever armed for a
      // non-terminal state, however long real time passes.
      expect(screen.getByTestId(`goal-pill-${state === 'blocked' ? 'blocked' : 'claim-overturned'}`)).toBeInTheDocument()
    },
  )
})

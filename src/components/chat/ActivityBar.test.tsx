// ActivityBar — persistent activity strip tests.
//
// Covers: idle (nothing ever ran), 1 running, N running (span + bash), a
// background command alone (mounts, agent count excluded), the count
// text, clicking the bar opens the ActivityPanel slide-out, and (Fix 1,
// 2026-07-16) the revised mount matrix: an open panel survives running→0,
// a retained failure keeps the pill mounted in its failed-state variant,
// a purely-successful idle history still unmounts, and the spinner only
// ever appears while something is running.

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react'
import { act } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { ActivityBar } from './ActivityBar'
import { useChatStore } from '@/store/chat'
import type { ChatMessage, SubagentSpan } from '@/store/chat'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAgents: vi.fn().mockResolvedValue([
      { id: 'ray', name: 'Ray', type: 'Subagent', locked: false, status: 'active', color: '#4488ff', icon: 'compass' },
      { id: 'ext-1', name: 'ClaudeCode', type: 'subagent_3p', locked: false, status: 'active' },
    ]),
  }
})

// ADR-091 D7: ActivityPanel's ActivityRow calls useNavigate unconditionally
// (for the open control agent rows carry) — the panel mounts inside
// ActivityBar, so it needs a router context here too.
vi.mock('@tanstack/react-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@tanstack/react-router')>()
  return {
    ...actual,
    useNavigate: () => vi.fn(),
  }
})

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderBar() {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ActivityBar />
    </QueryClientProvider>,
  )
}

function makeAssistantMessage(spans: SubagentSpan[]): ChatMessage {
  return {
    id: 'msg_1',
    role: 'assistant',
    content: '',
    timestamp: new Date().toISOString(),
    status: 'done',
    spans,
  } as ChatMessage
}

// ADR-091 D7/FR-E-005 (cross-family review finding 20): `lifecycleState`
// defaults to 'running' here, not just `status` — the pill/avatar stack now
// key off `lifecycleState === 'running'` exactly (a span can be
// `status: 'running'` — the parent's "still open" flag — while queued or
// already lifecycle-terminal; see
// useRunningActivity.runningChildren-lifecycle.test.ts for that distinction
// pinned directly). A test that wants a queued/lifecycle-terminal-but-open
// span for THIS specific gap passes `lifecycleState` as an override.
function runningSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 's1',
    parentCallId: 'c1',
    taskLabel: 'digging into logs',
    status: 'running',
    lifecycleState: 'running',
    ...overrides,
  } as SubagentSpan
}

/** A terminal (non-running) span — status defaults to 'error' since most Fix-1 tests need a failure. */
function finishedSpan(overrides: Partial<SubagentSpan> = {}): SubagentSpan {
  return {
    spanId: 's1',
    parentCallId: 'c1',
    taskLabel: 'digging into logs',
    status: 'error',
    durationMs: 1200,
    ...overrides,
  } as SubagentSpan
}

beforeEach(() => {
  act(() => {
    useChatStore.setState({ messages: [], toolCalls: {} })
  })
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('ActivityBar — idle (0 running)', () => {
  // Regression test for a /visual-qa finding: the bar previously always
  // rendered, including an always-visible "No active background work" idle
  // state stretched full-width above the composer — noise, not signal, and
  // inconsistent with RateLimitIndicator's own precedent of conditional
  // mounting. The bar must now render nothing at all when idle.
  it('renders nothing when there is no running activity', () => {
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    expect(screen.queryByText('No active background work')).not.toBeInTheDocument()
  })
})

describe('ActivityBar — 1 running', () => {
  it('renders "1 running" for a single running agent span', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
  })

  // Fix 3 (2026-07-16): the pill was missing any in-progress signal beyond
  // the static avatar stack — the spinning ArrowsClockwise (same running-icon
  // vocabulary as toolStatusConfig's getToolBadgeStatusConfig/getSpanStatusDot
  // 'running' case) now sits before the "N running" text, decorative
  // (aria-hidden) since the text already carries the label for assistive tech.
  it('shows a spinning indicator (.animate-spin) in the pill while something is running', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
    const bar = screen.getByTestId('activity-bar')
    const spinner = bar.querySelector('.animate-spin')
    expect(spinner).not.toBeNull()
    expect(spinner).toHaveAttribute('aria-hidden', 'true')
  })
})

// ADR-091 D7/FR-E-005 (founder decision, round 8): the pill's count is now
// direct AGENT children in `running` only — "The SPA must not count shell
// jobs in the pill, because the pill answers 'how many sub-agents are
// running'" (WP-E spec, explicit prohibitions). Replaces the old
// "2 running" combined-count test, which pinned exactly the behavior this
// ADR retires.
describe('ActivityBar — N running (agent children only, ADR-091 FR-E-005)', () => {
  it('renders "1 running" for one agent span plus one background bash call — the bash call is excluded from the count', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
        toolCalls: {
          call_1: {
            id: 'call_1',
            call_id: 'call_1',
            tool: 'bash',
            params: { command: 'npm test', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_1","status":"running"}',
          },
        },
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })
  })

  // Founder decision 2026-09-25: a background command alone MUST bring the pill
  // back. The pill is ActivityPanel's only entry point, and the panel is the only
  // place background commands are listed, so gating the mount on agent children
  // made them unreachable. The COUNT stays agent-only (FR-E-005) — this is about
  // whether the pill exists at all. This test previously asserted the opposite.
  it('mounts for a background bash job alone, and names it — the panel is otherwise unreachable', () => {
    act(() => {
      useChatStore.setState({
        toolCalls: {
          call_1: {
            id: 'call_1',
            call_id: 'call_1',
            tool: 'bash',
            params: { command: 'npm test', run_in_background: true },
            status: 'success',
            result: '{"sessionId":"call_1","status":"running"}',
          },
        },
      })
    })
    renderBar()
    // Commands pill only — an agent-only mount gate fails this test (AC-1).
    // The Agents pill must stay down: a background command is not an agent child (D5).
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    const commands = screen.getByTestId('activity-pill-commands')
    expect(commands).toHaveTextContent('Commands')
    expect(commands).toHaveTextContent('1 background command')
    fireEvent.click(commands)
    expect(screen.getByText('npm test')).toBeInTheDocument()
    expect(screen.getByTestId('activity-section-commands')).toContainElement(screen.getByText('npm test'))
  })
})

describe('ActivityBar — avatar stack cap', () => {
  it('caps the rendered avatar stack at MAX_STACK_AVATARS (4) while the count label shows the true total of 5 agent children', async () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ spanId: 's1', parentCallId: 'c1', agentId: 'ray' }),
            runningSpan({ spanId: 's2', parentCallId: 'c2', agentId: 'ray' }),
            runningSpan({ spanId: 's3', parentCallId: 'c3', agentId: 'ray' }),
            runningSpan({ spanId: 's4', parentCallId: 'c4', agentId: 'ray' }),
            runningSpan({ spanId: 's5', parentCallId: 'c5', agentId: 'ray' }),
          ]),
        ],
      })
    })
    renderBar()

    // Count label must reflect the TRUE total (5), not the capped stack size.
    await waitFor(() => {
      expect(screen.getByText('5 running')).toBeInTheDocument()
    })
    expect(screen.getByTestId('activity-bar')).toHaveAttribute('aria-label', 'Agents — 5 running')

    // Stack itself is capped at MAX_STACK_AVATARS (4) — each stacked avatar is
    // wrapped in a `ring-2` div in ActivityBar.tsx's stackItems.map render.
    const bar = screen.getByTestId('activity-bar')
    const stackedAvatarWrappers = bar.querySelectorAll('.ring-2')
    expect(stackedAvatarWrappers.length).toBe(4)
  })
})

describe('ActivityBar — opens the panel on click', () => {
  it('reveals "Running now" in the slide-out after clicking the bar', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })

    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
    fireEvent.click(screen.getByTestId('activity-bar'))

    await waitFor(() => {
      expect(screen.getByText('Running now')).toBeInTheDocument()
    })
    expect(screen.getByText('digging into logs')).toBeInTheDocument()
  })

  // Deliberate, accepted tradeoff (see ActivityBar.tsx's header comment): a
  // FRESH mount with no running/finished activity at all has nothing to
  // click, by design — this is distinct from Fix 1's later matrix (a panel
  // that WAS opened, or a failure that WAS retained, keeps the pill
  // mounted); this test only covers the true "nothing has ever happened"
  // starting state.
  it('has no clickable entry point at rest — nothing has ever run or finished', () => {
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
    expect(screen.queryByText('Running now')).not.toBeInTheDocument()
  })
})

// ── Fix 1 (2026-07-16): revised mount matrix ────────────────────────────────
// The bar previously unmounted unconditionally the instant runningCount hit
// 0 (`if (runningCount === 0) return null`), which killed an OPEN panel
// mid-inspection and made a retained failure unreachable at idle — the
// panel is now the designated failure-transparency surface for delegation/
// background-bash outcomes hidden from the thread by default
// (toolVisibility.ts). This block pins the corrected matrix.

describe('ActivityBar — Fix 1: an open panel survives running -> 0', () => {
  it('keeps the panel mounted (and open) after the last running item finishes while the panel is open', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    await waitFor(() => {
      expect(screen.getByText('1 running')).toBeInTheDocument()
    })

    fireEvent.click(screen.getByTestId('activity-bar'))
    await waitFor(() => {
      expect(screen.getByText('Running now')).toBeInTheDocument()
    })

    // The running item finishes (successfully) — runningCount drops to 0.
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            finishedSpan({ agentId: 'ray', status: 'success' }),
          ]),
        ],
      })
    })

    // The panel (and its "Recently finished" section) must still be visible
    // — the whole component must NOT have unmounted out from under it.
    await waitFor(() => {
      expect(screen.getByText('Recently finished')).toBeInTheDocument()
    })
    expect(screen.getByTestId('activity-bar')).toBeInTheDocument()
  })
})

describe('ActivityBar — Fix 1: a retained failure keeps the pill mounted at idle', () => {
  it('shows a red "1 failed" pill (no spinner) when idle with an error item in recentlyFinished', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'error' })])],
      })
    })
    renderBar()

    const bar = await screen.findByTestId('activity-bar')
    expect(screen.getByText('1 failed')).toBeInTheDocument()
    // No spinner while idle — spinner is reserved for runningCount > 0.
    expect(bar.querySelector('.animate-spin')).toBeNull()
    // Failed-state variant uses the error-toned dot, not the muted/idle one.
    const dot = bar.querySelector('[class*="rounded-full"]')
    expect(dot?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('also stays mounted for an interrupted or timeout item (not just error)', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'interrupted' })])],
      })
    })
    renderBar()
    expect(await screen.findByText('1 failed')).toBeInTheDocument()
  })

  it('does NOT count a cancelled item as a failure (cancelled is a deliberate stop, not a failure)', () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'cancelled' })])],
      })
    })
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
  })

  it('does NOT count a parked item as a failure (ADR-057 UAT C2: awaiting the parent, not a failure)', () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'parked' })])],
      })
    })
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
  })

  it('opening the panel from the failed-state pill still reveals the failed row', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'error', taskLabel: 'broken task' })])],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    fireEvent.click(bar)
    await waitFor(() => {
      expect(screen.getByText('broken task')).toBeInTheDocument()
    })
  })
})

describe('ActivityBar — Fix 1: a purely-successful idle history still unmounts', () => {
  it('renders nothing when recentlyFinished contains only success items and nothing is running', () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'success' })])],
      })
    })
    renderBar()
    expect(screen.queryByTestId('activity-bar')).not.toBeInTheDocument()
  })
})

describe('ActivityBar — Fix 1: the spinner only ever shows while running', () => {
  it('does not render .animate-spin when idle-but-mounted via a retained failure', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([finishedSpan({ agentId: 'ray', status: 'timeout' })])],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    expect(bar.querySelector('.animate-spin')).toBeNull()
  })

  it('renders .animate-spin once something is running again, even with a failure also retained', async () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            finishedSpan({ spanId: 's_failed', parentCallId: 'c_failed', agentId: 'ray', status: 'error' }),
            runningSpan({ spanId: 's_running', parentCallId: 'c_running', agentId: 'ray' }),
          ]),
        ],
      })
    })
    renderBar()
    const bar = await screen.findByTestId('activity-bar')
    expect(bar.querySelector('.animate-spin')).not.toBeNull()
    expect(screen.getByText('1 running')).toBeInTheDocument()
  })
})

// ── Defect 1 (ADR-091 fix lane RX-FRONTEND): a queued-only child must still
// surface the bar. Commit 8b8d6ef9d redefined `runningChildren` to count
// ONLY `lifecycleState === 'running'` (useRunningActivity.ts::isRunningAgentChild)
// but left ActivityBar's mount gate (`isRunning = runningChildren > 0`)
// reading that same narrowed count. The launcher emits `subagent_start` then
// `subagent_state(queued)` back-to-back at launch (verified against
// pkg/agent/steer_launcher.go::publishSteeredLaunch) — `running` only
// arrives later at Dispatch. Under a saturated admission gate a child can
// stay `queued` indefinitely: `runningChildren` stays 0, nothing has
// finished, so the OLD `shouldMount` computation never becomes true and the
// delegation is completely invisible — no pill, no panel, no way to see it
// exists — even though `ActivityPanel`'s queued dot (efc29991a) is fully
// able to render it once mounted.
describe('ActivityBar — Defect 1: a queued-only child must mount the bar', () => {
  it('mounts the bar (and keeps it clickable) for a child stuck at lifecycleState "queued", with no other running/failed items', () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ agentId: 'ray', lifecycleState: 'queued', taskLabel: 'saturated gate, still queued' }),
          ]),
        ],
      })
    })
    renderBar()
    expect(screen.getByTestId('activity-bar')).toBeInTheDocument()
  })

  it('opening the bar from a queued-only child reveals the delegation in the panel', async () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ agentId: 'ray', lifecycleState: 'queued', taskLabel: 'saturated gate, still queued' }),
          ]),
        ],
      })
    })
    renderBar()
    fireEvent.click(screen.getByTestId('activity-bar'))
    await waitFor(() => {
      expect(screen.getByText('saturated gate, still queued')).toBeInTheDocument()
    })
  })
})

describe('ActivityBar — two pills (D5, AC-2, AC-3)', () => {
  function bashCall(id: string, command: string) {
    return {
      id,
      call_id: id,
      tool: 'bash',
      params: { command, run_in_background: true },
      status: 'success' as const,
      result: `{"sessionId":"${id}","status":"running"}`,
    }
  }

  it('AC-2: an agent child alone mounts the Agents pill and not the Commands pill', async () => {
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray' })])],
      })
    })
    renderBar()
    const agents = await screen.findByTestId('activity-bar')
    expect(agents).toHaveTextContent('Agents')
    expect(within(agents).getByTestId('activity-bar-label')).toHaveTextContent('1 running')
    expect(screen.queryByTestId('activity-pill-commands')).not.toBeInTheDocument()
  })

  it('AC-2: the Agents count excludes bash and queued children; the command gets its own pill', async () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ spanId: 's_run', parentCallId: 'c_run', agentId: 'ray' }),
            runningSpan({
              spanId: 's_q',
              parentCallId: 'c_q',
              agentId: 'ray',
              lifecycleState: 'queued',
              taskLabel: 'waiting',
            }),
          ]),
        ],
        toolCalls: {
          call_1: bashCall('call_1', 'npm test'),
          call_2: bashCall('call_2', 'npm run lint'),
        },
      })
    })
    renderBar()
    const agents = await screen.findByTestId('activity-bar')
    // 1 running agent, not 2 (the queued child) and not 4 (queued + two commands).
    expect(within(agents).getByTestId('activity-bar-label')).toHaveTextContent('1 running')
    expect(agents).not.toHaveTextContent('background')
    const commands = screen.getByTestId('activity-pill-commands')
    expect(within(commands).getByTestId('activity-pill-commands-label')).toHaveTextContent('2 background commands')
  })

  it('AC-3: both pills open one panel, and Commands scrolls it to the background-commands section', async () => {
    // jsdom does not implement scrollIntoView; define it so the spy can record the call.
    Element.prototype.scrollIntoView ??= () => {}
    const scrollIntoView = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(() => {})
    act(() => {
      useChatStore.setState({
        messages: [makeAssistantMessage([runningSpan({ agentId: 'ray', taskLabel: 'digging into logs' })])],
        toolCalls: { call_1: bashCall('call_1', 'npm test') },
      })
    })
    renderBar()
    const agents = await screen.findByTestId('activity-bar')
    const commands = screen.getByTestId('activity-pill-commands')

    fireEvent.click(agents)
    const commandsSection = await screen.findByTestId('activity-section-commands')
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    expect(screen.getByText('Running now')).toBeInTheDocument()
    expect(scrollIntoView.mock.instances).not.toContain(commandsSection)

    scrollIntoView.mockClear()
    fireEvent.click(commands)
    await waitFor(() => {
      expect(scrollIntoView.mock.instances).toContain(commandsSection)
    })
    // Still one dialog — Commands did not open a second panel.
    expect(screen.getAllByRole('dialog')).toHaveLength(1)
    expect(agents).toHaveAttribute('aria-expanded', 'true')
    expect(commands).toHaveAttribute('aria-expanded', 'true')
    expect(within(commandsSection).getByText('npm test')).toBeInTheDocument()
    scrollIntoView.mockRestore()
  })
})

describe('ActivityBar — queued section (AC-4)', () => {
  it('lists queued children under Queued in launch order and adds no delegation chat line', () => {
    act(() => {
      useChatStore.setState({
        messages: [
          makeAssistantMessage([
            runningSpan({ spanId: 'run', parentCallId: 'c0', lifecycleState: 'running', taskLabel: 'already going' }),
            runningSpan({ spanId: 'q1', parentCallId: 'c1', lifecycleState: 'queued', taskLabel: 'zeta first' }),
            runningSpan({ spanId: 'q2', parentCallId: 'c2', lifecycleState: 'queued', taskLabel: 'alpha second' }),
          ]),
        ],
      })
    })
    renderBar()
    // The children are on screen, so a chat line rendered next to them would be visible.
    fireEvent.click(screen.getByTestId('activity-bar'))
    const queued = screen.getByTestId('activity-section-queued')
    expect(within(queued).getAllByTestId('activity-queue-position').map((el) => el.textContent)).toEqual(['1', '2'])
    const queuedText = queued.textContent ?? ''
    expect(queuedText.indexOf('zeta first')).toBeGreaterThanOrEqual(0)
    expect(queuedText.indexOf('zeta first')).toBeLessThan(queuedText.indexOf('alpha second'))
    expect(within(queued).queryByText('already going')).not.toBeInTheDocument()
    expect(screen.getByText('already going')).toBeInTheDocument()
    expect(screen.queryByText(/Delegated to/)).not.toBeInTheDocument()
    expect(screen.queryByText(/started ·/)).not.toBeInTheDocument()
  })
})

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { ToolCallBadge } from './ToolCallBadge'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { ToolCall } from '@/lib/api'

// test_tool_call_badge_states (test #6)
// test_tool_call_collapse_expand (test #7)
// test_tool_component_registry (test #8)
// Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
//             wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
//             wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
//             wave5a-wire-ui-spec.md — Scenario: Expanding a collapsed tool call shows details
//             wave5a-wire-ui-spec.md — Scenario Outline: Built-in tool uses custom component
//             wave5a-wire-ui-spec.md — Scenario: Unknown tool uses generic component

type ToolCallWithId = ToolCall & { call_id: string }

function makeToolCall(overrides: Partial<ToolCallWithId>): ToolCallWithId {
  return {
    id: 'tc_1',
    call_id: 'tc_1',
    tool: 'web_search',
    params: { query: 'AWS pricing' },
    status: 'running',
    ...overrides,
  }
}

// ── stable data-tool contract (gate 6, F1) ───────────────────────────────────
// Pinned at the unit level (mirrors the same assertion in
// GenericToolCall.test.tsx) — several consumers (e2e/visual-QA selectors,
// SubagentBlock step rows) key off `data-tool` to find a specific tool call's
// badge, so its presence/value is a contract, not an implementation detail.

describe('ToolCallBadge — stable data-tool contract (F1)', () => {
  it('renders the tool-call-badge root with a data-tool attribute matching the raw tool name', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'read_file', status: 'success', duration_ms: 5 })} />)
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveAttribute('data-tool', 'read_file')
  })

  it('data-tool reflects the raw (non-humanized) tool id even for a dotted/aliased name', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'browser.navigate', status: 'success', duration_ms: 5 })} />)
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveAttribute('data-tool', 'browser.navigate')
  })
})

// ── delegation-denied branch (gate 3) — ported from GenericToolCall.tsx ──────
// A denied delegate call rendered amber "Delegation denied · <axis>" via
// GenericToolCall's live/replay path but red generic "Failed" via
// ToolCallBadge (SubagentBlock steps / MessageItem history) for the exact
// same DelegationFailure sentinel shape. Both callers must now render the
// same chip.
//
// Fix 2 (2026-07-16, revised): a denied delegate 'run' call (the default
// action/shape these fixtures all use) is now hidden in the thread even on
// error — isError no longer overrides delegate visibility (see
// toolVisibility.ts's shouldRenderToolCall doc comment: the failure is
// explained by the delegating agent's own response text, not a thread
// chip). This describe block is testing the CHIP'S OWN render logic (does it
// draw the amber "Delegation denied" branch correctly), which is a separate
// concern from whether the row is visible by default — so verbose chat is
// forced on here to get past the now-independent visibility gate, exactly
// as a user who opted into verbose chat would see it.

describe('ToolCallBadge — delegation-denied branch (ported from GenericToolCall)', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
  })

  afterEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('renders amber "Delegation denied · <axis>" and a warning-colored dot, not generic "Failed"', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          status: 'error',
          result: {
            error: 'delegation_denied',
            reason: 'Scout is not in your trust set for delegation.',
            policy: 'trust_set',
            tool: 'delegate',
            target_agent_id: 'scout-01',
          },
        })}
      />
    )
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    expect(badge).not.toHaveTextContent(/\bFailed\b/)
    const dot = badge.querySelector('.bg-\\[var\\(--color-warning\\)\\]')
    expect(dot).toBeTruthy()
  })

  it('renders the mode policy axis label for a mode-denied delegation', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          status: 'error',
          result: {
            error: 'delegation_denied',
            reason: 'Delegation is disabled in solo mode.',
            policy: 'mode',
            tool: 'delegate',
          },
        })}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toHaveTextContent('Delegation denied · Delegation mode')
  })

  it('a denial with a non-error status still renders the amber chip (result shape drives the branch, not status alone)', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          status: 'success',
          result: {
            error: 'delegation_denied',
            reason: 'depth exceeded',
            policy: 'depth',
            tool: 'delegate',
          },
        })}
      />
    )
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Delegation depth')
  })
})

describe('ToolCallBadge — running state (test #6)', () => {
  it('shows tool name and spinning icon while running', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Running tool call shows spinner
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'web_search', status: 'running' })} />)
    // Collapsed chip shows the humanized label, not the raw id.
    expect(screen.getByText('Search the web')).toBeInTheDocument()
    // Spinning icon from animate-spin class
    const spinner = document.querySelector('.animate-spin')
    expect(spinner).toBeTruthy()
  })

  it('shows "Cancelled" label with the cancelled-colored status dot for cancelled tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Cancel during tool execution
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'cancelled' })} />)
    expect(screen.getByText(/Cancelled/i)).toBeInTheDocument()
  })
})

describe('ToolCallBadge — success state (test #6 continued)', () => {
  it('shows the success-colored status dot and duration for successful tool call', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    // Was: queried the CheckCircle icon's `.text-[var(--color-success)]` text-color
    // class. Flat text-line design (ticket "Tool components in chat") replaced the
    // status icon with an 8px filled dot, so success is now a `bg-` class, not `text-`.
    const { container } = render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'success', duration_ms: 2300, result: { results: [] } })}
      />
    )
    const dot = container.querySelector('.bg-\\[var\\(--color-success\\)\\]')
    expect(dot).toBeTruthy()
    expect(dot?.className).toContain('rounded-full')
    expect(screen.getByText(/2\.3s/i)).toBeInTheDocument()
  })

  it('collapses by default — result not visible without clicking', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Successful tool call collapses by default
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          duration_ms: 1000,
          result: { super_secret_result: 'hidden' },
        })}
      />
    )
    // Result detail should not be visible in collapsed state
    expect(screen.queryByText(/super_secret_result/i)).toBeNull()
  })
})

describe('ToolCallBadge — error state (test #6 continued)', () => {
  it('shows error-colored status dot and error message', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Failed tool call shows error with retry
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'error', error: 'Timeout after 30s' })}
      />
    )
    // Expand to see error (error shown in expanded view)
    const btn = document.querySelector('button')
    if (btn) fireEvent.click(btn)
    expect(screen.getByText(/Timeout after 30s/i)).toBeInTheDocument()
  })
})

describe('ToolCallBadge — collapse/expand toggle (test #7)', () => {
  it('expands to show parameters and result when clicked', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Expanding a collapsed tool call shows details
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          params: { query: 'AWS m5 instance pricing' },
          result: { hits: 5 },
          duration_ms: 1500,
        })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    expect(btn).toBeTruthy()
    fireEvent.click(btn)
    // After expand, params should be visible
    expect(screen.getByText(/AWS m5 instance pricing/i)).toBeInTheDocument()
    // Result should be visible
    expect(screen.getByText(/hits/i)).toBeInTheDocument()
  })

  it('clicking again collapses the badge', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status: 'success',
          params: { query: 'test' },
          result: { items: [] },
        })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    // Expand
    fireEvent.click(btn)
    expect(screen.getByText(/test/i)).toBeInTheDocument()
    // Collapse
    fireEvent.click(btn)
    expect(screen.queryByText(/items/i)).toBeNull()
  })
})

describe('ToolCallBadge — tool icon registry (test #8)', () => {
  it.each([
    // [rawTool, humanizedCollapsedLabel]
    ['bash', 'Run command'],
    ['exec', 'Run command'],
    ['web_search', 'Search the web'],
    ['file.read', 'Read'],
    ['browser.navigate', 'Navigate browser'],
  ])('renders humanized label for built-in tool: %s → %s', (toolName, label) => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario Outline: Built-in tool uses custom component
    const { container } = render(
      <ToolCallBadge toolCall={makeToolCall({ tool: toolName, status: 'success', duration_ms: 100 })} />
    )
    expect(container.firstChild).toBeTruthy()
    // Collapsed chip shows the humanized label; raw id is NOT shown collapsed.
    expect(screen.getByText(label)).toBeInTheDocument()
  })

  it('renders generic label for unknown MCP tool and exposes the raw id when expanded', () => {
    // Traces to: wave5a-wire-ui-spec.md — Scenario: Unknown tool uses generic component
    render(
      <ToolCallBadge toolCall={makeToolCall({ tool: 'custom_mcp_tool', status: 'success', duration_ms: 80 })} />
    )
    // Collapsed: humanized label.
    expect(screen.getByText('Custom mcp tool')).toBeInTheDocument()
    expect(screen.queryByText('custom_mcp_tool')).toBeNull()
    // Expand: raw tool id is visible to power users.
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    fireEvent.click(btn)
    expect(screen.getByText('custom_mcp_tool')).toBeInTheDocument()
  })
})

describe('ToolCallBadge — cannot click when running', () => {
  it('clicking the header while running does not expand (running is not expandable)', () => {
    // Traces to: wave5a-wire-ui-spec.md — AC1: spinner shown; detail not accessible while running
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'running' })} />)
    const btn = document.querySelector('button') as HTMLButtonElement
    fireEvent.click(btn)
    // Still no expanded content
    expect(screen.queryByText(/Parameters/i)).toBeNull()
  })

  // Inert-focusable fix: while running there's nothing to expand, so the
  // header button must be genuinely disabled (dropped from the tab order,
  // Enter/Space can't no-op on it) rather than a focusable button that just
  // silently ignores activation — mirrors GenericToolCall.tsx's
  // `disabled={!hasDetail}` gate.
  it('disables the header button and omits aria-expanded while running', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'running' })} />)
    const btn = document.querySelector('button') as HTMLButtonElement
    expect(btn).toBeDisabled()
    expect(btn).not.toHaveAttribute('aria-expanded')
  })

  it('re-enables the header button and restores aria-expanded once the call finishes', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'success', duration_ms: 100 })} />)
    const btn = document.querySelector('button') as HTMLButtonElement
    expect(btn).not.toBeDisabled()
    expect(btn).toHaveAttribute('aria-expanded', 'false')
  })
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded card was replaced by a flat text line whose
// only status color comes from an 8px dot (running keeps the spinning icon
// in the same slot). These lock the dot-per-status color mapping and the
// absence of any card-frame class on the root, independent of the
// icon/label assertions above.

describe('ToolCallBadge — flat text-line status dot', () => {
  /** First child of the toggle button is always the status indicator (dot or spinner). */
  function getIndicatorEl(container: HTMLElement) {
    return container.querySelector('button')?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = render(<ToolCallBadge toolCall={makeToolCall({ status: 'running' })} />)
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it.each([
    ['success', 'bg-[var(--color-success)]'],
    ['error', 'bg-[var(--color-error)]'],
    ['cancelled', 'bg-[var(--color-cancelled)]'],
  ] as const)('%s: indicator is an 8px %s dot', (status, dotColor) => {
    const { container } = render(
      <ToolCallBadge
        toolCall={makeToolCall({
          status,
          duration_ms: 100,
          error: status === 'error' ? 'boom' : undefined,
        })}
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain(dotColor)
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
    expect(indicator?.getAttribute('class')).toContain('w-2')
    expect(indicator?.getAttribute('class')).toContain('h-2')
  })

  it('the outer container has no card-frame classes — flat/transparent on the thread', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'success', duration_ms: 100 })} />)
    const root = screen.getByTestId('tool-call-badge')
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('expanded detail uses a left accent line, not a full bordered panel', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'success', duration_ms: 100, params: { q: 'x' }, result: { ok: true } })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    fireEvent.click(btn)
    // "Tool" label only appears in the expanded panel, so its ancestor is the detail block.
    const detail = screen.getByText('Tool').parentElement?.parentElement
    expect(detail?.className).toContain('border-l-2')
    expect(detail?.className).not.toContain('border-t')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ status: 'success', duration_ms: 100, params: { q: 'x' }, result: { ok: true } })}
      />
    )
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    fireEvent.click(btn)
    const root = screen.getByTestId('tool-call-badge')
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('the caret lives INSIDE the toggle button — the whole row is one click target (no unjustified split-out caret)', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ status: 'success', duration_ms: 100 })} />)
    const btn = document.querySelector('button[aria-expanded]') as HTMLButtonElement
    // The caret svg is a descendant of the toggle button itself, not a sibling.
    expect(btn.querySelector('svg')).toBeTruthy()
    const root = screen.getByTestId('tool-call-badge')
    expect(root.querySelectorAll('button').length).toBe(1)
  })
})

// ── activity-bar tool visibility: verbose-chat gate ─────────────────────────
// Traces to: src/lib/toolVisibility.ts shouldRenderToolCall — ToolCallBadge is
// the single shared renderer used by both MessageItem (historical list) and
// SubagentBlock (nested steps), so gating here covers both call sites.

describe('ToolCallBadge — verbose chat gate', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('hides a ToolSearch call by default (verboseChatEnabled false)', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'ToolSearch', status: 'success' })} />)
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('shows a ToolSearch call when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'ToolSearch', status: 'success' })} />)
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // (item 8a, 2026-07-16 fix wave): component-level pin of the ToolSearch
  // isError override (mirrors the equivalent GenericToolCall.test.tsx pair)
  // — mutation-proofs the `toolCall.status === 'error' || marshalErr ||
  // !!delegationFailure` argument ToolCallBadge threads into
  // shouldRenderToolCall.
  it('a ToolSearch call with status "error" is VISIBLE even when verboseChatEnabled is false', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'ToolSearch', status: 'error' })} />)
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  it('a ToolSearch call that succeeded but whose result is a _marshal_error sentinel is VISIBLE even when verboseChatEnabled is false', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'ToolSearch',
          status: 'success',
          result: { _marshal_error: 'json: unsupported type: chan int' },
        })}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // (item 8c, 2026-07-16 fix wave): a LIVE toggle within a single render —
  // proves the row reacts to the store subscription rather than only being
  // correct at mount time (all the tests around this one render fresh per
  // state instead).
  it('a background delegate dispatch appears when verbose chat is toggled on live, and disappears when toggled back off — same render instance', () => {
    render(
      <ToolCallBadge toolCall={makeToolCall({ tool: 'delegate', params: {}, status: 'success' })} />
    )
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()

    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()

    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    expect(screen.queryByTestId('tool-call-badge')).not.toBeInTheDocument()
  })

  it('hides a background delegate dispatch by default (action=run, async=true)', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({ tool: 'delegate', params: {}, status: 'success' })}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('an always-visible tool (remember) still renders regardless of verbose setting', () => {
    render(<ToolCallBadge toolCall={makeToolCall({ tool: 'remember', status: 'success' })} />)
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // REVISED 2026-07-16 (was: "REGRESSION ... is NOT hidden" asserting
  // visible-on-error): delegate no longer gets an isError override. A
  // failed/denied delegate 'run' call is returned to the delegating agent's
  // own turn as the tool result — that agent explains it in its own
  // response text, and the raw denial stays inspectable in the
  // ActivityPanel (surface="panel" — see ActivityPanel.test.tsx). The
  // thread row stays hidden here; only verboseChatEnabled brings it back
  // (see the next test).
  it('a background delegate dispatch with an error status stays HIDDEN — no isError exception for delegate (LLM-mediated failure presentation)', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          params: {},
          status: 'error',
          error: 'delegation_denied',
        })}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('...but becomes visible once verbose chat is enabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          params: {},
          status: 'error',
          error: 'delegation_denied',
        })}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // REVISED 2026-07-16 (was: "REGRESSION ... is NOT hidden"): the
  // background-dispatch/poll/read sub-cases of `bash` carry the same
  // LLM-mediated rationale as delegate above — no isError exception.
  it('a background bash dispatch with an error status stays HIDDEN — no isError exception for background bash', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'bash',
          params: { run_in_background: true },
          status: 'error',
          error: 'exit code 127',
        })}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  // REVISED 2026-07-16 (was: "REGRESSION ... is NOT hidden"): same
  // no-isError-exception rule applies to the `_marshal_error` sentinel too
  // — it is just another outcome signal, and delegate/background-bash
  // ignore outcome entirely now (verbose-only).
  it('a background delegate dispatch with a _marshal_error result and success status stays HIDDEN', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'delegate',
          params: {},
          status: 'success',
          result: { _marshal_error: 'json: unsupported type: chan int' },
        })}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a background bash dispatch with a _marshal_error result and success status stays HIDDEN', () => {
    render(
      <ToolCallBadge
        toolCall={makeToolCall({
          tool: 'bash',
          params: { run_in_background: true },
          status: 'success',
          result: { _marshal_error: 'json: unsupported type: chan int' },
        })}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })
})

// GenericToolCall component tests — W1-4 truncation/marshal-error sentinel rendering
// Traces to: temporal-puzzling-melody.md §Wave 1 W1-4 (TS half)

import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { GenericToolCall } from './GenericToolCall'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import type { MessagePartStatus } from '@assistant-ui/react'

const COMPLETE_STATUS: MessagePartStatus = { type: 'complete' }
const RUNNING_STATUS: MessagePartStatus = { type: 'running' }

// ── W1-4: truncated sentinel rendering ───────────────────────────────────────

describe('GenericToolCall — truncated result sentinel', () => {
  it('renders truncation banner with human-readable size when _truncated is true', () => {
    const truncatedResult = {
      _truncated: true as const,
      original_size_bytes: 2 * 1024 * 1024, // 2 MiB
      preview: 'first 10 KiB of content...',
    }

    render(
      <GenericToolCall
        toolName="fs.read"
        result={truncatedResult}
        status={COMPLETE_STATUS}
      />
    )

    // Expand to see the result pane
    fireEvent.click(screen.getByRole('button'))

    // Banner must be visible
    const banner = screen.getByTestId('result-truncated-banner')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveTextContent('Truncated')
    expect(banner).toHaveTextContent('10 KiB')
    expect(banner).toHaveTextContent('2.0 MiB')

    // Preview text renders below the banner
    expect(screen.getByText('first 10 KiB of content...')).toBeInTheDocument()
  })

  it('renders truncation banner for 512 KiB original size', () => {
    const truncatedResult = {
      _truncated: true as const,
      original_size_bytes: 512 * 1024, // 512 KiB
      preview: 'preview text',
    }

    render(
      <GenericToolCall
        toolName="fs.read"
        result={truncatedResult}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const banner = screen.getByTestId('result-truncated-banner')
    expect(banner).toHaveTextContent('Truncated')
    // 512 KiB renders as KiB
    expect(banner).toHaveTextContent('512.0 KiB')
  })

  it('does NOT render truncation banner for normal results', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result={{ exit_code: 0, stdout: 'ok' }}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    expect(screen.queryByTestId('result-truncated-banner')).toBeNull()
    // Normal JSON result renders
    expect(screen.getByText(/"exit_code"/)).toBeInTheDocument()
  })
})

// ── W1-4: marshal-error sentinel rendering ───────────────────────────────────

describe('GenericToolCall — marshal-error result sentinel', () => {
  it('renders red error banner with the marshal error message', () => {
    const marshalErrorResult = {
      _marshal_error: 'json: unsupported type: chan int',
    }

    render(
      <GenericToolCall
        toolName="exec"
        result={marshalErrorResult}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const banner = screen.getByTestId('result-marshal-error')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveTextContent('json: unsupported type: chan int')
    expect(banner).toHaveTextContent('serialization failed')
  })

  it('does NOT render marshal-error banner for normal results', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result="normal string result"
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    expect(screen.queryByTestId('result-marshal-error')).toBeNull()
  })
})

// ── BLOCKER 2: delegation-denied sentinel rendering ──────────────────────────

describe('GenericToolCall — delegation-denied result sentinel', () => {
  // Fix 2 (2026-07-16, revised): a `delegate` call with the default/absent
  // args shape these fixtures use (action defaults to "run") is now hidden
  // in the thread by default REGARDLESS of outcome — isError/marshalErr no
  // longer override delegate visibility (toolVisibility.ts's
  // shouldRenderToolCall doc comment: the failure is explained by the
  // delegating agent's own response text, not a thread chip; the raw
  // denial stays inspectable in the ActivityPanel). This describe block
  // tests the delegation-denied CHIP'S OWN render logic (does it draw the
  // amber branch correctly), a separate concern from default visibility —
  // so verbose chat is forced on to get past the now-independent
  // visibility gate, exactly as an opted-in user would see it. The
  // default-hidden case itself is covered by the REGRESSION-turned-positive
  // test at the end of this block.
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

  it('renders a distinct delegation-failure block (reason + policy), NOT raw JSON', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    // Issue #617: a finished tool call ALWAYS carries a truthy `result`, so
    // AssistantUI's part status is 'complete' in production, never
    // 'incomplete' (toMessagePartStatus short-circuits to COMPLETE_STATUS
    // the moment `result` is truthy — see message-runtime.ts). The real
    // production path to isError for a denied delegation is the `error`
    // string GenericToolCall reads from the store (ChatScreen.tsx's
    // `error={tc.error}`) — set from the same tool_call_result frame that
    // carries the denial sentinel (store/chat.ts: `tc.error = frame.error`).
    // {result: truthy, status: incomplete} is not a pair GenericToolCall
    // ever actually receives; this producible pairing is.
    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={COMPLETE_STATUS}
        error={delegationDenied.reason}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-delegation-denied')
    expect(block).toBeInTheDocument()
    // Human reason surfaced verbatim.
    expect(block).toHaveTextContent('Scout is not in your trust set for delegation.')
    // Policy axis rendered with its human label.
    expect(block).toHaveTextContent('Trust set')
    // Target agent shown when present.
    expect(block).toHaveTextContent('scout-01')
    expect(block).toHaveTextContent('Delegation denied')

    // The raw JSON sentinel keys must NOT leak into a code/pre blob.
    expect(screen.queryByText(/"delegation_denied"/)).toBeNull()
    expect(screen.queryByText(/"error":/)).toBeNull()
  })

  it('renders the mode policy axis and omits target agent when absent', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Delegation is disabled in solo mode.',
      policy: 'mode' as const,
      tool: 'delegate',
    }

    // Issue #617: producible pairing (result + status:complete + error) —
    // see the previous test's comment for the full rationale.
    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={COMPLETE_STATUS}
        error={delegationDenied.reason}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-delegation-denied')
    expect(block).toHaveTextContent('Delegation is disabled in solo mode.')
    expect(block).toHaveTextContent('Delegation mode')
    expect(block).not.toHaveTextContent('Target agent')
  })

  it('(G17) collapsed header shows "Delegation denied · <axis>", not generic "Failed"', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    // Issue #617: producible pairing — see the first test in this describe
    // block for the full rationale.
    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={COMPLETE_STATUS}
        error={delegationDenied.reason}
      />
    )

    // WITHOUT expanding, the collapsed header names the denial + the policy axis.
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    // The generic "Failed" label must not be used for a delegation denial.
    expect(badge).not.toHaveTextContent(/\bFailed\b/)
  })

  // Was "REGRESSION: ... is NOT hidden" pre-Fix-2: shouldRenderToolCall used
  // to decide visibility from tool+params alone, with no idea the call had
  // been denied, so an isError/delegationFailure override forced it visible.
  // Fix 2 (revised 2026-07-16) removed that override for delegate entirely —
  // a `delegate` call with fully default/absent args (action defaults to
  // "run") now stays hidden in the thread EVEN when denied; verbose chat is
  // the only way back. This test now pins BOTH halves: hidden by default,
  // and — once verbose is on (this describe's own beforeEach) — the exact
  // same chip content the original regression test asserted.
  it('a policy-denied delegation with fully default/absent args is hidden by default, and shows the full denial chip once verbose chat is enabled', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    // First: with verbose forced back OFF, the row must be hidden — the
    // failure is left for the delegating agent's own response text and the
    // ActivityPanel to surface, not a thread chip.
    // Issue #617: producible pairing (result + status:complete + error) —
    // see the first test in this describe block for the full rationale.
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
    const { unmount } = render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={COMPLETE_STATUS}
        error={delegationDenied.reason}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
    unmount()

    // Then: verbose chat back on (this describe's default) reveals the full
    // chip, with the same content the pre-Fix-2 test pinned.
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <GenericToolCall
        toolName="delegate"
        // No `args` prop at all — the default/common shape.
        result={delegationDenied}
        status={COMPLETE_STATUS}
        error={delegationDenied.reason}
      />
    )

    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toBeInTheDocument()
    expect(badge).toHaveTextContent('Delegation denied · Trust set')

    fireEvent.click(screen.getByRole('button'))
    const block = screen.getByTestId('result-delegation-denied')
    expect(block).toBeInTheDocument()
    expect(block).toHaveTextContent('Scout is not in your trust set for delegation.')
  })
})

// ── stable data-tool contract (gate 6, F1) ───────────────────────────────────
// Pinned at the unit level in the MAIN suite too (GenericToolCall.edge.test.tsx
// already covers this for a degenerate tool name; this locks the contract for
// the common/happy-path case that this file otherwise exercises).

describe('GenericToolCall — stable data-tool contract (F1)', () => {
  it('renders the tool-call-badge root with a data-tool attribute matching the raw tool name', () => {
    render(<GenericToolCall toolName="read_file" status={COMPLETE_STATUS} result={{ ok: true }} />)
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveAttribute('data-tool', 'read_file')
  })
})

// ── Baseline: non-sentinel result still renders normally ────────────────────

describe('GenericToolCall — baseline rendering', () => {
  it('renders header with humanized tool name', () => {
    render(
      <GenericToolCall
        toolName="web_search"
        status={RUNNING_STATUS}
      />
    )
    // Collapsed chip shows the humanized label, not the raw id.
    expect(screen.getByText('Search the web')).toBeInTheDocument()
  })

  it('expanded pane shows plain result when result is a plain object', () => {
    render(
      <GenericToolCall
        toolName="exec"
        result={{ stdout: 'hello\n', exit_code: 0 }}
        status={COMPLETE_STATUS}
      />
    )

    fireEvent.click(screen.getByRole('button'))

    // Neither banner appears
    expect(screen.queryByTestId('result-truncated-banner')).toBeNull()
    expect(screen.queryByTestId('result-marshal-error')).toBeNull()
    // JSON content renders
    expect(screen.getByText(/"stdout"/)).toBeInTheDocument()
  })
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old bordered/backgrounded card was replaced by a flat text line whose
// only status color comes from an 8px dot (running keeps the spinning icon
// in the same slot; delegation-denied reuses the same dot via the shared
// `statusDot` helper — see toolStatusConfig.tsx's file header). The five
// result-path banners lost their bordered/backgrounded boxes in favor of a
// left accent line, keeping their sentinel testids and (where applicable)
// amber/warning text color.

describe('GenericToolCall — flat text-line status dot', () => {
  /** The toggle button is always the first <button> in the row (Watch Live,
   * when present, is a separate sibling button) — its first child is always
   * the status indicator (dot or spinner). */
  function getIndicatorEl(container: HTMLElement) {
    return container.querySelector('button')?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = render(<GenericToolCall toolName="exec" status={RUNNING_STATUS} />)
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('success: indicator is an 8px success-colored dot', () => {
    const { container } = render(
      <GenericToolCall toolName="exec" result={{ ok: true }} status={COMPLETE_STATUS} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('error: indicator is an 8px error-colored dot', () => {
    const { container } = render(
      <GenericToolCall
        toolName="exec"
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it("cancelled: indicator is an 8px muted-colored dot (GenericToolCall's cancelledVariant)", () => {
    const { container } = render(
      <GenericToolCall
        toolName="exec"
        status={{ type: 'incomplete', reason: 'cancelled' } as MessagePartStatus}
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
  })

  // (item 8e, 2026-07-16 fix wave): these two tests need verboseChatEnabled
  // true to get past the visibility gate at all (toolName="delegate") —
  // scoped to their own nested describe with a beforeEach/afterEach pair,
  // matching the pattern the "delegation-denied result sentinel" describe
  // above (lines 141-151) already uses, instead of each test managing its
  // own inline act()/setState set-and-reset.
  describe('delegation-denied variant (verbose forced on)', () => {
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

    it('delegation-denied: indicator is an 8px warning-colored dot (local branch, shares statusDot)', () => {
      // Verbose is forced on by this describe's beforeEach so this purely
      // tests the indicator's OWN color, not the (separately-tested)
      // visibility gate.
      // Issue #617: producible pairing (result + status:complete + error) —
      // see "GenericToolCall — delegation-denied result sentinel" above for
      // the full rationale.
      const { container } = render(
        <GenericToolCall
          toolName="delegate"
          result={{ error: 'delegation_denied', reason: 'nope', policy: 'mode', tool: 'delegate' }}
          status={COMPLETE_STATUS}
          error="nope"
        />
      )
      const indicator = getIndicatorEl(container)
      expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-warning)]')
    })

    it('the delegation-denied block uses a left accent, not a full bordered/backgrounded box', () => {
      render(
        <GenericToolCall
          toolName="delegate"
          result={{ error: 'delegation_denied', reason: 'nope', policy: 'mode', tool: 'delegate' }}
          status={COMPLETE_STATUS}
          error="nope"
        />
      )
      fireEvent.click(screen.getByRole('button'))
      const block = screen.getByTestId('result-delegation-denied')
      // Both assertions run BEFORE the afterEach resets verbose back off
      // (item 8e fix — the prior version reset state mid-test, then
      // asserted against `block`, a DOM node the reset re-render had
      // already detached from the document).
      expect(block.className).toContain('border-l-2')
      expect(block.className).not.toContain('rounded')
    })
  })

  it('the outer container has no card-frame classes — flat/transparent on the thread', () => {
    render(<GenericToolCall toolName="exec" result={{ ok: true }} status={COMPLETE_STATUS} />)
    const root = screen.getByTestId('tool-call-badge')
    expect(root.className).not.toContain('rounded-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('overflow-hidden')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('expanded detail uses a left accent line, not a full bordered panel', () => {
    render(
      <GenericToolCall toolName="exec" args={{ cmd: 'ls' }} result={{ ok: true }} status={COMPLETE_STATUS} />
    )
    fireEvent.click(screen.getByRole('button'))
    const detail = screen.getByText('Tool').parentElement?.parentElement
    expect(detail?.className).toContain('border-l-2')
    expect(detail?.className).not.toContain('border-t')
  })

  it('the truncated-result banner uses a left accent, not a bordered/backgrounded box (amber text kept)', () => {
    render(
      <GenericToolCall
        toolName="fs.read"
        result={{ _truncated: true, original_size_bytes: 2048, preview: 'preview' }}
        status={COMPLETE_STATUS}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    const banner = screen.getByTestId('result-truncated-banner')
    expect(banner.className).toContain('border-l-2')
    expect(banner.className).not.toContain('rounded')
    expect(banner.className).not.toContain('bg-amber-500/10')
    expect(banner.className).toContain('text-amber-400')
  })

  it('the marshal-error banner uses a left accent, not a bordered/backgrounded box', () => {
    render(<GenericToolCall toolName="exec" result={{ _marshal_error: 'boom' }} status={COMPLETE_STATUS} />)
    fireEvent.click(screen.getByRole('button'))
    const banner = screen.getByTestId('result-marshal-error')
    expect(banner.className).toContain('border-l-2')
    expect(banner.className).not.toContain('rounded')
    expect(banner.className).not.toContain('bg-[var(--color-error)]/10')
  })

  it('no descendant carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1) — border-l-2 accent survives', () => {
    render(
      <GenericToolCall toolName="exec" args={{ cmd: 'ls' }} result={{ ok: true }} status={COMPLETE_STATUS} />
    )
    fireEvent.click(screen.getByRole('button'))
    const root = screen.getByTestId('tool-call-badge')
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })

  it('toggle button: disabled and aria-expanded omitted while there is nothing to expand (running, no args/result/error)', () => {
    render(<GenericToolCall toolName="exec" status={RUNNING_STATUS} />)
    const btn = screen.getByRole('button')
    expect(btn).toBeDisabled()
    expect(btn).not.toHaveAttribute('aria-expanded')
  })
})

// ── caret lives inside the toggle button (clickable expand target) ─────────
// Regression guard for the fix-wave finding: the caret used to render as a
// plain <span> sibling OUTSIDE the toggle <button> (ml-auto), so clicking
// the caret glyph — the strongest expand affordance on the row — did
// nothing. The caret must now be the toggle button's own last child, so
// clicking it fires the button's onClick and expands the detail, and the
// toggle must still span the row (flex-1) as the full-row click target.

describe('GenericToolCall — caret lives inside the toggle button (clickable expand target)', () => {
  it('toggle button has flex-1 and clicking the caret element (inside the button) expands the detail', () => {
    render(
      <GenericToolCall toolName="exec" args={{ cmd: 'ls' }} result={{ ok: true }} status={COMPLETE_STATUS} />
    )

    const toggle = screen.getByRole('button')
    expect(toggle.className).toContain('flex-1')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')

    // The caret (CaretUp/CaretDown, a Phosphor <svg>) is the only svg the
    // success-status toggle renders — the status indicator itself is a
    // <span> dot (see "flat text-line status dot" above) — so finding an
    // svg inside the button and clicking it proves the caret is nested
    // INSIDE the clickable toggle, not a dead sibling glyph outside it.
    const caret = toggle.querySelector('svg')
    expect(caret).not.toBeNull()

    fireEvent.click(caret!)

    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Tool')).toBeInTheDocument()
  })
})

// ── activity-bar tool visibility: verbose-chat gate ─────────────────────────
// Traces to: src/lib/toolVisibility.ts shouldRenderToolCall — GenericToolCall
// is the live/streaming fallback renderer for ToolSearch and delegate (neither
// has a dedicated makeAssistantToolUI registration).

describe('GenericToolCall — verbose chat gate', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('a ToolSearch call renders nothing when verboseChatEnabled is false', () => {
    render(
      <GenericToolCall
        toolName="ToolSearch"
        args={{ name: 'web_search' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a ToolSearch call renders normally when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <GenericToolCall
        toolName="ToolSearch"
        args={{ name: 'web_search' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // (item 8a, 2026-07-16 fix wave): pins the ToolSearch isError override at
  // the COMPONENT level (not just toolVisibility.test.ts's unit-level
  // shouldRenderToolCall coverage) — mutation-proofs the `isError` argument
  // GenericToolCall actually threads through at render.tsx line ~309.
  it('a ToolSearch call with an error status is VISIBLE even when verboseChatEnabled is false', () => {
    render(
      <GenericToolCall
        toolName="ToolSearch"
        args={{ name: 'web_search' }}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  it('a ToolSearch call that succeeded but whose result is a _marshal_error sentinel is VISIBLE even when verboseChatEnabled is false', () => {
    render(
      <GenericToolCall
        toolName="ToolSearch"
        args={{ name: 'web_search' }}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  it('a background delegate call (default action=run, async=true) renders nothing when verboseChatEnabled is false', () => {
    render(
      <GenericToolCall
        toolName="delegate"
        args={{}}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a background delegate call renders normally when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <GenericToolCall
        toolName="delegate"
        args={{}}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // INVERTED 2026-07-16 (was: "... still renders regardless of verbose
  // setting"): Fix 2 hides a 'run' delegation for BOTH sync and async.
  // Revised same day: the thread has NO default delegation surface at all
  // anymore — SubagentBlock's span card is ALSO verbose-only now
  // (shouldRenderSubagentSpan, toolVisibility.ts), so this isn't "redundant
  // with the card", it's simply hidden, same as the card, until verbose
  // chat is on.
  it('an explicit blocking delegate call (async: false) is ALSO hidden by default — no sync exception', () => {
    render(
      <GenericToolCall
        toolName="delegate"
        args={{ async: false }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('...but renders once verbose chat is enabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <GenericToolCall
        toolName="delegate"
        args={{ async: false }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  it('an always-visible tool (remember) still renders regardless of verbose setting', () => {
    render(
      <GenericToolCall
        toolName="remember"
        args={{ content: 'note' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  // INVERTED 2026-07-16 (was: "REGRESSION ... is NOT hidden", from when the
  // isError/marshalErr override was a blanket short-circuit ahead of the
  // switch). shouldRenderToolCall's outcome override is now per-tool-class
  // (see its doc comment) — delegate and the background-dispatch/poll/read
  // sub-cases of `bash` deliberately do NOT honor isError/marshalErr
  // anymore: the calling agent's own turn explains the failure, and the raw
  // result stays inspectable in the ActivityPanel (surface="panel"). Only
  // ToolSearch keeps the override (covered above). Only verboseChatEnabled
  // brings these two rows back now.
  it('a background delegate call with a _marshal_error result and non-error status stays HIDDEN — no outcome exception for delegate', () => {
    render(
      <GenericToolCall
        toolName="delegate"
        args={{}}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a background bash call with a _marshal_error result and non-error status stays HIDDEN — no outcome exception for background bash', () => {
    render(
      <GenericToolCall
        toolName="bash"
        args={{ run_in_background: true }}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('both the delegate and background-bash _marshal_error cases above become visible once verbose chat is enabled', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    const { unmount } = render(
      <GenericToolCall
        toolName="delegate"
        args={{}}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
    unmount()

    render(
      <GenericToolCall
        toolName="bash"
        args={{ run_in_background: true }}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })
})

describe('GenericToolCall — file-exists refusal sentinel (ADR-059 W5)', () => {
  // This is the ONLY renderer on the replay path for write_file: ChatScreen
  // routes historical tool calls here, while FileWriteConfirm is reachable only
  // for the live streaming message. So the "what does a refusal look like after
  // a reload" question — the one W5 exists to answer — is answered entirely by
  // this component, and it shipped with no test at all.
  const refusal = {
    error: 'file_exists' as const,
    reason: 'file: /w/a.svg already exists. Set overwrite=true to replace.',
    tool: 'write_file',
    path: '/w/a.svg',
  }

  // Issue #617: a finished tool call always carries a truthy `result`, so
  // AssistantUI's part status is 'complete' in production (see the
  // delegation-denied describe block above for the full mechanism). All four
  // tests below now pair a truthy result with `status:complete` + a real
  // `error` string — the same producible shape ChatScreen.tsx's replay path
  // (`error={tc.error}`) actually feeds this component.

  it('renders the reason in a distinct block, NOT as raw JSON', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={refusal}
        status={COMPLETE_STATUS}
        error={refusal.reason}
      />
    )
    fireEvent.click(screen.getByRole('button'))

    const block = screen.getByTestId('result-file-exists')
    expect(block).toBeInTheDocument()
    expect(block).toHaveTextContent('already exists')
    expect(block).toHaveTextContent('overwrite=true')
  })

  it('labels it "File already exists", not "Failed"', () => {
    // A precondition refusal is not an I/O failure — the file is simply
    // already there, which is frequently the correct outcome when a sibling
    // agent got there first. FileWriteConfirm (the LIVE renderer) says the
    // same thing, deliberately: an earlier version of these two disagreed.
    render(
      <GenericToolCall
        toolName="write_file"
        result={refusal}
        status={COMPLETE_STATUS}
        error={refusal.reason}
      />
    )
    expect(screen.getByText('File already exists')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('keeps the payload out of the plain-result JSON dump', () => {
    const { container } = render(
      <GenericToolCall
        toolName="write_file"
        result={refusal}
        status={COMPLETE_STATUS}
        error={refusal.reason}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    // The discriminator is for machines. Seeing it means the object fell
    // through to plainResult and is being dumped as text.
    expect(container.textContent).not.toContain('file_exists')
  })

  it('does not claim an unrelated error payload', () => {
    render(
      <GenericToolCall
        toolName="write_file"
        result={{ error: 'timeout', reason: 'took too long' }}
        status={COMPLETE_STATUS}
        error="took too long"
      />
    )
    expect(screen.queryByTestId('result-file-exists')).not.toBeInTheDocument()
  })
})

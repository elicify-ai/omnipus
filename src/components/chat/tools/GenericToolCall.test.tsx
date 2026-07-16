// GenericToolCall component tests — W1-4 truncation/marshal-error sentinel rendering
// Traces to: temporal-puzzling-melody.md §Wave 1 W1-4 (TS half)

import { describe, it, expect, beforeEach } from 'vitest'
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
  it('renders a distinct delegation-failure block (reason + policy), NOT raw JSON', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
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

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
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

    render(
      <GenericToolCall
        toolName="delegate"
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )

    // WITHOUT expanding, the collapsed header names the denial + the policy axis.
    const badge = screen.getByTestId('tool-call-badge')
    expect(badge).toHaveTextContent('Delegation denied · Trust set')
    // The generic "Failed" label must not be used for a delegation denial.
    expect(badge).not.toHaveTextContent(/\bFailed\b/)
  })

  // REGRESSION for the invisible-denial bug: shouldRenderToolCall used to
  // decide visibility from tool+params alone, with no idea the call had
  // been denied. A `delegate` call with fully default/absent args (the
  // ordinary shape pkg/tools/delegate.go emits — action defaults to "run",
  // async defaults to true) matches the "background dispatch, hide it" case
  // even when the result is a policy denial — so the denial vanished with no
  // chip, no error text, nothing. This is the exact scenario: no `args` prop
  // at all, a DelegationFailure result, status incomplete.
  it('REGRESSION: a policy-denied delegation with fully default/absent args is NOT hidden', () => {
    const delegationDenied = {
      error: 'delegation_denied' as const,
      reason: 'Scout is not in your trust set for delegation.',
      policy: 'trust_set' as const,
      tool: 'delegate',
      target_agent_id: 'scout-01',
    }

    render(
      <GenericToolCall
        toolName="delegate"
        // No `args` prop at all — the default/common shape. Before the fix,
        // this matched the background-dispatch hidden case regardless of
        // the denial result.
        result={delegationDenied}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )

    // The chip itself must exist — this is what the bug made disappear entirely.
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

  it('delegation-denied: indicator is an 8px warning-colored dot (local branch, shares statusDot)', () => {
    const { container } = render(
      <GenericToolCall
        toolName="delegate"
        result={{ error: 'delegation_denied', reason: 'nope', policy: 'mode', tool: 'delegate' }}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-warning)]')
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

  it('the delegation-denied block uses a left accent, not a full bordered/backgrounded box', () => {
    render(
      <GenericToolCall
        toolName="delegate"
        result={{ error: 'delegation_denied', reason: 'nope', policy: 'mode', tool: 'delegate' }}
        status={{ type: 'incomplete', reason: 'error' } as MessagePartStatus}
      />
    )
    fireEvent.click(screen.getByRole('button'))
    const block = screen.getByTestId('result-delegation-denied')
    expect(block.className).toContain('border-l-2')
    expect(block.className).not.toContain('rounded')
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

// ── activity-bar tool visibility: verbose-chat gate ─────────────────────────
// Traces to: src/lib/toolVisibility.ts shouldRenderToolCall — GenericToolCall
// is the live/streaming fallback renderer for load_tool and delegate (neither
// has a dedicated makeAssistantToolUI registration).

describe('GenericToolCall — verbose chat gate', () => {
  beforeEach(() => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: false })
    })
  })

  it('a load_tool call renders nothing when verboseChatEnabled is false', () => {
    render(
      <GenericToolCall
        toolName="load_tool"
        args={{ name: 'web_search' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.queryByTestId('tool-call-badge')).toBeNull()
  })

  it('a load_tool call renders normally when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(
      <GenericToolCall
        toolName="load_tool"
        args={{ name: 'web_search' }}
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

  it('an explicit blocking delegate call (async: false) still renders regardless of verbose setting', () => {
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

  // REGRESSION: a hidden-by-default background tool call (delegate, default
  // action=run/async=true) whose result is the `_marshal_error` sentinel must
  // still render — a result that silently failed to marshal during
  // replay-frame construction is an error outcome even though `status` alone
  // doesn't say so (the tool call itself can have succeeded). Before the
  // fix, the visibility gate only looked at `isError`/`delegationFailure`,
  // computed BEFORE `marshalErr` existed at all — so this case vanished
  // with no chip, no error text, nothing. `bash`/`delegate` are used here
  // (not `exec`, which falls into the switch's always-visible default
  // bucket and wouldn't actually exercise the hidden-by-default gate).
  it('REGRESSION: a background delegate call with a _marshal_error result and non-error status is NOT hidden', () => {
    render(
      <GenericToolCall
        toolName="delegate"
        args={{}}
        result={{ _marshal_error: 'json: unsupported type: chan int' }}
        status={COMPLETE_STATUS}
      />
    )
    expect(screen.getByTestId('tool-call-badge')).toBeInTheDocument()
  })

  it('REGRESSION: a background bash call with a _marshal_error result and non-error status is NOT hidden', () => {
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

/**
 * disclosure-row.test.tsx — the shared "tool call row header" primitive
 * (DisclosureRow) built for the ~9-file recurring shape C2-PREP's
 * inventory.json flagged: GenericToolCall.tsx (canonical), ToolCallBadge.tsx,
 * ActivityPanel.tsx, SubagentBlock.tsx, FileReadPreview.tsx,
 * WebSearchResult.tsx, WebFetchPreview.tsx, BrowserNavigate.tsx,
 * BrowserTool.tsx, BashOutput.tsx — plus FileTreeView.tsx (C1-owned, same
 * header shape).
 *
 * Every `describe` block below models one of those real shapes so the C2
 * lanes rewriting those files can swap with confidence.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DisclosureRow } from './disclosure-row'
import { Button } from './button'

describe('DisclosureRow — base contract', () => {
  it('renders a button with the given content and a trailing caret when expandable', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="row">
        Read file
      </DisclosureRow>,
    )
    const row = screen.getByTestId('row')
    expect(row.tagName).toBe('BUTTON')
    expect(row).toHaveTextContent('Read file')
    expect(row).toHaveAttribute('aria-expanded', 'false')
  })

  it('calls onExpandedChange with the toggled value on click', async () => {
    const user = userEvent.setup()
    const onExpandedChange = vi.fn()
    render(
      <DisclosureRow expanded={false} onExpandedChange={onExpandedChange} expandable data-testid="row">
        Read file
      </DisclosureRow>,
    )
    await user.click(screen.getByTestId('row'))
    expect(onExpandedChange).toHaveBeenCalledWith(true)
  })

  it('reflects an externally-controlled expanded=true as aria-expanded=true', () => {
    render(
      <DisclosureRow expanded onExpandedChange={vi.fn()} expandable data-testid="row">
        Read file
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row')).toHaveAttribute('aria-expanded', 'true')
  })

  // The deliberate omission convention carried over byte-for-byte from
  // GenericToolCall.tsx / ToolCallBadge.tsx: a non-expandable row (still
  // running, or no detail at all) never gets aria-expanded="false" — the
  // attribute is OMITTED entirely, and the row is natively disabled
  // (removed from the tab order), so assistive tech never announces
  // "collapsible" on a row that can never actually expand.
  it('omits aria-expanded and disables the row when not expandable', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable={false} data-testid="row">
        Bash — Running…
      </DisclosureRow>,
    )
    const row = screen.getByTestId('row')
    expect(row).not.toHaveAttribute('aria-expanded')
    expect(row).toBeDisabled()
  })

  it('does not call onExpandedChange when not expandable', async () => {
    const user = userEvent.setup()
    const onExpandedChange = vi.fn()
    render(
      <DisclosureRow expanded={false} onExpandedChange={onExpandedChange} expandable={false} data-testid="row">
        Bash — Running…
      </DisclosureRow>,
    )
    await user.click(screen.getByTestId('row'))
    expect(onExpandedChange).not.toHaveBeenCalled()
  })

  it('hides the caret when not expandable, and shows it (up/down) when expandable', () => {
    const { rerender } = render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable={false} data-testid="row">
        Bash — Running…
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row').querySelector('svg')).not.toBeInTheDocument()

    rerender(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="row">
        Bash — Done
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row').querySelector('svg')).toBeInTheDocument()
  })

  it('supports hideCaret to suppress the trailing caret even when expandable', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable hideCaret data-testid="row">
        Read file
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row').querySelector('svg')).not.toBeInTheDocument()
  })

  it('is a normal, explicit Tab stop when expandable (WebKit tabindex convention)', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="row">
        Read file
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row')).toHaveAttribute('tabindex', '0')
  })

  it('merges a caller-supplied className onto the root rather than replacing the defaults', () => {
    render(
      <DisclosureRow
        expanded={false}
        onExpandedChange={vi.fn()}
        expandable
        data-testid="row"
        className="ds-test-marker"
      >
        Read file
      </DisclosureRow>,
    )
    const row = screen.getByTestId('row')
    expect(row).toHaveClass('ds-test-marker')
    expect(row).toHaveClass('flex-1') // default preserved alongside the caller's class
  })

  it('forwards arbitrary Button props (e.g. data-tool) onto the rendered button', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="row" data-tool="bash">
        Bash
      </DisclosureRow>,
    )
    expect(screen.getByTestId('row')).toHaveAttribute('data-tool', 'bash')
  })
})

describe('DisclosureRow — real call-site shapes', () => {
  // GenericToolCall.tsx / ToolCallBadge.tsx: status indicator + label + muted
  // status text, all inside the toggle, caret pushed to the row's far right.
  it('status indicator + label + status text row (GenericToolCall/ToolCallBadge shape)', () => {
    render(
      <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="tool-call-toggle">
        <span data-testid="status-dot" aria-hidden="true">●</span>
        <span className="font-medium">Read file</span>
        <span className="text-[var(--color-muted)]">Succeeded</span>
      </DisclosureRow>,
    )
    const row = screen.getByTestId('tool-call-toggle')
    expect(row).toHaveTextContent('Read file')
    expect(row).toHaveTextContent('Succeeded')
    expect(screen.getByTestId('status-dot')).toBeInTheDocument()
  })

  // BrowserNavigate.tsx: a "Watch live" launcher is a SIBLING of the toggle
  // inside the caller's own flex row, never nested inside it — a button
  // cannot nest inside a button. DisclosureRow provides no slot for a
  // second interactive control; the caller composes them side by side.
  it('renders as one flex item so a caller can place a sibling action beside it (BrowserNavigate shape)', () => {
    render(
      <div className="flex w-full items-center gap-[var(--space-2)]">
        <DisclosureRow expanded={false} onExpandedChange={vi.fn()} expandable data-testid="browser-toggle">
          Browser navigate
        </DisclosureRow>
        <Button type="button" variant="ghost" data-testid="watch-live">
          Watch live
        </Button>
      </div>,
    )
    const toggle = screen.getByTestId('browser-toggle')
    const watchLive = screen.getByTestId('watch-live')
    // Siblings, not nested: neither button contains the other.
    expect(toggle.contains(watchLive)).toBe(false)
    expect(watchLive.contains(toggle)).toBe(false)
    expect(toggle.parentElement).toBe(watchLive.parentElement)
  })

  // Every one of the ~9 audited call sites disables the row while the tool
  // call is still running by folding `isRunning` into the `expandable` gate
  // the caller passes in (not a separate prop on DisclosureRow itself).
  it('a caller can fold "still running" into expandable, disabling the row (all 9 call sites shape)', () => {
    const isRunning = true
    const hasResult = false
    render(
      <DisclosureRow
        expanded={false}
        onExpandedChange={vi.fn()}
        expandable={!isRunning && hasResult}
        data-testid="running-toggle"
      >
        Bash — Running…
      </DisclosureRow>,
    )
    const row = screen.getByTestId('running-toggle')
    expect(row).toBeDisabled()
    expect(row).not.toHaveAttribute('aria-expanded')
  })
})

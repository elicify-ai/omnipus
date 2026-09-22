/**
 * segmented-control.test.tsx — the shared `role="group"` toggle-button
 * cluster (SegmentedControl / SegmentedControlItem) built for the 11 audited
 * "segmented toggle-group member" call sites (aria-pressed inside
 * role="group"): LibraryPdfPreview.tsx, LibraryTextPreview.tsx,
 * AuthMethodControl.tsx, ProviderDetailPanel.tsx, RiskySettingControl.tsx,
 * SsrfEditor.tsx, ToolPolicyEditor.tsx, McpServerModal.tsx,
 * Step1Identity.tsx, CalendarToolbar.tsx, UsageScreen.tsx.
 *
 * Every `describe` block below models one of those real shapes so the C2
 * lanes rewriting those files can swap with confidence.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SegmentedControl, SegmentedControlItem } from './segmented-control'

describe('SegmentedControl — base contract', () => {
  it('renders a labelled role="group" with each item independently tabbable (no roving tabindex)', () => {
    render(
      <SegmentedControl value="view" onValueChange={vi.fn()} aria-label="View mode">
        <SegmentedControlItem value="view">View</SegmentedControlItem>
        <SegmentedControlItem value="edit">Edit</SegmentedControlItem>
      </SegmentedControl>,
    )
    const group = screen.getByRole('group', { name: 'View mode' })
    expect(group).toBeInTheDocument()
    const view = screen.getByRole('button', { name: 'View' })
    const edit = screen.getByRole('button', { name: 'Edit' })
    // WAI-ARIA "group of toggle buttons" — distinct from RadioGroup's roving
    // tabindex: BOTH buttons keep a normal, explicit tabIndex=0 Tab stop.
    expect(view).toHaveAttribute('tabindex', '0')
    expect(edit).toHaveAttribute('tabindex', '0')
  })

  it('supports aria-labelledby as the alternative accessible-name mechanism', () => {
    render(
      <>
        <span id="ext-label">Authentication method</span>
        <SegmentedControl value="a" onValueChange={vi.fn()} aria-labelledby="ext-label">
          <SegmentedControlItem value="a">Sign in</SegmentedControlItem>
          <SegmentedControlItem value="b">API key</SegmentedControlItem>
        </SegmentedControl>
      </>,
    )
    expect(screen.getByRole('group', { name: 'Authentication method' })).toBeInTheDocument()
  })

  it('marks the selected item aria-pressed=true and the rest false', () => {
    render(
      <SegmentedControl value="edit" onValueChange={vi.fn()} aria-label="View mode">
        <SegmentedControlItem value="view">View</SegmentedControlItem>
        <SegmentedControlItem value="edit">Edit</SegmentedControlItem>
      </SegmentedControl>,
    )
    expect(screen.getByRole('button', { name: 'View' })).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByRole('button', { name: 'Edit' })).toHaveAttribute('aria-pressed', 'true')
  })

  it('calls onValueChange with the clicked item\'s value, including re-clicking the active item', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <SegmentedControl value="view" onValueChange={onValueChange} aria-label="View mode">
        <SegmentedControlItem value="view">View</SegmentedControlItem>
        <SegmentedControlItem value="edit">Edit</SegmentedControlItem>
      </SegmentedControl>,
    )
    await user.click(screen.getByRole('button', { name: 'Edit' }))
    expect(onValueChange).toHaveBeenCalledWith('edit')

    onValueChange.mockClear()
    await user.click(screen.getByRole('button', { name: 'View' }))
    expect(onValueChange).toHaveBeenCalledWith('view')
  })

  it('disables every item from the group-level disabled prop, and a per-item disabled overrides it', () => {
    render(
      <SegmentedControl value="a" onValueChange={vi.fn()} aria-label="Choice" disabled>
        <SegmentedControlItem value="a">A</SegmentedControlItem>
        <SegmentedControlItem value="b" disabled={false}>B</SegmentedControlItem>
      </SegmentedControl>,
    )
    expect(screen.getByRole('button', { name: 'A' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'B' })).not.toBeDisabled()
  })

  it('does not fire onValueChange when a disabled item is clicked', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <SegmentedControl value="a" onValueChange={onValueChange} aria-label="Choice">
        <SegmentedControlItem value="a">A</SegmentedControlItem>
        <SegmentedControlItem value="b" disabled>B</SegmentedControlItem>
      </SegmentedControl>,
    )
    await user.click(screen.getByRole('button', { name: 'B' }))
    expect(onValueChange).not.toHaveBeenCalled()
  })

  it("merges a caller-supplied className onto the root and each item rather than replacing the defaults", () => {
    render(
      <SegmentedControl value="a" onValueChange={vi.fn()} aria-label="Choice" className="ds-test-root-marker">
        <SegmentedControlItem value="a" className="ds-test-item-marker">A</SegmentedControlItem>
      </SegmentedControl>,
    )
    const group = screen.getByRole('group', { name: 'Choice' })
    expect(group).toHaveClass('ds-test-root-marker')
    expect(group).toHaveClass('rounded-md') // default preserved alongside the caller's class
    const item = screen.getByRole('button', { name: 'A' })
    expect(item).toHaveClass('ds-test-item-marker')
    expect(item).toHaveClass('rounded') // default preserved alongside the caller's class
  })

  it('throws when SegmentedControlItem is rendered outside a SegmentedControl', () => {
    // Swallow the expected React error-boundary console noise for this one assertion.
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<SegmentedControlItem value="a">A</SegmentedControlItem>)).toThrow(
      'SegmentedControlItem must be rendered inside a <SegmentedControl>',
    )
    spy.mockRestore()
  })

  it('forwards data-testid and other arbitrary props onto the rendered item', () => {
    render(
      <SegmentedControl value="a" onValueChange={vi.fn()} aria-label="Choice">
        <SegmentedControlItem value="a" data-testid="segment-a">A</SegmentedControlItem>
      </SegmentedControl>,
    )
    expect(screen.getByTestId('segment-a')).toBe(screen.getByRole('button', { name: 'A' }))
  })
})

describe('SegmentedControl — real call-site shapes', () => {
  // LibraryPdfPreview.tsx / LibraryTextPreview.tsx: two-button, icon-only
  // View/Edit mode toggle. Comment in LibraryTextPreview explains WHY this is
  // a 2-button aria-pressed group and not a single toggle — screen readers
  // must hear which of view/edit is current, not infer it.
  it('View/Edit icon-only mode toggle (LibraryPdfPreview / LibraryTextPreview shape)', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <SegmentedControl value="view" onValueChange={onValueChange} aria-label="View mode">
        <SegmentedControlItem value="view" aria-label="View" title="View" data-testid="library-preview-mode-view">
          <span aria-hidden="true">👁</span>
        </SegmentedControlItem>
        <SegmentedControlItem value="edit" aria-label="Edit" title="Edit" data-testid="library-preview-mode-edit">
          <span aria-hidden="true">✎</span>
        </SegmentedControlItem>
      </SegmentedControl>,
    )
    const editBtn = screen.getByRole('button', { name: 'Edit' })
    expect(editBtn).toHaveAttribute('title', 'Edit')
    await user.click(editBtn)
    expect(onValueChange).toHaveBeenCalledWith('edit')
  })

  // RiskySettingControl.tsx / SsrfEditor.tsx: a dynamic option list mapped
  // into items, each stamped data-testid={`risky-option-${value}`}, plus a
  // group-level `disabled` gate.
  it('dynamic option list with per-item data-testid stamps (RiskySettingControl / SsrfEditor shape)', () => {
    const options = [
      { value: 'off', label: 'Off' },
      { value: 'warn', label: 'Warn' },
      { value: 'block', label: 'Block' },
    ]
    render(
      <SegmentedControl value="warn" onValueChange={vi.fn()} aria-label="Risky setting">
        {options.map((opt) => (
          <SegmentedControlItem key={opt.value} value={opt.value} data-testid={`risky-option-${opt.value}`}>
            {opt.label}
          </SegmentedControlItem>
        ))}
      </SegmentedControl>,
    )
    expect(screen.getByTestId('risky-option-warn')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('risky-option-off')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('risky-option-block')).toHaveAttribute('aria-pressed', 'false')
  })

  // UsageScreen.tsx: a period selector where only the pressed state (not the
  // label) distinguishes selection, driven straight off a `PERIODS` array.
  it('period selector (UsageScreen shape)', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    const PERIODS = [
      { value: '24h', label: 'Last 24h' },
      { value: '7d', label: 'Last 7d' },
      { value: '30d', label: 'Last 30d' },
    ]
    render(
      <SegmentedControl value="7d" onValueChange={onValueChange} aria-label="Select time period">
        {PERIODS.map(({ value, label }) => (
          <SegmentedControlItem key={value} value={value} data-testid={`period-${value}`}>
            {label}
          </SegmentedControlItem>
        ))}
      </SegmentedControl>,
    )
    await user.click(screen.getByTestId('period-30d'))
    expect(onValueChange).toHaveBeenCalledWith('30d')
  })

  // AuthMethodControl.tsx: the segment is conditionally rendered at all only
  // when there is a real 2-way choice ("a single-button segmented control is
  // a lie about what the operator can do") — modeled here as the consuming
  // component's own responsibility, with the segment itself rendering two
  // flex-1 buttons with a leading icon plus label.
  it('two flex-1 buttons with icon + label, only rendered when there is a real choice (AuthMethodControl shape)', () => {
    const signInOffered = true
    const apiKeyOffered = true
    render(
      <>
        {signInOffered && apiKeyOffered && (
          <SegmentedControl value="sign_in" onValueChange={vi.fn()} aria-label="Authentication method" data-testid="auth-segment">
            <SegmentedControlItem value="sign_in" data-testid="auth-segment-sign_in" className="flex-1">
              <span aria-hidden="true">→</span>
              Sign in with Acme
            </SegmentedControlItem>
            <SegmentedControlItem value="api_key" data-testid="auth-segment-api_key" className="flex-1">
              <span aria-hidden="true">🔑</span>
              API key
            </SegmentedControlItem>
          </SegmentedControl>
        )}
      </>,
    )
    expect(screen.getByTestId('auth-segment')).toBeInTheDocument()
    expect(screen.getByTestId('auth-segment-sign_in')).toHaveAttribute('aria-pressed', 'true')
  })

  // CalendarToolbar.tsx: an explicit code comment there documents that this
  // is deliberately role="group" + aria-pressed, NOT role="tablist" — no
  // roving tabindex or aria-controls is wired, so every view stays its own
  // Tab stop.
  it('four-button view switcher, no roving tabindex (CalendarToolbar shape)', () => {
    const views = ['day', 'week', 'month', 'year'] as const
    render(
      <SegmentedControl value="week" onValueChange={vi.fn()} aria-label="Calendar view">
        {views.map((view) => (
          <SegmentedControlItem key={view} value={view} aria-label={view} data-testid={`calendar-view-${view}`}>
            {view[0].toUpperCase()}
          </SegmentedControlItem>
        ))}
      </SegmentedControl>,
    )
    for (const view of views) {
      expect(screen.getByTestId(`calendar-view-${view}`)).toHaveAttribute('tabindex', '0')
    }
    expect(screen.getByTestId('calendar-view-week')).toHaveAttribute('aria-pressed', 'true')
  })
})

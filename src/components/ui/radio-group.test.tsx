/**
 * radio-group.test.tsx — the shared WAI-ARIA radio-group primitive
 * (RadioGroup / RadioGroupItem) built for the 4 audited "radio-group member"
 * call sites: AltitudeToggle.tsx, WorkspaceTasksTab.tsx's ViewSwitcher (both
 * already roving-tabindex with Left/Right/Up/Down arrow-key navigation),
 * PromptGuardSection.tsx and SkillTrustSection.tsx (role="radiogroup"/"radio"
 * with every option at a fixed tabIndex=0 and no arrow-key handling at all —
 * a pre-existing gap this component fixes for real, not just visually).
 *
 * Every `describe` block below models one of those real shapes so the C2
 * lanes rewriting those files can swap with confidence.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RadioGroup, RadioGroupItem } from './radio-group'

// Matches AltitudeToggle.test.tsx's own convention for a synthetic key event.
function fireEventKeyDown(element: HTMLElement, key: string) {
  return fireEvent.keyDown(element, { key })
}

describe('RadioGroup — base contract', () => {
  it('renders a labelled role="radiogroup" with role="radio" children and correct aria-checked', () => {
    render(
      <RadioGroup value="top-level" onValueChange={vi.fn()} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radiogroup', { name: 'Board depth' })).toBeInTheDocument()
    expect(screen.getByRole('radio', { name: 'Top-level' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByRole('radio', { name: 'Show all' })).toHaveAttribute('aria-checked', 'false')
  })

  it('supports aria-labelledby as the alternative accessible-name mechanism', () => {
    render(
      <>
        <span id="ext-label">Skill trust level</span>
        <RadioGroup value="trusted" onValueChange={vi.fn()} aria-labelledby="ext-label">
          <RadioGroupItem value="trusted">Trusted</RadioGroupItem>
          <RadioGroupItem value="sandboxed">Sandboxed</RadioGroupItem>
        </RadioGroup>
      </>,
    )
    expect(screen.getByRole('radiogroup', { name: 'Skill trust level' })).toBeInTheDocument()
  })

  it('fires onValueChange when the inactive option is clicked', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="top-level" onValueChange={onValueChange} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    await user.click(screen.getByRole('radio', { name: 'Show all' }))
    expect(onValueChange).toHaveBeenCalledWith('show-all')
  })

  // ── Roving tabindex (WAI-ARIA radio group pattern) ────────────────────────

  it('gives the checked radio tabIndex 0 and every other radio tabIndex -1', () => {
    render(
      <RadioGroup value="top-level" onValueChange={vi.fn()} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radio', { name: 'Top-level' })).toHaveAttribute('tabindex', '0')
    expect(screen.getByRole('radio', { name: 'Show all' })).toHaveAttribute('tabindex', '-1')
  })

  it('moves the roving tabindex when the checked value changes', () => {
    const { rerender } = render(
      <RadioGroup value="top-level" onValueChange={vi.fn()} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    rerender(
      <RadioGroup value="show-all" onValueChange={vi.fn()} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radio', { name: 'Top-level' })).toHaveAttribute('tabindex', '-1')
    expect(screen.getByRole('radio', { name: 'Show all' })).toHaveAttribute('tabindex', '0')
  })

  // ── Arrow-key navigation (with wrap) + Home/End ───────────────────────────

  it('ArrowRight/ArrowDown selects the next option, wrapping past the last', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="c" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b">B</RadioGroupItem>
        <RadioGroupItem value="c">C</RadioGroupItem>
      </RadioGroup>,
    )
    // Checked is C (last); ArrowRight from it must wrap to A.
    fireEventKeyDown(screen.getByRole('radio', { name: 'C' }), 'ArrowRight')
    expect(onValueChange).toHaveBeenCalledWith('a')

    onValueChange.mockClear()
    fireEventKeyDown(screen.getByRole('radio', { name: 'C' }), 'ArrowDown')
    expect(onValueChange).toHaveBeenCalledWith('a')
  })

  it('ArrowLeft/ArrowUp selects the previous option, wrapping past the first', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="a" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b">B</RadioGroupItem>
        <RadioGroupItem value="c">C</RadioGroupItem>
      </RadioGroup>,
    )
    // Checked is A (first); ArrowLeft from it must wrap to C.
    fireEventKeyDown(screen.getByRole('radio', { name: 'A' }), 'ArrowLeft')
    expect(onValueChange).toHaveBeenCalledWith('c')

    onValueChange.mockClear()
    fireEventKeyDown(screen.getByRole('radio', { name: 'A' }), 'ArrowUp')
    expect(onValueChange).toHaveBeenCalledWith('c')
  })

  it('moves DOM focus to the newly-selected option on arrow navigation', () => {
    render(
      <RadioGroup value="a" onValueChange={vi.fn()} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b">B</RadioGroupItem>
      </RadioGroup>,
    )
    const a = screen.getByRole('radio', { name: 'A' })
    const b = screen.getByRole('radio', { name: 'B' })
    a.focus()
    fireEventKeyDown(a, 'ArrowRight')
    expect(b).toHaveFocus()
  })

  it('Home selects the first option and End selects the last, regardless of current position', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="b" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b">B</RadioGroupItem>
        <RadioGroupItem value="c">C</RadioGroupItem>
      </RadioGroup>,
    )
    fireEventKeyDown(screen.getByRole('radio', { name: 'B' }), 'Home')
    expect(onValueChange).toHaveBeenLastCalledWith('a')

    onValueChange.mockClear()
    fireEventKeyDown(screen.getByRole('radio', { name: 'B' }), 'End')
    expect(onValueChange).toHaveBeenLastCalledWith('c')
  })

  it('a non-navigation key is ignored', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="a" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b">B</RadioGroupItem>
      </RadioGroup>,
    )
    fireEventKeyDown(screen.getByRole('radio', { name: 'A' }), 'Enter')
    expect(onValueChange).not.toHaveBeenCalled()
  })

  // ── Disabled items ─────────────────────────────────────────────────────

  it('disables every item from the group-level disabled prop, and a per-item disabled overrides it', () => {
    render(
      <RadioGroup value="a" onValueChange={vi.fn()} aria-label="Choice" disabled>
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b" disabled={false}>B</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radio', { name: 'A' })).toBeDisabled()
    expect(screen.getByRole('radio', { name: 'B' })).not.toBeDisabled()
  })

  it('skips a disabled option during arrow-key navigation', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="a" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b" disabled>B</RadioGroupItem>
        <RadioGroupItem value="c">C</RadioGroupItem>
      </RadioGroup>,
    )
    fireEventKeyDown(screen.getByRole('radio', { name: 'A' }), 'ArrowRight')
    expect(onValueChange).toHaveBeenCalledWith('c') // B is skipped
  })

  it('does not fire onValueChange when a disabled item is clicked', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="a" onValueChange={onValueChange} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
        <RadioGroupItem value="b" disabled>B</RadioGroupItem>
      </RadioGroup>,
    )
    // A disabled native <button> does not dispatch click events at all.
    await user.click(screen.getByRole('radio', { name: 'B' }))
    expect(onValueChange).not.toHaveBeenCalled()
  })

  it("merges a caller-supplied className onto the root and each item rather than replacing the defaults", () => {
    render(
      <RadioGroup value="a" onValueChange={vi.fn()} aria-label="Choice" className="ds-test-root-marker">
        <RadioGroupItem value="a" className="ds-test-item-marker">A</RadioGroupItem>
      </RadioGroup>,
    )
    const group = screen.getByRole('radiogroup', { name: 'Choice' })
    expect(group).toHaveClass('ds-test-root-marker')
    expect(group).toHaveClass('flex') // default preserved alongside the caller's class
    const item = screen.getByRole('radio', { name: 'A' })
    expect(item).toHaveClass('ds-test-item-marker')
    expect(item).toHaveClass('rounded-md') // default preserved alongside the caller's class
  })

  it('throws when RadioGroupItem is rendered outside a RadioGroup', () => {
    const spy = vi.spyOn(console, 'error').mockImplementation(() => {})
    expect(() => render(<RadioGroupItem value="a">A</RadioGroupItem>)).toThrow(
      'RadioGroupItem must be rendered inside a <RadioGroup>',
    )
    spy.mockRestore()
  })

  it('sets aria-orientation from the orientation prop, defaulting to horizontal', () => {
    const { rerender } = render(
      <RadioGroup value="a" onValueChange={vi.fn()} aria-label="Choice">
        <RadioGroupItem value="a">A</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radiogroup', { name: 'Choice' })).toHaveAttribute('aria-orientation', 'horizontal')
    rerender(
      <RadioGroup value="a" onValueChange={vi.fn()} aria-label="Choice" orientation="vertical">
        <RadioGroupItem value="a">A</RadioGroupItem>
      </RadioGroup>,
    )
    expect(screen.getByRole('radiogroup', { name: 'Choice' })).toHaveAttribute('aria-orientation', 'vertical')
  })
})

describe('RadioGroup — real call-site shapes', () => {
  // AltitudeToggle.tsx: two pill options (Top-level / Show-all), horizontal.
  it('two-option pill toggle (AltitudeToggle shape)', () => {
    const onValueChange = vi.fn()
    render(
      <RadioGroup value="top-level" onValueChange={onValueChange} aria-label="Board depth">
        <RadioGroupItem value="top-level">Top-level</RadioGroupItem>
        <RadioGroupItem value="show-all">Show all</RadioGroupItem>
      </RadioGroup>,
    )
    fireEventKeyDown(screen.getByRole('radio', { name: 'Top-level' }), 'ArrowRight')
    expect(onValueChange).toHaveBeenCalledWith('show-all')
  })

  // WorkspaceTasksTab.tsx's ViewSwitcher: three icon+label options
  // (Board/List/Graph), each with a data-testid stamp.
  it('icon + label three-option view switcher with data-testid stamps (WorkspaceTasksTab ViewSwitcher shape)', () => {
    const VIEW_OPTIONS = [
      { value: 'board', label: 'Board' },
      { value: 'list', label: 'List' },
      { value: 'graph', label: 'Graph' },
    ]
    render(
      <RadioGroup value="board" onValueChange={vi.fn()} aria-label="Task view">
        {VIEW_OPTIONS.map((opt) => (
          <RadioGroupItem key={opt.value} value={opt.value} data-testid={`tasks-view-${opt.value}`}>
            <span aria-hidden="true">*</span>
            {opt.label}
          </RadioGroupItem>
        ))}
      </RadioGroup>,
    )
    expect(screen.getByTestId('tasks-view-board')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('tasks-view-list')).toHaveAttribute('tabindex', '-1')
  })

  // PromptGuardSection.tsx / SkillTrustSection.tsx: full-width vertical card
  // rows with a label + subtitle. Pre-existing gap this component fixes for
  // real: neither original had ANY arrow-key handling — every option sat at
  // a fixed tabIndex=0. This RadioGroup gives both roving tabindex and
  // arrow/Home/End navigation for the first time.
  it('vertical card rows with label + subtitle (PromptGuardSection / SkillTrustSection shape)', () => {
    const LEVELS = [
      { value: 'off', label: 'Off', subtitle: 'No sanitisation.' },
      { value: 'standard', label: 'Standard', subtitle: 'Sanitises common injection patterns.' },
      { value: 'strict', label: 'Strict', subtitle: 'Aggressively sanitises all untrusted output.' },
    ]
    const onValueChange = vi.fn()
    render(
      <RadioGroup
        value="standard"
        onValueChange={onValueChange}
        aria-label="Prompt injection defense level"
        orientation="vertical"
      >
        {LEVELS.map((lvl) => (
          <RadioGroupItem key={lvl.value} value={lvl.value}>
            <span aria-hidden="true" />
            <span>
              <span>{lvl.label}</span>
              <p>{lvl.subtitle}</p>
            </span>
          </RadioGroupItem>
        ))}
      </RadioGroup>,
    )
    const group = screen.getByRole('radiogroup', { name: 'Prompt injection defense level' })
    expect(group).toHaveAttribute('aria-orientation', 'vertical')
    expect(screen.getByRole('radio', { name: /Standard/ })).toHaveAttribute('aria-checked', 'true')
    fireEventKeyDown(screen.getByRole('radio', { name: /Standard/ }), 'ArrowDown')
    expect(onValueChange).toHaveBeenCalledWith('strict')
    fireEventKeyDown(screen.getByRole('radio', { name: /Standard/ }), 'Home')
    expect(onValueChange).toHaveBeenLastCalledWith('off')
  })
})

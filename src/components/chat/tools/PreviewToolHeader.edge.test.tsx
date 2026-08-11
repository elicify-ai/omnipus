/**
 * PreviewToolHeader edge-case render tests (Phase 5, Agent B)
 *
 * PreviewToolHeader is exported directly, so we test it without needing to
 * capture via makeAssistantToolUI.
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { PreviewToolHeader } from './PreviewToolHeader'
import { Globe, Terminal } from '@phosphor-icons/react'

// ── toolName edge cases ───────────────────────────────────────────────────────

describe.each([
  ['empty tool name', ''],
  ['normal tool name', 'web_serve'],
  ['very long tool name', 'tool_'.repeat(200)],
  ['tool name with dots', 'browser.navigate.click'],
  ['tool name with XSS', '<script>alert(1)</script>'],
  ['tool name with unicode', '\u{1F680}_tool'],
] as Array<[string, string]>)(
  'PreviewToolHeader renders toolName "%s" without throwing',
  (_label, toolName) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName={toolName}
            isRunning={false}
            isError={false}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── label edge cases ──────────────────────────────────────────────────────────

describe.each([
  ['no label (undefined)', undefined],
  ['empty label', ''],
  ['normal label', 'vite dev'],
  ['very long label', 'a'.repeat(5_000)],
  ['label with unicode', '\u{1F680} dev server'],
  ['label with XSS', '<script>alert(1)</script>'],
  ['label with multiline (display-only)', 'line1\nline2'],
] as Array<[string, string | undefined]>)(
  'PreviewToolHeader renders label "%s" without throwing',
  (_label, label) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName="web_serve"
            label={label}
            isRunning={false}
            isError={false}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── isRunning / isError combinations ─────────────────────────────────────────

describe.each([
  ['running, would-be error', true, true],
  ['running, would-be success', true, false],
  ['not running, errored', false, true],
  ['not running, succeeded', false, false],
] as Array<[string, boolean, boolean]>)(
  'PreviewToolHeader renders state "%s" without throwing',
  (_label, isRunning, isError) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName="web_serve"
            isRunning={isRunning}
            isError={isError}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── icon variants ─────────────────────────────────────────────────────────────

describe.each([
  ['Globe icon', <Globe size={13} />],
  ['Terminal icon', <Terminal size={13} />],
  ['null icon (edge case)', null],
  ['string icon (edge case — degenerate)', 'X' as unknown as React.ReactNode],
] as Array<[string, React.ReactNode]>)(
  'PreviewToolHeader renders icon "%s" without throwing',
  (_label, icon) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={icon}
            toolName="web_serve"
            isRunning={false}
            isError={false}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── trailing element edge cases ───────────────────────────────────────────────

describe.each([
  ['no trailing', undefined],
  ['string trailing', 'OK'],
  ['span trailing', <span>:3000</span>],
  ['very long trailing text', <span>{'x'.repeat(1_000)}</span>],
  ['null trailing', null],
] as Array<[string, React.ReactNode | undefined]>)(
  'PreviewToolHeader renders trailing "%s" without throwing',
  (_label, trailing) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName="web_serve"
            trailing={trailing}
            isRunning={false}
            isError={false}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── data-testid edge cases ─────────────────────────────────────────────────────

describe.each([
  ['no testid', undefined],
  ['normal testid', 'webserve-tool-header'],
  ['testid with special chars', 'header<script>'],
] as Array<[string, string | undefined]>)(
  'PreviewToolHeader renders testid "%s" without throwing',
  (_label, testId) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName="web_serve"
            data-testid={testId}
            isRunning={false}
            isError={true}
            isCancelled={false}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── Positive invariants ───────────────────────────────────────────────────────
//
// .not.toThrow() does not prove the user sees anything. These assertions verify
// that the tool name is visible in the rendered output.

it('renders the toolName as visible text', () => {
  render(
    <PreviewToolHeader
      icon={<Globe size={13} />}
      toolName="web_serve"
      isRunning={false}
      isError={false}
      isCancelled={false}
    />
  )
  expect(screen.getByText('web_serve')).toBeInTheDocument()
})

it('renders a label when provided', () => {
  render(
    <PreviewToolHeader
      icon={<Terminal size={13} />}
      toolName="run_in_workspace"
      label="npm run dev"
      isRunning={true}
      isError={true}
      isCancelled={false}
    />
  )
  expect(screen.getByText('run_in_workspace')).toBeInTheDocument()
  expect(screen.getByText('npm run dev')).toBeInTheDocument()
})

// ── flat text-line status dot (ticket "Tool components in chat", P2) ────────
// The old rounded-t-md/border/bg-surface-1 header frame with a status-tinted
// border is gone — the header is now a flat text line whose only status
// color comes from a leading 8px dot (running keeps the spinning icon in the
// same slot), shared with the other restyled tool rows via
// getToolBadgeStatusConfig.

describe('PreviewToolHeader — flat text-line status dot', () => {
  /** The status indicator (dot or spinner) is always the header's first child. */
  function getIndicatorEl(container: HTMLElement) {
    return container.firstElementChild?.children[0] as HTMLElement | undefined
  }

  it('running: indicator is the spinning icon, not a dot', () => {
    const { container } = render(
      <PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={true} isError={true} isCancelled={false} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('svg')
    expect(indicator?.getAttribute('class')).toContain('animate-spin')
  })

  it('not running, no error: indicator is an 8px success-colored dot', () => {
    const { container } = render(
      <PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={false} isCancelled={false} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.tagName.toLowerCase()).toBe('span')
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(indicator?.getAttribute('class')).toContain('rounded-full')
  })

  it('not running, errored: indicator is an 8px error-colored dot', () => {
    const { container } = render(
      <PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={true} isCancelled={false} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
  })

  it('the caller-supplied icon still renders alongside the status dot', () => {
    render(
      <PreviewToolHeader
        icon={<Terminal size={13} data-testid="type-icon" />}
        toolName="web_serve"
        isRunning={false}
        isError={false}
        isCancelled={false}
      />
    )
    expect(screen.getByTestId('type-icon')).toBeInTheDocument()
  })

  it('running: renders the muted "Running..." status text (label is no longer computed and discarded)', () => {
    render(<PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={true} isError={true} isCancelled={false} />)
    expect(screen.getByText('Running...')).toBeInTheDocument()
  })

  it('not running, no error: renders the muted "Done" status text', () => {
    render(<PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={false} isCancelled={false} />)
    expect(screen.getByText('Done')).toBeInTheDocument()
  })

  it('not running, errored: renders the muted "Failed" status text', () => {
    render(<PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={true} isCancelled={false} />)
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })

  // F2 (issue #617 follow-up): PreviewToolHeader previously had no cancelled
  // state at all — WebServeBlock had no `status` object to derive it from,
  // so a cancelled web_serve call rendered as a plain success dot ("Done").
  it('not running, cancelled: indicator is an 8px muted-colored dot with a "Cancelled" label, not the success dot', () => {
    const { container } = render(
      <PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={false} isCancelled={true} />
    )
    const indicator = getIndicatorEl(container)
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-success)]')
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
  })

  it('cancelled wins over errored — a cancelled call never shows "Failed"', () => {
    render(
      <PreviewToolHeader icon={<Globe size={13} />} toolName="web_serve" isRunning={false} isError={true} isCancelled={true} />
    )
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).not.toBeInTheDocument()
  })

  it('the header has no card-frame classes — flat/transparent, no rounded-t-md/border/bg-surface-1', () => {
    render(
      <PreviewToolHeader
        icon={<Globe size={13} />}
        toolName="web_serve"
        data-testid="webserve-tool-header"
        isRunning={false}
        isError={false}
        isCancelled={false}
      />
    )
    const root = screen.getByTestId('webserve-tool-header')
    expect(root.className).not.toContain('rounded-t-md')
    expect(root.className).not.toContain('border')
    expect(root.className).not.toContain('bg-[var(--color-surface-1)]')
  })

  it('no descendant anywhere in the tree carries a card-frame class (rounded-md/overflow-hidden/bg-surface-1)', () => {
    render(
      <PreviewToolHeader
        icon={<Terminal size={13} />}
        toolName="web_serve"
        label="vite dev"
        data-testid="webserve-tool-header"
        isRunning={false}
        isError={false}
        isCancelled={false}
      />
    )
    const root = screen.getByTestId('webserve-tool-header')
    expect(
      root.querySelector('[class*="rounded-md"], [class*="overflow-hidden"], [class*="bg-[var(--color-surface-1)]"]')
    ).toBeNull()
  })
})

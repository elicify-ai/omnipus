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
            hasResult={true}
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
            hasResult={true}
          />
        )
      ).not.toThrow()
    })
  }
)

// ── isRunning / hasResult combinations ───────────────────────────────────────

describe.each([
  ['running, no result', true, false],
  ['running, has result', true, true],
  ['not running, no result', false, false],
  ['not running, has result', false, true],
] as Array<[string, boolean, boolean]>)(
  'PreviewToolHeader renders state "%s" without throwing',
  (_label, isRunning, hasResult) => {
    it('renders', () => {
      expect(() =>
        render(
          <PreviewToolHeader
            icon={<Globe size={13} />}
            toolName="web_serve"
            isRunning={isRunning}
            hasResult={hasResult}
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
            hasResult={true}
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
            hasResult={true}
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
            hasResult={false}
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
      hasResult={true}
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
      hasResult={false}
    />
  )
  expect(screen.getByText('run_in_workspace')).toBeInTheDocument()
  expect(screen.getByText('npm run dev')).toBeInTheDocument()
})

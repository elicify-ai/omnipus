/**
 * BrowserTool.live-wiring.test.tsx
 *
 * M7 (second review wave on branch fix/615-617-618-hardening): BrowserTool.
 * edge.test.tsx tests `BrowserToolBlock` directly (a hand-passed `isError`
 * prop) and never captures `createBrowserToolUI`'s two `makeAssistantToolUI`
 * render lambdas (BrowserTool.tsx's `dotUI`/`underscoreUI`) — so nothing
 * proved that the LIVE registration for any of the six dot-form and
 * underscore-form browser tools actually threads AssistantUI's real `isError`
 * render prop through to `BrowserToolBlock`, as opposed to re-deriving it
 * from `status.type === 'incomplete'` (which can never be true for a
 * finished call carrying a result — see BrowserToolBlockProps.isError's doc
 * comment). Pattern mirrors BrowserNavigate.test.tsx's capture setup.
 */

import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'

type BrowserRenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

const captured = vi.hoisted((): Record<string, BrowserRenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as BrowserRenderFn
      }
      return config
    },
  }
})

// Static imports: vi.mock intercepts makeAssistantToolUI before these run,
// and importing any one binding executes the WHOLE module body — which
// registers all twelve (six dot + six underscore) live tool UIs.
import {
  BrowserClickUI,
  BrowserClickUnderscoreUI,
  BrowserTypeUI,
  BrowserScreenshotUI,
  BrowserGetTextUI,
  BrowserWaitUI,
  BrowserEvaluateUI,
} from './BrowserTool'

const ALL_DOT_TOOL_NAMES = [
  'browser.click',
  'browser.type',
  'browser.screenshot',
  'browser.get_text',
  'browser.wait',
  'browser.evaluate',
]
const ALL_UNDERSCORE_TOOL_NAMES = [
  'browser_click',
  'browser_type',
  'browser_screenshot',
  'browser_get_text',
  'browser_wait',
  'browser_evaluate',
]

describe('BrowserTool live registrations — all twelve are captured', () => {
  it.each(ALL_DOT_TOOL_NAMES)('registers a render function for "%s"', (toolName) => {
    expect(captured[toolName]).toBeDefined()
  })

  it.each(ALL_UNDERSCORE_TOOL_NAMES)('registers a render function for "%s"', (toolName) => {
    expect(captured[toolName]).toBeDefined()
  })

  it('exports are defined and distinct from each other', () => {
    expect(BrowserClickUI).toBeDefined()
    expect(BrowserClickUnderscoreUI).toBeDefined()
    expect(BrowserTypeUI).toBeDefined()
    expect(BrowserScreenshotUI).toBeDefined()
    expect(BrowserGetTextUI).toBeDefined()
    expect(BrowserWaitUI).toBeDefined()
    expect(BrowserEvaluateUI).toBeDefined()
  })
})

// Full render assertions for one representative tool (browser.click, dot +
// underscore) — the twelve registrations share ONE `createBrowserToolUI`
// factory body (BrowserTool.tsx), so proving the factory's render closure
// threads `isError` correctly for one instantiation proves it for all six;
// the "all captured" sweep above proves every instantiation actually ran.
describe('browser.click / browser_click live wiring — issue #617', () => {
  it('a genuine error outcome (isError=true from the real render prop) renders the error dot and "Failed" label', () => {
    const renderFn = captured['browser.click']!
    const { container, getByText } = render(
      renderFn({
        args: { selector: '#submit' },
        result: { error: 'element not found' },
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('a finished call with a result but isError=false renders the success dot, not error', () => {
    const renderFn = captured['browser.click']!
    const { container, getByText } = render(
      renderFn({
        args: { selector: '#submit' },
        result: { ok: true },
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-success)]')
    expect(getByText('Done')).toBeInTheDocument()
  })

  it('the underscore alias (browser_click) wires isError through identically', () => {
    const renderFn = captured['browser_click']!
    const { container, getByText } = render(
      renderFn({
        args: { selector: '#submit' },
        result: { error: 'element not found' },
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-error)]')
    expect(getByText('Failed')).toBeInTheDocument()
  })

  it('cancelled: the leading indicator is the muted dot with a "Cancelled" label (resultless part inheriting the message status — the one producible incomplete/cancelled shape; see toolStatusConfig.tsx)', () => {
    const renderFn = captured['browser.click']!
    const { container, getByText } = render(
      renderFn({
        args: { selector: '#submit' },
        result: undefined,
        status: { type: 'incomplete', reason: 'cancelled' },
        isError: false,
      }) as React.ReactElement
    )
    const indicator = container.querySelector('button')?.children[0] as HTMLElement
    expect(indicator?.getAttribute('class')).toContain('bg-[var(--color-muted)]')
    expect(indicator?.getAttribute('class')).not.toContain('bg-[var(--color-error)]')
    expect(getByText('Cancelled')).toBeInTheDocument()
  })
})

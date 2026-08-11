/**
 * WebServeUI.live-wiring.test.tsx
 *
 * F6 (second review wave on branch fix/615-617-618-hardening): WebServeUI.
 * test.tsx mocks `makeAssistantToolUI: (config) => config` and every one of
 * its tests calls `WebServeBlock` directly with hand-passed props — none of
 * them ever captures or invokes `makeWebServeUI`'s actual `render` closure
 * (WebServeUI.tsx's `render: ({ args, result, status, isError }) => (...)`),
 * so nothing proved that the LIVE registration for `web_serve` (or its two
 * replay aliases, `serve_workspace` / `run_in_workspace`) actually threads
 * AssistantUI's real `isError` render prop and `isCancelledStatus(status)`
 * through to `WebServeBlock`, as opposed to those two props silently being
 * dropped from the lambda (which the existing suite would not catch — it
 * never calls the lambda at all). Pattern mirrors BrowserTool.live-wiring.
 * test.tsx (the same gap already closed for BrowserTool/BrowserNavigate).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'

type WebServeRenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string; reason?: string }
  isError?: boolean
}) => React.ReactNode

const captured = vi.hoisted((): Record<string, WebServeRenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as WebServeRenderFn
      }
      return config
    },
  }
})

vi.mock('@/store/ui', () => ({
  useUiStore: () => ({ addToast: vi.fn() }),
}))

// Static imports: vi.mock intercepts makeAssistantToolUI before these run,
// and importing any one binding executes the module bodies that register
// all three live tool UIs (web_serve directly, serve_workspace/
// run_in_workspace via their own alias modules, which both call the same
// makeWebServeUI factory).
import { WebServeUI } from './WebServeUI'
import { ServeWorkspaceUI } from './ServeWorkspaceUI'
import { RunInWorkspaceUI } from './RunInWorkspaceUI'

beforeEach(() => {
  Object.defineProperty(window, 'location', {
    value: { hostname: 'localhost', protocol: 'http:', origin: 'http://localhost:5000', port: '5000' },
    writable: true,
  })
})

describe('WebServeUI live registrations — all three are captured', () => {
  it('registers a render function for "web_serve", "serve_workspace", and "run_in_workspace"', () => {
    expect(captured['web_serve']).toBeDefined()
    expect(captured['serve_workspace']).toBeDefined()
    expect(captured['run_in_workspace']).toBeDefined()
  })

  it('exports are defined', () => {
    expect(WebServeUI).toBeDefined()
    expect(ServeWorkspaceUI).toBeDefined()
    expect(RunInWorkspaceUI).toBeDefined()
  })
})

const STATIC_RESULT = {
  kind: 'static' as const,
  url: 'http://localhost:5000/preview/jim/tok/',
  path: 'elicify-hello',
  expires_at: '2099-01-01T00:00:00Z',
}

describe('web_serve live wiring — issue #617/F6', () => {
  it('a genuine error outcome (isError=true from the real render prop) renders "Failed", not "Done"', () => {
    const renderFn = captured['web_serve']!
    render(
      renderFn({
        args: { path: 'elicify-hello' },
        result: STATIC_RESULT,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement,
    )
    expect(screen.getByText('Failed')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
  })

  it('a finished call with a well-formed result but isError=false renders "Done", not "Failed"', () => {
    const renderFn = captured['web_serve']!
    render(
      renderFn({
        args: { path: 'elicify-hello' },
        result: STATIC_RESULT,
        status: { type: 'complete' },
        isError: false,
      }) as React.ReactElement,
    )
    expect(screen.getByText('Done')).toBeInTheDocument()
    expect(screen.queryByText('Failed')).toBeNull()
  })

  it('cancelled: the real render prop threads isCancelledStatus(status) through to "Cancelled", not "Done"/"Failed"', () => {
    const renderFn = captured['web_serve']!
    render(
      renderFn({
        args: { path: 'elicify-hello' },
        result: null,
        status: { type: 'incomplete', reason: 'cancelled' },
        isError: false,
      }) as React.ReactElement,
    )
    expect(screen.getByText('Cancelled')).toBeInTheDocument()
    expect(screen.queryByText('Done')).toBeNull()
    expect(screen.queryByText('Failed')).toBeNull()
  })

  it('the serve_workspace replay alias wires isError through identically', () => {
    const renderFn = captured['serve_workspace']!
    render(
      renderFn({
        args: {},
        result: STATIC_RESULT,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement,
    )
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })

  it('the run_in_workspace replay alias wires isError through identically', () => {
    const renderFn = captured['run_in_workspace']!
    render(
      renderFn({
        args: {},
        result: STATIC_RESULT,
        status: { type: 'complete' },
        isError: true,
      }) as React.ReactElement,
    )
    expect(screen.getByText('Failed')).toBeInTheDocument()
  })
})
